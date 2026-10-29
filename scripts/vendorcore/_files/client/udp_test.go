// Added by dnstt-cli (scripts/vendorcore/_files): not part of the dnstt upstream.

package client

import (
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// forwarder is a resolver between the client and the in-process server: it passes every
// query on from a socket of its own and the answer back, and counts the source ports of
// the queries.
type forwarder struct {
	conn *net.UDPConn
	mu   sync.Mutex
	src  map[int]int // source port → queries from it
	n    int
}

func newForwarder(t *testing.T, server *net.UDPAddr) *forwarder {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	f := &forwarder{conn: conn, src: map[int]int{}}
	go func() {
		for {
			buf := make([]byte, 4096)
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			f.mu.Lock()
			f.src[from.Port]++
			f.n++
			f.mu.Unlock()
			go func() {
				up, err := net.DialUDP("udp", nil, server)
				if err != nil {
					return
				}
				defer up.Close()
				up.Write(buf[:n])
				up.SetReadDeadline(time.Now().Add(5 * time.Second))
				answer := make([]byte, 4096)
				m, err := up.Read(answer)
				if err != nil {
					return
				}
				conn.WriteToUDP(answer[:m], from)
			}()
		}
	}()
	return f
}

// P3: in UDP mode the queries of a session leave from different source ports. Ports
// freed by answered queries may come again, so a few repeats are allowed; upstream sends
// every query from the same one.
func TestUDPSocketPerQuery(t *testing.T) {
	srv := startServer(t)
	f := newForwarder(t, srv.Addr)
	cfg := udpConfig(srv)
	cfg.Resolver.Address = f.conn.LocalAddr().String()
	s := dial(t, cfg)
	if err := echo(s, make([]byte, 20000)); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.n < 20 || len(f.src) < f.n*9/10 {
		t.Errorf("%d queries from %d source ports, want a port per query", f.n, len(f.src))
	}
}

// P3: no more than udpMaxInFlight queries wait for their answers; a query over the limit
// is dropped without a socket. A socket gives up after udpQueryTimeout, and Close ends
// the waits.
func TestUDPInFlightLimit(t *testing.T) {
	limit, timeout := udpMaxInFlight, udpQueryTimeout
	udpMaxInFlight, udpQueryTimeout = 4, 300*time.Millisecond
	t.Cleanup(func() { udpMaxInFlight, udpQueryTimeout = limit, timeout })

	silent, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()

	var sockets atomic.Int32
	cfg := Config{Control: func(string, string, syscall.RawConn) error {
		sockets.Add(1)
		return nil
	}}
	s := newSockets(&cfg)
	defer s.closeAll()
	resolver := silent.LocalAddr().(*net.UDPAddr).AddrPort()
	tr := newUDPTransport(s, netip.AddrPortFrom(resolver.Addr().Unmap(), resolver.Port()))
	addr := net.UDPAddrFromAddrPort(tr.resolver)

	query := func(n int) {
		for range n {
			if _, err := tr.WriteTo([]byte("query"), addr); err != nil {
				t.Fatalf("WriteTo: %v", err)
			}
		}
	}
	query(10)
	if n := sockets.Load(); n != 4 {
		t.Fatalf("%d sockets for 10 queries, want the limit of 4", n)
	}
	time.Sleep(600 * time.Millisecond) // the waits time out
	query(2)
	if n := sockets.Load(); n != 6 {
		t.Fatalf("%d sockets after the timeout and 2 more queries, want 6", n)
	}

	tr.Close()
	if _, err := tr.WriteTo([]byte("query"), addr); err == nil {
		t.Error("WriteTo after Close succeeded")
	}
	if _, _, err := tr.ReadFrom(make([]byte, 10)); err == nil {
		t.Error("ReadFrom after Close succeeded")
	}
	deadline := time.Now().Add(5 * time.Second)
	for goroutinesOf("(*udpTransport)") != "" {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines left after Close:\n\n%s", goroutinesOf("(*udpTransport)"))
		}
		time.Sleep(20 * time.Millisecond)
	}
}
