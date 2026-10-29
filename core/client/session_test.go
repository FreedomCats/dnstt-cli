// Added by dnstt-cli (scripts/vendorcore/_files): not part of the dnstt upstream.

package client

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/FreedomCats/dnstt-cli/core/dns"
	"github.com/FreedomCats/dnstt-cli/core/server"
)

// The session tests run the upstream server in-process (core/server/testing.go) on
// loopback UDP, with an echo server as its upstream TCP address.

func TestMain(m *testing.M) {
	// The upstream client and server log through the log package; only -v shows it.
	flag.Parse()
	if !testing.Verbose() {
		log.SetOutput(io.Discard)
	}
	os.Exit(m.Run())
}

var testDomain = func() dns.Name {
	name, err := dns.ParseName("t.example.com")
	if err != nil {
		panic(err)
	}
	return name
}()

// echoServer accepts TCP connections on loopback and writes back what it reads.
func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				io.Copy(conn, conn)
			}()
		}
	}()
	return ln.Addr().String()
}

func startServer(t *testing.T) *server.TestServer {
	t.Helper()
	srv, err := server.StartTest(testDomain, echoServer(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	return srv
}

func udpConfig(srv *server.TestServer) Config {
	return Config{
		Domain:   testDomain,
		PubKey:   srv.PubKey,
		Resolver: Resolver{Transport: UDP, Address: srv.Addr.String()},
		Logf:     func(string, ...any) {},
	}
}

func dial(t *testing.T, cfg Config) *Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := Dial(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// echo sends a message through a stream and expects it back.
func echo(s *Session, msg []byte) error {
	stream, err := s.OpenStream()
	if err != nil {
		return err
	}
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := stream.Write(msg); err != nil {
		return err
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(stream, got); err != nil {
		return err
	}
	if !bytes.Equal(got, msg) {
		return fmt.Errorf("echo: got %d bytes that differ from the %d sent", len(got), len(msg))
	}
	return nil
}

// Two streams in parallel through one session, every socket through Control.
func TestSessionStreams(t *testing.T) {
	srv := startServer(t)
	cfg := udpConfig(srv)
	var controls atomic.Int32
	cfg.Control = func(network, address string, c syscall.RawConn) error {
		controls.Add(1)
		return nil
	}
	s := dial(t, cfg)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Go(func() {
			msg := bytes.Repeat([]byte(strconv.Itoa(i)), 20000)
			errs[i] = echo(s, msg)
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("stream %d: %v", i, err)
		}
	}

	// A UDP session has one socket.
	if n := controls.Load(); n != 1 {
		t.Errorf("Control called %d times, want 1", n)
	}
	st := s.Stats()
	if st.BytesSent < 40000 || st.BytesReceived < 40000 {
		t.Errorf("stats: %+v", st)
	}
}

// A silent server ends the session within the keepalive timeout: Done closes, Err says
// why, new streams fail.
func TestSessionServerSilent(t *testing.T) {
	srv := startServer(t)
	cfg := udpConfig(srv)
	cfg.KeepAliveTimeout = 2 * time.Second
	s := dial(t, cfg)
	if err := echo(s, []byte("before")); err != nil {
		t.Fatal(err)
	}

	srv.Close()
	// smux checks the timeout once per its period: the session ends between one and two
	// timeouts after the last data.
	select {
	case <-s.Done():
	case <-time.After(3 * cfg.KeepAliveTimeout):
		t.Fatal("session still alive after the server went silent")
	}
	if !errors.Is(s.Err(), ErrSilent) {
		t.Errorf("Err: %v, want ErrSilent", s.Err())
	}
	if _, err := s.OpenStream(); !errors.Is(err, ErrSilent) {
		t.Errorf("OpenStream after the end: %v", err)
	}
}

func TestSessionClose(t *testing.T) {
	srv := startServer(t)
	s := dial(t, udpConfig(srv))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done():
	default:
		t.Fatal("Done not closed after Close")
	}
	if !errors.Is(s.Err(), ErrClosed) {
		t.Errorf("Err: %v, want ErrClosed", s.Err())
	}
	if _, err := s.OpenStream(); err == nil {
		t.Error("OpenStream after Close succeeded")
	}
	s.Close() // a second Close is harmless
}

// clientGoroutines lists the goroutines of client sessions: everything running code of
// this package or a client KCP conn, except the tests and their echo server.
func clientGoroutines() []string {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	var found []string
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "core/client.Test") || strings.Contains(g, "core/client.echoServer") {
			continue
		}
		if strings.Contains(g, "core/client.") || strings.Contains(g, "(*UDPSession).readLoop") {
			found = append(found, g)
		}
	}
	return found
}

// Close leaves nothing running: the loops of the transport and of DNSPacketConn end with
// the session, unlike upstream, which has one session per process.
func TestSessionLeavesNoGoroutines(t *testing.T) {
	srv := startServer(t)
	s := dial(t, udpConfig(srv))
	if err := echo(s, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	s.Close()

	deadline := time.Now().Add(5 * time.Second)
	for {
		left := clientGoroutines()
		if len(left) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d goroutines left after Close:\n\n%s", len(left), strings.Join(left, "\n\n"))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// DoH and DoT, with uTLS and without, connect through Control. The resolver here is a
// TCP listener that drops every connection, so Dial fails; the connection attempt is what
// counts. Nothing is left running after the failed Dial.
func TestTransportsUseControl(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var accepted atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			conn.Close()
		}
	}()

	srv := startServer(t)
	for _, c := range []struct {
		name string
		r    Resolver
		utls string
	}{
		{"DoH", Resolver{DoH, "https://" + ln.Addr().String() + "/dns-query"}, "none"},
		{"DoH uTLS", Resolver{DoH, "https://" + ln.Addr().String() + "/dns-query"}, "Chrome_120"},
		{"DoT", Resolver{DoT, ln.Addr().String()}, "none"},
		{"DoT uTLS", Resolver{DoT, ln.Addr().String()}, "Firefox_120"},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg := udpConfig(srv)
			cfg.Resolver, cfg.UTLS = c.r, c.utls
			var controls atomic.Int32
			cfg.Control = func(network, address string, _ syscall.RawConn) error {
				if network != "tcp4" || address != ln.Addr().String() {
					t.Errorf("Control(%q, %q)", network, address)
				}
				controls.Add(1)
				return nil
			}
			before := accepted.Load()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if s, err := Dial(ctx, cfg); err == nil {
				s.Close()
				t.Fatal("Dial succeeded")
			}
			if controls.Load() == 0 || accepted.Load() == before {
				t.Errorf("Control calls %d, connections accepted %d", controls.Load(), accepted.Load()-before)
			}
			deadline := time.Now().Add(5 * time.Second)
			for len(clientGoroutines()) > 0 {
				if time.Now().After(deadline) {
					t.Fatalf("goroutines left after a failed Dial:\n\n%s", strings.Join(clientGoroutines(), "\n\n"))
				}
				time.Sleep(50 * time.Millisecond)
			}
		})
	}
}

// A resolver that never answers: Dial ends with the context and closes its socket.
func TestDialContextEnds(t *testing.T) {
	silent, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()

	srv := startServer(t)
	cfg := udpConfig(srv)
	cfg.Resolver.Address = silent.LocalAddr().String()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = Dial(ctx, cfg)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Dial: %v, want the context's deadline", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("Dial returned %v after the deadline", d)
	}
}

func TestDialRejects(t *testing.T) {
	srv := startServer(t)
	for name, spoil := range map[string]func(*Config){
		"short key":     func(c *Config) { c.PubKey = c.PubKey[:10] },
		"no domain":     func(c *Config) { c.Domain = nil },
		"plain http":    func(c *Config) { c.Resolver = Resolver{Transport: DoH, Address: "http://doh.example/dns-query"} },
		"no port":       func(c *Config) { c.Resolver = Resolver{Transport: DoT, Address: "dot.example"} },
		"bad UDP port":  func(c *Config) { c.Resolver = Resolver{Transport: UDP, Address: "192.0.2.1:dns"} },
		"bad transport": func(c *Config) { c.Resolver = Resolver{Transport: "doq", Address: "192.0.2.1:853"} },
		"bad uTLS":      func(c *Config) { c.UTLS = "1*Netscape" },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := udpConfig(srv)
			spoil(&cfg)
			if s, err := Dial(context.Background(), cfg); err == nil {
				s.Close()
				t.Error("Dial succeeded")
			}
		})
	}
}

// DefaultUTLS is the upstream default of -utls: the flag in upstream main must carry the
// same string, otherwise upstream changed its default and DefaultUTLS has to follow.
func TestDefaultUTLSIsUpstream(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(src, []byte(strconv.Quote(DefaultUTLS))) {
		t.Errorf("upstream main.go has no %q: its -utls default changed", DefaultUTLS)
	}
}
