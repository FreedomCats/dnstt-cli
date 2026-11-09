package datapath

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/FreedomCats/dnstt-cli/internal/exit"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	ip4 "gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

const (
	// nicID is the one NIC of the stack: the adapter's link endpoint.
	nicID tcpip.NICID = 1

	// forwardWindow is the receive window of a forwarded connection: 64 KiB, the amount
	// of data the tunnel can carry in flight. It is the size of the tunnel, not a
	// setting.
	forwardWindow = 64 << 10

	// forwardInFlight is the number of connection requests the forwarder may have in
	// flight. Every connection is a stream through the exit; more would only queue in
	// the tunnel, and a flood of SYNs must not grow goroutines without bound.
	forwardInFlight = 1024
)

// netstack is the gVisor stack in the shell: every TCP connection that arrives from the
// adapter becomes a stream through the exit, a DNS query to the adapter's address goes as
// a DNS-over-TCP exchange to tunDNS, and every other UDP datagram is refused by the IPv4
// layer with ICMP port unreachable. The answer goes back to the adapter. The link endpoint
// is the adapter's: the test passes a channel endpoint, the TUN adapter passes a fdbased
// one.
type netstack struct {
	stack  *stack.Stack
	link   stack.LinkEndpoint
	dialer exit.Dialer
	tunDNS netip.Addr // the DNS behind the exit; the zero address — no DNS listener

	ctx    context.Context // ends with close
	cancel context.CancelFunc
	wg     sync.WaitGroup // TCP streams, the DNS read loop and its exchanges

	// dnsTimeout bounds one DNS exchange; dnsQueryTimeout unless a test lowers it.
	dnsTimeout time.Duration
	dnsConn    *gonet.UDPConn // the listener on dnsAddr; nil — not bound
	dnsAddr    netip.Addr     // the adapter address the listener is bound to

	once   sync.Once
	mu     sync.Mutex
	closed bool // no more streams or exchanges are started
}

// newNetstack starts the stack on link. tunDNS, when set, is the DNS behind the exit that
// the DNS listener of the adapter's address will reach. The stack owns the link and closes
// it in Close.
func newNetstack(link stack.LinkEndpoint, dialer exit.Dialer, tunDNS netip.Addr) (*netstack, error) {
	if link == nil {
		return nil, errors.New("netstack: no link endpoint")
	}
	if dialer == nil {
		return nil, errors.New("netstack: no exit dialer")
	}
	if tunDNS.IsValid() && (!tunDNS.Is4() || tunDNS.IsUnspecified()) {
		return nil, fmt.Errorf("netstack: tun_dns %v is not a usable IPv4 address", tunDNS)
	}
	s := stack.New(stack.Options{
		NetworkProtocols: []stack.NetworkProtocolFactory{ip4.NewProtocol},
		// UDP has no default handler: the listener on dnsPort takes DNS queries, and any
		// other datagram finds no endpoint, so the IPv4 layer itself answers with ICMP
		// port unreachable (RFC 1122 §3.2.2.1).
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})
	if err := s.CreateNIC(nicID, link); err != nil {
		s.Close()
		return nil, fmt.Errorf("netstack: create NIC: %v", err)
	}
	// The adapter receives packets addressed to arbitrary remote hosts: the NIC must
	// accept them, and the forwarder must see the original destination, not the
	// adapter's own address.
	if err := s.SetPromiscuousMode(nicID, true); err != nil {
		s.Close()
		return nil, fmt.Errorf("netstack: promiscuous mode: %v", err)
	}
	if err := s.SetSpoofing(nicID, true); err != nil {
		s.Close()
		return nil, fmt.Errorf("netstack: spoofing: %v", err)
	}
	s.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: nicID}})
	n := &netstack{
		stack:      s,
		link:       link,
		dialer:     dialer,
		tunDNS:     tunDNS,
		dnsTimeout: dnsQueryTimeout,
	}
	n.ctx, n.cancel = context.WithCancel(context.Background())
	fwd := tcp.NewForwarder(s, forwardWindow, forwardInFlight, n.handle)
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, fwd.HandlePacket)
	return n, nil
}

// addAddress adds a /32 address of the adapter to the stack: the system resolver and the
// DNS listener use it, and the stack accepts it as local in addition to the spoofed
// destinations. With tunDNS set, the DNS listener binds to the address and its read loop
// starts here: the address must be assigned first.
func (n *netstack) addAddress(a netip.Addr) error {
	if !a.Is4() {
		return fmt.Errorf("netstack: %v is not an IPv4 address", a)
	}
	err := n.stack.AddProtocolAddress(nicID, tcpip.ProtocolAddress{
		Protocol:          ip4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddrFrom4(a.As4()).WithPrefix(),
	}, stack.AddressProperties{})
	if err != nil {
		return fmt.Errorf("netstack: address %v: %v", a, err)
	}
	if !n.tunDNS.IsValid() {
		return nil
	}
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		_ = n.stack.RemoveAddress(nicID, tcpip.AddrFrom4(a.As4()))
		return errors.New("netstack: closed")
	}
	if n.dnsConn != nil {
		bound := n.dnsAddr
		n.mu.Unlock()
		_ = n.stack.RemoveAddress(nicID, tcpip.AddrFrom4(a.As4()))
		return fmt.Errorf("netstack: address %v: the DNS listener is already bound to %v", a, bound)
	}
	conn, derr := gonet.DialUDP(n.stack, &tcpip.FullAddress{
		NIC:  nicID,
		Addr: tcpip.AddrFrom4(a.As4()),
		Port: dnsPort,
	}, nil, ip4.ProtocolNumber)
	if derr != nil {
		n.mu.Unlock()
		_ = n.stack.RemoveAddress(nicID, tcpip.AddrFrom4(a.As4()))
		return fmt.Errorf("netstack: DNS listener on %v: %v", a, derr)
	}
	n.dnsConn = conn
	n.dnsAddr = a
	// closed was checked under this lock, so the slot cannot be refused.
	n.startLocked()
	n.mu.Unlock()
	go n.serveDNS(conn)
	return nil
}

// removeAddress removes the adapter's address and stops the DNS listener bound to it; a
// repeated call is harmless.
func (n *netstack) removeAddress(a netip.Addr) {
	if !a.Is4() {
		return
	}
	n.mu.Lock()
	if n.dnsConn != nil && n.dnsAddr == a {
		n.dnsConn.Close()
		n.dnsConn = nil
		n.dnsAddr = netip.Addr{}
	}
	n.mu.Unlock()
	_ = n.stack.RemoveAddress(nicID, tcpip.AddrFrom4(a.As4()))
}

// serveDNS answers the DNS queries that arrive at the adapter's address: one ReadFrom loop
// and one exchange per query in its own goroutine, so the loop never blocks on the tunnel.
func (n *netstack) serveDNS(conn *gonet.UDPConn) {
	defer n.wg.Done()
	slots := make(chan struct{}, dnsInFlight)
	buf := make([]byte, dnsMaxSize)
	for {
		size, client, err := conn.ReadFrom(buf)
		if err != nil {
			// The listener is closed by removeAddress or close; the error is expected.
			return
		}
		if size < dnsHeaderLen {
			// DNS traffic, not "other UDP": no answer and no ICMP either.
			continue
		}
		// The buffer is reused by the next read, and the exchange outlives the loop's
		// iteration.
		query := append([]byte(nil), buf[:size]...)
		select {
		case slots <- struct{}{}:
		default:
			// A retry is cheaper than a queue that outlives the client.
			if answer := dnsServfail(query); answer != nil {
				_, _ = conn.WriteTo(answer, client)
			}
			continue
		}
		if !n.start() {
			<-slots
			return
		}
		go n.exchangeDNS(conn, slots, client, query)
	}
}

// exchangeDNS carries one query through the exit and sends the answer back to the client.
// An error of the exchange (a dead tunnel, a bad answer, a timeout) becomes SERVFAIL, so
// the stub resolver retries at once instead of waiting for its own timeout.
func (n *netstack) exchangeDNS(conn *gonet.UDPConn, slots chan struct{}, client net.Addr, query []byte) {
	defer n.wg.Done()
	defer func() { <-slots }()
	ctx, cancel := context.WithTimeout(n.ctx, n.dnsTimeout)
	defer cancel()
	answer, err := dnsExchange(ctx, n.dialer, n.tunDNS, query)
	if err != nil {
		answer = dnsServfail(query)
	}
	if answer != nil {
		_, _ = conn.WriteTo(answer, client)
	}
}

// handle is the forwarder's handler: it creates the endpoint for the connection request
// and starts a goroutine for the dial and the relay. The handler itself stays short.
func (n *netstack) handle(r *tcp.ForwarderRequest) {
	id := r.ID()
	var wq waiter.Queue
	ep, err := r.CreateEndpoint(&wq)
	if err != nil {
		// The client has no endpoint: a reset tells it at once instead of leaving it
		// to wait for the timeout.
		r.Complete(true)
		return
	}
	// CreateEndpoint has taken what it needs from the request; Complete releases the
	// rest. The forwarder requires it even when an endpoint was created.
	r.Complete(false)
	if !n.start() {
		ep.Abort()
		return
	}
	go n.serve(id, &wq, ep)
}

// start takes a stream or a DNS exchange in and counts its goroutine in one step: close
// sets closed under the same lock, so no goroutine is added once close waits for them.
// false — the netstack is closing, and the caller drops what it has.
func (n *netstack) start() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.startLocked()
}

// startLocked is start with the lock held: addAddress takes in its DNS read loop together
// with binding the listener.
func (n *netstack) startLocked() bool {
	if n.closed {
		return false
	}
	n.wg.Add(1)
	return true
}

// serve dials the original destination through the exit and relays the stream until one
// side ends. The stream has no half-close, so the end of either direction closes both.
func (n *netstack) serve(id stack.TransportEndpointID, wq *waiter.Queue, ep tcpip.Endpoint) {
	defer n.wg.Done()
	defer ep.Close()
	conn := gonet.NewTCPConn(wq, ep)
	ctx, cancel := context.WithTimeout(n.ctx, dialTimeout)
	target, err := n.dialer.Dial(ctx, net.JoinHostPort(id.LocalAddress.String(), strconv.Itoa(int(id.LocalPort))))
	cancel()
	if err != nil {
		// The client has a connection that is not going anywhere: reset it instead of
		// leaving it to wait for data that will never come.
		ep.Abort()
		return
	}
	relay(conn, target)
}

// inject delivers a packet from the adapter to the stack. The packet is a whole IPv4
// datagram; anything else is dropped. The IPv6 rule is complete here: the stack registers
// no IPv6 network protocol, and the adapter's fdbased endpoint of the next child needs no
// second filter. A short packet is dropped, not read past its end.
func (n *netstack) inject(b []byte) {
	if header.IPVersion(b) != header.IPv4Version {
		return
	}
	ep, ok := n.link.(interface {
		InjectInbound(tcpip.NetworkProtocolNumber, *stack.PacketBuffer)
	})
	if !ok {
		return
	}
	pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(b)})
	ep.InjectInbound(ip4.ProtocolNumber, pkt)
	pkt.DecRef()
}

// next reads the next packet the stack sends and passes it to write. false when the stack
// is closed or ctx ends.
func (n *netstack) next(ctx context.Context, write func([]byte)) bool {
	ep, ok := n.link.(interface {
		ReadContext(context.Context) *stack.PacketBuffer
	})
	if !ok {
		return false
	}
	pkt := ep.ReadContext(ctx)
	if pkt == nil {
		return false
	}
	v := pkt.ToView()
	pkt.DecRef()
	write(v.AsSlice())
	v.Release()
	return true
}

// close stops the stack and waits for the streams, the DNS read loop and its exchanges.
// Repeated calls are harmless.
func (n *netstack) close() {
	n.once.Do(func() {
		// closed and the listener are taken under one lock: no read loop is started once
		// close waits for them.
		n.mu.Lock()
		conn := n.dnsConn
		n.dnsConn = nil
		n.dnsAddr = netip.Addr{}
		n.closed = true
		n.mu.Unlock()
		if conn != nil {
			// ReadFrom returns; closing the stack below would not wake it.
			conn.Close()
		}
		n.cancel()
		n.stack.Close()
		n.stack.Wait()
		n.wg.Wait()
	})
}
