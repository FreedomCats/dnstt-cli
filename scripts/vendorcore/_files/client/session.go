// Added by dnstt-cli (scripts/vendorcore/_files): not part of the dnstt upstream.

package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/xtaci/kcp-go/v5"
	"github.com/xtaci/smux"

	"github.com/FreedomCats/dnstt-cli/core/dns"
	"github.com/FreedomCats/dnstt-cli/core/noise"
	"github.com/FreedomCats/dnstt-cli/core/turbotunnel"
)

// DefaultUTLS is the uTLS distribution of the upstream -utls flag.
const DefaultUTLS = "4*random,3*Firefox_120,1*Firefox_105,3*Chrome_120,1*Chrome_102,1*iOS_14,1*iOS_13"

// Config is what a session needs: the tunnel, the resolver and the hooks of the shell.
type Config struct {
	// Domain is the root of the tunnel's DNS zone.
	Domain dns.Name
	// PubKey is the server's Noise public key, noise.KeyLen bytes.
	PubKey []byte
	// Resolver carries the queries.
	Resolver Resolver
	// UTLS is the uTLS distribution in the syntax of the upstream -utls flag: "" is
	// DefaultUTLS, "none" is the standard TLS of Go. Used by DoH and DoT.
	UTLS string
	// Control is put on every socket the session creates (§06 of doc/architecture.md).
	// nil leaves the sockets as they are.
	Control func(network, address string, c syscall.RawConn) error
	// LookupIP resolves the host names of resolvers. nil is the system resolver.
	LookupIP func(ctx context.Context, host string) ([]netip.Addr, error)
	// KeepAliveTimeout is how long the session lives without data from the server; 0 is
	// upstream's 2 minutes. The server sends a keepalive every 10 s.
	KeepAliveTimeout time.Duration
	// Logf receives the session's own messages. nil discards them. The upstream code
	// logs through the log package.
	Logf func(format string, args ...any)
}

var (
	// ErrClosed is the Err of a session closed by Close.
	ErrClosed = errors.New("session closed")
	// ErrSilent is the Err of a session that heard nothing from the server for the
	// keepalive timeout.
	ErrSilent = errors.New("no data from the server for the keepalive timeout")
)

// Stats is a snapshot of a session's counters.
type Stats struct {
	// Streams is the number of open streams.
	Streams int
	// BytesSent and BytesReceived count the Noise channel: payload plus encryption, without
	// KCP and DNS overhead.
	BytesSent, BytesReceived uint64
	// RTT is KCP's smoothed round-trip time.
	RTT time.Duration
}

// Session is one connection to the dnstt server: transport → DNSPacketConn → KCP → Noise
// → smux, the chain of upstream run. It lives until Close or until the server goes silent
// for the keepalive timeout.
type Session struct {
	sockets *sockets
	dnsConn *DNSPacketConn
	kcpConn *kcp.UDPSession
	noise   *countingConn
	mux     *smux.Session

	done   chan struct{}
	errMu  sync.Mutex
	err    error
	closed sync.Once
}

// Dial builds the chain and returns after the Noise handshake, or with an error when ctx
// ends first.
func Dial(ctx context.Context, cfg Config) (*Session, error) {
	if len(cfg.Domain) == 0 {
		return nil, errors.New("no tunnel domain")
	}
	if len(cfg.PubKey) != noise.KeyLen {
		return nil, fmt.Errorf("server public key is %d bytes, must be %d", len(cfg.PubKey), noise.KeyLen)
	}
	logf := cfg.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	mtu := dnsNameCapacity(cfg.Domain) - 8 - 1 - numPadding - 1 // clientid + padding length prefix + padding + data length prefix
	if mtu < 80 {
		return nil, fmt.Errorf("domain %s leaves only %d bytes for payload", cfg.Domain, mtu)
	}
	spec := cfg.UTLS
	if spec == "" {
		spec = DefaultUTLS
	}
	id, err := sampleUTLSDistribution(spec)
	if err != nil {
		return nil, fmt.Errorf("uTLS: %w", err)
	}
	keepAlive := cfg.KeepAliveTimeout
	if keepAlive == 0 {
		keepAlive = idleTimeout
	}

	s := &Session{sockets: newSockets(&cfg), done: make(chan struct{})}
	fail := func(err error) (*Session, error) {
		s.teardown()
		return nil, err
	}

	addr, transport, err := openTransport(ctx, &cfg, s.sockets, id)
	if err != nil {
		return fail(fmt.Errorf("resolver %s: %w", cfg.Resolver, err))
	}
	s.dnsConn = NewDNSPacketConn(transport, addr, cfg.Domain)

	// KCP, as upstream run sets it up.
	s.kcpConn, err = kcp.NewConn2(addr, nil, 0, 0, s.dnsConn)
	if err != nil {
		return fail(fmt.Errorf("opening KCP conn: %w", err))
	}
	s.kcpConn.SetStreamMode(true)
	s.kcpConn.SetNoDelay(0, 0, 0, 1)
	s.kcpConn.SetWindowSize(turbotunnel.QueueSize/2, turbotunnel.QueueSize/2)
	if !s.kcpConn.SetMtu(mtu) {
		return fail(fmt.Errorf("KCP refused MTU %d", mtu))
	}
	fingerprint := "none"
	if id != nil && cfg.Resolver.Transport != UDP {
		fingerprint = id.Client + " " + id.Version
	}
	logf("session %08x: resolver %s, MTU %d, uTLS %s", s.kcpConn.GetConv(), cfg.Resolver, mtu, fingerprint)

	// The Noise handshake waits for one answer from the server; nothing else in the chain
	// waits. noise.NewClient has no context, so ctx ends it by closing the chain under it.
	s.noise = &countingConn{ReadWriteCloser: s.kcpConn}
	type handshake struct {
		rw  io.ReadWriteCloser
		err error
	}
	ch := make(chan handshake, 1)
	go func() {
		rw, err := noise.NewClient(s.noise, cfg.PubKey)
		ch <- handshake{rw, err}
	}()
	var rw io.ReadWriteCloser
	select {
	case h := <-ch:
		if h.err != nil {
			return fail(fmt.Errorf("noise handshake: %w", h.err))
		}
		rw = h.rw
	case <-ctx.Done():
		return fail(ctx.Err())
	}

	// smux, as upstream run sets it up, with the keepalive timeout of the shell.
	smuxConfig := smux.DefaultConfig()
	smuxConfig.Version = 2
	smuxConfig.KeepAliveTimeout = keepAlive
	smuxConfig.KeepAliveInterval = min(smuxConfig.KeepAliveInterval, keepAlive/2)
	smuxConfig.MaxStreamBuffer = 1 * 1024 * 1024 // default is 65536
	s.mux, err = smux.Client(rw, smuxConfig)
	if err != nil {
		return fail(fmt.Errorf("opening smux session: %w", err))
	}
	go s.watch()
	return s, nil
}

// watch ends the session when smux closes: by Close, or by its keepalive when the server
// has been silent for the timeout.
func (s *Session) watch() {
	<-s.mux.CloseChan()
	s.setErr(ErrSilent)
	s.teardown()
	close(s.done)
}

func (s *Session) setErr(err error) {
	s.errMu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.errMu.Unlock()
}

// teardown closes the chain from the bottom: the upstream code does not close what is
// under it.
func (s *Session) teardown() {
	s.closed.Do(func() {
		if s.kcpConn != nil {
			s.kcpConn.Close()
		}
		if s.dnsConn != nil {
			s.dnsConn.Close()
		}
		s.sockets.closeAll()
	})
}

// OpenStream opens a stream to the server's exit.
func (s *Session) OpenStream() (net.Conn, error) {
	stream, err := s.mux.OpenStream()
	if err != nil {
		select {
		case <-s.done:
			return nil, fmt.Errorf("opening stream: %w", s.Err())
		default:
		}
		return nil, fmt.Errorf("opening stream: %w", err)
	}
	return stream, nil
}

// Done is closed when the session has ended and its sockets are closed.
func (s *Session) Done() <-chan struct{} { return s.done }

// Err is why the session ended: ErrClosed or ErrSilent; nil while it lives.
func (s *Session) Err() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.err
}

// Stats returns the session's counters.
func (s *Session) Stats() Stats {
	return Stats{
		Streams:       s.mux.NumStreams(),
		BytesSent:     s.noise.sent.Load(),
		BytesReceived: s.noise.received.Load(),
		RTT:           time.Duration(s.kcpConn.GetSRTT()) * time.Millisecond,
	}
}

// Close ends the session: open streams break, the sockets close. It returns when the
// session is down.
func (s *Session) Close() error {
	s.setErr(ErrClosed)
	s.mux.Close()
	<-s.done
	return nil
}

// countingConn counts the bytes between KCP and Noise.
type countingConn struct {
	io.ReadWriteCloser
	sent, received atomic.Uint64
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.ReadWriteCloser.Read(p)
	c.received.Add(uint64(n))
	return n, err
}

func (c *countingConn) Write(p []byte) (int, error) {
	n, err := c.ReadWriteCloser.Write(p)
	c.sent.Add(uint64(n))
	return n, err
}
