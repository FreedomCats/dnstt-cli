package supervisor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/FreedomCats/dnstt-cli/core/client"
	"github.com/FreedomCats/dnstt-cli/internal/exit"
	"github.com/FreedomCats/dnstt-cli/internal/resolvers"
)

const (
	// DefaultHandshakeTimeout is how long a dial waits for the Noise handshake: the
	// server's first answer through the resolver. Preliminary: to be measured against the
	// live server (plan.md S0).
	DefaultHandshakeTimeout = 20 * time.Second
	// DefaultDegradedAfter is how long a session may hear nothing from the server before
	// it counts as degraded. The server sends a smux keepalive every 10 s.
	DefaultDegradedAfter = 25 * time.Second
	// DefaultDrainTimeout is how long a stop waits for open connections (Options.Drain)
	// before it closes the session under them. The second Ctrl+C cuts it short.
	DefaultDrainTimeout = 5 * time.Second
)

// Session is a session of the core: *client.Session.
type Session interface {
	OpenStream() (net.Conn, error)
	Done() <-chan struct{}
	Err() error
	Stats() client.Stats
	Close() error
}

// Options are what a Tunnel is built from.
type Options struct {
	// Session is the configuration of every session: the tunnel, the uTLS distribution,
	// the socket hook, the keepalive timeout, the log. Resolver and LookupIP are set
	// per attempt from Resolvers and Bootstrap.
	Session client.Config
	// Resolvers is the resolver list with its health; required.
	Resolvers *resolvers.Pool
	// Bootstrap resolves the host names of resolvers that are not pinned; nil — such a
	// resolver fails its dial.
	Bootstrap *resolvers.Bootstrap
	// NetworkDNS gives the DNS servers the network gives the system now
	// (linuxsys.NetworkDNS, winsys.NetworkDNS), for the entry "network"
	// (resolvers.NetworkEntry); every dial through it reads them again, so a new network
	// is taken up by itself. nil — the entry fails its dial.
	NetworkDNS func() ([]netip.Addr, error)
	// CheckExit checks the exit through a new session before it counts as connected
	// (exit.SOCKS5.Check). A login refusal (exit.IsLogin) and an exit that is not
	// SOCKS5 (exit.ErrNotSOCKS5) stop the tunnel; any other error is a retry. nil — no
	// check.
	CheckExit func(ctx context.Context, open exit.OpenStream) error
	// Drain is called once on a stop while a session is up, before it is closed: the
	// datapath stops taking connections and waits for the open ones until ctx ends —
	// after DrainTimeout or at the second Ctrl+C (WithAbort). nil — the session is closed
	// at once.
	Drain func(ctx context.Context)
	// OnTransition receives every change of state. nil — silent. It and Logf are called
	// from one goroutine at a time.
	OnTransition func(Transition)
	// Logf receives the lines of the supervisor: why an attempt failed, that the network
	// changed. nil — silent.
	Logf func(format string, args ...any)

	// HandshakeTimeout, DegradedAfter, DrainTimeout: 0 is the Default.
	HandshakeTimeout time.Duration
	DegradedAfter    time.Duration
	DrainTimeout     time.Duration
}

// ErrStopped is the error of OpenStream once the tunnel is stopping.
var ErrStopped = errors.New("the tunnel is stopping")

// ErrNoSession is wrapped by the error of OpenStream when its ctx ended before a session
// came up.
var ErrNoSession = errors.New("no session to the server")

// ResolverFailure is a failed dial through one resolver.
type ResolverFailure struct {
	Resolver string
	Err      error
}

// NeverConnectedError: no resolver completed a handshake on the first round of a connect.
// The reason cannot be told apart from here (architecture.md §08), so it names all four.
type NeverConnectedError struct {
	Failures []ResolverFailure
}

func (e *NeverConnectedError) Error() string {
	var b strings.Builder
	b.WriteString("no resolver completed a handshake with the server: the tunnel domain or " +
		"the server key is wrong, the server is down, or every resolver is blocked in this network")
	for _, f := range e.Failures {
		fmt.Fprintf(&b, "\n  %s: %v", f.Resolver, f.Err)
	}
	return b.String()
}

// Status is a snapshot for the status line.
type Status struct {
	State State
	// Resolver is the resolver of the attempt or the session, by Name.
	Resolver string
	// Since is when the session came up; zero without one.
	Since time.Time
	// Handshake is how long the dial of the session took.
	Handshake time.Duration
	// Stats are the counters of the session.
	Stats client.Stats
}

// Tunnel keeps sessions to the server up for one connect. Run is called once.
type Tunnel struct {
	opts Options

	// dial, sleep and now are replaced in tests.
	dial  func(ctx context.Context, cfg client.Config) (Session, error)
	sleep func(ctx context.Context, d time.Duration) bool
	now   func() time.Time

	backoff     backoff // the Run goroutine only
	networkTurn int     // the next of the network's DNS servers; the Run goroutine only
	// networkCount is how many DNS servers the network gave at the last read; the Run
	// goroutine only.
	networkCount int
	running      atomic.Bool

	// restart is a request of Restart not carried out yet. One is enough: a restart asked
	// for twice is still one.
	restart chan error

	mu        sync.Mutex
	state     State
	resolver  string
	live      *live         // the session that is up; nil between sessions
	ready     chan struct{} // closed when the next session is up
	since     time.Time
	handshake time.Duration

	stopping     chan struct{} // closed when the tunnel starts to stop
	stoppingOnce sync.Once
}

// live is a session that is up; ended is closed when Run lets it go.
type live struct {
	s     Session
	ended chan struct{}
}

// New builds a tunnel. Nothing is dialed before Run.
func New(opts Options) (*Tunnel, error) {
	if opts.Resolvers == nil || opts.Resolvers.Len() == 0 {
		return nil, errors.New("supervisor: no resolvers")
	}
	if opts.HandshakeTimeout <= 0 {
		opts.HandshakeTimeout = DefaultHandshakeTimeout
	}
	if opts.DegradedAfter <= 0 {
		opts.DegradedAfter = DefaultDegradedAfter
	}
	if opts.DrainTimeout <= 0 {
		opts.DrainTimeout = DefaultDrainTimeout
	}
	return &Tunnel{
		opts: opts,
		dial: func(ctx context.Context, cfg client.Config) (Session, error) {
			s, err := client.Dial(ctx, cfg)
			if err != nil {
				return nil, err
			}
			return s, nil
		},
		sleep:    sleepCtx,
		now:      time.Now,
		restart:  make(chan error, 1),
		state:    StateIdle,
		ready:    make(chan struct{}),
		stopping: make(chan struct{}),
	}, nil
}

// Restart ends the session or the attempt in progress with reason and dials again at once,
// from the top of the list, with the backoff and the health of the resolvers cleared. It
// cuts short a delay in progress. Called from any goroutine: on a network change
// (netguard.Watch).
func (t *Tunnel) Restart(reason error) {
	select {
	case t.restart <- reason:
	default:
		// A request is already waiting.
	}
}

// State is the current state.
func (t *Tunnel) State() State {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

// Status is a snapshot of the tunnel.
func (t *Tunnel) Status() Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := Status{State: t.state, Resolver: t.resolver}
	if t.live != nil {
		st.Since, st.Handshake, st.Stats = t.since, t.handshake, t.live.s.Stats()
	}
	return st
}

// OpenStream opens a stream to the exit through the session that is up. Without one it
// waits for the next session until ctx ends (an error wrapping ErrNoSession and the
// error of ctx). Once the tunnel is stopping — ErrStopped. It is an exit.OpenStream.
func (t *Tunnel) OpenStream(ctx context.Context) (net.Conn, error) {
	for {
		select {
		case <-t.stopping:
			return nil, ErrStopped
		default:
		}
		t.mu.Lock()
		l, ready := t.live, t.ready
		t.mu.Unlock()
		if l != nil {
			stream, err := l.s.OpenStream()
			if err == nil {
				return stream, nil
			}
			// The session is dead, and Run is about to let it go.
			ready = l.ended
		}
		select {
		case <-ready:
		case <-t.stopping:
			return nil, ErrStopped
		case <-ctx.Done():
			return nil, fmt.Errorf("%w: %w", ErrNoSession, context.Cause(ctx))
		}
	}
}

// Run keeps the tunnel up until ctx ends or until going on is pointless.
//
// The end of ctx is no error: nil. An error only when Run ends by itself: no resolver
// completed a handshake on the first round (*NeverConnectedError), the exit refused the
// login (exit.IsLogin) or is not a SOCKS5 server (exit.ErrNotSOCKS5). Every other failure
// leads to the next attempt.
//
// If ctx comes from WithAbort (Interrupt), the second Ctrl+C cuts the stop short: Drain is
// not waited for. On return the session is closed and the state is StateStopping.
func (t *Tunnel) Run(ctx context.Context) (err error) {
	if !t.running.CompareAndSwap(false, true) {
		return errors.New("supervisor: Run called twice")
	}
	aborted := make(chan struct{})
	var abortOnce sync.Once
	defer OnAbort(ctx, func() { abortOnce.Do(func() { close(aborted) }) })()
	defer func() { t.stop(err) }()

	pool := t.opts.Resolvers
	prev := -1                  // the resolver dialed last; Next goes on after it
	var round []ResolverFailure // dials failed in a row since the last session
	heard := false              // a handshake completed in this Run
	for {
		if ctx.Err() != nil {
			return nil
		}
		i := pool.Next(prev)
		r := pool.Resolver(i)
		prev = i
		t.set(StateDialing, r.Name(), nil, 0)

		wctx, unwatch := t.watchRestart(ctx)
		s, name, took, err := t.dialOnce(wctx, r)
		if err == nil {
			heard = true
			err = t.checkExit(wctx, s)
		}
		reason, restarted := unwatch()
		switch {
		case ctx.Err() != nil:
			closeSession(s)
			return nil
		case restarted:
			closeSession(s)
			t.restarted(reason)
			prev, round = -1, nil
			continue
		case s == nil:
			// The dial failed: the next resolver at once, a delay after a full round.
			pool.HandshakeFailed(i)
			round = append(round, ResolverFailure{name, err})
			t.logf("%s: %v", name, err)
			if len(round) < t.roundSize() {
				continue
			}
			if !heard {
				return &NeverConnectedError{Failures: round}
			}
			round = nil
			cause := fmt.Errorf("no resolver of %d completed a handshake", pool.Len())
			if t.pause(ctx, name, cause, t.backoff.next()) {
				prev = -1
			}
			continue
		case err != nil:
			// The handshake went through, the exit did not.
			s.Close()
			round = nil
			if exit.IsLogin(err) || errors.Is(err, exit.ErrNotSOCKS5) {
				return err
			}
			prev = i - 1 // the same resolver first: it is not to blame
			if t.pause(ctx, name, err, t.backoff.next()) {
				prev = -1
			}
			continue
		}

		round = nil
		end := t.serve(ctx, name, s, took)
		switch {
		case end.stop:
			t.beginStop()
			t.drain(aborted)
			s.Close()
			return nil
		case end.restarted:
			s.Close()
			t.set(StateReconnecting, name, end.reason, 0)
			t.restarted(end.reason)
			prev = -1
		default:
			s.Close()
			pool.SessionEnded(i, end.lived)
			var delay time.Duration
			if end.lived >= resolvers.ShortSession {
				t.backoff.reset()
			} else {
				delay = t.backoff.next()
			}
			prev = i - 1 // the same resolver first, unless it is put aside now
			if t.pause(ctx, name, end.reason, delay) {
				prev = -1
			}
		}
	}
}

// dialOnce dials a session through r and waits for its handshake. name is how the
// resolver is shown: its Name, and for the network entry with the server dialed.
func (t *Tunnel) dialOnce(ctx context.Context, r resolvers.Resolver) (s Session, name string, took time.Duration, err error) {
	name = r.Name()
	if r.Network {
		var server netip.Addr
		if server, err = t.networkServer(); err != nil {
			return nil, name, 0, err
		}
		r = resolvers.NetworkResolver(server)
		name = fmt.Sprintf("%s (%s)", resolvers.NetworkEntry, server)
	}
	cfg := t.opts.Session
	cfg.Resolver = r.Core()
	var fallback resolvers.LookupFunc
	if t.opts.Bootstrap != nil {
		fallback = t.opts.Bootstrap.LookupIP
	}
	cfg.LookupIP = r.LookupIP(fallback)

	hctx, cancel := context.WithTimeout(ctx, t.opts.HandshakeTimeout)
	defer cancel()
	start := t.now()
	s, err = t.dial(hctx, cfg)
	took = t.now().Sub(start)
	if err != nil {
		if ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("no answer from the server within %v", t.opts.HandshakeTimeout)
		}
		return nil, name, took, err
	}
	return s, name, took, nil
}

// networkServer is the server for the next dial through the network entry: the network's
// DNS servers in turn, one per dial, so that a second server is tried when the first fails.
func (t *Tunnel) networkServer() (netip.Addr, error) {
	if t.opts.NetworkDNS == nil {
		return netip.Addr{}, errors.New("the network's DNS cannot be read here")
	}
	servers, err := t.opts.NetworkDNS()
	if err != nil {
		return netip.Addr{}, fmt.Errorf("the network's DNS: %w", err)
	}
	if len(servers) == 0 {
		return netip.Addr{}, errors.New("the network gave the system no DNS server")
	}
	t.networkCount = len(servers)
	server := servers[t.networkTurn%len(servers)]
	t.networkTurn++
	return server, nil
}

// roundSize is how many failed dials in a row make a full round: one per resolver, and for
// the network entry one per server the network gave at the last read.
func (t *Tunnel) roundSize() int {
	pool := t.opts.Resolvers
	n := pool.Len()
	for i := range n {
		if pool.Resolver(i).Network {
			n += max(t.networkCount, 1) - 1
		}
	}
	return n
}

// checkExit checks the exit through s (Options.CheckExit).
func (t *Tunnel) checkExit(ctx context.Context, s Session) error {
	if t.opts.CheckExit == nil {
		return nil
	}
	open := func(context.Context) (net.Conn, error) { return s.OpenStream() }
	return t.opts.CheckExit(ctx, open)
}

// end is how a session ended: stop — ctx ended; restarted — Restart with reason;
// otherwise it died by itself with reason after it lived for lived.
type end struct {
	stop, restarted bool
	reason          error
	lived           time.Duration
}

// serve publishes s for OpenStream and watches it until it dies, Restart or the end of
// ctx, moving between StateConnected and StateDegraded on the data from the server.
func (t *Tunnel) serve(ctx context.Context, name string, s Session, took time.Duration) end {
	l := &live{s: s, ended: make(chan struct{})}
	start := t.now()
	t.mu.Lock()
	t.live, t.since, t.handshake = l, start, took
	close(t.ready)
	t.mu.Unlock()
	t.set(StateConnected, name, nil, 0)
	defer func() {
		t.mu.Lock()
		t.live = nil
		t.ready = make(chan struct{})
		t.mu.Unlock()
		close(l.ended)
	}()

	tick := time.NewTicker(t.opts.DegradedAfter / 5)
	defer tick.Stop()
	received, heardAt := s.Stats().BytesReceived, start

	wctx, unwatch := t.watchRestart(ctx)
	for {
		select {
		case <-s.Done():
			if reason, restarted := unwatch(); restarted {
				return end{restarted: true, reason: reason}
			}
			return end{reason: s.Err(), lived: t.now().Sub(start)}
		case <-wctx.Done():
			if reason, restarted := unwatch(); restarted {
				return end{restarted: true, reason: reason}
			}
			return end{stop: true}
		case <-tick.C:
			now := t.now()
			if n := s.Stats().BytesReceived; n != received {
				received, heardAt = n, now
				if t.State() == StateDegraded {
					t.set(StateConnected, name, nil, 0)
				}
			} else if now.Sub(heardAt) >= t.opts.DegradedAfter && t.State() == StateConnected {
				t.set(StateDegraded, name, ErrNoData, 0)
			}
		}
	}
}

// pause waits delay before the next attempt in StateReconnecting. Restart cuts it short;
// then it clears the backoff and the health and reports true.
func (t *Tunnel) pause(ctx context.Context, name string, cause error, delay time.Duration) bool {
	t.set(StateReconnecting, name, cause, delay)
	if cause != nil {
		t.logf("%s: %v", name, cause)
	}
	wctx, unwatch := t.watchRestart(ctx)
	t.sleep(wctx, delay)
	reason, restarted := unwatch()
	if restarted && ctx.Err() == nil {
		t.restarted(reason)
		return true
	}
	return false
}

// restarted carries out Restart: a new network — new counts.
func (t *Tunnel) restarted(reason error) {
	if reason != nil {
		t.logf("restarting: %v", reason)
	}
	t.backoff.reset()
	t.opts.Resolvers.Reset()
	if t.opts.Bootstrap != nil {
		t.opts.Bootstrap.Forget()
	}
}

// watchRestart returns a context that ends with ctx or with Restart, and unwatch, which
// stops watching and reports whether Restart came, with its reason. A Restart that came
// after unwatch waits for the next watch.
func (t *Tunnel) watchRestart(ctx context.Context) (context.Context, func() (error, bool)) {
	wctx, cancel := context.WithCancel(ctx)
	type wakeup struct {
		reason    error
		restarted bool
	}
	woken := make(chan wakeup, 1)
	done := make(chan struct{})
	go func() {
		select {
		case reason := <-t.restart:
			woken <- wakeup{reason, true}
			cancel()
		case <-done:
			woken <- wakeup{}
		}
	}()
	var once sync.Once
	var w wakeup
	return wctx, func() (error, bool) {
		once.Do(func() {
			close(done)
			w = <-woken
			cancel()
		})
		return w.reason, w.restarted
	}
}

// drain lets the datapath finish the open connections (Options.Drain) until
// DrainTimeout or the second Ctrl+C.
func (t *Tunnel) drain(aborted <-chan struct{}) {
	if t.opts.Drain == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), t.opts.DrainTimeout)
	defer cancel()
	go func() {
		select {
		case <-aborted:
			cancel()
		case <-ctx.Done():
		}
	}()
	t.opts.Drain(ctx)
}

// beginStop refuses new streams: the tunnel is stopping.
func (t *Tunnel) beginStop() {
	t.stoppingOnce.Do(func() { close(t.stopping) })
	t.set(StateStopping, "", nil, 0)
}

// stop is the end of Run.
func (t *Tunnel) stop(err error) {
	t.stoppingOnce.Do(func() { close(t.stopping) })
	t.set(StateStopping, "", err, 0)
}

// set changes the state and reports the transition. A move into the same state is no
// transition, except a new resolver while dialing.
func (t *Tunnel) set(to State, resolver string, cause error, delay time.Duration) {
	t.mu.Lock()
	from, was := t.state, t.resolver
	if from == to && (to != StateDialing || was == resolver) {
		t.mu.Unlock()
		return
	}
	t.state = to
	if resolver != "" {
		t.resolver = resolver
	}
	t.mu.Unlock()
	if t.opts.OnTransition != nil {
		t.opts.OnTransition(Transition{From: from, To: to, Resolver: resolver, Cause: cause, Delay: delay})
	}
}

func (t *Tunnel) logf(format string, args ...any) {
	if t.opts.Logf != nil {
		t.opts.Logf(format, args...)
	}
}

func closeSession(s Session) {
	if s != nil {
		s.Close()
	}
}

// sleepCtx waits d or the end of ctx. false — the wait was cut short.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
