package supervisor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FreedomCats/dnstt-cli/core/client"
	"github.com/FreedomCats/dnstt-cli/internal/exit"
	"github.com/FreedomCats/dnstt-cli/internal/resolvers"
)

const (
	r1 = "udp:192.0.2.1:53"
	r2 = "udp:192.0.2.2:53"
	r3 = "udp:192.0.2.3:53"
)

// A failed dial moves to the next resolver at once; the first session is Connected.
func TestDialsThroughList(t *testing.T) {
	h := newHarness(t, []string{r1, r2, r3}, nil)
	h.dial = func(r string, n int) error {
		if r == r1 {
			return errDial
		}
		return nil
	}
	h.run(context.Background())
	tr := h.waitState(StateConnected, 1)
	if tr.Resolver != r2 {
		t.Errorf("connected through %s, want %s", tr.Resolver, r2)
	}
	if got := h.dialList(); !slices.Equal(got, []string{r1, r2}) {
		t.Errorf("dials %q", got)
	}
	if st := h.tun.Status(); st.State != StateConnected || st.Resolver != r2 || st.Since.IsZero() {
		t.Errorf("status %+v", st)
	}
	if got := h.sleepList(); len(got) != 0 {
		t.Errorf("slept %v", got)
	}
	if err := h.stop(); err != nil {
		t.Fatal(err)
	}
	if !h.session(0).closed.Load() {
		t.Error("the session is left open")
	}
	if h.tun.State() != StateStopping {
		t.Errorf("state %s after Run", h.tun.State())
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var path []State
	for _, tr := range h.trans {
		path = append(path, tr.To)
	}
	want := []State{StateDialing, StateDialing, StateConnected, StateStopping}
	if !slices.Equal(path, want) {
		t.Errorf("states %v, want %v", path, want)
	}
}

func TestFatalExitInterruptsRun(t *testing.T) {
	for _, phase := range []string{"before run", "dial", "check", "connected", "backoff"} {
		t.Run(phase, func(t *testing.T) {
			entered := make(chan struct{})
			h := newHarness(t, []string{r1, r2}, func(o *Options) {
				if phase == "check" || phase == "backoff" {
					o.CheckExit = func(ctx context.Context, _ exit.OpenStream) error {
						if phase == "backoff" {
							return errors.New("temporary exit interruption")
						}
						close(entered)
						<-ctx.Done()
						return ctx.Err()
					}
				}
			})
			// A fatal exit must not add the third strike, even during an interrupted dial.
			pool := h.tun.opts.Resolvers
			pool.HandshakeFailed(0)
			pool.HandshakeFailed(0)
			if phase == "dial" {
				h.dial = func(string, int) error { close(entered); return errBlock }
			}
			if phase == "backoff" {
				h.sleep = func(ctx context.Context, _ time.Duration) bool {
					close(entered)
					<-ctx.Done()
					return false
				}
			}
			want := fmt.Errorf("first fatal: %w", &exit.LoginError{})
			pending := make(chan error, 1)
			if phase == "before run" {
				h.tun.FatalExit(want)
				if h.tun.State() != StateIdle {
					t.Fatal("FatalExit changed state outside Run")
				}
			}
			h.run(context.Background())
			switch phase {
			case "dial", "check", "backoff":
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("Run did not enter " + phase)
				}
			case "connected":
				h.waitState(StateConnected, 1)
			}
			if phase != "connected" {
				go func() {
					c, err := h.tun.OpenStream(context.Background())
					if c != nil {
						c.Close()
					}
					pending <- err
				}()
			}
			h.tun.FatalExit(want)
			// Cancellation and Restart cannot replace an already reported fatal cause.
			h.tun.Restart(errors.New("network changed"))
			h.cancel()
			if got := h.stop(); got != want {
				t.Fatalf("Run = %v, want the first fatal error %v", got, want)
			}
			tr := h.waitState(StateStopping, 1)
			if tr.Cause != want {
				t.Fatalf("Stopping cause = %v, want %v", tr.Cause, want)
			}
			if phase != "connected" {
				select {
				case err := <-pending:
					if !errors.Is(err, ErrStopped) {
						t.Fatalf("pending OpenStream = %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("pending OpenStream did not wake")
				}
			}
			if _, aside := pool.Aside(0); aside {
				t.Error("fatal exit penalized the resolver")
			}
			wantDials, wantSleeps := 1, 0
			if phase == "before run" {
				wantDials = 0
			}
			if phase == "backoff" {
				wantSleeps = 1
			}
			if got := len(h.dialList()); got != wantDials {
				t.Errorf("dials = %d, want %d", got, wantDials)
			}
			if got := len(h.sleepList()); got != wantSleeps {
				t.Errorf("backoffs = %d, want %d", got, wantSleeps)
			}
			h.mu.Lock()
			defer h.mu.Unlock()
			stops := 0
			for _, tr := range h.trans {
				if tr.To == StateStopping {
					stops++
				}
			}
			if stops != 1 {
				t.Errorf("Stopping transitions = %d, want 1", stops)
			}
			for _, s := range h.sessions {
				if !s.closed.Load() {
					t.Error("session left open")
				}
			}
		})
	}
}

func TestFatalExitConcurrentFirstCause(t *testing.T) {
	h := newHarness(t, []string{r1}, nil)
	h.run(context.Background())
	h.waitState(StateConnected, 1)
	h.tun.FatalExit(nil)
	want := fmt.Errorf("first refusal: %w", &exit.LoginError{})
	h.tun.FatalExit(want)
	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.tun.FatalExit(fmt.Errorf("later refusal: %w", &exit.LoginError{}))
			h.tun.Restart(errors.New("network changed"))
			h.cancel()
		}()
	}
	wg.Wait()
	if err := h.stop(); err != want {
		t.Fatalf("Run = %v, want the first refusal", err)
	}
	if tr := h.waitState(StateStopping, 1); tr.Cause != want {
		t.Fatalf("Stopping = %+v", tr)
	}
	if got := h.dialList(); len(got) != 1 {
		t.Fatalf("redialed after a fatal exit: %v", got)
	}
}

type pendingSession struct {
	*fakeSession
	entered chan struct{}
	release chan struct{}
	conn    net.Conn
	once    sync.Once
}

func (s *pendingSession) OpenStream() (net.Conn, error) {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return s.conn, nil
}

func TestFatalExitPendingAcquisition(t *testing.T) {
	h := newHarness(t, []string{r1}, nil)
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	s := &pendingSession{fakeSession: newFakeSession(r1), entered: make(chan struct{}), release: make(chan struct{}), conn: a}
	h.tun.dial = func(context.Context, client.Config) (Session, error) { return s, nil }
	h.run(context.Background())
	h.waitState(StateConnected, 1)
	result := make(chan error, 1)
	go func() {
		c, err := h.tun.OpenStream(context.Background())
		if c != nil {
			c.Close()
		}
		result <- err
	}()
	<-s.entered
	want := &exit.LoginError{}
	h.tun.FatalExit(want)
	select {
	case err := <-result:
		if !errors.Is(err, ErrStopped) {
			t.Fatalf("OpenStream = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fatal exit did not interrupt acquisition")
	}
	if err := h.stop(); err != want || !s.closed.Load() {
		t.Fatalf("Run = %v, session closed = %v", err, s.closed.Load())
	}
	close(s.release)
	b.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := b.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("late stream was not closed: %v", err)
	}
}

func TestFatalExitDuringRestart(t *testing.T) {
	h := newHarness(t, []string{r1}, nil)
	entered := make(chan struct{})
	h.dial = func(_ string, n int) error {
		if n > 0 {
			close(entered)
			return errBlock
		}
		return nil
	}
	h.run(context.Background())
	h.waitState(StateConnected, 1)
	h.tun.Restart(errors.New("network changed"))
	<-entered
	want := &exit.LoginError{}
	h.tun.FatalExit(want)
	if err := h.stop(); err != want {
		t.Fatalf("Run = %v, want fatal error", err)
	}
	if tr := h.waitState(StateStopping, 1); tr.Cause != want {
		t.Fatalf("Stopping = %+v", tr)
	}
	if got := h.dialList(); len(got) != 2 {
		t.Fatalf("dials = %v, want no dial after the signal", got)
	}
}

func TestFatalExitDrainsOnce(t *testing.T) {
	var h *harness
	var calls atomic.Int32
	want := &exit.LoginError{}
	h = newHarness(t, []string{r1}, func(o *Options) {
		o.Drain = func(context.Context) {
			calls.Add(1)
			if h.tun.State() != StateStopping || h.session(0).closed.Load() {
				t.Error("fatal drain did not run while stopping with the session still up")
			}
			if _, err := h.tun.OpenStream(context.Background()); !errors.Is(err, ErrStopped) {
				t.Error("fatal drain did not refuse a new stream")
			}
		}
	})
	h.run(context.Background())
	h.waitState(StateConnected, 1)
	h.tun.FatalExit(want)
	if err := h.stop(); err != want || calls.Load() != 1 || !h.session(0).closed.Load() {
		t.Fatalf("fatal Run = %v, drain calls = %d, session closed = %v", err, calls.Load(), h.session(0).closed.Load())
	}
}

func TestFatalExitSimultaneousCallers(t *testing.T) {
	h := newHarness(t, []string{r1}, nil)
	h.run(context.Background())
	h.waitState(StateConnected, 1)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			h.tun.FatalExit(fmt.Errorf("refusal %d: %w", i, &exit.LoginError{}))
		}()
	}
	close(start)
	wg.Wait()
	first := h.tun.fatalCause()
	if first == nil || h.stop() != first || h.waitState(StateStopping, 1).Cause != first {
		t.Fatal("concurrent fatal callers did not preserve one cause in Run and Stopping")
	}
}

func TestFatalExitWinsPendingCancellation(t *testing.T) {
	h := newHarness(t, []string{r1}, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	onTransition := h.tun.opts.OnTransition
	h.tun.opts.OnTransition = func(tr Transition) {
		onTransition(tr)
		if tr.To == StateConnected {
			close(entered)
			<-release
		}
	}
	h.run(context.Background())
	<-entered
	// Run has not handled either notification: parent cancellation is already pending
	// when the fatal application error arrives.
	h.cancel()
	want := &exit.LoginError{}
	h.tun.FatalExit(want)
	h.tun.Restart(errors.New("network changed"))
	if h.tun.State() != StateConnected {
		t.Error("the application goroutine changed the supervisor's state")
	}
	close(release)
	if err := h.stop(); err != want || h.waitState(StateStopping, 1).Cause != want {
		t.Fatal("cancellation replaced the fatal cause")
	}
	if len(h.dialList()) != 1 || len(h.sleepList()) != 0 {
		t.Fatal("pending cancellation and fatal exit caused a retry")
	}
}

func TestHTTPStreamsFollowSessionReconnect(t *testing.T) {
	h := newHarness(t, []string{r1}, nil)
	h.tun.dial = wrapDial(h.tun.dial, func(s *fakeSession) {
		s.serve = func(c net.Conn) {
			r := bufio.NewReader(c)
			req, err := http.ReadRequest(r)
			if err != nil {
				return
			}
			if req.Host == "blocked.example:443" {
				io.WriteString(c, "HTTP/1.1 403 Forbidden\r\n\r\n")
				return
			}
			if req.Host == "malformed.example:443" {
				io.WriteString(c, "SSH-2.0-fixture\r\n")
				return
			}
			if _, err := io.WriteString(c, "HTTP/1.1 200 Connected\r\n\r\n"); err != nil {
				return
			}
			io.Copy(c, r)
		}
	})
	h.run(context.Background())
	h.waitState(StateConnected, 1)
	d := &exit.HTTP{Open: h.tun.OpenStream}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, target := range []string{"blocked.example:443", "malformed.example:443"} {
		if _, err := d.Dial(ctx, target); err == nil || exit.IsLogin(err) {
			t.Fatalf("per-request refusal: %v", err)
		}
	}
	if len(h.dialList()) != 1 || len(h.sleepList()) != 0 || h.tun.State() != StateConnected {
		t.Fatal("per-request HTTP failures restarted the tunnel")
	}
	paused, release := make(chan struct{}), make(chan struct{})
	h.mu.Lock()
	h.sleep = func(ctx context.Context, _ time.Duration) bool {
		close(paused)
		select {
		case <-release:
			return true
		case <-ctx.Done():
			return false
		}
	}
	h.mu.Unlock()
	h.session(0).die(client.ErrSilent)
	<-paused
	result := make(chan error, 1)
	go func() {
		c, err := d.Dial(ctx, "echo.example:7")
		if err == nil {
			defer c.Close()
			_, err = io.WriteString(c, "data")
			if err == nil {
				var got [4]byte
				_, err = io.ReadFull(c, got[:])
				if err == nil && string(got[:]) != "data" {
					err = errors.New("incorrect echo after reconnect")
				}
			}
		}
		result <- err
	}()
	close(release)
	h.waitState(StateConnected, 2)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("HTTP stream did not move to the new session")
	}
	if got := h.dialList(); !slices.Equal(got, []string{r1, r1}) {
		t.Fatalf("normal reconnection dials = %v", got)
	}
}

// No resolver completes a handshake on the first round: Run stops and names every
// resolver and the four reasons.
func TestNeverConnected(t *testing.T) {
	h := newHarness(t, []string{r1, r2, r3}, func(o *Options) { o.HandshakeTimeout = 50 * time.Millisecond })
	h.dial = func(r string, n int) error {
		if r == r2 {
			return errBlock
		}
		return errDial
	}
	h.run(context.Background())
	var err error
	select {
	case err = <-h.result:
		h.result <- err
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop")
	}
	var ne *NeverConnectedError
	if !errors.As(err, &ne) || len(ne.Failures) != 3 {
		t.Fatalf("got %v", err)
	}
	for _, want := range []string{"domain", "key", "down", "blocked", r1, r3, errDialText,
		r2 + ": no answer from the server within 50ms"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q does not say %q", err, want)
		}
	}
	if got := h.sleepList(); len(got) != 0 {
		t.Errorf("slept %v before stopping", got)
	}
}

// After one session, a failed round is a delay, never a stop: 1·2^n s up to 64 s.
func TestBackoffAfterSuccess(t *testing.T) {
	h := newHarness(t, []string{r1, r2}, nil)
	h.dial = func(r string, n int) error {
		if r == r1 && n == 0 {
			return nil
		}
		return errDial
	}
	var slept atomic.Int32
	h.sleep = func(ctx context.Context, d time.Duration) bool {
		if slept.Add(1) >= 9 {
			h.cancel()
		}
		return ctx.Err() == nil
	}
	h.run(context.Background())
	h.waitState(StateConnected, 1)
	h.session(0).die(client.ErrSilent)
	if err := func() error {
		select {
		case err := <-h.result:
			h.result <- err
			return err
		case <-time.After(10 * time.Second):
			t.Fatal("Run did not return")
			return nil
		}
	}(); err != nil {
		t.Fatal(err)
	}
	s := time.Second
	// The short session first, then a delay per failed round.
	want := []time.Duration{1 * s, 2 * s, 4 * s, 8 * s, 16 * s, 32 * s, 64 * s, 64 * s, 64 * s}
	if got := h.sleepList(); !slices.Equal(got[:min(len(got), len(want))], want) {
		t.Errorf("delays %v, want %v", got, want)
	}
	tr := h.waitState(StateReconnecting, 1)
	if !errors.Is(tr.Cause, client.ErrSilent) || tr.Delay != time.Second || tr.Resolver != r1 {
		t.Errorf("reconnecting: %+v", tr)
	}
}

// A session that lived long clears the backoff and is redialed through the same resolver
// at once.
func TestSessionDiesAfterLiving(t *testing.T) {
	h := newHarness(t, []string{r1, r2}, nil)
	var clock atomic.Int64
	clock.Store(time.Unix(1000000, 0).UnixNano())
	h.tun.now = func() time.Time { return time.Unix(0, clock.Load()) }
	h.run(context.Background())
	h.waitState(StateConnected, 1)
	clock.Add(int64(2 * time.Minute))
	h.session(0).die(client.ErrSilent)
	h.waitState(StateConnected, 2)
	if got := h.dialList(); !slices.Equal(got, []string{r1, r1}) {
		t.Errorf("dials %q, want the same resolver again", got)
	}
	if got := h.sleepList(); !slices.Equal(got, []time.Duration{0}) {
		t.Errorf("delays %v, want none", got)
	}
	if st := h.tun.Status(); st.Since.IsZero() {
		t.Error("no Since")
	}
}

// Three short sessions put the resolver aside: the next dial goes to the next resolver.
func TestShortSessionsPutAside(t *testing.T) {
	h := newHarness(t, []string{r1, r2}, nil)
	h.run(context.Background())
	for i := range 3 {
		h.waitState(StateConnected, i+1)
		h.session(i).die(client.ErrSilent)
	}
	h.waitState(StateConnected, 4)
	if got := h.dialList(); !slices.Equal(got, []string{r1, r1, r1, r2}) {
		t.Errorf("dials %q", got)
	}
	if _, aside := h.tun.opts.Resolvers.Aside(0); !aside {
		t.Error("r1 is not aside")
	}
}

// socksAnswer is a stream of an exit that answers the greeting with answer.
func socksAnswer(answer string) func(net.Conn) {
	return func(c net.Conn) {
		c.Read(make([]byte, 64))
		c.Write([]byte(answer))
		io.Copy(io.Discard, c)
	}
}

func socksCheck(ctx context.Context, open exit.OpenStream) error {
	return (&exit.SOCKS5{Open: open}).Check(ctx)
}

// A refused login and an exit that is not SOCKS5 stop the tunnel; an exit that breaks
// the stream is a retry.
func TestExitCheck(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer string
		final  func(error) bool
	}{
		{"login", "\x05\xff", exit.IsLogin},
		{"not socks5", "HTTP/1.0 400 Bad Request\r\n\r\n", func(err error) bool { return errors.Is(err, exit.ErrNotSOCKS5) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, []string{r1, r2}, func(o *Options) { o.CheckExit = socksCheck })
			h.tun.dial = wrapDial(h.tun.dial, func(s *fakeSession) { s.serve = socksAnswer(tc.answer) })
			h.run(context.Background())
			var err error
			select {
			case err = <-h.result:
				h.result <- err
			case <-time.After(10 * time.Second):
				t.Fatal("Run did not stop")
			}
			if !tc.final(err) {
				t.Fatalf("got %v", err)
			}
			if !h.session(0).closed.Load() {
				t.Error("the session is left open")
			}
			if got := h.dialList(); len(got) != 1 {
				t.Errorf("dials %q after a final refusal", got)
			}
		})
	}

	t.Run("retry", func(t *testing.T) {
		h := newHarness(t, []string{r1, r2}, func(o *Options) { o.CheckExit = socksCheck })
		var n atomic.Int32
		h.tun.dial = wrapDial(h.tun.dial, func(s *fakeSession) {
			if n.Add(1) == 1 {
				s.serve = func(c net.Conn) { c.Read(make([]byte, 64)) } // closes the stream
			} else {
				s.serve = socksAnswer("\x05\x00")
			}
		})
		h.run(context.Background())
		h.waitState(StateConnected, 1)
		// The same resolver again: the exit is not its fault.
		if got := h.dialList(); !slices.Equal(got, []string{r1, r1}) {
			t.Errorf("dials %q", got)
		}
		if got := h.sleepList(); !slices.Equal(got, []time.Duration{time.Second}) {
			t.Errorf("delays %v", got)
		}
		if !h.session(0).closed.Load() {
			t.Error("the session with a broken exit is left open")
		}
	})
}

// wrapDial lets a test set up each session before Run gets it.
func wrapDial(dial func(context.Context, client.Config) (Session, error), setup func(*fakeSession)) func(context.Context, client.Config) (Session, error) {
	return func(ctx context.Context, cfg client.Config) (Session, error) {
		s, err := dial(ctx, cfg)
		if err == nil {
			setup(s.(*fakeSession))
		}
		return s, err
	}
}

// Restart ends the session and dials from the top at once, with the health cleared; in a
// delay it cuts the delay short.
func TestRestart(t *testing.T) {
	h := newHarness(t, []string{r1, r2}, nil)
	h.dial = func(r string, n int) error {
		if r == r1 && n > 0 && n < 4 {
			return errDial
		}
		return nil
	}
	h.run(context.Background())
	h.waitState(StateConnected, 1)

	// r1 is put aside by its next three failures.
	h.tun.opts.Resolvers.HandshakeFailed(0)
	h.tun.opts.Resolvers.HandshakeFailed(0)
	h.tun.opts.Resolvers.HandshakeFailed(0)
	errNet := errors.New("network changed")
	h.tun.Restart(errNet)
	tr := h.waitState(StateReconnecting, 1)
	if tr.Cause != errNet || tr.Delay != 0 {
		t.Errorf("reconnecting: %+v", tr)
	}
	if !h.session(0).closed.Load() {
		t.Error("the session is left open")
	}
	h.waitState(StateConnected, 2)
	// From the top, with r1 back from aside: r1 fails (n=1..3), r2 connects.
	if got := h.dialList(); !slices.Equal(got, []string{r1, r1, r2}) {
		t.Errorf("dials %q", got)
	}
	if got := h.sleepList(); len(got) != 0 {
		t.Errorf("slept %v", got)
	}

	// A delay in progress is cut short by Restart.
	blocked := make(chan struct{})
	h.mu.Lock()
	h.sleep = func(ctx context.Context, d time.Duration) bool {
		close(blocked)
		<-ctx.Done()
		return false
	}
	h.mu.Unlock()
	h.session(1).die(client.ErrSilent)
	<-blocked
	h.tun.Restart(errNet)
	h.waitState(StateConnected, 3)
}

// OpenStream waits for a session, takes the next one after a death, and refuses once the
// tunnel stops.
func TestOpenStream(t *testing.T) {
	h := newHarness(t, []string{r1}, nil)
	release := make(chan struct{})
	h.dial = func(r string, n int) error {
		<-release
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := h.tun.OpenStream(ctx); !errors.Is(err, ErrNoSession) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("no session: %v", err)
	}

	got := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		c, err := h.tun.OpenStream(ctx)
		if err == nil {
			c.Close()
		}
		got <- err
	}()
	h.run(context.Background())
	time.Sleep(20 * time.Millisecond)
	close(release)
	if err := <-got; err != nil {
		t.Fatalf("waiting for the first session: %v", err)
	}

	// A dead session: the stream comes from the next one.
	h.waitState(StateConnected, 1)
	h.session(0).die(client.ErrSilent)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	c, err := h.tun.OpenStream(ctx2)
	if err != nil {
		t.Fatalf("after a death: %v", err)
	}
	c.Close()
	h.wait("a second session", func() bool { return len(h.sessions) == 2 })

	h.stop()
	if _, err := h.tun.OpenStream(context.Background()); !errors.Is(err, ErrStopped) {
		t.Errorf("after the stop: %v", err)
	}
}

// No data from the server for DegradedAfter: Degraded; data again: Connected.
func TestDegraded(t *testing.T) {
	h := newHarness(t, []string{r1}, func(o *Options) { o.DegradedAfter = 50 * time.Millisecond })
	h.run(context.Background())
	h.waitState(StateConnected, 1)
	tr := h.waitState(StateDegraded, 1)
	if tr.Cause != ErrNoData {
		t.Errorf("degraded: %+v", tr)
	}
	h.session(0).received.Add(100)
	h.waitState(StateConnected, 2)
}

// The stop lets Drain finish the open connections, for DrainTimeout at most; the second
// Ctrl+C (abort) cuts it short.
func TestDrain(t *testing.T) {
	for _, tc := range []struct {
		name  string
		abort bool
	}{{"timeout", false}, {"abort", true}} {
		t.Run(tc.name, func(t *testing.T) {
			var sessionOpen atomic.Bool
			var h *harness
			drained := make(chan time.Duration, 1)
			h = newHarness(t, []string{r1}, func(o *Options) {
				o.DrainTimeout = 200 * time.Millisecond
				o.Drain = func(ctx context.Context) {
					sessionOpen.Store(!h.session(0).closed.Load())
					start := time.Now()
					<-ctx.Done()
					drained <- time.Since(start)
				}
			})
			ctx, abort := WithAbort(context.Background())
			h.run(ctx)
			h.waitState(StateConnected, 1)
			h.cancel()
			if tc.abort {
				h.waitState(StateStopping, 1)
				for !abort() {
					time.Sleep(time.Millisecond)
				}
			}
			d := <-drained
			<-h.result
			h.result <- nil
			if !sessionOpen.Load() {
				t.Error("the session was closed before Drain")
			}
			if !h.session(0).closed.Load() {
				t.Error("the session is left open")
			}
			if tc.abort && d >= 200*time.Millisecond {
				t.Errorf("abort did not cut the drain short: %v", d)
			}
			if !tc.abort && d < 150*time.Millisecond {
				t.Errorf("drain ended after %v", d)
			}
		})
	}
}

// SetDrain is the datapath's stop hook (the socks relay drain): a stop calls it while the
// session is still up, and the second Ctrl+C cuts the wait short.
func TestSetDrainAbort(t *testing.T) {
	var sessionOpen atomic.Bool
	var h *harness
	drained := make(chan time.Duration, 1)
	h = newHarness(t, []string{r1}, func(o *Options) { o.DrainTimeout = 500 * time.Millisecond })
	h.tun.SetDrain(func(ctx context.Context) {
		sessionOpen.Store(!h.session(0).closed.Load())
		start := time.Now()
		<-ctx.Done()
		drained <- time.Since(start)
	})
	ctx, abort := WithAbort(context.Background())
	h.run(ctx)
	h.waitState(StateConnected, 1)
	h.cancel()
	h.waitState(StateStopping, 1)
	if !abort() {
		t.Fatal("nothing to abort while the drain runs")
	}
	var d time.Duration
	select {
	case d = <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("the stop did not call the drain")
	}
	<-h.result
	h.result <- nil
	if !sessionOpen.Load() {
		t.Error("the session was closed before the drain")
	}
	if d >= 500*time.Millisecond {
		t.Errorf("abort did not cut the drain short: %v", d)
	}
}

func TestRunTwice(t *testing.T) {
	h := newHarness(t, []string{r1}, nil)
	h.run(context.Background())
	h.waitState(StateConnected, 1)
	if err := h.tun.Run(context.Background()); err == nil {
		t.Error("a second Run started")
	}
}

func TestNewWithoutResolvers(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Error("no error")
	}
	if _, err := New(Options{Resolvers: resolvers.NewPool(nil)}); err == nil {
		t.Error("no error for an empty pool")
	}
}

// The network entry dials the network's DNS servers read at each dial, one server per
// dial, and a full round counts each of them.
func TestNetworkEntry(t *testing.T) {
	var servers atomic.Pointer[[]netip.Addr]
	set := func(s ...string) {
		var a []netip.Addr
		for _, x := range s {
			a = append(a, netip.MustParseAddr(x))
		}
		servers.Store(&a)
	}
	set("198.51.100.1", "198.51.100.2")
	h := newHarness(t, []string{"network", r2}, func(o *Options) {
		o.NetworkDNS = func() ([]netip.Addr, error) { return *servers.Load(), nil }
	})
	h.dial = func(r string, n int) error {
		if r == "udp:203.0.113.1:53" {
			return nil
		}
		return errDial
	}
	h.run(context.Background())
	var err error
	select {
	case err = <-h.result:
		h.result <- err
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop")
	}
	var ne *NeverConnectedError
	if !errors.As(err, &ne) {
		t.Fatalf("got %v", err)
	}
	want := []string{"udp:198.51.100.1:53", r2, "udp:198.51.100.2:53"}
	if got := h.dialList(); !slices.Equal(got, want) {
		t.Errorf("dials %q, want %q", got, want)
	}
	if !strings.Contains(err.Error(), "network (198.51.100.2)") {
		t.Errorf("%q does not name the server of the network entry", err)
	}

	// Another network: the entry follows it.
	set("203.0.113.1")
	h2 := newHarness(t, []string{"network"}, func(o *Options) {
		o.NetworkDNS = func() ([]netip.Addr, error) { return *servers.Load(), nil }
	})
	h2.dial = h.dial
	h2.run(context.Background())
	if tr := h2.waitState(StateConnected, 1); tr.Resolver != "network (203.0.113.1)" {
		t.Errorf("connected through %q", tr.Resolver)
	}
}

// The datapath starts with a session and is suspended after every session: a death, a
// Restart while connected and the stop all take the system traffic out; a Restart in the
// middle starts it again without a delay.
func TestDatapathAcrossSessions(t *testing.T) {
	h := newHarness(t, []string{r1}, nil)
	dp := &fakeDatapath{}
	h.tun.SetDatapath(dp)
	h.run(context.Background())

	h.waitState(StateConnected, 1)
	if got := dp.snapshot(); !slices.Equal(got, []string{"Start"}) {
		t.Fatalf("calls after the first session: %v", got)
	}

	// The session dies: Suspend before the next Start.
	h.session(0).die(client.ErrSilent)
	h.waitState(StateConnected, 2)
	if got := dp.snapshot(); !slices.Equal(got, []string{"Start", "Suspend", "Start"}) {
		t.Fatalf("calls after a death: %v", got)
	}

	// A network change while connected: Suspend then Start, and no delay.
	h.tun.Restart(errors.New("network changed"))
	h.waitState(StateConnected, 3)
	if got := dp.snapshot(); !slices.Equal(got, []string{"Start", "Suspend", "Start", "Suspend", "Start"}) {
		t.Fatalf("calls after a restart: %v", got)
	}
	if got := h.sleepList(); !slices.Equal(got, []time.Duration{time.Second}) {
		t.Fatalf("delays %v, want only the one after the death", got)
	}

	// The stop has a session too: Suspend is the last call and Run returns nil.
	if err := h.stop(); err != nil {
		t.Fatal(err)
	}
	calls := dp.snapshot()
	if len(calls) == 0 || calls[len(calls)-1] != "Suspend" {
		t.Fatalf("calls after the stop: %v, want Suspend last", calls)
	}
	if want := []string{"Start", "Suspend", "Start", "Suspend", "Start", "Suspend"}; !slices.Equal(calls, want) {
		t.Fatalf("calls %v, want %v", calls, want)
	}
}

// noticeLog collects the user-facing lines of the supervisor.
type noticeLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *noticeLog) notice(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *noticeLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.lines)
}

// A death and a Restart say where system traffic goes while the tunnel reconnects, then
// that it is back; the first connection says nothing, and a failed Start prints no up line.
func TestNoticeBetweenSessions(t *testing.T) {
	log := &noticeLog{}
	h := newHarness(t, []string{r1}, func(o *Options) { o.Notice = log.notice })
	dp := &fakeDatapath{}
	h.tun.SetDatapath(dp)
	h.tun.SetKillSwitch(func() bool { return true })
	h.run(context.Background())

	h.waitState(StateConnected, 1)
	if got := log.snapshot(); len(got) != 0 {
		t.Fatalf("the first connection printed %q, want nothing", got)
	}

	h.session(0).die(client.ErrSilent)
	h.waitState(StateConnected, 2)
	down := "tunnel reconnecting — system traffic is blocked (kill-switch)"
	up := "tunnel up again — system traffic goes through it"
	if got := log.snapshot(); !slices.Equal(got, []string{down, up}) {
		t.Errorf("lines after a death %q, want %q", got, []string{down, up})
	}

	h.tun.Restart(errors.New("network changed"))
	h.waitState(StateConnected, 3)
	if got := log.snapshot(); !slices.Equal(got, []string{down, up, down, up}) {
		t.Errorf("lines after a restart %q, want a second down/up pair", got)
	}
}

// A Start that fails prints no up line; the first successful Start after a down line does.
func TestNoticeNoUpAfterFailedStart(t *testing.T) {
	log := &noticeLog{}
	h := newHarness(t, []string{r1}, func(o *Options) { o.Notice = log.notice })
	dp := &fakeDatapath{}
	h.tun.SetDatapath(dp)
	h.tun.SetKillSwitch(func() bool { return true })
	gate := make(chan struct{})
	h.sleep = func(ctx context.Context, _ time.Duration) bool {
		select {
		case <-gate:
			return true
		case <-ctx.Done():
			return false
		}
	}
	h.run(context.Background())

	h.waitState(StateConnected, 1)
	dp.setStartErr(errors.New("adapter refused"))
	h.session(0).die(client.ErrSilent)
	gate <- struct{}{} // let the retry with the failing Start through
	h.wait("the failed Start", func() bool { return len(dp.snapshot()) == 3 })
	down := "tunnel reconnecting — system traffic is blocked (kill-switch)"
	if got := log.snapshot(); !slices.Equal(got, []string{down}) {
		t.Fatalf("lines after the failed Start %q, want only %q", got, down)
	}

	dp.setStartErr(nil)
	gate <- struct{}{}
	h.waitState(StateConnected, 2)
	if got := log.snapshot(); len(got) != 2 || got[1] != "tunnel up again — system traffic goes through it" {
		t.Errorf("lines after the recovered Start %q, want the up line", got)
	}
}

// The stop prints no down line: the run is over, and there is nothing to reconnect to.
func TestNoticeNoDownOnStop(t *testing.T) {
	log := &noticeLog{}
	h := newHarness(t, []string{r1}, func(o *Options) { o.Notice = log.notice })
	h.tun.SetDatapath(&fakeDatapath{})
	h.tun.SetKillSwitch(func() bool { return true })
	h.run(context.Background())

	h.waitState(StateConnected, 1)
	if err := h.stop(); err != nil {
		t.Fatal(err)
	}
	if got := log.snapshot(); len(got) != 0 {
		t.Errorf("the stop printed %q, want nothing", got)
	}
}

// Without the kill-switch the down line says the traffic goes past the tunnel.
func TestNoticeKillSwitchOff(t *testing.T) {
	log := &noticeLog{}
	h := newHarness(t, []string{r1}, func(o *Options) { o.Notice = log.notice })
	h.tun.SetDatapath(&fakeDatapath{})
	h.tun.SetKillSwitch(func() bool { return false })
	h.run(context.Background())

	h.waitState(StateConnected, 1)
	h.session(0).die(client.ErrSilent)
	h.waitState(StateConnected, 2)
	want := []string{
		"tunnel reconnecting — system traffic goes past the tunnel (kill-switch off)",
		"tunnel up again — system traffic goes through it",
	}
	if got := log.snapshot(); !slices.Equal(got, want) {
		t.Errorf("lines after a death %q, want %q", got, want)
	}
}

// Without a datapath (socks) no session is routed: a death and a Restart say nothing
// about system traffic, and the first connection says nothing either.
func TestNoticeWithoutDatapath(t *testing.T) {
	log := &noticeLog{}
	h := newHarness(t, []string{r1}, func(o *Options) { o.Notice = log.notice })
	// Socks: no SetDatapath and no kill-switch at all.
	h.run(context.Background())

	h.waitState(StateConnected, 1)
	h.session(0).die(client.ErrSilent)
	h.waitState(StateConnected, 2)
	h.tun.Restart(errors.New("network changed"))
	h.waitState(StateConnected, 3)
	if got := log.snapshot(); len(got) != 0 {
		t.Errorf("socks printed user-facing lines about system traffic: %q", got)
	}
}

// A nil Notice falls back to Logf.
func TestNoticeFallsBackToLogf(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	h := newHarness(t, []string{r1}, func(o *Options) {
		o.Logf = func(format string, args ...any) {
			mu.Lock()
			defer mu.Unlock()
			lines = append(lines, fmt.Sprintf(format, args...))
		}
	})
	h.tun.SetDatapath(&fakeDatapath{})
	h.tun.SetKillSwitch(func() bool { return true })
	h.run(context.Background())

	h.waitState(StateConnected, 1)
	h.session(0).die(client.ErrSilent)
	h.waitState(StateConnected, 2)
	mu.Lock()
	joined := strings.Join(lines, "\n")
	mu.Unlock()
	if !strings.Contains(joined, "tunnel reconnecting — system traffic is blocked (kill-switch)") {
		t.Errorf("Logf did not get the down line: %q", joined)
	}
}

// A failed Start closes the session, does not count as Connected, is reported with its
// cause in Reconnecting and retried after the usual delay with the same resolver first;
// the resolver is not penalized. With the fault cleared the session connects.
func TestDatapathStartFailure(t *testing.T) {
	h := newHarness(t, []string{r1, r2}, nil)
	dp := &fakeDatapath{}
	startErr := errors.New("adapter refused")
	dp.setStartErr(startErr)
	h.tun.SetDatapath(dp)

	// The delay is held open: the Connected of the second session must not appear until
	// the fault is cleared.
	release := make(chan struct{})
	h.sleep = func(ctx context.Context, _ time.Duration) bool {
		select {
		case <-release:
		case <-ctx.Done():
			return false
		}
		dp.setStartErr(nil)
		return true
	}

	h.run(context.Background())
	tr := h.waitState(StateReconnecting, 1)
	if tr.Cause == nil || !errors.Is(tr.Cause, startErr) {
		t.Errorf("Reconnecting cause = %v, want %v", tr.Cause, startErr)
	}
	if tr.Resolver != r1 || tr.Delay != time.Second {
		t.Errorf("Reconnecting = %+v, want %s after 1s", tr, r1)
	}
	if !h.session(0).closed.Load() {
		t.Error("the session with the failed Start is left open")
	}
	if got := h.dialList(); !slices.Equal(got, []string{r1}) {
		t.Errorf("dials %q, want no new dial before the delay ends", got)
	}
	h.mu.Lock()
	for _, other := range h.trans {
		if other.To == StateConnected {
			t.Error("Connected was reported for a session whose Start failed")
		}
	}
	h.mu.Unlock()

	close(release)
	h.waitState(StateConnected, 1)
	if got := h.dialList(); !slices.Equal(got, []string{r1, r1}) {
		t.Errorf("dials %q, want the same resolver first", got)
	}
	if got := h.sleepList(); !slices.Equal(got, []time.Duration{time.Second}) {
		t.Errorf("delays %v, want the first backoff 1s", got)
	}
	if _, aside := h.tun.opts.Resolvers.Aside(0); aside {
		t.Error("a failed datapath start penalized the resolver")
	}
	// One failed Start, one Start that went through, one Suspend at the stop.
	h.wait("the datapath lifecycle", func() bool {
		return len(dp.snapshot()) == 2
	})
	if err := h.stop(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"Start", "Start", "Suspend"}; !slices.Equal(dp.snapshot(), want) {
		t.Errorf("calls %v, want %v", dp.snapshot(), want)
	}
}

// The datapath is not started without a session: neither a failed dial nor a failed exit
// check may put the system traffic into an adapter that serves nothing.
func TestDatapathNeverStartsWithoutASession(t *testing.T) {
	t.Run("dial fails", func(t *testing.T) {
		h := newHarness(t, []string{r1}, nil)
		dp := &fakeDatapath{}
		h.tun.SetDatapath(dp)
		h.dial = func(string, int) error { return errDial }
		h.run(context.Background())
		var err error
		select {
		case err = <-h.result:
			h.result <- err
		case <-time.After(10 * time.Second):
			t.Fatal("Run did not stop")
		}
		var ne *NeverConnectedError
		if !errors.As(err, &ne) {
			t.Fatalf("Run = %v, want NeverConnectedError", err)
		}
		if got := dp.snapshot(); len(got) != 0 {
			t.Errorf("datapath calls %v, want none", got)
		}
	})

	t.Run("exit check fails", func(t *testing.T) {
		h := newHarness(t, []string{r1}, func(o *Options) {
			o.CheckExit = func(context.Context, exit.OpenStream) error {
				return errors.New("exit not ready")
			}
		})
		dp := &fakeDatapath{}
		h.tun.SetDatapath(dp)
		h.sleep = func(ctx context.Context, _ time.Duration) bool {
			<-ctx.Done()
			return false
		}
		h.run(context.Background())
		h.waitState(StateReconnecting, 1)
		if err := h.stop(); err != nil {
			t.Fatal(err)
		}
		if got := dp.snapshot(); len(got) != 0 {
			t.Errorf("datapath calls %v, want none", got)
		}
	})
}

// A Suspend error is logged and does not replace the outcome of the session: the tunnel
// reconnects and the next Start runs.
func TestDatapathSuspendFailure(t *testing.T) {
	var (
		mu    sync.Mutex
		lines []string
	)
	h := newHarness(t, []string{r1}, func(o *Options) {
		o.Logf = func(format string, args ...any) {
			mu.Lock()
			defer mu.Unlock()
			lines = append(lines, fmt.Sprintf(format, args...))
		}
	})
	dp := &fakeDatapath{}
	suspendErr := errors.New("suspend failed")
	dp.setSuspendErr(suspendErr)
	h.tun.SetDatapath(dp)
	h.run(context.Background())

	h.waitState(StateConnected, 1)
	h.session(0).die(client.ErrSilent)
	h.waitState(StateConnected, 2)
	if got := dp.snapshot(); !slices.Equal(got, []string{"Start", "Suspend", "Start"}) {
		t.Fatalf("calls %v, want a Suspend between the two Starts", got)
	}
	mu.Lock()
	joined := strings.Join(lines, "\n")
	mu.Unlock()
	if !strings.Contains(joined, suspendErr.Error()) {
		t.Errorf("the Suspend error was not logged: %q", joined)
	}
	if err := h.stop(); err != nil {
		t.Fatal(err)
	}
}

// Without a way to read the network's DNS, or with none given, the entry fails its dial
// with a reason.
func TestNetworkEntryNoDNS(t *testing.T) {
	for _, tc := range []struct {
		name string
		dns  func() ([]netip.Addr, error)
		want string
	}{
		{"no reader", nil, "cannot be read"},
		{"none", func() ([]netip.Addr, error) { return nil, nil }, "no DNS server"},
		{"error", func() ([]netip.Addr, error) { return nil, errors.New("resolv.conf: missing") }, "resolv.conf: missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, []string{"network"}, func(o *Options) { o.NetworkDNS = tc.dns })
			h.run(context.Background())
			var err error
			select {
			case err = <-h.result:
				h.result <- err
			case <-time.After(10 * time.Second):
				t.Fatal("Run did not stop")
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v", err)
			}
			if got := h.dialList(); len(got) != 0 {
				t.Errorf("dialed %q", got)
			}
		})
	}
}
