package datapath

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/FreedomCats/dnstt-cli/internal/exit"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	ip4 "gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
)

// outQueue is the size of the channel between a test's stack and its link endpoint. It is
// the test's own: the netstack takes the endpoint from outside.
const outQueue = 1024

// pump passes packets between the two stacks of a test: each stack's outgoing packets go
// into the other stack's link endpoint. It ends when ctx is canceled or either link is
// closed.
func pump(ctx context.Context, a, b *netstack) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for a.next(ctx, func(p []byte) { b.inject(p) }) {
		}
	}()
	for b.next(ctx, func(p []byte) { a.inject(p) }) {
	}
	<-done
}

// newTestNetstack starts a stack on a channel endpoint and closes it with the test.
func newTestNetstack(t *testing.T, dialer exit.Dialer) *netstack {
	t.Helper()
	link := channel.New(outQueue, TunnelMTU, "")
	ns, err := newNetstack(link, dialer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ns.close)
	return ns
}

// pair is a client stack and a server stack joined by a packet pump.
type pair struct {
	client *netstack
	server *netstack
	cancel context.CancelFunc
}

func newPair(t *testing.T, dialer exit.Dialer) *pair {
	t.Helper()
	client := newTestNetstack(t, dialerFunc(func(context.Context, string) (net.Conn, error) {
		return nil, errors.New("unexpected dial")
	}))
	server := newTestNetstack(t, dialer)
	ctx, cancel := context.WithCancel(context.Background())
	go pump(ctx, client, server)
	p := &pair{client: client, server: server, cancel: cancel}
	t.Cleanup(p.close)
	return p
}

func (p *pair) close() {
	p.cancel()
	p.client.close()
	p.server.close()
}

// addAddresses gives the client and the server the addresses the adapter would have: the
// client's own address, and the server's own address next to the spoofed destinations.
func (p *pair) addAddresses(t *testing.T) {
	t.Helper()
	if err := p.client.addAddress(netip.MustParseAddr("192.0.2.1")); err != nil {
		t.Fatal(err)
	}
	if err := p.server.addAddress(netip.MustParseAddr("192.0.2.2")); err != nil {
		t.Fatal(err)
	}
}

// dialErr dials the destination through the client stack. It returns an error instead of
// failing the test, so it can run in a goroutine.
func (p *pair) dialErr(addr string) (*gonet.TCPConn, error) {
	ap := netip.MustParseAddrPort(addr)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return gonet.DialContextTCP(ctx, p.client.stack, tcpip.FullAddress{
		Addr: tcpip.AddrFrom4(ap.Addr().As4()),
		Port: ap.Port(),
	}, ip4.ProtocolNumber)
}

// dial is dialErr on the main goroutine: a failed dial fails the test.
func (p *pair) dial(t *testing.T, addr string) *gonet.TCPConn {
	t.Helper()
	conn, err := p.dialErr(addr)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

// echoThroughErr sends msg and expects it back, in one direction at a time. It returns an
// error instead of failing the test, so it can run in a goroutine.
func echoThroughErr(c net.Conn, msg []byte) error {
	c.SetDeadline(time.Now().Add(60 * time.Second))
	defer c.SetDeadline(time.Time{})
	errc := make(chan error, 1)
	go func() {
		_, err := c.Write(msg)
		errc <- err
	}()
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(c, got); err != nil {
		return err
	}
	if err := <-errc; err != nil {
		return err
	}
	if !bytes.Equal(got, msg) {
		return fmt.Errorf("echo: %d bytes differ from the %d sent", len(got), len(msg))
	}
	return nil
}

// A TCP connection from the client stack reaches the echo server through the forwarder and
// the fake exit, in both directions; the exit sees the original destination.
func TestNetstackTCP(t *testing.T) {
	echo := echoServer(t)
	var mu sync.Mutex
	var seen []string
	dialer := dialerFunc(func(ctx context.Context, address string) (net.Conn, error) {
		mu.Lock()
		seen = append(seen, address)
		mu.Unlock()
		var d net.Dialer
		return d.DialContext(ctx, "tcp", echo)
	})
	p := newPair(t, dialer)
	p.addAddresses(t)

	conn := p.dial(t, "203.0.113.7:80")
	echoThrough(t, conn, payload(50000))
	mu.Lock()
	got := append([]string(nil), seen...)
	mu.Unlock()
	if len(got) != 1 || got[0] != "203.0.113.7:80" {
		t.Fatalf("the exit saw %v, want [203.0.113.7:80]", got)
	}
	conn.Close()

	// Several connections at once, each with its own stream.
	const n = 4
	errc := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			conn, err := p.dialErr("203.0.113.7:80")
			if err != nil {
				errc <- err
				return
			}
			defer conn.Close()
			errc <- echoThroughErr(conn, payload(20000))
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errc; err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	got = append([]string(nil), seen...)
	mu.Unlock()
	if len(got) != n+1 {
		t.Fatalf("the exit saw %d connections, want %d", len(got), n+1)
	}
}

// A refused exit dial gives the client an immediate error, not a wait for the timeout.
func TestNetstackDialRefused(t *testing.T) {
	p := newPair(t, dialerFunc(func(context.Context, string) (net.Conn, error) {
		return nil, errors.New("the exit refused")
	}))
	p.addAddresses(t)

	start := time.Now()
	conn, err := p.dialErr("203.0.113.7:80")
	if err == nil {
		// The handshake completed; the reset arrives on the first read.
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		_, err = conn.Read(make([]byte, 1))
		conn.Close()
	}
	if err == nil {
		t.Fatal("the exit refused the dial, but the client saw no error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("the refusal took %v, want it at once", elapsed)
	}
}

// Close leaves no goroutine of the datapath behind, and a connection open at close ends
// without a panic.
func TestNetstackCloseLeavesNothing(t *testing.T) {
	echo := echoServer(t)
	before := runtime.NumGoroutine()
	p := newPair(t, dialerFunc(func(ctx context.Context, address string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", echo)
	}))
	p.addAddresses(t)
	conn := p.dial(t, "203.0.113.7:80")
	echoThrough(t, conn, []byte("before the close"))

	p.close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("the connection is still open after close")
	}
	conn.Close()

	deadline := time.Now().Add(10 * time.Second)
	for {
		if n := runtime.NumGoroutine(); n <= before {
			return
		}
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<20)
			n := runtime.Stack(buf, true)
			t.Fatalf("goroutines: %d before, %d after close\n%s", before, runtime.NumGoroutine(), buf[:n])
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The adapter's address is a local address of the stack, and removal is harmless.
func TestNetstackAddress(t *testing.T) {
	ns := newTestNetstack(t, dialerFunc(func(context.Context, string) (net.Conn, error) {
		return nil, errors.New("unexpected dial")
	}))
	a := netip.MustParseAddr("192.0.2.2")
	if err := ns.addAddress(a); err != nil {
		t.Fatal(err)
	}
	got, err := ns.stack.GetMainNICAddress(nicID, ip4.ProtocolNumber)
	if err != nil {
		t.Fatal(err)
	}
	if got.Address != tcpip.AddrFrom4(a.As4()) {
		t.Fatalf("the stack's address is %v, want %v", got.Address, a)
	}
	ns.removeAddress(a)
	got, err = ns.stack.GetMainNICAddress(nicID, ip4.ProtocolNumber)
	if err != nil {
		t.Fatal(err)
	}
	if got.Address != (tcpip.Address{}) {
		t.Fatalf("the address is still there after removeAddress: %v", got.Address)
	}
	// A second remove is harmless.
	ns.removeAddress(a)
}
