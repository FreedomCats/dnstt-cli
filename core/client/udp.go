// Added by dnstt-cli (scripts/vendorcore/_files): not part of the dnstt upstream.

package client

import (
	"net"
	"net/netip"
	"sync"
	"time"
)

// The limits of the UDP transport; variables for tests.
var (
	// udpQueryTimeout is how long a query's socket waits for the answer. The dnstt
	// server answers within a second; the rest is the resolver's.
	udpQueryTimeout = 10 * time.Second
	// udpMaxInFlight is how many queries wait for their answers at most. KCP keeps up to
	// QueueSize/2 packets in flight, one per query, plus the polls. A query over the
	// limit is dropped, as a lost packet: KCP sends it again.
	udpMaxInFlight = 256
)

// udpTransport is the transport of UDP mode (P3): every query leaves from a new socket,
// which waits for one answer and closes. Upstream sends all queries from one socket, and
// some networks block a UDP association with the same address and port within seconds.
//
// An answer is taken from any source address, as from the one socket of upstream: some
// resolvers answer from another address of theirs.
type udpTransport struct {
	s        *sockets
	resolver netip.AddrPort
	timeout  time.Duration // udpQueryTimeout
	answers  chan []byte
	inFlight chan struct{} // a semaphore of udpMaxInFlight

	mu     sync.Mutex
	open   map[net.PacketConn]struct{}
	closed chan struct{}
}

func newUDPTransport(s *sockets, resolver netip.AddrPort) *udpTransport {
	return &udpTransport{
		s:        s,
		resolver: resolver,
		timeout:  udpQueryTimeout,
		answers:  make(chan []byte),
		inFlight: make(chan struct{}, udpMaxInFlight),
		open:     map[net.PacketConn]struct{}{},
		closed:   make(chan struct{}),
	}
}

// WriteTo sends p to the resolver from a new socket; addr is the resolver's address that
// DNSPacketConn passes back.
func (t *udpTransport) WriteTo(p []byte, addr net.Addr) (int, error) {
	select {
	case t.inFlight <- struct{}{}:
	case <-t.closed:
		return 0, net.ErrClosed
	default:
		return len(p), nil
	}
	conn, err := t.s.listenUDP(t.s.ctx, t.resolver)
	if err == nil {
		err = t.add(conn)
	}
	if err != nil {
		<-t.inFlight
		return 0, err
	}
	if _, err := conn.WriteTo(p, net.UDPAddrFromAddrPort(t.resolver)); err != nil {
		t.remove(conn)
		<-t.inFlight
		return 0, err
	}
	go t.await(conn)
	return len(p), nil
}

// await reads the answer to the query sent from conn and closes conn.
func (t *udpTransport) await(conn net.PacketConn) {
	defer func() {
		t.remove(conn)
		<-t.inFlight
	}()
	conn.SetReadDeadline(time.Now().Add(t.timeout))
	buf := make([]byte, 4096)
	n, _, err := conn.ReadFrom(buf)
	if err != nil {
		return
	}
	select {
	case t.answers <- buf[:n]:
	case <-t.closed:
	}
}

// ReadFrom gives the next answer, as if from the resolver.
func (t *udpTransport) ReadFrom(p []byte) (int, net.Addr, error) {
	select {
	case b := <-t.answers:
		return copy(p, b), net.UDPAddrFromAddrPort(t.resolver), nil
	case <-t.closed:
		return 0, nil, net.ErrClosed
	}
}

// add registers a query's socket to be closed with the transport.
func (t *udpTransport) add(conn net.PacketConn) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	select {
	case <-t.closed:
		conn.Close()
		return net.ErrClosed
	default:
	}
	t.open[conn] = struct{}{}
	return nil
}

func (t *udpTransport) remove(conn net.PacketConn) {
	t.mu.Lock()
	delete(t.open, conn)
	t.mu.Unlock()
	conn.Close()
}

// Close closes the sockets of the queries still waiting; the waits end with them.
func (t *udpTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	select {
	case <-t.closed:
		return nil
	default:
	}
	close(t.closed)
	for conn := range t.open {
		conn.Close()
	}
	return nil
}

func (t *udpTransport) LocalAddr() net.Addr {
	return &net.UDPAddr{}
}

func (t *udpTransport) SetDeadline(time.Time) error      { return nil }
func (t *udpTransport) SetReadDeadline(time.Time) error  { return nil }
func (t *udpTransport) SetWriteDeadline(time.Time) error { return nil }
