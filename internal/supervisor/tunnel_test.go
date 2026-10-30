package supervisor

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"slices"
	"strings"
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
