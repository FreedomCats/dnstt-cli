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
	// server's first answer through the resolver. 10 s is about 5.5x the slowest measured
	// fresh handshake (~1.8 s), which leaves room for a cold bootstrap and cache misses;
	// the supervisor's dials and "resolvers check" share this value.
	DefaultHandshakeTimeout = 10 * time.Second
	// DefaultKeepAliveTimeout is how long a session lives without data from the server
	// (client.Config.KeepAliveTimeout). 30 s is 2.5x the worst measured healthy-silence
	// bound (~12 s across the measured transports); the server's progress arrives about
	// every 10 s. smux sends its keepalive every min(10 s, timeout/2) = 10 s, but checks
	// the timeout on its own 30 s period, so silence is declared dead one to two periods
	// (30–60 s) after the last data.
	DefaultKeepAliveTimeout = 30 * time.Second
	// DefaultDegradedAfter is how long a session may hear nothing from the server before
	// it counts as degraded. Retained: above the worst measured healthy silence (~12 s)
	// and below the 30 s keepalive, so the status shows "degraded" before the session
	// is declared dead — nominally 5 s before the earliest detection, less the poll step.
	// Data from the server restores Connected; serve polls every fifth of it.
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

// Datapath is the part of a datapath that goes with a session: the tun adapter. Start is
// called for every session that passed the exit check, Suspend after every session, so the
// routes and the system DNS never lead into an adapter that is not serving. The socks
// datapath has no such part.
type Datapath interface {
	Start() error
	Suspend() error
}

// Options are what a Tunnel is built from.
type Options struct {
	// Session is the configuration of every session: the tunnel, the uTLS distribution,
	// the socket hook, the keepalive timeout (the caller sets it; there is no supervisor
	// default), the log. Resolver and LookupIP are set per attempt from Resolvers and
	// Bootstrap.
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
	// Notice receives the user-facing lines about system traffic between sessions; nil —
	// Logf.
	Notice func(format string, args ...any)

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

	mu         sync.Mutex
	state      State
	resolver   string
	datapath   Datapath              // set once before Run; nil — the datapath needs no session part
	drainFn    func(context.Context) // set once before Run; nil — Options.Drain
	killSwitch func() bool           // set once before Run; nil — no kill-switch
	live       *live                 // the session that is up; nil between sessions
	ready      chan struct{}         // closed when the next session is up
	since      time.Time
	handshake  time.Duration
	fatalErr   error              // the first fatal exit failure; protected by mu
	cancelRun  context.CancelFunc // interrupts dial, check, serve and backoff

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

// SetDatapath gives Run the part of the datapath that goes with a session (the tun
// adapter). It is called once before Run: the command builds the datapath after the
// tunnel, because the datapath needs the tunnel's exit.
func (t *Tunnel) SetDatapath(dp Datapath) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.datapath = dp
}

// sessionDatapath is SetDatapath's value for Run, read under mu.
func (t *Tunnel) sessionDatapath() Datapath {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.datapath
}

// SetDrain gives Run the hook that stops the datapath and waits for its open connections
// (Options.Drain). It is called once before Run, like SetDatapath: the socks datapath is
// built after the tunnel, and its listener is already up by then.
func (t *Tunnel) SetDrain(drain func(context.Context)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.drainFn = drain
}

// sessionDrain is the stop hook of Run: SetDrain's value, or Options.Drain, read under mu.
func (t *Tunnel) sessionDrain() func(context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.drainFn != nil {
		return t.drainFn
	}
	return t.opts.Drain
}

// SetKillSwitch gives Run the state of the kill-switch: true while system traffic is
// closed between sessions. It is called once before Run, like SetDatapath: the protection
// is opened after the tunnel is built.
func (t *Tunnel) SetKillSwitch(killSwitch func() bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.killSwitch = killSwitch
}

// killSwitchOn is SetKillSwitch's value for Run, read under mu; false without one.
func (t *Tunnel) killSwitchOn() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.killSwitch != nil && t.killSwitch()
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

// FatalExit reports an exit failure that another resolver cannot fix. The first error
// wins. It refuses new streams and interrupts Run; only Run changes state and shuts down
// the session. Calls after stopping, and nil errors, have no effect.
func (t *Tunnel) FatalExit(err error) {
	if err == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.fatalErr != nil || t.state == StateStopping {
		return
	}
	t.fatalErr = err
	t.stoppingOnce.Do(func() { close(t.stopping) })
	if t.cancelRun != nil {
		t.cancelRun()
	}
}

func (t *Tunnel) fatalCause() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.fatalErr
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
			stream, err := t.openSession(ctx, l.s, l.ended)
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

// openSession bounds smux acquisition, which has no context API. An abandoned result is
// closed even if OpenStream completes after cancellation or after the session was replaced.
func (t *Tunnel) openSession(ctx context.Context, s Session, ended <-chan struct{}) (net.Conn, error) {
	select {
	case <-t.stopping:
		return nil, ErrStopped
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: %w", ErrNoSession, context.Cause(ctx))
	case <-ended:
		return nil, net.ErrClosed
	default:
	}
	type result struct {
		conn net.Conn
		err  error
	}
	opened := make(chan result)
	abandoned := make(chan struct{})
	defer close(abandoned)
	go func() {
		conn, err := s.OpenStream()
		select {
		case opened <- result{conn, err}:
		case <-abandoned:
			if conn != nil {
				conn.Close()
			}
		}
	}()
	var res result
	select {
	case res = <-opened:
	case <-t.stopping:
		return nil, ErrStopped
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: %w", ErrNoSession, context.Cause(ctx))
	case <-ended:
		return nil, net.ErrClosed
	}
	select {
	case <-t.stopping:
		res.err = ErrStopped
	case <-ctx.Done():
		res.err = fmt.Errorf("%w: %w", ErrNoSession, context.Cause(ctx))
	case <-ended:
		res.err = net.ErrClosed
	default:
	}
	if res.err != nil && res.conn != nil {
		go res.conn.Close()
		res.conn = nil
	}
	return res.conn, res.err
}

// Run keeps the tunnel up until ctx ends or until going on is pointless.
//
// The end of ctx is no error: nil. An error only when Run ends by itself: no resolver
// completed a handshake on the first round (*NeverConnectedError), the exit refused the
// login (exit.IsLogin), is not a SOCKS5 server (exit.ErrNotSOCKS5), or FatalExit was called.
// Every other session failure leads to the next attempt.
//
// If ctx comes from WithAbort (Interrupt), the second Ctrl+C cuts the stop short: Drain is
// not waited for. On return the session is closed and the state is StateStopping.
func (t *Tunnel) Run(ctx context.Context) (err error) {
	if !t.running.CompareAndSwap(false, true) {
		return errors.New("supervisor: Run called twice")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	t.mu.Lock()
	t.cancelRun = cancel
	if t.fatalErr != nil {
		cancel()
	}
	t.mu.Unlock()
	aborted := make(chan struct{})
	var abortOnce sync.Once
	defer OnAbort(ctx, func() { abortOnce.Do(func() { close(aborted) }) })()
	defer func() { err = t.stop(err) }()

	pool := t.opts.Resolvers
	prev := -1                  // the resolver dialed last; Next goes on after it
	var round []ResolverFailure // dials failed in a row since the last session
	heard := false              // a handshake completed in this Run
	routed := false             // a session has been routed into the datapath
	bypassed := false           // the last Suspend left system traffic out of the tunnel
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
		case t.fatalCause() != nil:
			closeSession(s)
			return t.fatalCause()
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
			t.mu.Lock()
			if t.fatalErr != nil {
				t.mu.Unlock()
				return t.fatalCause()
			}
			pool.HandshakeFailed(i)
			t.mu.Unlock()
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
			// The handshake went through, the exit did not. The same goes for a failed
			// Start of the datapath below: the exit is not to blame for the local
			// adapter.
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

		// The session is accepted and the exit is known to serve: only now may the
		// system traffic lead into the datapath. A failed Start is an attempt failure,
		// not a session one: the session is closed and tries again after the usual
		// delay, with the resolver not blamed.
		if dp := t.sessionDatapath(); dp != nil {
			if err := dp.Start(); err != nil {
				s.Close()
				round = nil
				prev = i - 1
				if t.pause(ctx, name, err, t.backoff.next()) {
					prev = -1
				}
				continue
			}
			if bypassed {
				t.noticef("tunnel up again — system traffic goes through it")
				bypassed = false
			}
			routed = true
		}

		round = nil
		end := t.serve(ctx, name, s, took)
		// Every path out of serve had a session: take the system traffic out of the
		// datapath before the session is closed. An error here is the datapath's own
		// and does not replace the outcome of the session; Close retries it.
		if dp := t.sessionDatapath(); dp != nil {
			if err := dp.Suspend(); err != nil {
				t.logf("%s: datapath: %v", name, err)
			}
		}
		switch {
		case end.stop || t.fatalCause() != nil:
			err := t.stop(nil)
			t.drain(aborted)
			s.Close()
			return err
		case end.restarted:
			s.Close()
			if routed {
				t.noticeDown()
				bypassed = true
			}
			t.set(StateReconnecting, name, end.reason, 0)
			t.restarted(end.reason)
			prev = -1
		default:
			s.Close()
			t.mu.Lock()
			if t.fatalErr != nil {
				t.mu.Unlock()
				return t.fatalCause()
			}
			pool.SessionEnded(i, end.lived)
			t.mu.Unlock()
			if routed {
				t.noticeDown()
				bypassed = true
			}
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
	if err := ctx.Err(); err != nil {
		return nil, name, 0, err
	}
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
	if err := hctx.Err(); err != nil {
		return nil, name, 0, err
	}
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
	if err := ctx.Err(); err != nil {
		return err
	}
	open := func(ctx context.Context) (net.Conn, error) { return t.openSession(ctx, s, s.Done()) }
	checked := make(chan error, 1)
	go func() { checked <- t.opts.CheckExit(ctx, open) }()
	select {
	case err := <-checked:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
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
	if t.fatalErr != nil || ctx.Err() != nil {
		t.mu.Unlock()
		return end{stop: true}
	}
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
	if ctx.Err() != nil || t.fatalCause() != nil {
		return false
	}
	t.set(StateReconnecting, name, cause, delay)
	if cause != nil {
		t.logf("%s: %v", name, cause)
	}
	wctx, unwatch := t.watchRestart(ctx)
	if wctx.Err() == nil {
		t.sleep(wctx, delay)
	}
	reason, restarted := unwatch()
	if restarted && ctx.Err() == nil {
		t.restarted(reason)
		return true
	}
	return false
}

// restarted carries out Restart: a new network — new counts.
func (t *Tunnel) restarted(reason error) {
	t.mu.Lock()
	if t.fatalErr != nil {
		t.mu.Unlock()
		return
	}
	t.backoff.reset()
	t.opts.Resolvers.Reset()
	if t.opts.Bootstrap != nil {
		t.opts.Bootstrap.Forget()
	}
	t.mu.Unlock()
	if reason != nil {
		t.logf("restarting: %v", reason)
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

// drain lets the datapath finish the open connections (Options.Drain, or SetDrain) until
// DrainTimeout or the second Ctrl+C.
func (t *Tunnel) drain(aborted <-chan struct{}) {
	drain := t.sessionDrain()
	if drain == nil {
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
	drain(ctx)
}

// stop commits the stopping cause before draining. FatalExit and cancellation are ordered
// by mu, so a fatal error already reported wins in both the transition and Run's result.
func (t *Tunnel) stop(err error) error {
	t.mu.Lock()
	if t.fatalErr != nil {
		err = t.fatalErr
	}
	from := t.state
	t.state = StateStopping
	t.stoppingOnce.Do(func() { close(t.stopping) })
	t.mu.Unlock()
	if from != StateStopping && t.opts.OnTransition != nil {
		t.opts.OnTransition(Transition{From: from, To: StateStopping, Cause: err})
	}
	return err
}

// set changes the state and reports the transition. A move into the same state is no
// transition, except a new resolver while dialing.
func (t *Tunnel) set(to State, resolver string, cause error, delay time.Duration) {
	t.mu.Lock()
	if t.fatalErr != nil {
		t.mu.Unlock()
		return
	}
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

// noticef is a user-facing line: Options.Notice, or Logf when it is nil.
func (t *Tunnel) noticef(format string, args ...any) {
	f := t.opts.Notice
	if f == nil {
		f = t.opts.Logf
	}
	if f != nil {
		f(format, args...)
	}
}

// noticeDown says where system traffic goes while the tunnel is reconnecting. The caller
// has a routed session: it sets bypassed, and the next successful Start prints the up
// line.
func (t *Tunnel) noticeDown() {
	if t.killSwitchOn() {
		t.noticef("tunnel reconnecting — system traffic is blocked (kill-switch)")
	} else {
		t.noticef("tunnel reconnecting — system traffic goes past the tunnel (kill-switch off)")
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
