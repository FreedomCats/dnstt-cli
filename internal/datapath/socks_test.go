package datapath

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"net/netip"
	"os"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FreedomCats/dnstt-cli/core/client"
	"github.com/FreedomCats/dnstt-cli/core/dns"
	"github.com/FreedomCats/dnstt-cli/core/server"
	"github.com/FreedomCats/dnstt-cli/internal/exit"
	"golang.org/x/net/proxy"
)

func TestMain(m *testing.M) {
	// The upstream server logs through the log package; only -v shows it.
	flag.Parse()
	if !testing.Verbose() {
		log.SetOutput(io.Discard)
	}
	os.Exit(m.Run())
}

// The whole path of a program's connection: a SOCKS5 client → the datapath's listener →
// exit.SOCKS5 → a session of the core → the upstream server in-process → the SOCKS5 server
// behind it → the target.
func TestThroughTunnel(t *testing.T) {
	echo := echoServer(t)
	srv := &exitServer{names: map[string]string{"echo.example.com": echo}}
	s := tunnelSession(t, srv)
	d := &exit.SOCKS5{Open: func(context.Context) (net.Conn, error) { return s.OpenStream() }}
	dp := newSocks(t, SocksOptions{Dialer: d, Up: func() bool { return true }})
	client := socksClient(t, dp)

	// By name: the name goes to the exit and is resolved there.
	conn, err := client.Dial("tcp", "echo.example.com:7")
	if err != nil {
		t.Fatal(err)
	}
	echoThrough(t, conn, payload(50000))
	if got := srv.events(); !slices.Contains(got, "connect domain echo.example.com:7") {
		t.Fatalf("the exit saw %v, want the name as a name", got)
	}
	conn.Close()

	// By address.
	conn, err = client.Dial("tcp", echo)
	if err != nil {
		t.Fatal(err)
	}
	echoThrough(t, conn, payload(1000))
	if got := srv.events(); !slices.Contains(got, "connect ipv4 "+echo) {
		t.Fatalf("the exit saw %v, want the address as an address", got)
	}
	conn.Close()

	// A second connection after the first one is closed: the listener lives on.
	conn, err = client.Dial("tcp", "echo.example.com:7")
	if err != nil {
		t.Fatal(err)
	}
	echoThrough(t, conn, payload(200000))
	conn.Close()

	// A closed port behind the exit: its code reaches the client.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := ln.Addr().String()
	ln.Close()
	if _, err := client.Dial("tcp", closed); err == nil || !bytes.Contains([]byte(err.Error()), []byte("connection refused")) {
		t.Fatalf("to a closed port: %v, want connection refused", err)
	}
	// The session is still good after the refusal.
	conn, err = client.Dial("tcp", "echo.example.com:7")
	if err != nil {
		t.Fatal(err)
	}
	echoThrough(t, conn, []byte("after the refusal"))
	conn.Close()

	// An IPv6 address goes to the exit as an IPv6 address: the exit decides whether it can
	// reach it, the shell does not. The test's exit cannot reach one, so the program gets
	// its refusal — the point is what the exit was asked for.
	if _, err := client.Dial("tcp", "[2001:db8::7]:443"); err == nil {
		t.Fatal("an IPv6 target: the test's exit cannot reach one, so it must refuse")
	}
	if got := srv.events(); !slices.Contains(got, "connect ipv6 [2001:db8::7]:443") {
		t.Fatalf("the exit saw %v, want the IPv6 address as an address", got)
	}
}

// A failure that is not a refusal of the exit is a general failure for the program: the
// session ended, the exit is not SOCKS5, the stream closed.
func TestGeneralFailure(t *testing.T) {
	dp := newSocks(t, SocksOptions{Dialer: dialerFunc(func(context.Context, string) (net.Conn, error) {
		return nil, errors.New("the session ended")
	})})
	_, err := socksClient(t, dp).Dial("tcp", "echo.example.com:7")
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("general SOCKS server failure")) {
		t.Fatalf("a failure of the tunnel: %v, want a general failure", err)
	}
}

// A refusal of the exit reaches a real client as its code.
func TestConnectRefusalCodes(t *testing.T) {
	dp := newSocks(t, SocksOptions{Dialer: dialerFunc(func(context.Context, string) (net.Conn, error) {
		return nil, &exit.ConnectError{Address: "198.51.100.7:80", Code: 0x02}
	})})
	_, err := socksClient(t, dp).Dial("tcp", "198.51.100.7:80")
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("connection not allowed by ruleset")) {
		t.Fatalf("a refusal of the exit: %v, want its code", err)
	}
}

// BIND and UDP ASSOCIATE are refused at once, and an unknown address type too; the greeting
// is answered by hand, because a client library would refuse to send what it must not.
func TestUnsupportedRequests(t *testing.T) {
	dp := newSocks(t, SocksOptions{Dialer: dialerFunc(func(context.Context, string) (net.Conn, error) {
		t.Error("the exit was asked for a connection")
		return nil, errors.New("unexpected")
	})})
	raw := func(request []byte) byte {
		c, err := net.Dial("tcp", dp.ListenAddr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(10 * time.Second))
		if _, err := c.Write([]byte{socksVersion, 1, methodNone}); err != nil {
			t.Fatal(err)
		}
		var choice [2]byte
		if _, err := io.ReadFull(c, choice[:]); err != nil {
			t.Fatal(err)
		}
		if choice != [2]byte{socksVersion, methodNone} {
			t.Fatalf("the greeting was answered with %v", choice)
		}
		if _, err := c.Write(request); err != nil {
			t.Fatal(err)
		}
		var reply [10]byte
		if _, err := io.ReadFull(c, reply[:]); err != nil {
			t.Fatal(err)
		}
		if reply[0] != socksVersion || reply[2] != 0 {
			t.Fatalf("a reply of %v", reply)
		}
		return reply[1]
	}
	v4 := []byte{127, 0, 0, 1}
	port := []byte{0x00, 0x50}
	if code := raw(append([]byte{socksVersion, 0x02, 0, atypIPv4}, append(v4, port...)...)); code != repCommandNotSupported {
		t.Errorf("BIND: reply %#02x, want %#02x", code, repCommandNotSupported)
	}
	if code := raw(append([]byte{socksVersion, 0x03, 0, atypIPv4}, append(v4, port...)...)); code != repCommandNotSupported {
		t.Errorf("UDP ASSOCIATE: reply %#02x, want %#02x", code, repCommandNotSupported)
	}
	// An address type with an unknown length: the reply goes out without the rest of the
	// request.
	if code := raw([]byte{socksVersion, cmdConnect, 0, 0x09}); code != repAddressNotSupported {
		t.Errorf("address type 0x09: reply %#02x, want %#02x", code, repAddressNotSupported)
	}
	// An empty name is no address at all.
	if code := raw([]byte{socksVersion, cmdConnect, 0, atypDomain, 0, 0, 80}); code != repHostUnreachable {
		t.Errorf("an empty name: reply %#02x, want %#02x", code, repHostUnreachable)
	}
}

// The hook says the tunnel is down: the refusal is immediate, and the exit is not asked.
func TestNoSession(t *testing.T) {
	var asked atomic.Int32
	d := dialerFunc(func(context.Context, string) (net.Conn, error) {
		asked.Add(1)
		return nil, errors.New("unexpected")
	})
	dp := newSocks(t, SocksOptions{Dialer: d, Up: func() bool { return false }})
	start := time.Now()
	_, err := socksClient(t, dp).Dial("tcp", "echo.example.com:7")
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("network unreachable")) {
		t.Fatalf("with no session: %v, want network unreachable", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("the refusal took %v, want it at once", elapsed)
	}
	if n := asked.Load(); n != 0 {
		t.Fatalf("the exit was asked %d times, want never", n)
	}
}

// A listener address that is not loopback is refused by New: there is no password on the
// local side.
func TestNotLoopback(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:1080", "[::]:1080", "192.0.2.10:1080"} {
		_, err := NewSocks(SocksOptions{Listen: addr, Dialer: dialerFunc(nil)})
		if err == nil || !bytes.Contains([]byte(err.Error()), []byte("loopback")) {
			t.Errorf("%s: %v, want a refusal because it is not loopback", addr, err)
		}
	}
	for _, addr := range []string{"not an address", "127.0.0.1", "127.0.0.1:http"} {
		if _, err := NewSocks(SocksOptions{Listen: addr, Dialer: dialerFunc(nil)}); err == nil {
			t.Errorf("%q: no error for a bad address", addr)
		}
	}
}

// A busy port is found out by New, before anything is connected.
func TestBusyPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, err = NewSocks(SocksOptions{Listen: ln.Addr().String(), Dialer: dialerFunc(nil)})
	if err == nil {
		t.Fatal("no error for a busy port")
	}
	// The listener of the test is the one that holds the port: the failure is the
	// system's, not a refusal of the address. The error of a busy port reads differently
	// on each system, so what is checked is its kind, a failed listen.
	var opErr *net.OpError
	if !errors.As(err, &opErr) || opErr.Op != "listen" {
		t.Fatalf("a busy port: %v, want a failed listen", err)
	}
}

// A client that offers only a login gets 0xFF and no more: the local side has no password.
func TestNoAcceptableMethod(t *testing.T) {
	dp := newSocks(t, SocksOptions{Dialer: dialerFunc(nil)})
	c, err := net.Dial("tcp", dp.ListenAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.Write([]byte{socksVersion, 1, 0x02}); err != nil {
		t.Fatal(err)
	}
	var answer [2]byte
	if _, err := io.ReadFull(c, answer[:]); err != nil {
		t.Fatal(err)
	}
	if answer != [2]byte{socksVersion, methodNoAcceptable} {
		t.Fatalf("the answer to a login-only client: %v, want 0xFF", answer)
	}
	// The connection is not spoken to again.
	if n, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatalf("the server said %d more bytes", n)
	}
}

// The codes of the exit are passed on as they are; everything else is a general failure.
func TestReplyCode(t *testing.T) {
	for _, code := range []byte{0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08} {
		err := &exit.ConnectError{Address: "198.51.100.7:80", Code: code}
		if got := replyCode(err); got != code {
			t.Errorf("a refusal with code %#02x: got %#02x", code, got)
		}
	}
	cases := []error{
		errors.New("the stream closed"),
		&exit.LoginError{},
		&exit.HTTPConnectError{StatusCode: 403},
		&exit.HTTPConnectError{StatusCode: 502},
		exit.ErrHTTPProtocol,
		exit.ErrNotSOCKS5,
		context.DeadlineExceeded,
	}
	for _, err := range cases {
		if got := replyCode(err); got != repFailure {
			t.Errorf("%v: got %#02x, want a general failure", err, got)
		}
	}
}

// A refusal of the exit reaches a real client as its code, through the whole path.
func TestReplyThroughExit(t *testing.T) {
	srv := &exitServer{refuse: 0x04}
	s := tunnelSession(t, srv)
	d := &exit.SOCKS5{Open: func(context.Context) (net.Conn, error) { return s.OpenStream() }}
	dp := newSocks(t, SocksOptions{Dialer: d})
	_, err := socksClient(t, dp).Dial("tcp", "192.0.2.1:80")
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("host unreachable")) {
		t.Fatalf("the exit refused: %v, want host unreachable", err)
	}
}

// A dial that waits for the exit does not outlive Close: the program is let go with a
// failure instead of waiting for a session that will never come.
func TestCloseEndsAWaitingDial(t *testing.T) {
	started := make(chan struct{})
	d := dialerFunc(func(ctx context.Context, address string) (net.Conn, error) {
		close(started)
		// Unlike the real exit, which has its own limit, this one waits for the end of ctx.
		<-ctx.Done()
		return nil, ctx.Err()
	})
	dp := newSocks(t, SocksOptions{Dialer: d})
	c, err := net.Dial("tcp", dp.ListenAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.Write([]byte{socksVersion, 1, methodNone, socksVersion, cmdConnect, 0, atypDomain, 11}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write([]byte("echo.example.com\x00\x07")); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := dp.Close(); err != nil {
		t.Fatal(err)
	}
	var answer [12]byte
	if _, err := io.ReadFull(c, answer[:]); err != nil {
		// Close may also cut the connection before the reply: either way the program is
		// not left waiting.
		return
	}
	if answer[2] != socksVersion || answer[3] != repFailure {
		t.Fatalf("the answer after Close: %v, want a general failure or an end of the connection", answer)
	}
}

// A program that closes its side of the conversation after the request does not hang: the
// stream has no half-close, so the end of one direction ends the connection, and the read
// that follows returns instead of waiting for a target that has nothing more to say.
func TestHalfCloseEndsTheConnection(t *testing.T) {
	// A server that reads to the end and closes: it says nothing back.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	served := make(chan struct{})
	go func() {
		defer close(served)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		io.Copy(io.Discard, conn)
	}()

	// The client library wraps the socket, so CloseWrite is not reached through it: the
	// handshake is done by hand, and the socket stays a *net.TCPConn.
	dp := newSocks(t, SocksOptions{Dialer: directDialer{}})
	c, err := net.Dial("tcp", dp.ListenAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	conn := c.(*net.TCPConn)
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	port := ln.Addr().(*net.TCPAddr).Port
	req := []byte{socksVersion, 1, methodNone}
	req = append(req, socksVersion, cmdConnect, 0, atypIPv4, 127, 0, 0, 1)
	req = binary.BigEndian.AppendUint16(req, uint16(port))
	if _, err := conn.Write(req); err != nil {
		t.Fatal(err)
	}
	var answer [12]byte
	if _, err := io.ReadFull(conn, answer[:]); err != nil {
		t.Fatal(err)
	}
	// The greeting's answer is 2 bytes, the request's 10, so the whole exchange is 12.
	if answer[0] != socksVersion || answer[1] != methodNone || answer[2] != socksVersion || answer[3] != repSucceeded {
		t.Fatalf("the answer to the greeting and the request: %v", answer)
	}
	if _, err := conn.Write([]byte("a request")); err != nil {
		t.Fatal(err)
	}
	if err := conn.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-served:
	case <-time.After(10 * time.Second):
		t.Fatal("the target never saw the end of the request: the relay held it")
	}
	// The target is gone; the read ends instead of waiting forever.
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("the connection is still open after the target closed")
	}
}

// Close ends the listener and the connections in progress, and leaves no goroutine of the
// datapath behind: an idle client, one in the middle of a request and one in the relay.
func TestCloseLeavesNothing(t *testing.T) {
	echo := echoServer(t)
	before := runtime.NumGoroutine()
	dp := newSocks(t, SocksOptions{Dialer: directDialer{}})

	// An idle client: it never sends the greeting.
	idle, err := net.Dial("tcp", dp.ListenAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	// A client in the middle of a request.
	half, err := net.Dial("tcp", dp.ListenAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	half.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := half.Write([]byte{socksVersion, 1, methodNone}); err != nil {
		t.Fatal(err)
	}
	var choice [2]byte
	if _, err := io.ReadFull(half, choice[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := half.Write([]byte{socksVersion, 0x01, 0x00, atypIPv4}); err != nil {
		t.Fatal(err)
	}
	// A client in the relay.
	live, err := socksClient(t, dp).Dial("tcp", echo)
	if err != nil {
		t.Fatal(err)
	}
	echoThrough(t, live, []byte("before the close"))

	if err := dp.Close(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []net.Conn{idle, half, live} {
		c.SetDeadline(time.Now().Add(10 * time.Second))
		if _, err := c.Read(make([]byte, 1)); err == nil {
			t.Fatalf("%v is still served after Close", c.RemoteAddr())
		}
		c.Close()
	}
	// A listener that is closed does not take new connections.
	if c, err := net.Dial("tcp", dp.ListenAddr().String()); err == nil {
		c.Close()
		t.Fatal("the listener still accepts after Close")
	}
	// Close again is harmless.
	if err := dp.Close(); err != nil {
		t.Fatal(err)
	}

	// The goroutines of the datapath are gone. The echo server was started before the
	// count, so its own goroutines are in the baseline; the ones of the test's clients may
	// take a moment to end, hence the settle.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if n := runtime.NumGoroutine(); n <= before {
			return
		}
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<20)
			n := runtime.Stack(buf, true)
			t.Fatalf("goroutines: %d before, %d after Close\n%s", before, runtime.NumGoroutine(), buf[:n])
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The address the exit was asked for: a name stays a name, an address goes as an address.
func TestRequestAddress(t *testing.T) {
	cases := []struct {
		req  request
		want string
	}{
		{request{host: "echo.example.com", port: 7}, "echo.example.com:7"},
		{request{addr: netip.MustParseAddr("192.0.2.7"), port: 80}, "192.0.2.7:80"},
		{request{addr: netip.MustParseAddr("2001:db8::7"), port: 443}, "[2001:db8::7]:443"},
	}
	for _, c := range cases {
		if got := c.req.address(); got != c.want {
			t.Errorf("%v: %q, want %q", c.req, got, c.want)
		}
	}
}

// --- helpers ---------------------------------------------------------------------------

// dialerFunc is an exit.Dialer from a function.
type dialerFunc func(ctx context.Context, address string) (net.Conn, error)

func (f dialerFunc) Dial(ctx context.Context, address string) (net.Conn, error) {
	if f == nil {
		return nil, errors.New("unexpected dial")
	}
	return f(ctx, address)
}

// directDialer is an exit that connects by itself, without a tunnel: the tests of the
// datapath alone.
type directDialer struct{}

func (directDialer) Dial(ctx context.Context, address string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "tcp", address)
}

// newSocks starts a datapath on a free loopback port and closes it with the test.
func newSocks(t *testing.T, opts SocksOptions) *Socks {
	t.Helper()
	opts.Listen = "127.0.0.1:0"
	if testing.Verbose() {
		opts.Logf = t.Logf
	}
	dp, err := NewSocks(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := dp.Close(); err != nil {
			t.Error(err)
		}
	})
	return dp
}

// socksClient is a real SOCKS5 client pointed at the datapath.
func socksClient(t *testing.T, dp *Socks) proxy.Dialer {
	t.Helper()
	c, err := proxy.SOCKS5("tcp", dp.ListenAddr().String(), nil, proxy.Direct)
	if err != nil {
		t.Fatal(err)
	}
	ctx, ok := c.(proxy.ContextDialer)
	if !ok {
		t.Fatal("the client does not speak with a context")
	}
	return &contextClient{ctx}
}

// contextClient gives Dial a deadline, as a program would.
type contextClient struct{ c proxy.ContextDialer }

func (c *contextClient) Dial(network, address string) (net.Conn, error) {
	return c.DialContext(context.Background(), network, address)
}

func (c *contextClient) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return c.c.DialContext(ctx, network, address)
}

// payload is n bytes of a pattern that repeats only every 256, so a swapped or lost block
// of an echo shows up.
func payload(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}

// echoThrough sends msg and expects it back, in one direction at a time.
func echoThrough(t *testing.T, c net.Conn, msg []byte) {
	t.Helper()
	c.SetDeadline(time.Now().Add(60 * time.Second))
	defer c.SetDeadline(time.Time{})
	errc := make(chan error, 1)
	go func() {
		_, err := c.Write(msg)
		errc <- err
	}()
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("echo: %d bytes differ from the %d sent", len(got), len(msg))
	}
}

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

// tunnelSession runs the upstream dnstt server in-process with the SOCKS5 server srv as its
// upstream and dials a session to it.
func tunnelSession(t *testing.T, srv *exitServer) *client.Session {
	t.Helper()
	domain, err := dns.ParseName("t.example.com")
	if err != nil {
		t.Fatal(err)
	}
	ts, err := server.StartTest(domain, srv.listen(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ts.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := client.Dial(ctx, client.Config{
		Domain:   domain,
		PubKey:   ts.PubKey,
		Resolver: client.Resolver{Transport: client.UDP, Address: ts.Addr.String()},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// exitServer is a SOCKS5 server for the tests (RFC 1928 CONNECT, no login), in the role of
// danted behind the dnstt server.
type exitServer struct {
	// names resolves host names; a name not in it is "host unreachable".
	names map[string]string
	// refuse, if not 0, is the reply to every CONNECT.
	refuse byte

	mu   sync.Mutex
	seen []string // what each connection asked for: "connect domain echo.example.com:7"…
}

func (s *exitServer) log(event string) {
	s.mu.Lock()
	s.seen = append(s.seen, event)
	s.mu.Unlock()
}

func (s *exitServer) events() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

// listen serves on loopback TCP and returns the address.
func (s *exitServer) listen(t *testing.T) string {
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
			go s.serve(conn)
		}
	}()
	return ln.Addr().String()
}

func (s *exitServer) serve(conn net.Conn) {
	defer conn.Close()
	var head [2]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil || head[0] != socksVersion {
		return
	}
	methods := make([]byte, head[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}
	if !slices.Contains(methods, methodNone) {
		conn.Write([]byte{socksVersion, methodNoAcceptable})
		return
	}
	if _, err := conn.Write([]byte{socksVersion, methodNone}); err != nil {
		return
	}

	var req [4]byte
	if _, err := io.ReadFull(conn, req[:]); err != nil || req[1] != cmdConnect {
		return
	}
	var host, kind string
	switch req[3] {
	case atypIPv4, atypIPv6:
		n := 4
		kind = "ipv4"
		if req[3] == atypIPv6 {
			n, kind = 16, "ipv6"
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(conn, b); err != nil {
			return
		}
		a, _ := netip.AddrFromSlice(b)
		host = a.String()
	case atypDomain:
		var n [1]byte
		if _, err := io.ReadFull(conn, n[:]); err != nil {
			return
		}
		b := make([]byte, n[0])
		if _, err := io.ReadFull(conn, b); err != nil {
			return
		}
		host, kind = string(b), "domain"
	default:
		return
	}
	var port [2]byte
	if _, err := io.ReadFull(conn, port[:]); err != nil {
		return
	}
	address := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port[:]))))
	s.log("connect " + kind + " " + address)

	if s.refuse != 0 {
		reply(conn, s.refuse)
		return
	}
	if kind == "domain" {
		target, ok := s.names[host]
		if !ok {
			reply(conn, repHostUnreachable)
			return
		}
		address = target
	}
	if kind == "ipv6" {
		// The tunnel does not carry IPv6 in these tests, and dialing an address of the
		// documentation range would wait for a timeout.
		reply(conn, repNetworkUnreachable)
		return
	}
	target, err := net.Dial("tcp", address)
	if err != nil {
		// The tests dial only loopback: a failure is a closed port.
		reply(conn, 0x05)
		return
	}
	defer target.Close()
	if reply(conn, repSucceeded) != nil {
		return
	}
	go func() {
		io.Copy(target, conn)
		target.Close()
	}()
	io.Copy(conn, target)
}

// reply writes the answer to CONNECT with a fixed bound address.
func reply(conn net.Conn, code byte) error {
	b := []byte{socksVersion, code, 0, atypIPv4, 192, 0, 2, 1, 0x1f, 0x90}
	_, err := conn.Write(b)
	return err
}
