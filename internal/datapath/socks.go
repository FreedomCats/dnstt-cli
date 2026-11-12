package datapath

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/FreedomCats/dnstt-cli/internal/exit"
)

// DefaultSocksListen is where the SOCKS5 datapath listens unless told otherwise.
const DefaultSocksListen = "127.0.0.1:1080"

const (
	// handshakeTimeout bounds the greeting and the request of a program: a client that
	// went silent does not hold a connection forever.
	handshakeTimeout = 10 * time.Second
	// dialTimeout bounds the wait for the exit to connect to the target. Through DNS
	// every message of the exchange is a round trip, so it is generous; it is not longer
	// than the exit's own limit (exit.SOCKS5, 60 s).
	dialTimeout = 60 * time.Second
)

// SocksOptions are the settings of the SOCKS5 datapath.
type SocksOptions struct {
	// Listen is the SOCKS5 address; only loopback: there is no password on the local
	// side, and the tunnel would be open to the whole network. "" — DefaultSocksListen.
	Listen string

	// Dialer is the exit: every CONNECT becomes one connection through it. Required.
	Dialer exit.Dialer

	// Up answers whether a session to the server is up right now. It is called before
	// Dialer.Dial, and "no" is refused at once with "network unreachable": a program
	// falls back instead of waiting in a queue while the tunnel is down. nil — no check,
	// every request goes to the exit, which waits for the session itself.
	//
	// The datapath knows nothing of the tunnel, so the caller passes its own view of it
	// (a supervisor tunnel is up in StateConnected and StateDegraded) as a func. Several
	// connections call it at once, so it must not block.
	Up func() bool

	// Logf receives the lines of the datapath: a connection opened, a request refused.
	// nil — silent.
	Logf func(format string, args ...any)
}

// Socks is the datapath with no privileges: a SOCKS5 server on loopback that turns a
// program's proxy setting into a stream through the exit (architecture.md §05, R6). It
// carries TCP only, and only for the programs that can be pointed at a proxy.
//
// New opens the listener at once, and the listener lives as long as the run, not as long
// as one session: a program can be pointed at it while the tunnel is still dialing, and a
// busy port is found out before any connection to the server.
type Socks struct {
	opts SocksOptions
	ln   net.Listener

	ctx    context.Context // ends with Close
	cancel context.CancelFunc
	wg     sync.WaitGroup // the accept loop and one goroutine for each connection
	// handshakes counts accepted connections until their SOCKS5 reply has been sent.
	handshakes sync.WaitGroup

	mu      sync.Mutex
	conns   map[net.Conn]net.Conn // the program's side of each connection, to its stream
	down    bool                  // no more accepted connections
	closing bool                  // Close is forcing all connections to end
	// draining is set by Drain: accepted connections may no longer become relays.
	draining bool
	// relays counts established connections; Drain waits for them. A pending handshake
	// is Shutdown's business and must not delay a fatal stop.
	relays    int
	relayDone chan struct{} // closed when relays drops to zero; nil while none
}

// NewSocks opens the listener. A busy port or an address that is not loopback is an error
// here, before anything else of the run starts.
func NewSocks(opts SocksOptions) (*Socks, error) {
	if opts.Dialer == nil {
		return nil, errors.New("socks: no exit dialer")
	}
	if opts.Listen == "" {
		opts.Listen = DefaultSocksListen
	}
	ap, err := netip.ParseAddrPort(opts.Listen)
	if err != nil {
		return nil, fmt.Errorf("socks: listen address %q is not an IP:port", opts.Listen)
	}
	if !ap.Addr().Unmap().IsLoopback() {
		return nil, fmt.Errorf("socks: listen address %v is not loopback: a SOCKS5 without a password would open the tunnel to the whole network", ap)
	}
	ln, err := net.Listen("tcp", ap.String())
	if err != nil {
		return nil, fmt.Errorf("socks: %w", err)
	}
	s := &Socks{
		opts:  opts,
		ln:    ln,
		conns: make(map[net.Conn]net.Conn),
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.wg.Add(1)
	go s.accept()
	s.logf("[SOCKS] listening on %v", ln.Addr())
	return s, nil
}

// Requires: nothing, as the zero value of Requirements says. Socks runs as an ordinary
// user and sends nothing of the system into the tunnel by itself.
func (s *Socks) Requires() Requirements { return Requirements{} }

// ListenAddr is the address of the listener: the one asked for, unless the port was 0.
func (s *Socks) ListenAddr() net.Addr { return s.ln.Addr() }

// Close stops the listener, closes the connections in progress and waits for their
// goroutines to end. Repeated calls are harmless.
func (s *Socks) Close() error {
	s.mu.Lock()
	s.down = true
	s.closing = true
	s.mu.Unlock()
	err := s.ln.Close()
	s.cancel()
	s.closeConns()
	s.wg.Wait()
	if errors.Is(err, net.ErrClosed) {
		err = nil
	}
	return err
}

// Shutdown stops accepting connections and lets accepted handshakes send their replies
// until ctx ends. It then calls Close, including for established relays. Close may be
// called concurrently to force shutdown at once. The caller must bound ctx.
func (s *Socks) Shutdown(ctx context.Context) error {
	err := s.stopAccepting()
	finished := make(chan struct{})
	go func() { s.handshakes.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-ctx.Done():
		err = ctx.Err()
	case <-s.ctx.Done():
	}
	if closeErr := s.Close(); err == nil {
		err = closeErr
	}
	<-finished
	return err
}

// Drain stops accepting connections and waits until the established ones have finished,
// or ctx ends: the stop of a run lets the relay of an open connection end while the
// session is still up (architecture.md §08). A connection that is still handshaking is
// not waited for — Shutdown finishes those after Run, so a fatal stop is not delayed.
// Repeated and concurrent calls are harmless, and after Close it returns at once. The
// caller bounds ctx.
func (s *Socks) Drain(ctx context.Context) error {
	s.mu.Lock()
	s.down = true
	s.draining = true
	done := s.relayDone
	s.mu.Unlock()
	err := s.closeListener()
	if done == nil {
		return err
	}
	select {
	case <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// stopAccepting refuses new connections and closes the listener. Repeated calls and calls
// after Close are harmless.
func (s *Socks) stopAccepting() error {
	s.mu.Lock()
	s.down = true
	s.mu.Unlock()
	return s.closeListener()
}

// closeListener closes the listener; an already closed one is not an error.
func (s *Socks) closeListener() error {
	err := s.ln.Close()
	if errors.Is(err, net.ErrClosed) {
		err = nil
	}
	return err
}

func (s *Socks) accept() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			if s.closed() {
				return
			}
			// A temporary shortage of descriptors or memory: do not spin.
			s.logf("[SOCKS] accept: %v", err)
			select {
			case <-s.ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		if !s.start(c) {
			return
		}
		go s.serve(c)
	}
}

// start takes an accepted connection in and counts its goroutine in one step: Close sets
// down under the same lock, so no goroutine is added once Close waits for them. false — the
// datapath is closing, and c is closed here.
func (s *Socks) start(c net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.down {
		c.Close()
		return false
	}
	s.conns[c] = c
	s.wg.Add(1)
	s.handshakes.Add(1)
	return true
}

// serve is one SOCKS5 connection, from the greeting to the end of the relay.
func (s *Socks) serve(c net.Conn) {
	defer s.wg.Done()
	defer s.untrack(c, c)
	handshaking := true
	defer func() {
		if handshaking {
			s.handshakes.Done()
		}
	}()
	var target net.Conn
	defer func() {
		if target != nil {
			target.Close()
		}
	}()
	// The deadline of the greeting covers the greeting and the request only: the wait for
	// the exit comes after it and is bounded by the dial's own ctx, and the reply must be
	// written whatever time the exit took.
	_ = c.SetDeadline(time.Now().Add(handshakeTimeout))
	if err := greet(c); err != nil {
		s.logf("[SOCKS] %v: %v", c.RemoteAddr(), err)
		return
	}
	req, err := readRequest(c)
	_ = c.SetDeadline(time.Time{})
	if err != nil {
		var ref *refusal
		if errors.As(err, &ref) {
			_ = s.reply(c, ref.code)
			s.logf("[SOCKS] %v: %v", req, ref.err)
		} else {
			s.logf("[SOCKS] %v: %v", c.RemoteAddr(), err)
		}
		return
	}

	if s.opts.Up != nil && !s.opts.Up() {
		// Not through the exit: it would wait for the next session, and the program is
		// better told at once.
		_ = s.reply(c, repNetworkUnreachable)
		s.logf("[SOCKS] %v: no session", req)
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, dialTimeout)
	target, err = s.opts.Dialer.Dial(ctx, req.address())
	cancel() // the deadline is over with the dial: the relay has none
	if err != nil {
		code := replyCode(err)
		_ = s.reply(c, code)
		s.logf("[SOCKS] %v: reply %#02x: %v", req, code, err)
		return
	}
	if !s.track(c, target) {
		return
	}
	defer s.untrack(c, target)

	if err := s.reply(c, repSucceeded); err != nil {
		return
	}
	handshaking = false
	s.handshakes.Done()
	// Established: no deadlines on either side any more.
	_ = c.SetDeadline(time.Time{})
	s.logf("[SOCKS] %v: opened", req)
	relay(c, target)
	s.logf("[SOCKS] %v: closed", req)
}

// reply writes the answer to the program with a deadline of its own: the one of the
// greeting is over by then, and a program that stopped reading must not hold the goroutine.
func (s *Socks) reply(c net.Conn, code byte) error {
	_ = c.SetDeadline(time.Now().Add(handshakeTimeout))
	return writeReply(c, code)
}

// replyCode is the code for a failed Dial. A refusal of the exit has its own code, which
// the program understands; everything else — the session ended, the exit is not SOCKS5,
// the stream closed — is a general failure.
func replyCode(err error) byte {
	var ce *exit.ConnectError
	if errors.As(err, &ce) && ce.Code != 0 {
		return ce.Code
	}
	return repFailure
}

// relay passes the data both ways until both sides are over. A smux stream has no
// CloseWrite, so a half-close becomes a full one: the end of either direction closes both
// sides, and the other copy then ends instead of waiting for data from a connection that is
// already dead. Unlike a relay with half-close, a program that closes its side of a
// finished request ends the connection, not only that direction — with the tunnel there is
// nothing finer to pass on.
func relay(a, b net.Conn) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(b, a)
		_ = b.Close()
		_ = a.Close()
	}()
	_, _ = io.Copy(a, b)
	_ = a.Close()
	_ = b.Close()
	<-done
}

// track adds the pair of sockets of one connection to the ones Close closes and counts it
// among the relays Drain waits for. c is the key: the local connection is unique. false —
// the datapath is closing or draining, and the sockets are closed here.
func (s *Socks) track(c, peer net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.draining {
		c.Close()
		peer.Close()
		return false
	}
	if s.relays == 0 {
		s.relayDone = make(chan struct{})
	}
	s.relays++
	s.conns[c] = peer
	return true
}

// untrack closes and forgets the sockets of a connection whose goroutine has ended. The
// second deferred call of a relay finds its entry already gone and does nothing.
func (s *Socks) untrack(c, peer net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.Close()
	peer.Close()
	stored, ok := s.conns[c]
	if !ok {
		return
	}
	delete(s.conns, c)
	if stored == c {
		return // a handshake that never became a relay
	}
	s.relays--
	if s.relays == 0 {
		close(s.relayDone)
		s.relayDone = nil
	}
}

// closeConns closes the sockets of every connection in progress. A deadline in the past
// breaks a write stuck in the session as well.
func (s *Socks) closeConns() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c, peer := range s.conns {
		_ = c.SetDeadline(time.Unix(1, 0))
		_ = c.Close()
		_ = peer.Close()
	}
}

func (s *Socks) closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.down
}

func (s *Socks) logf(format string, args ...any) {
	if s.opts.Logf != nil {
		s.opts.Logf(format, args...)
	}
}
