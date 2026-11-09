package datapath

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"sync"

	"github.com/FreedomCats/dnstt-cli/internal/exit"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	ip4 "gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
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

// netstack is the gVisor TCP stack in the shell: every TCP connection that arrives from
// the adapter becomes a stream through the exit, and the answer goes back to the adapter.
// The link endpoint is the adapter's: the test passes a channel endpoint, the TUN adapter
// passes a fdbased one.
type netstack struct {
	stack  *stack.Stack
	link   stack.LinkEndpoint
	dialer exit.Dialer

	ctx    context.Context // ends with close
	cancel context.CancelFunc
	wg     sync.WaitGroup // one goroutine for each stream

	once   sync.Once
	mu     sync.Mutex
	closed bool // no more streams are started
}

// newNetstack starts a TCP stack on link. The stack owns the link and closes it in Close.
func newNetstack(link stack.LinkEndpoint, dialer exit.Dialer) (*netstack, error) {
	if link == nil {
		return nil, errors.New("netstack: no link endpoint")
	}
	if dialer == nil {
		return nil, errors.New("netstack: no exit dialer")
	}
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ip4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol},
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
		stack:  s,
		link:   link,
		dialer: dialer,
	}
	n.ctx, n.cancel = context.WithCancel(context.Background())
	fwd := tcp.NewForwarder(s, forwardWindow, forwardInFlight, n.handle)
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, fwd.HandlePacket)
	return n, nil
}

// addAddress adds a /32 address of the adapter to the stack: the system resolver and the
// DNS listener use it, and the stack accepts it as local in addition to the spoofed
// destinations.
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
	return nil
}

// removeAddress removes the adapter's address; a repeated call is harmless.
func (n *netstack) removeAddress(a netip.Addr) {
	if !a.Is4() {
		return
	}
	_ = n.stack.RemoveAddress(nicID, tcpip.AddrFrom4(a.As4()))
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

// start takes the stream in and counts its goroutine in one step: close sets closed under
// the same lock, so no goroutine is added once close waits for them. false — the netstack
// is closing, and the endpoint is aborted here.
func (n *netstack) start() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
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
// datagram; anything else is dropped: IPv6 is not carried in this child (the next child
// completes the rule). A short packet is dropped, not read past its end.
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

// close stops the stack and waits for the streams. Repeated calls are harmless.
func (n *netstack) close() {
	n.once.Do(func() {
		n.cancel()
		n.mu.Lock()
		n.closed = true
		n.mu.Unlock()
		n.stack.Close()
		n.stack.Wait()
		n.wg.Wait()
	})
}
