package resolvers

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// dnsServer is a DNS server on loopback, UDP and TCP on one port, with a fixed zone:
//
//	a.example     A 192.0.2.10, 192.0.2.11
//	v6.example    AAAA 2001:db8::10, no A
//	empty.example no addresses
//	none.example  NXDOMAIN
//	big.example   truncated over UDP; A 192.0.2.20 over TCP
//	spoof.example an answer with another ID first, then A 192.0.2.30
//	lo.example    A 127.0.0.1
type dnsServer struct {
	addr netip.AddrPort

	mu    sync.Mutex
	asked []string // "udp A a.example."…
}

func startDNS(t *testing.T) *dnsServer {
	t.Helper()
	s := &dnsServer{}
	var pc net.PacketConn
	var ln net.Listener
	for range 10 {
		var err error
		if pc, err = net.ListenPacket("udp4", "127.0.0.1:0"); err != nil {
			t.Fatal(err)
		}
		if ln, err = net.Listen("tcp4", pc.LocalAddr().String()); err == nil {
			break
		}
		pc.Close()
		pc = nil
	}
	if pc == nil {
		t.Fatal("no free port for UDP and TCP")
	}
	t.Cleanup(func() { pc.Close(); ln.Close() })
	s.addr = netip.MustParseAddrPort(pc.LocalAddr().String())

	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			for _, a := range s.answer(buf[:n], "udp") {
				pc.WriteTo(a, from)
			}
		}
	}()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				var size [2]byte
				if _, err := io.ReadFull(c, size[:]); err != nil {
					return
				}
				q := make([]byte, binary.BigEndian.Uint16(size[:]))
				if _, err := io.ReadFull(c, q); err != nil {
					return
				}
				for _, a := range s.answer(q, "tcp") {
					c.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(a))), a...))
				}
			}()
		}
	}()
	return s
}

func (s *dnsServer) queries() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.asked)
}

func (s *dnsServer) answer(query []byte, proto string) [][]byte {
	var p dnsmessage.Parser
	h, err := p.Start(query)
	if err != nil {
		return nil
	}
	q, err := p.Question()
	if err != nil {
		return nil
	}
	s.mu.Lock()
	s.asked = append(s.asked, proto+" "+strings.TrimPrefix(q.Type.String(), "Type")+" "+q.Name.String())
	s.mu.Unlock()

	rh := dnsmessage.Header{ID: h.ID, Response: true, RecursionDesired: true, RecursionAvailable: true}
	var a4 []netip.Addr
	var a6 []netip.Addr
	var out [][]byte
	switch strings.ToLower(q.Name.String()) {
	case "a.example.":
		a4 = addrs("192.0.2.10", "192.0.2.11")
	case "lo.example.":
		a4 = addrs("127.0.0.1")
	case "v6.example.":
		a6 = addrs("2001:db8::10")
	case "empty.example.":
	case "none.example.":
		rh.RCode = dnsmessage.RCodeNameError
	case "big.example.":
		if proto == "udp" {
			rh.Truncated = true
		} else {
			a4 = addrs("192.0.2.20")
		}
	case "spoof.example.":
		other := rh
		other.ID++
		out = append(out, build(other, q, nil))
		a4 = addrs("192.0.2.30")
	default:
		rh.RCode = dnsmessage.RCodeServerFailure
	}
	recs := a4
	if q.Type == dnsmessage.TypeAAAA {
		recs = a6
	}
	return append(out, build(rh, q, recs))
}

func build(h dnsmessage.Header, q dnsmessage.Question, recs []netip.Addr) []byte {
	b := dnsmessage.NewBuilder(nil, h)
	b.StartQuestions()
	b.Question(q)
	b.StartAnswers()
	for _, a := range recs {
		rh := dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 60}
		if a.Is4() {
			b.AResource(rh, dnsmessage.AResource{A: a.As4()})
		} else {
			b.AAAAResource(rh, dnsmessage.AAAAResource{AAAA: a.As16()})
		}
	}
	msg, _ := b.Finish()
	return msg
}

// silentServer reads queries and never answers.
func silentServer(t *testing.T) netip.AddrPort {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go io.Copy(io.Discard, readerOf(pc))
	return netip.MustParseAddrPort(pc.LocalAddr().String())
}

type packetReader struct{ pc net.PacketConn }

func (r packetReader) Read(b []byte) (int, error) {
	n, _, err := r.pc.ReadFrom(b)
	return n, err
}

func readerOf(pc net.PacketConn) io.Reader { return packetReader{pc} }

func lookupCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestBootstrap(t *testing.T) {
	srv := startDNS(t)
	var mu sync.Mutex
	var networks []string
	control := func(network, address string, c syscall.RawConn) error {
		mu.Lock()
		networks = append(networks, network)
		mu.Unlock()
		return nil
	}
	b := NewBootstrap([]netip.AddrPort{srv.addr}, control)
	ctx := lookupCtx(t)

	for _, tc := range []struct {
		host string
		want []netip.Addr
		err  error
	}{
		{"a.example", addrs("192.0.2.10", "192.0.2.11"), nil},
		{"A.Example.", addrs("192.0.2.10", "192.0.2.11"), nil},
		{"v6.example", addrs("2001:db8::10"), nil},
		{"big.example", addrs("192.0.2.20"), nil},
		{"spoof.example", addrs("192.0.2.30"), nil},
		{"192.0.2.99", addrs("192.0.2.99"), nil},
		{"none.example", nil, ErrNotFound},
		{"empty.example", nil, ErrNoAddress},
	} {
		got, err := b.LookupIP(ctx, tc.host)
		if tc.err != nil {
			if !errors.Is(err, tc.err) {
				t.Errorf("%s: %v, want %v", tc.host, err, tc.err)
			}
			continue
		}
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("%s: %v, %v; want %v", tc.host, got, err, tc.want)
		}
	}

	// a.example came from the cache the second time; big.example went over TCP.
	q := srv.queries()
	count := func(s string) (n int) {
		for _, x := range q {
			if x == s {
				n++
			}
		}
		return n
	}
	for s, want := range map[string]int{
		"udp A a.example.":        1,
		"udp A v6.example.":       1,
		"udp AAAA v6.example.":    1,
		"udp A big.example.":      1,
		"tcp A big.example.":      1,
		"udp A none.example.":     1,
		"udp AAAA empty.example.": 1,
	} {
		if got := count(s); got != want {
			t.Errorf("%q asked %d times, want %d (all: %q)", s, got, want, q)
		}
	}
	// Every socket went through control.
	mu.Lock()
	defer mu.Unlock()
	if !slices.Contains(networks, "udp4") || !slices.Contains(networks, "tcp4") {
		t.Errorf("control saw %q, want udp4 and tcp4", networks)
	}
	if len(networks) != len(q) {
		t.Errorf("control saw %d sockets for %d queries", len(networks), len(q))
	}
}

// A silent server is passed over for the next one; the answer is kept until its TTL.
func TestBootstrapFailover(t *testing.T) {
	srv := startDNS(t)
	b := NewBootstrap([]netip.AddrPort{silentServer(t), srv.addr}, nil)
	b.timeout = 100 * time.Millisecond
	now := time.Unix(1000000, 0)
	b.now = func() time.Time { return now }

	if got, err := b.LookupIP(lookupCtx(t), "a.example"); err != nil || len(got) != 2 {
		t.Fatalf("%v, %v", got, err)
	}
	now = now.Add(59 * time.Second)
	b.LookupIP(lookupCtx(t), "a.example")
	if n := len(srv.queries()); n != 1 {
		t.Errorf("%d queries within the TTL", n)
	}
	now = now.Add(2 * time.Second)
	b.LookupIP(lookupCtx(t), "a.example")
	if n := len(srv.queries()); n != 2 {
		t.Errorf("%d queries after the TTL, want 2", n)
	}
	b.Forget()
	b.LookupIP(lookupCtx(t), "a.example")
	if n := len(srv.queries()); n != 3 {
		t.Errorf("%d queries after Forget, want 3", n)
	}
}

// Only silent servers: an error after every round, and ctx stops the wait.
func TestBootstrapNoAnswer(t *testing.T) {
	b := NewBootstrap([]netip.AddrPort{silentServer(t), silentServer(t)}, nil)
	b.timeout = 50 * time.Millisecond
	start := time.Now()
	_, err := b.LookupIP(lookupCtx(t), "a.example")
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(err.Error(), "a.example") {
		t.Errorf("%q does not name the host", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v", d)
	}

	b.timeout = time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	if _, err := b.LookupIP(ctx, "a.example"); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}

	if _, err := NewBootstrap(nil, nil).LookupIP(lookupCtx(t), "a.example"); err == nil {
		t.Error("no servers: no error")
	}
}

func TestBootstrapServers(t *testing.T) {
	list, err := Expand([]string{"doh-google", "udp:192.0.2.53", "udp:dns.example@192.0.2.54", "udp:[::ffff:192.0.2.55]:5353", "udp:192.0.2.53:53"})
	if err != nil {
		t.Fatal(err)
	}
	got := BootstrapServers(list)
	want := append([]netip.AddrPort{
		netip.MustParseAddrPort("192.0.2.53:53"),
		netip.MustParseAddrPort("192.0.2.55:5353"),
	}, DefaultBootstrap...)
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
