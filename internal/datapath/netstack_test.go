package datapath

import (
	"bytes"
	"context"
	"encoding/binary"
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
	"golang.org/x/net/dns/dnsmessage"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
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
// tunDNS is the DNS behind the exit; the zero address leaves the DNS listener out.
func newTestNetstack(t *testing.T, dialer exit.Dialer, tunDNS netip.Addr) *netstack {
	t.Helper()
	link := channel.New(outQueue, TunnelMTU, "")
	ns, err := newNetstack(link, dialer, tunDNS)
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
	return newPairWith(t, dialer, netip.Addr{})
}

// newDNSPair is newPair with the documentation tun_dns on the server stack: its DNS
// listener answers the client's queries through dialer.
func newDNSPair(t *testing.T, dialer exit.Dialer) *pair {
	t.Helper()
	return newPairWith(t, dialer, netip.MustParseAddr("203.0.113.53"))
}

func newPairWith(t *testing.T, dialer exit.Dialer, tunDNS netip.Addr) *pair {
	t.Helper()
	client := newTestNetstack(t, dialerFunc(func(context.Context, string) (net.Conn, error) {
		return nil, errors.New("unexpected dial")
	}), netip.Addr{})
	server := newTestNetstack(t, dialer, tunDNS)
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
	waitGoroutines(t, before)
}

// waitGoroutines waits until no more goroutines run than before the test's datapath
// started, and fails with all the stacks otherwise.
func waitGoroutines(t *testing.T, before int) {
	t.Helper()
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
	}), netip.Addr{})
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

// udpTo dials a UDP address on ns, sending from local.
func udpTo(ns *netstack, local netip.Addr, remote netip.AddrPort) (*gonet.UDPConn, error) {
	return gonet.DialUDP(ns.stack,
		&tcpip.FullAddress{NIC: nicID, Addr: tcpip.AddrFrom4(local.As4())},
		&tcpip.FullAddress{Addr: tcpip.AddrFrom4(remote.Addr().As4()), Port: remote.Port()},
		ip4.ProtocolNumber)
}

// udp is udpTo from the client stack's own address; a failed dial fails the test.
func (p *pair) udp(t *testing.T, remote string) *gonet.UDPConn {
	t.Helper()
	conn, err := udpTo(p.client, netip.MustParseAddr("192.0.2.1"), netip.MustParseAddrPort(remote))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// dnsExit is a fake exit with a DNS-over-TCP server behind it: Dial records the target and
// returns one end of a net.Pipe, and serve serves the other end.
type dnsExit struct {
	mu      sync.Mutex
	targets []string
	serve   func(net.Conn)
}

func (d *dnsExit) Dial(_ context.Context, address string) (net.Conn, error) {
	d.mu.Lock()
	d.targets = append(d.targets, address)
	d.mu.Unlock()
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		d.serve(server)
	}()
	return client, nil
}

func (d *dnsExit) seen() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.targets...)
}

// serveARecord answers every exchange with an A record for the question in the query.
func serveARecord(c net.Conn) {
	q, err := readDNSFrame(c)
	if err != nil {
		return
	}
	answer, err := dnsARecord(q, [4]byte{203, 0, 113, 80})
	if err != nil {
		return
	}
	_ = writeDNSFrame(c, answer)
}

// serveRaw reads the query and answers with raw bytes, whatever they are.
func serveRaw(raw []byte) func(net.Conn) {
	return func(c net.Conn) {
		if _, err := readDNSFrame(c); err != nil {
			return
		}
		_, _ = c.Write(raw)
	}
}

// servePrebuilt reads the query and answers with the frame msg.
func servePrebuilt(msg []byte) func(net.Conn) {
	return func(c net.Conn) {
		if _, err := readDNSFrame(c); err != nil {
			return
		}
		_ = writeDNSFrame(c, msg)
	}
}

// serveSilent reads the query and never answers, until the datapath closes the stream.
func serveSilent(c net.Conn) {
	if _, err := readDNSFrame(c); err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, c)
}

// A DNS query from the client to the adapter's address reaches the DNS server behind the
// fake exit as one DNS-over-TCP exchange to tun_dns, and the answer comes back as one UDP
// datagram from the adapter's address.
func TestNetstackDNS(t *testing.T) {
	const id = 0x1234
	query := dnsQuery(t, id, "t.example.com.")
	want, err := dnsARecord(query, [4]byte{203, 0, 113, 80})
	if err != nil {
		t.Fatal(err)
	}
	ex := &dnsExit{serve: serveARecord}
	p := newDNSPair(t, ex)
	p.addAddresses(t)
	conn := p.udp(t, "192.0.2.2:53")
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(query); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, dnsMaxSize)
	n, from, err := conn.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(want) || !bytes.Equal(buf[:n], want) {
		t.Fatalf("the client got %d bytes, want the %d-byte answer", n, len(want))
	}
	ua, ok := from.(*net.UDPAddr)
	if !ok || !ua.IP.Equal(net.IPv4(192, 0, 2, 2)) || ua.Port != dnsPort {
		t.Fatalf("the answer came from %v, want 192.0.2.2:53", from)
	}
	var pr dnsmessage.Parser
	hdr, err := pr.Start(buf[:n])
	if err != nil {
		t.Fatalf("the answer does not parse: %v", err)
	}
	if hdr.ID != id || !hdr.Response {
		t.Fatalf("the answer: ID %#x, response %v", hdr.ID, hdr.Response)
	}
	qs, err := pr.AllQuestions()
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 1 || qs[0].Name.String() != "t.example.com." {
		t.Fatalf("the answer echoes questions %v", qs)
	}
	as, err := pr.AllAnswers()
	if err != nil {
		t.Fatal(err)
	}
	a, ok := as[0].Body.(*dnsmessage.AResource)
	if !ok || a.A != [4]byte{203, 0, 113, 80} {
		t.Fatalf("the answer carries %v, want an A record", as[0].Body)
	}
	if got := ex.seen(); len(got) != 1 || got[0] != "203.0.113.53:53" {
		t.Fatalf("the exit saw %v, want one dial to 203.0.113.53:53", got)
	}
}

// A dead tunnel, a bad length prefix, an answer for another query and a timeout each give
// the client a SERVFAIL with the query's ID instead of a hang.
func TestNetstackDNSServfail(t *testing.T) {
	const id = 0x4321
	query := dnsQuery(t, id, "t.example.com.")
	answer, err := dnsARecord(query, [4]byte{203, 0, 113, 80})
	if err != nil {
		t.Fatal(err)
	}
	another := append([]byte(nil), answer...)
	binary.BigEndian.PutUint16(another, id+1)
	notResponse := append([]byte(nil), answer...)
	notResponse[2] &^= 0x80

	refused := dialerFunc(func(context.Context, string) (net.Conn, error) {
		return nil, errors.New("the exit refused")
	})
	cases := []struct {
		name    string
		dialer  exit.Dialer
		timeout time.Duration
	}{
		{"refused dial", refused, 0},
		{"zero length prefix", &dnsExit{serve: serveRaw([]byte{0, 0})}, 0},
		{"short length prefix", &dnsExit{serve: serveRaw([]byte{0, 5})}, 0},
		{"another id", &dnsExit{serve: servePrebuilt(another)}, 0},
		{"not a response", &dnsExit{serve: servePrebuilt(notResponse)}, 0},
		{"silent", &dnsExit{serve: serveSilent}, 50 * time.Millisecond},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newDNSPair(t, tc.dialer)
			if tc.timeout > 0 {
				p.server.dnsTimeout = tc.timeout
			}
			p.addAddresses(t)
			conn := p.udp(t, "192.0.2.2:53")
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			if _, err := conn.Write(query); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, dnsMaxSize)
			n, _, err := conn.ReadFrom(buf)
			if err != nil {
				t.Fatalf("no answer: %v", err)
			}
			var pr dnsmessage.Parser
			hdr, err := pr.Start(buf[:n])
			if err != nil {
				t.Fatalf("the answer does not parse: %v", err)
			}
			if hdr.ID != id {
				t.Fatalf("the answer is for ID %#x, want %#x", hdr.ID, id)
			}
			if !hdr.Response || hdr.RCode != dnsmessage.RCodeServerFailure {
				t.Fatalf("the answer: response %v, rcode %v", hdr.Response, hdr.RCode)
			}
		})
	}
}

// A datagram shorter than a DNS header is dropped without an answer and without a dial.
func TestNetstackDNSShortDatagram(t *testing.T) {
	ex := &dnsExit{serve: serveSilent}
	p := newDNSPair(t, ex)
	p.addAddresses(t)
	conn := p.udp(t, "192.0.2.2:53")
	conn.SetDeadline(time.Now().Add(200 * time.Millisecond))
	if _, err := conn.Write([]byte("short")); err != nil {
		t.Fatal(err)
	}
	if n, _, err := conn.ReadFrom(make([]byte, 512)); err == nil {
		t.Fatalf("a 5-byte datagram got a %d-byte answer", n)
	}
	if got := ex.seen(); len(got) != 0 {
		t.Fatalf("the exit saw %v for a datagram below the header", got)
	}
}

// A slow exchange does not delay another one: the delayed answer arrives after the fast
// one, although its query went first.
func TestNetstackDNSSlowFast(t *testing.T) {
	const delay = 200 * time.Millisecond
	ex := &dnsExit{serve: func(c net.Conn) {
		q, err := readDNSFrame(c)
		if err != nil {
			return
		}
		name, err := dnsQuestionName(q)
		if err != nil {
			return
		}
		if name == "slow.t.example.com." {
			time.Sleep(delay)
		}
		answer, err := dnsARecord(q, [4]byte{203, 0, 113, 1})
		if err != nil {
			return
		}
		_ = writeDNSFrame(c, answer)
	}}
	p := newDNSPair(t, ex)
	p.addAddresses(t)

	type result struct {
		id  uint16
		err error
	}
	results := make(chan result, 2)
	send := func(query []byte) {
		go func() {
			conn, err := udpTo(p.client, netip.MustParseAddr("192.0.2.1"), netip.MustParseAddrPort("192.0.2.2:53"))
			if err != nil {
				results <- result{err: err}
				return
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			if _, err := conn.Write(query); err != nil {
				results <- result{err: err}
				return
			}
			buf := make([]byte, dnsMaxSize)
			n, _, err := conn.ReadFrom(buf)
			if err != nil {
				results <- result{err: err}
				return
			}
			var pr dnsmessage.Parser
			hdr, err := pr.Start(buf[:n])
			if err != nil {
				results <- result{err: err}
				return
			}
			results <- result{id: hdr.ID}
		}()
	}
	send(dnsQuery(t, 1, "slow.t.example.com."))
	time.Sleep(50 * time.Millisecond)
	send(dnsQuery(t, 2, "fast.t.example.com."))
	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("an exchange failed: %v, %v", first.err, second.err)
	}
	if first.id != 2 || second.id != 1 {
		t.Fatalf("the answers came in order %#x, %#x; the fast one must be first", first.id, second.id)
	}
}

// Several queries at once each get their own answer.
func TestNetstackDNSConcurrent(t *testing.T) {
	const count = 8
	ex := &dnsExit{serve: func(c net.Conn) {
		q, err := readDNSFrame(c)
		if err != nil {
			return
		}
		var p dnsmessage.Parser
		hdr, err := p.Start(q)
		if err != nil {
			return
		}
		answer, err := dnsARecord(q, [4]byte{203, 0, 113, byte(hdr.ID)})
		if err != nil {
			return
		}
		_ = writeDNSFrame(c, answer)
	}}
	p := newDNSPair(t, ex)
	p.addAddresses(t)

	queries := make([][]byte, count)
	for i := range queries {
		queries[i] = dnsQuery(t, uint16(i+1), fmt.Sprintf("q%d.t.example.com.", i))
	}
	errc := make(chan error, count)
	for i := 0; i < count; i++ {
		go func(i int) {
			name := fmt.Sprintf("q%d.t.example.com.", i)
			conn, err := udpTo(p.client, netip.MustParseAddr("192.0.2.1"), netip.MustParseAddrPort("192.0.2.2:53"))
			if err != nil {
				errc <- err
				return
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(10 * time.Second))
			if _, err := conn.Write(queries[i]); err != nil {
				errc <- err
				return
			}
			buf := make([]byte, dnsMaxSize)
			size, _, err := conn.ReadFrom(buf)
			if err != nil {
				errc <- err
				return
			}
			var pr dnsmessage.Parser
			hdr, err := pr.Start(buf[:size])
			if err != nil {
				errc <- err
				return
			}
			if hdr.ID != uint16(i+1) {
				errc <- fmt.Errorf("the answer is for ID %#x, want %#x", hdr.ID, i+1)
				return
			}
			qs, err := pr.AllQuestions()
			if err != nil {
				errc <- err
				return
			}
			if len(qs) != 1 || qs[0].Name.String() != name {
				errc <- fmt.Errorf("the answer echoes %v, want %s", qs, name)
				return
			}
			as, err := pr.AllAnswers()
			if err != nil {
				errc <- err
				return
			}
			a, ok := as[0].Body.(*dnsmessage.AResource)
			if !ok || a.A != [4]byte{203, 0, 113, byte(i + 1)} {
				errc <- fmt.Errorf("the answer %d carries %v", i, as[0].Body)
				return
			}
			errc <- nil
		}(i)
	}
	for i := 0; i < count; i++ {
		if err := <-errc; err != nil {
			t.Fatal(err)
		}
	}
}

// No more than dnsInFlight exchanges are carried at once: the next query is answered with
// SERVFAIL at once instead of queueing behind them, and the exit is not dialed for it. A
// finished exchange releases its slot: the same listener carries a query again.
func TestNetstackDNSInFlight(t *testing.T) {
	const id = 0x5151
	query := dnsQuery(t, id, "t.example.com.")
	want, err := dnsARecord(query, [4]byte{203, 0, 113, 90})
	if err != nil {
		t.Fatal(err)
	}
	// The exit holds every exchange until release is closed and then answers it: all the
	// slots stay taken while the limit is checked, and the answers let the test wait for
	// the exchanges to finish.
	seen := make(chan struct{}, dnsInFlight)
	release := make(chan struct{})
	var unblockOnce sync.Once
	unblock := func() { unblockOnce.Do(func() { close(release) }) }
	defer unblock()
	ex := &dnsExit{serve: func(c net.Conn) {
		q, err := readDNSFrame(c)
		if err != nil {
			return
		}
		seen <- struct{}{}
		<-release
		answer, err := dnsARecord(q, [4]byte{203, 0, 113, 90})
		if err != nil {
			return
		}
		_ = writeDNSFrame(c, answer)
	}}
	p := newDNSPair(t, ex)
	p.addAddresses(t)

	remote := netip.MustParseAddrPort("192.0.2.2:53")
	conns := make([]*gonet.UDPConn, dnsInFlight)
	for i := range conns {
		conn, err := udpTo(p.client, netip.MustParseAddr("192.0.2.1"), remote)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		conns[i] = conn
		if _, err := conn.Write(query); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < dnsInFlight; i++ {
		select {
		case <-seen:
		case <-time.After(10 * time.Second):
			t.Fatalf("only %d of %d exchanges reached the exit", i, dnsInFlight)
		}
	}

	// Every slot is taken by a held exchange; the next query finds none.
	conn, err := udpTo(p.client, netip.MustParseAddr("192.0.2.1"), remote)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(query); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, dnsMaxSize)
	size, _, err := conn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("the query over the limit got no answer: %v", err)
	}
	var pr dnsmessage.Parser
	hdr, err := pr.Start(buf[:size])
	if err != nil {
		t.Fatalf("the answer does not parse: %v", err)
	}
	if hdr.ID != id || !hdr.Response || hdr.RCode != dnsmessage.RCodeServerFailure {
		t.Fatalf("the answer over the limit: ID %#x, response %v, rcode %v", hdr.ID, hdr.Response, hdr.RCode)
	}
	if got := len(ex.seen()); got != dnsInFlight {
		t.Fatalf("the exit was dialed %d times, want %d", got, dnsInFlight)
	}

	// The held exchanges finish: reading their answers waits for every exchange to have
	// written its answer, and so for the slots to be released.
	unblock()
	for i, held := range conns {
		held.SetDeadline(time.Now().Add(5 * time.Second))
		size, _, err := held.ReadFrom(buf)
		if err != nil {
			t.Fatalf("exchange %d got no answer: %v", i, err)
		}
		if !bytes.Equal(buf[:size], want) {
			t.Fatalf("exchange %d got %d bytes, want the %d-byte answer", i, size, len(want))
		}
	}

	// The listener carries a query again: a slot was released, the exit is dialed and a
	// real answer comes back instead of SERVFAIL.
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(query); err != nil {
		t.Fatal(err)
	}
	size, _, err = conn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("the query after the release got no answer: %v", err)
	}
	var after dnsmessage.Parser
	hdr, err = after.Start(buf[:size])
	if err != nil {
		t.Fatalf("the answer does not parse: %v", err)
	}
	if hdr.ID != id || !hdr.Response || hdr.RCode != dnsmessage.RCodeSuccess {
		t.Fatalf("the answer after the release: ID %#x, response %v, rcode %v", hdr.ID, hdr.Response, hdr.RCode)
	}
	if !bytes.Equal(buf[:size], want) {
		t.Fatalf("the answer after the release is %d bytes, want the %d-byte answer", size, len(want))
	}
	if got := len(ex.seen()); got != dnsInFlight+1 {
		t.Fatalf("the exit was dialed %d times, want %d", got, dnsInFlight+1)
	}
}

// hasAddress reports whether addr is assigned to the NIC of ns.
func hasAddress(ns *netstack, addr netip.Addr) bool {
	for _, a := range ns.stack.NICInfo()[nicID].ProtocolAddresses {
		if a.AddressWithPrefix.Address == tcpip.AddrFrom4(addr.As4()) {
			return true
		}
	}
	return false
}

// The DNS listener follows the adapter's address: a second address is refused while the
// listener is bound and is not left in the stack, and after removeAddress the listener
// binds to another address and answers there; close with a bound listener returns.
func TestNetstackDNSAddress(t *testing.T) {
	ex := &dnsExit{serve: serveARecord}
	p := newDNSPair(t, ex)
	p.addAddresses(t)
	first := netip.MustParseAddr("192.0.2.2")
	second := netip.MustParseAddr("198.51.100.2")

	if err := p.server.addAddress(second); err == nil {
		t.Fatal("a second address was accepted while the DNS listener is bound")
	}
	if hasAddress(p.server, second) {
		t.Fatalf("the refused address %v is still local", second)
	}

	p.server.removeAddress(first)
	if hasAddress(p.server, first) {
		t.Fatalf("the address %v is still local after removeAddress", first)
	}
	if err := p.server.addAddress(second); err != nil {
		t.Fatalf("the address was not accepted after removeAddress: %v", err)
	}
	if !hasAddress(p.server, second) {
		t.Fatalf("the address %v was not assigned", second)
	}
	query := dnsQuery(t, 0x2468, "t.example.com.")
	want, err := dnsARecord(query, [4]byte{203, 0, 113, 80})
	if err != nil {
		t.Fatal(err)
	}
	conn := p.udp(t, "198.51.100.2:53")
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(query); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, dnsMaxSize)
	size, _, err := conn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("the rebound listener gave no answer: %v", err)
	}
	if !bytes.Equal(buf[:size], want) {
		t.Fatalf("the client got %d bytes, want the %d-byte answer", size, len(want))
	}
	if got := ex.seen(); len(got) != 1 || got[0] != "203.0.113.53:53" {
		t.Fatalf("the exit saw %v, want one dial to 203.0.113.53:53", got)
	}

	// The first address can go back in after the listener bound to the second is gone.
	p.server.removeAddress(second)
	if err := p.server.addAddress(first); err != nil {
		t.Fatalf("the first address was not accepted again: %v", err)
	}

	// close with a bound listener returns; t.Cleanup calls it again.
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		p.close()
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("close waits for the DNS listener")
	}
}

// A unicast UDP datagram to any other destination produces ICMP type 3 code 3 from the
// original destination, carrying the original IPv4 packet; no stream is opened.
func TestNetstackICMPPortUnreachable(t *testing.T) {
	dials := 0
	dialer := dialerFunc(func(context.Context, string) (net.Conn, error) {
		dials++
		return nil, errors.New("unexpected dial")
	})
	client := newTestNetstack(t, dialer, netip.Addr{})
	server := newTestNetstack(t, dialer, netip.Addr{})
	if err := client.addAddress(netip.MustParseAddr("192.0.2.1")); err != nil {
		t.Fatal(err)
	}
	if err := server.addAddress(netip.MustParseAddr("192.0.2.2")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// A one-way pump: the client's datagrams go to the server, and the server's answers
	// are read by the test.
	toServer := make(chan []byte, 1)
	go func() {
		for client.next(ctx, func(p []byte) {
			toServer <- append([]byte(nil), p...)
		}) {
		}
	}()

	for _, target := range []string{"203.0.113.7:443", "192.0.2.2:5353"} {
		remote := netip.MustParseAddrPort(target)
		conn, err := udpTo(client, netip.MustParseAddr("192.0.2.1"), remote)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Write(payload(16)); err != nil {
			t.Fatal(err)
		}
		var sent []byte
		select {
		case sent = <-toServer:
		case <-time.After(5 * time.Second):
			t.Fatal("the client sent no datagram")
		}
		conn.Close()

		server.inject(sent)
		rctx, rcancel := context.WithTimeout(ctx, 5*time.Second)
		var reply []byte
		ok := server.next(rctx, func(p []byte) { reply = append([]byte(nil), p...) })
		rcancel()
		if !ok {
			t.Fatalf("no ICMP answer for %v", target)
		}
		checkICMP(t, reply, sent, remote.Addr(), netip.MustParseAddr("192.0.2.1"))
	}
	if dials != 0 {
		t.Fatalf("the exit was dialed %d times for a non-DNS datagram", dials)
	}
}

// checkICMP parses reply as an ICMP destination unreachable for original: type 3, code 3,
// source, destination and the original IPv4 packet in the payload.
func checkICMP(t *testing.T, reply, original []byte, source, destination netip.Addr) {
	t.Helper()
	if len(reply) < header.IPv4MinimumSize {
		t.Fatalf("the reply is %d bytes, too short for IPv4", len(reply))
	}
	ip := header.IPv4(reply)
	if !ip.IsValid(len(reply)) {
		t.Fatal("the reply is not a valid IPv4 packet")
	}
	if got := ip.Protocol(); got != uint8(header.ICMPv4ProtocolNumber) {
		t.Fatalf("the reply carries protocol %d, want ICMP", got)
	}
	if got := ip.SourceAddress(); got != tcpip.AddrFrom4(source.As4()) {
		t.Fatalf("the ICMP source is %v, want the original destination %v", got, source)
	}
	if got := ip.DestinationAddress(); got != tcpip.AddrFrom4(destination.As4()) {
		t.Fatalf("the ICMP destination is %v, want %v", got, destination)
	}
	icmp := header.ICMPv4(ip.Payload())
	if len(icmp) < header.ICMPv4MinimumSize {
		t.Fatalf("the ICMP header is %d bytes", len(icmp))
	}
	if icmp.Type() != header.ICMPv4DstUnreachable || icmp.Code() != header.ICMPv4PortUnreachable {
		t.Fatalf("the ICMP type is %d code %d, want 3/3", icmp.Type(), icmp.Code())
	}
	if got := icmp.Payload(); !bytes.Equal(got, original) {
		t.Fatalf("the ICMP payload is %d bytes, want the original %d-byte packet", len(got), len(original))
	}
}

// An IPv6 datagram never reaches transport: no answer and no dial.
func TestNetstackIPv6Dropped(t *testing.T) {
	dials := 0
	dialer := dialerFunc(func(context.Context, string) (net.Conn, error) {
		dials++
		return nil, errors.New("unexpected dial")
	})
	ns := newTestNetstack(t, dialer, netip.MustParseAddr("203.0.113.53"))
	if err := ns.addAddress(netip.MustParseAddr("192.0.2.2")); err != nil {
		t.Fatal(err)
	}
	pkt := make([]byte, header.IPv6MinimumSize+8)
	pkt[0] = 0x60
	ip := header.IPv6(pkt)
	ip.SetPayloadLength(8)
	ip.SetNextHeader(uint8(header.UDPProtocolNumber))
	ip.SetHopLimit(64)
	ip.SetSourceAddress(tcpip.AddrFrom16(netip.MustParseAddr("2001:db8::1").As16()))
	ip.SetDestinationAddress(tcpip.AddrFrom16(netip.MustParseAddr("2001:db8::2").As16()))
	ns.inject(pkt)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if ns.next(ctx, func([]byte) {
		t.Error("the stack answered an IPv6 datagram")
	}) {
		t.Fatal("the stack sent a packet for an IPv6 datagram")
	}
	if dials != 0 {
		t.Fatalf("the exit was dialed %d times for IPv6", dials)
	}
}

// A query in flight at close ends without a panic, and no datapath goroutine survives.
func TestNetstackCloseDNSInFlight(t *testing.T) {
	before := runtime.NumGoroutine()
	query := dnsQuery(t, 0x7777, "t.example.com.")
	seen := make(chan struct{})
	ex := &dnsExit{serve: func(c net.Conn) {
		if _, err := readDNSFrame(c); err != nil {
			return
		}
		close(seen)
		_, _ = io.Copy(io.Discard, c)
	}}
	p := newDNSPair(t, ex)
	p.addAddresses(t)
	conn := p.udp(t, "192.0.2.2:53")
	if _, err := conn.Write(query); err != nil {
		t.Fatal(err)
	}
	select {
	case <-seen:
	case <-time.After(5 * time.Second):
		t.Fatal("the exit saw no query")
	}

	p.close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, _, err := conn.ReadFrom(make([]byte, 512)); err == nil {
		t.Fatal("the client got an answer after close")
	}
	waitGoroutines(t, before)
}
