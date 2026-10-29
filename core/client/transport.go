// Added by dnstt-cli (scripts/vendorcore/_files): not part of the dnstt upstream.

package client

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"sync"
	"time"

	utls "github.com/refraction-networking/utls"

	"github.com/FreedomCats/dnstt-cli/core/turbotunnel"
)

// dialFunc dials a TCP connection to a resolver. The uTLS code of the upstream takes it
// from its caller (a vendorcore rule), so that every connection of the core goes through
// sockets.dial.
type dialFunc = func(ctx context.Context, network, addr string) (net.Conn, error)

// Transport is how DNS messages reach the resolver.
type Transport string

const (
	DoH Transport = "doh" // DNS over HTTPS, RFC 8484
	DoT Transport = "dot" // DNS over TLS, RFC 7858
	UDP Transport = "udp" // plain DNS over UDP
)

// Resolver is the recursive resolver a session sends its queries through.
type Resolver struct {
	Transport Transport
	// Address is the URL for DoH (https only) and host:port for DoT and UDP. A host name
	// is resolved by Config.LookupIP only.
	Address string
}

func (r Resolver) String() string { return string(r.Transport) + ":" + r.Address }

// numHTTPSenders is how many DoH requests a session keeps in flight, as upstream main.
const numHTTPSenders = 32

// sockets creates the sockets of one session and closes them with it.
//
// Every socket gets Config.Control (§06 of doc/architecture.md), and every dial ends when
// the session does: closeAll cancels the dials in progress and closes what they made.
// Without it a closed session would leave its connections to the resolver open — upstream
// never closes them, it has one session per process.
type sockets struct {
	cfg    *Config
	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	open   map[io.Closer]struct{}
	closed bool
}

func newSockets(cfg *Config) *sockets {
	ctx, cancel := context.WithCancel(context.Background())
	return &sockets{cfg: cfg, ctx: ctx, cancel: cancel, open: map[io.Closer]struct{}{}}
}

// track registers c to be closed with the session. A session already closed closes c at
// once and reports net.ErrClosed.
func (s *sockets) track(c io.Closer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		c.Close()
		return net.ErrClosed
	}
	s.open[c] = struct{}{}
	return nil
}

func (s *sockets) forget(c io.Closer) {
	s.mu.Lock()
	delete(s.open, c)
	s.mu.Unlock()
}

// closeAll cancels the dials in progress and closes every socket still open.
func (s *sockets) closeAll() {
	s.cancel()
	s.mu.Lock()
	s.closed = true
	open := s.open
	s.open = nil
	s.mu.Unlock()
	for c := range open {
		c.Close()
	}
}

// lookup gives the addresses of host: the address itself if host is one, otherwise
// Config.LookupIP (P4). The system resolver is never asked: under the kill-switch it is
// closed, and without it the name of the tunnel's resolver would go to the ISP.
func (s *sockets) lookup(ctx context.Context, host string) ([]netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{ip.Unmap()}, nil
	}
	if err := s.canLookup(host); err != nil {
		return nil, err
	}
	ips, err := s.cfg.LookupIP(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("resolving %s: no addresses", host)
	}
	for i := range ips {
		ips[i] = ips[i].Unmap()
	}
	return ips, nil
}

// canLookup reports whether lookup can give the addresses of host. DoH and DoT dial when
// they first send, after Dial has started the handshake; openTransport asks this first, so
// that a host name without LookupIP fails Dial at once.
func (s *sockets) canLookup(host string) error {
	if _, err := netip.ParseAddr(host); err == nil || s.cfg.LookupIP != nil {
		return nil
	}
	return fmt.Errorf("resolving %s: a host name needs Config.LookupIP", host)
}

// dial is the dialFunc of the session: a TCP connection with Control, to the addresses
// of the host in turn.
func (s *sockets) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := s.lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	d := &net.Dialer{Control: s.cfg.Control}
	var errs []error
	for _, ip := range ips {
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		t := &trackedConn{Conn: conn, s: s}
		if err := s.track(t); err != nil {
			return nil, err
		}
		return t, nil
	}
	return nil, errors.Join(errs...)
}

// listenUDP opens a UDP socket with Control for a query to addr, of addr's family. The
// socket is the caller's to close: udpTransport closes it after the answer, and every
// socket still open when it is closed.
func (s *sockets) listenUDP(ctx context.Context, addr netip.AddrPort) (net.PacketConn, error) {
	network := "udp4"
	if addr.Addr().Is6() {
		network = "udp6"
	}
	lc := &net.ListenConfig{Control: s.cfg.Control}
	return lc.ListenPacket(ctx, network, ":0")
}

// trackedConn leaves the session's set of sockets when it is closed.
type trackedConn struct {
	net.Conn
	s    *sockets
	once sync.Once
}

func (c *trackedConn) Close() error {
	c.once.Do(func() { c.s.forget(c) })
	return c.Conn.Close()
}

// openTransport opens the transport to the resolver of cfg: the PacketConn that
// DNSPacketConn sends DNS messages through, and the address to send them to. id is the
// uTLS fingerprint, nil for the standard TLS of Go.
func openTransport(ctx context.Context, cfg *Config, s *sockets, id *utls.ClientHelloID) (net.Addr, net.PacketConn, error) {
	r := cfg.Resolver
	switch r.Transport {
	case DoH:
		u, err := url.Parse(r.Address)
		if err != nil {
			return nil, nil, fmt.Errorf("DoH URL: %w", err)
		}
		// Plain http would also go past the hook: utlsRoundTripper hands it to
		// http.DefaultTransport.
		if u.Scheme != "https" || u.Host == "" {
			return nil, nil, fmt.Errorf("DoH URL %q: must start with https://", r.Address)
		}
		if err := s.canLookup(u.Hostname()); err != nil {
			return nil, nil, err
		}
		var rt http.RoundTripper
		if id == nil {
			// The fields of http.DefaultTransport, without its dialer and its proxy from
			// the environment: upstream takes no proxy either.
			rt = &http.Transport{
				DialContext:         s.dial,
				ForceAttemptHTTP2:   true,
				MaxIdleConns:        100,
				IdleConnTimeout:     90 * time.Second,
				TLSHandshakeTimeout: 10 * time.Second,
			}
		} else {
			rt = NewUTLSRoundTripper(nil, id, s.dial)
		}
		pconn, err := NewHTTPPacketConn(rt, r.Address, numHTTPSenders, cfg.Logf)
		if err != nil {
			return nil, nil, err
		}
		if err := s.track(pconn); err != nil {
			return nil, nil, err
		}
		return turbotunnel.DummyAddr{}, pconn, nil

	case DoT:
		host, _, err := net.SplitHostPort(r.Address)
		if err != nil {
			return nil, nil, fmt.Errorf("DoT address: %w", err)
		}
		if err := s.canLookup(host); err != nil {
			return nil, nil, err
		}
		dialTLS := func(ctx context.Context, network, addr string) (net.Conn, error) {
			if id != nil {
				return utlsDialContext(ctx, network, addr, nil, id, s.dial)
			}
			raw, err := s.dial(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			host, _, _ := net.SplitHostPort(addr)
			conn := tls.Client(raw, &tls.Config{ServerName: host})
			if err := conn.HandshakeContext(ctx); err != nil {
				raw.Close()
				return nil, err
			}
			return conn, nil
		}
		// NewTLSPacketConn dials with its own context: the session's end still cancels
		// the dial through sockets.dial.
		pconn, err := NewTLSPacketConn(r.Address, dialTLS, cfg.Logf)
		if err != nil {
			return nil, nil, err
		}
		if err := s.track(pconn); err != nil {
			return nil, nil, err
		}
		return turbotunnel.DummyAddr{}, pconn, nil

	case UDP:
		host, portString, err := net.SplitHostPort(r.Address)
		if err != nil {
			return nil, nil, fmt.Errorf("UDP address: %w", err)
		}
		port, err := strconv.ParseUint(portString, 10, 16)
		if err != nil {
			return nil, nil, fmt.Errorf("UDP address %q: bad port", r.Address)
		}
		ips, err := s.lookup(ctx, host)
		if err != nil {
			return nil, nil, err
		}
		addr := netip.AddrPortFrom(ips[0], uint16(port))
		pconn := newUDPTransport(s, addr)
		if err := s.track(pconn); err != nil {
			return nil, nil, err
		}
		return net.UDPAddrFromAddrPort(addr), pconn, nil
	}
	return nil, nil, fmt.Errorf("unknown resolver transport %q", r.Transport)
}
