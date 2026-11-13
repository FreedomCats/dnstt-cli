package exit

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

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

// echo sends msg through conn and expects it back.
func echo(t *testing.T, conn net.Conn, msg []byte) {
	t.Helper()
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	errc := make(chan error, 1)
	go func() {
		_, err := conn.Write(msg)
		errc <- err
	}()
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("echo: got %d bytes that differ from the %d sent", len(got), len(msg))
	}
}

func dialCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// A name goes to the exit as a name, without a login when the exit needs none.
func TestDialNoLogin(t *testing.T) {
	srv := &socksServer{names: map[string]string{"echo.example.com": echoServer(t)}}
	d := &SOCKS5{Open: srv.pipe()}
	conn, err := d.Dial(dialCtx(t), "echo.example.com:7")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	echo(t, conn, []byte("hello"))
	if got, want := srv.events(), []string{"connect domain echo.example.com:7"}; !slices.Equal(got, want) {
		t.Errorf("server saw %q, want %q", got, want)
	}
}

func TestDialLogin(t *testing.T) {
	srv := &socksServer{user: "user", password: "secret"}
	d := &SOCKS5{Open: srv.pipe(), User: "user", Password: "secret"}
	conn, err := d.Dial(dialCtx(t), echoServer(t))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	echo(t, conn, bytes.Repeat([]byte("x"), 100000))
	if ev := srv.events(); len(ev) != 2 || ev[0] != "login user" || !strings.HasPrefix(ev[1], "connect ipv4 127.0.0.1:") {
		t.Errorf("server saw %q", ev)
	}
}

// An exit that lets anyone in is not sent the password.
func TestDialLoginNotNeeded(t *testing.T) {
	srv := &socksServer{}
	d := &SOCKS5{Open: srv.pipe(), User: "user", Password: "secret"}
	conn, err := d.Dial(dialCtx(t), echoServer(t))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	echo(t, conn, []byte("hello"))
	for _, ev := range srv.events() {
		if strings.HasPrefix(ev, "login") {
			t.Errorf("the login was sent to an exit without one: %q", ev)
		}
	}
}

func TestDialLoginRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		srv  *socksServer
		d    SOCKS5
		want string
	}{
		{"wrong password", &socksServer{user: "user", password: "secret"},
			SOCKS5{User: "user", Password: "wrong"}, "wrong user name or password"},
		{"wrong user", &socksServer{user: "user", password: "secret"},
			SOCKS5{User: "other", Password: "secret"}, "wrong user name or password"},
		{"no login", &socksServer{user: "user", password: "secret"},
			SOCKS5{}, "the profile has none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.d.Open = tc.srv.pipe()
			conn, err := tc.d.Dial(dialCtx(t), "192.0.2.1:80")
			if err == nil {
				conn.Close()
				t.Fatal("Dial succeeded")
			}
			if !IsLogin(err) {
				t.Fatalf("%v: not a login error", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%q does not say %q", err, tc.want)
			}
			for _, ev := range tc.srv.events() {
				if strings.HasPrefix(ev, "connect") {
					t.Errorf("a request after a refused login: %q", ev)
				}
			}
			// Check gets the same answer.
			if err := tc.d.Check(dialCtx(t)); !IsLogin(err) {
				t.Errorf("Check: %v, want a login error", err)
			}
		})
	}
}

func TestDialConnectRefused(t *testing.T) {
	srv := &socksServer{refuse: 0x02}
	d := &SOCKS5{Open: srv.pipe()}
	_, err := d.Dial(dialCtx(t), "blocked.example.com:443")
	var ce *ConnectError
	if !errors.As(err, &ce) {
		t.Fatalf("%v: not a *ConnectError", err)
	}
	if ce.Code != 0x02 || ce.Address != "blocked.example.com:443" {
		t.Errorf("got %+v", ce)
	}
	if IsLogin(err) {
		t.Error("a refused CONNECT is a login error")
	}
	if want := "not allowed"; !strings.Contains(err.Error(), want) {
		t.Errorf("%q does not say %q", err, want)
	}
}

// A refusal cut short after its code is still a refusal.
func TestDialRefusalCutShort(t *testing.T) {
	open := func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			buf := make([]byte, 64)
			server.Read(buf)
			server.Write([]byte{socksVersion, methodNone})
			server.Read(buf)
			server.Write([]byte{socksVersion, 0x04})
		}()
		return client, nil
	}
	_, err := (&SOCKS5{Open: open}).Dial(dialCtx(t), "192.0.2.1:80")
	var ce *ConnectError
	if !errors.As(err, &ce) || ce.Code != 0x04 {
		t.Fatalf("got %v, want a *ConnectError with code 4", err)
	}
}

// The reply is read to its end and no further: the target's first bytes, sent in the same
// write, stay in the stream. The bound address here is a name, of a length read off the
// reply.
func TestDialTargetSpeaksFirst(t *testing.T) {
	srv := &socksServer{bindDomain: true, first: []byte("SSH-2.0-banner\r\n")}
	d := &SOCKS5{Open: srv.pipe()}
	conn, err := d.Dial(dialCtx(t), echoServer(t))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	got := make([]byte, len(srv.first))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, srv.first) {
		t.Errorf("first bytes %q, want %q", got, srv.first)
	}
}

// An exit that is not a SOCKS5 server is told apart from a refused login.
func TestDialNotSOCKS5(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer string
	}{
		{"http", "HTTP/1.0 400 Bad Request\r\n\r\n"},
		{"socks4", "\x00\x5b"},
		{"method not offered", "\x05\x02"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			open := func(context.Context) (net.Conn, error) {
				client, server := net.Pipe()
				go func() {
					defer server.Close()
					server.Read(make([]byte, 64))
					server.Write([]byte(tc.answer))
				}()
				return client, nil
			}
			_, err := (&SOCKS5{Open: open}).Dial(dialCtx(t), "192.0.2.1:80")
			if !errors.Is(err, ErrNotSOCKS5) {
				t.Fatalf("%v: not ErrNotSOCKS5", err)
			}
			if IsLogin(err) {
				t.Error("a login error")
			}
		})
	}
}

// A stream closed in the middle of the exchange is an error of the stream, not a refusal.
func TestDialStreamClosed(t *testing.T) {
	open := func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			server.Read(make([]byte, 64))
			server.Close()
		}()
		return client, nil
	}
	_, err := (&SOCKS5{Open: open}).Dial(dialCtx(t), "192.0.2.1:80")
	if err == nil || IsLogin(err) || errors.Is(err, ErrNotSOCKS5) {
		t.Fatalf("got %v", err)
	}
	if !errors.Is(err, io.EOF) {
		t.Errorf("%v does not wrap io.EOF", err)
	}
}

func TestDialOpenFails(t *testing.T) {
	failed := errors.New("opening stream: session closed")
	open := func(context.Context) (net.Conn, error) { return nil, failed }
	if _, err := (&SOCKS5{Open: open}).Dial(dialCtx(t), "192.0.2.1:80"); !errors.Is(err, failed) {
		t.Fatalf("got %v", err)
	}
}

// A silent exit holds Dial only until ctx ends, and the stream is closed then.
func TestDialCancel(t *testing.T) {
	closed := make(chan struct{})
	open := func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			io.Copy(io.Discard, server)
			close(closed)
		}()
		return client, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	_, err := (&SOCKS5{Open: open}).Dial(ctx, "192.0.2.1:80")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("Dial returned after %v", d)
	}
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Error("the stream stayed open")
	}
}

// A connection that outlives the ctx of its Dial keeps working: no deadline is left on
// it.
func TestDialCtxEndsAfter(t *testing.T) {
	srv := &socksServer{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	conn, err := (&SOCKS5{Open: srv.pipe()}).Dial(ctx, echoServer(t))
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	time.Sleep(50 * time.Millisecond)
	echo(t, conn, []byte("still here"))
}

func TestCheck(t *testing.T) {
	srv := &socksServer{user: "user", password: "secret"}
	d := &SOCKS5{Open: srv.pipe(), User: "user", Password: "secret"}
	if err := d.Check(dialCtx(t)); err != nil {
		t.Fatal(err)
	}
	// The server logs the login before it answers, so it is in the log by now.
	if got, want := srv.events(), []string{"login user"}; !slices.Equal(got, want) {
		t.Errorf("server saw %q, want %q", got, want)
	}
}

func TestConnectRequest(t *testing.T) {
	for _, tc := range []struct {
		address string
		want    []byte // nil: an error
	}{
		{"192.0.2.1:80", []byte{5, 1, 0, 1, 192, 0, 2, 1, 0, 80}},
		{"[::ffff:192.0.2.1]:443", []byte{5, 1, 0, 1, 192, 0, 2, 1, 1, 0xbb}},
		{"[2001:db8::1]:65535", []byte{5, 1, 0, 4, 0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0xff, 0xff}},
		{"t.example.com:1", append(append([]byte{5, 1, 0, 3, 13}, "t.example.com"...), 0, 1)},
		{"192.0.2.1", nil},
		{"192.0.2.1:0", nil},
		{"192.0.2.1:65536", nil},
		{"192.0.2.1:http", nil},
		{":80", nil},
		{"[fe80::1%eth0]:80", nil},
		{strings.Repeat("a", 256) + ":80", nil},
	} {
		got, err := connectRequest(tc.address)
		switch {
		case tc.want == nil && err == nil:
			t.Errorf("%q: no error, request % x", tc.address, got)
		case tc.want != nil && err != nil:
			t.Errorf("%q: %v", tc.address, err)
		case !bytes.Equal(got, tc.want):
			t.Errorf("%q: % x, want % x", tc.address, got, tc.want)
		}
	}
}

// A login that RFC 1929 cannot carry is refused before a stream is opened.
func TestBadLogin(t *testing.T) {
	open := func(context.Context) (net.Conn, error) {
		t.Error("a stream was opened")
		return nil, errors.New("unexpected")
	}
	for _, d := range []SOCKS5{
		{Password: "secret"},
		{User: strings.Repeat("u", 256), Password: "secret"},
		{User: "user", Password: strings.Repeat("p", 256)},
	} {
		d.Open = open
		if err := d.Check(dialCtx(t)); err == nil || IsLogin(err) {
			t.Errorf("user of %d bytes, password of %d: %v", len(d.User), len(d.Password), err)
		}
	}
}

// The whole batch goes in one Write: over a pipe the fixture's first Read takes exactly the
// greeting, the RFC 1929 login and the CONNECT request.
func TestDialOneWrite(t *testing.T) {
	for _, tc := range []struct {
		name     string
		srv      *socksServer
		d        SOCKS5
		greeting string
		size     int
	}{
		{"login",
			&socksServer{user: "user", password: "secret", singleRead: true, logGreeting: true},
			SOCKS5{User: "user", Password: "secret"}, "greeting 02", 26},
		{"no login",
			&socksServer{singleRead: true, logGreeting: true},
			SOCKS5{}, "greeting 00", 13},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.d.Open = tc.srv.pipe()
			tc.d.OneWrite = true
			conn, err := tc.d.Dial(dialCtx(t), echoServer(t))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if got := tc.srv.readSizes(); !slices.Equal(got, []int{tc.size}) {
				t.Errorf("first reads %v, want [%d]", got, tc.size)
			}
			if ev := tc.srv.events(); len(ev) == 0 || ev[0] != tc.greeting {
				t.Errorf("server saw %q, want %q first", ev, tc.greeting)
			}
			echo(t, conn, []byte("hello"))
		})
	}
}

// The sequential greeting still offers both methods when there is a login.
func TestDialGreetingSequential(t *testing.T) {
	srv := &socksServer{user: "user", password: "secret", logGreeting: true}
	d := &SOCKS5{Open: srv.pipe(), User: "user", Password: "secret"}
	conn, err := d.Dial(dialCtx(t), echoServer(t))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if ev := srv.events(); len(ev) == 0 || ev[0] != "greeting 00 02" {
		t.Errorf("server saw %q, want a greeting 00 02", ev)
	}
}

// Replies coalesced into one Write and fragmented one byte at a time arrive in order, and
// the target's first bytes that follow them stay intact.
func TestDialOneWriteReplies(t *testing.T) {
	first := []byte("SSH-2.0-banner\r\n")
	for _, tc := range []struct {
		name string
		srv  *socksServer
	}{
		{"coalesced", &socksServer{singleRead: true, coalesce: true, first: first}},
		{"dribbled", &socksServer{singleRead: true, dribble: true, first: first}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &SOCKS5{Open: tc.srv.pipe(), OneWrite: true}
			conn, err := d.Dial(dialCtx(t), echoServer(t))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(30 * time.Second))
			got := make([]byte, len(first))
			if _, err := io.ReadFull(conn, got); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, first) {
				t.Errorf("first bytes %q, want %q", got, first)
			}
			echo(t, conn, []byte("after the banner"))
		})
	}
}

// shortWriteConn passes at most n bytes to the underlying conn per Write.
type shortWriteConn struct {
	net.Conn
	n int
}

func (c *shortWriteConn) Write(b []byte) (int, error) {
	if len(b) > c.n {
		b = b[:c.n]
	}
	return c.Conn.Write(b)
}

// The batch is one logical message: a conn that takes a few bytes per Write gets the rest of
// the same batch, and the server sees the greeting, the login and the request in order.
func TestDialOneWriteShortWrites(t *testing.T) {
	batch := []byte{
		socksVersion, 1, methodPassword,
		passwordVersion, 4, 'u', 's', 'e', 'r', 6, 's', 'e', 'c', 'r', 'e', 't',
		socksVersion, cmdConnect, 0, atypIPv4, 192, 0, 2, 1, 0, 80,
	}
	reply := []byte{
		socksVersion, methodPassword,
		passwordVersion, 0,
		socksVersion, 0, 0, atypIPv4, 192, 0, 2, 1, 0x1f, 0x90,
	}
	open := func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			got := make([]byte, len(batch))
			if _, err := io.ReadFull(server, got); err != nil {
				t.Errorf("the batch was not read: %v", err)
				return
			}
			if !bytes.Equal(got, batch) {
				t.Errorf("batch % x, want % x", got, batch)
			}
			server.Write(reply)
		}()
		return &shortWriteConn{Conn: client, n: 3}, nil
	}
	conn, err := (&SOCKS5{Open: open, User: "user", Password: "secret", OneWrite: true}).
		Dial(dialCtx(t), "192.0.2.1:80")
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
}

// oneByteReadConn returns one byte per Read.
type oneByteReadConn struct{ net.Conn }

func (c oneByteReadConn) Read(b []byte) (int, error) {
	if len(b) > 1 {
		b = b[:1]
	}
	return c.Conn.Read(b)
}

// io.ReadFull reassembles replies that arrive one byte per Read, and the target's first
// bytes stay intact.
func TestDialOneWriteOneByteReads(t *testing.T) {
	first := []byte("SSH-2.0-banner\r\n")
	srv := &socksServer{singleRead: true, coalesce: true, first: first}
	open := srv.pipe()
	d := &SOCKS5{Open: func(ctx context.Context) (net.Conn, error) {
		conn, err := open(ctx)
		if err != nil {
			return nil, err
		}
		return oneByteReadConn{conn}, nil
	}, OneWrite: true}
	conn, err := d.Dial(dialCtx(t), echoServer(t))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	got := make([]byte, len(first))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, first) {
		t.Errorf("first bytes %q, want %q", got, first)
	}
	echo(t, conn, []byte("after the banner"))
}

// A method the one-write greeting did not offer is an error naming the key; 0xff stays a
// *LoginError, as in the sequential path.
func TestDialOneWriteMethod(t *testing.T) {
	for _, tc := range []struct {
		name     string
		srv      *socksServer
		d        SOCKS5
		login    bool
		notSocks bool
	}{
		{"none chosen with a login",
			&socksServer{singleRead: true, chooseSet: true, choose: methodNone},
			SOCKS5{User: "user", Password: "secret"}, false, true},
		{"login chosen without one",
			&socksServer{singleRead: true, chooseSet: true, choose: methodPassword},
			SOCKS5{}, false, true},
		{"unknown method",
			&socksServer{singleRead: true, chooseSet: true, choose: 0x05},
			SOCKS5{User: "user", Password: "secret"}, false, true},
		{"no acceptable method",
			&socksServer{singleRead: true, chooseSet: true, choose: methodNoAcceptable},
			SOCKS5{User: "user", Password: "secret"}, true, false},
		{"no acceptable method without a login",
			&socksServer{singleRead: true, chooseSet: true, choose: methodNoAcceptable},
			SOCKS5{}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.d.Open = tc.srv.pipe()
			tc.d.OneWrite = true
			_, err := tc.d.Dial(dialCtx(t), "192.0.2.1:80")
			if err == nil {
				t.Fatal("Dial succeeded")
			}
			if got := IsLogin(err); got != tc.login {
				t.Errorf("%v: IsLogin %v, want %v", err, got, tc.login)
			}
			if got := errors.Is(err, ErrNotSOCKS5); got != tc.notSocks {
				t.Errorf("%v: ErrNotSOCKS5 %v, want %v", err, got, tc.notSocks)
			}
			if !strings.Contains(err.Error(), "exit.one_write") {
				t.Errorf("%q does not name exit.one_write", err)
			}
		})
	}
}

// oneWriteAnswer serves a one-write Dial with the login user/secret to 192.0.2.1:80: the
// server reads the whole 26-byte batch and then writes answer and closes, so a short answer
// is cut off by the close.
func oneWriteAnswer(answer []byte) OpenStream {
	const batch = 26 // the greeting, the login and the CONNECT request of the test
	return func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			if _, err := io.ReadFull(server, make([]byte, batch)); err != nil {
				return
			}
			server.Write(answer)
		}()
		return client, nil
	}
}

// Malformed and cut-off replies are errors naming the key: a bad version or address type is
// a non-SOCKS5 answer, a short reply is an error of the stream.
func TestDialOneWriteMalformed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		answer   []byte
		notSocks bool
		stream   bool
	}{
		{"bad greeting version", []byte{6, methodPassword}, true, false},
		{"bad login version", []byte{socksVersion, methodPassword, 2, 0}, true, false},
		{"truncated login status", []byte{socksVersion, methodPassword, passwordVersion}, false, true},
		{"bad CONNECT version",
			[]byte{socksVersion, methodPassword, passwordVersion, 0, 4, 0, 0, atypIPv4, 192, 0, 2, 1, 0x1f, 0x90},
			true, false},
		{"unknown address type",
			[]byte{socksVersion, methodPassword, passwordVersion, 0, socksVersion, 0, 0, 0x09},
			true, false},
		{"short bound address",
			[]byte{socksVersion, methodPassword, passwordVersion, 0, socksVersion, 0, 0, atypIPv4, 192, 0},
			false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &SOCKS5{Open: oneWriteAnswer(tc.answer), User: "user", Password: "secret", OneWrite: true}
			_, err := d.Dial(dialCtx(t), "192.0.2.1:80")
			if err == nil {
				t.Fatal("Dial succeeded")
			}
			if got := errors.Is(err, ErrNotSOCKS5); got != tc.notSocks {
				t.Errorf("%v: ErrNotSOCKS5 %v, want %v", err, got, tc.notSocks)
			}
			if tc.stream && !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Errorf("%v does not wrap io.ErrUnexpectedEOF", err)
			}
			if !strings.Contains(err.Error(), "exit.one_write") ||
				!strings.Contains(err.Error(), "no fallback") {
				t.Errorf("%q does not name exit.one_write with no fallback", err)
			}
		})
	}
}

// A refused CONNECT is the same *ConnectError as in the sequential path, without the key,
// and the rest of the reply is not waited for.
func TestDialOneWriteConnectRefused(t *testing.T) {
	answer := []byte{socksVersion, methodPassword, passwordVersion, 0, socksVersion, 0x05}
	d := &SOCKS5{Open: oneWriteAnswer(answer), User: "user", Password: "secret", OneWrite: true}
	_, err := d.Dial(dialCtx(t), "192.0.2.1:80")
	var ce *ConnectError
	if !errors.As(err, &ce) {
		t.Fatalf("%v: not a *ConnectError", err)
	}
	if ce.Code != 0x05 || ce.Address != "192.0.2.1:80" {
		t.Errorf("got %+v", ce)
	}
	want := (&ConnectError{Address: "192.0.2.1:80", Code: 0x05}).Error()
	if err.Error() != want {
		t.Errorf("%q, want %q", err, want)
	}
	if strings.Contains(err.Error(), "exit.one_write") {
		t.Errorf("%q names the key, but a refused target is not a problem of the mode", err)
	}
	if IsLogin(err) {
		t.Error("a refused CONNECT is a login error")
	}
}

// streamCounter counts the streams opened through open and the times they were closed.
type streamCounter struct {
	open   OpenStream
	opened atomic.Int32
	closed atomic.Int32
}

func (c *streamCounter) Open(ctx context.Context) (net.Conn, error) {
	c.opened.Add(1)
	conn, err := c.open(ctx)
	if err != nil {
		return nil, err
	}
	return &countCloseConn{Conn: conn, closed: &c.closed}, nil
}

type countCloseConn struct {
	net.Conn
	closed *atomic.Int32
}

func (c *countCloseConn) Close() error {
	c.closed.Add(1)
	return c.Conn.Close()
}

// A server that reads one message and hangs up fails the batch with the key, not as a login
// refusal; the stream is opened and closed once.
func TestDialOneWriteDropped(t *testing.T) {
	srv := &socksServer{singleRead: true, dropBatch: true}
	c := &streamCounter{open: srv.pipe()}
	d := &SOCKS5{Open: c.Open, User: "user", Password: "secret", OneWrite: true}
	conn, err := d.Dial(dialCtx(t), "192.0.2.1:80")
	if err == nil {
		conn.Close()
		t.Fatal("Dial succeeded")
	}
	if IsLogin(err) {
		t.Errorf("%v: a login error", err)
	}
	var ce *ConnectError
	if errors.As(err, &ce) {
		t.Errorf("%v: a *ConnectError", err)
	}
	if !strings.Contains(err.Error(), "exit.one_write") || !strings.Contains(err.Error(), "no fallback") {
		t.Errorf("%q does not name exit.one_write with no fallback", err)
	}
	if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("%v does not wrap the end of the stream", err)
	}
	if got := c.opened.Load(); got != 1 {
		t.Errorf("opened %d streams, want 1", got)
	}
	if got := c.closed.Load(); got != 1 {
		t.Errorf("closed %d streams, want 1", got)
	}
}

// A server that reads the whole batch and answers nothing: the deadline fails the batch with
// the key, and the cause may be the context or the stream deadline. The batch is no
// *LoginError, no *ConnectError and no non-SOCKS5 answer, and the stream is opened and
// closed once.
func TestDialOneWriteNoAnswer(t *testing.T) {
	// The read of the batch is reported through a buffered channel, so the server goroutine
	// never blocks on it and the test knows the whole batch arrived.
	read := make(chan error, 1)
	open := func(ctx context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			_, err := io.ReadFull(server, make([]byte, 26))
			read <- err
			if err != nil {
				return
			}
			<-ctx.Done()
		}()
		return client, nil
	}
	c := &streamCounter{open: open}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	conn, err := (&SOCKS5{Open: c.Open, User: "user", Password: "secret", OneWrite: true}).
		Dial(ctx, "192.0.2.1:80")
	if err == nil {
		conn.Close()
		t.Fatal("Dial succeeded")
	}
	if rerr := <-read; rerr != nil {
		t.Fatalf("the server did not read the batch: %v", rerr)
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("%v: neither context.DeadlineExceeded nor os.ErrDeadlineExceeded", err)
	}
	if !strings.Contains(err.Error(), "exit.one_write") || !strings.Contains(err.Error(), "no fallback") {
		t.Errorf("%q does not name exit.one_write with no fallback", err)
	}
	if IsLogin(err) {
		t.Errorf("%v: a login error", err)
	}
	var ce *ConnectError
	if errors.As(err, &ce) {
		t.Errorf("%v: a *ConnectError", err)
	}
	if errors.Is(err, ErrNotSOCKS5) {
		t.Errorf("%v: a non-SOCKS5 answer", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("Dial returned after %v", d)
	}
	if got := c.opened.Load(); got != 1 {
		t.Errorf("opened %d streams, want 1", got)
	}
	if got := c.closed.Load(); got != 1 {
		t.Errorf("closed %d streams, want 1", got)
	}
}

// A refused login stays a *LoginError, names the key and is not retried sequentially.
func TestDialOneWriteLoginRefused(t *testing.T) {
	srv := &socksServer{user: "user", password: "secret", singleRead: true}
	c := &streamCounter{open: srv.pipe()}
	d := &SOCKS5{Open: c.Open, User: "user", Password: "wrong", OneWrite: true}
	_, err := d.Dial(dialCtx(t), "192.0.2.1:80")
	if !IsLogin(err) {
		t.Fatalf("%v: not a login error", err)
	}
	if !strings.Contains(err.Error(), "exit.one_write") {
		t.Errorf("%q does not name exit.one_write", err)
	}
	if got, want := srv.events(), []string{"login user"}; !slices.Equal(got, want) {
		t.Errorf("server saw %q, want %q", got, want)
	}
	if got := c.opened.Load(); got != 1 {
		t.Errorf("opened %d streams, want 1", got)
	}
}

// OneWrite never applies to Check: the greeting is the sequential one, no CONNECT follows,
// and a refused login stays a *LoginError.
func TestCheckOneWriteStaysSequential(t *testing.T) {
	srv := &socksServer{user: "user", password: "secret", singleRead: true, logGreeting: true}
	d := &SOCKS5{Open: srv.pipe(), User: "user", Password: "secret", OneWrite: true}
	if err := d.Check(dialCtx(t)); err != nil {
		t.Fatal(err)
	}
	if got := srv.readSizes(); !slices.Equal(got, []int{4}) {
		t.Errorf("first reads %v, want [4]", got)
	}
	ev := srv.events()
	if !slices.Contains(ev, "greeting 00 02") {
		t.Errorf("server saw %q, want a greeting 00 02", ev)
	}
	for _, e := range ev {
		if strings.HasPrefix(e, "connect") {
			t.Errorf("a CONNECT after Check: %q", e)
		}
	}

	bad := &SOCKS5{Open: srv.pipe(), User: "user", Password: "wrong", OneWrite: true}
	if err := bad.Check(dialCtx(t)); !IsLogin(err) {
		t.Errorf("wrong password: %v, want a login error", err)
	}
}

// A cancelled batch read ends with the context cause and the key, and the stream is opened
// and closed once. The cancel comes only after the batch has been read, and the server's end
// stays open until the test returns: neither a timer nor the peer's close can race the
// deadline that the cancel sets.
func TestDialOneWriteCancel(t *testing.T) {
	const batch = 26 // greeting, login and request of the profile below
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	read := make(chan error, 1)
	open := func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		t.Cleanup(func() { server.Close() })
		go func() {
			_, err := io.ReadFull(server, make([]byte, batch))
			read <- err
			cancel()
		}()
		return client, nil
	}
	c := &streamCounter{open: open}
	start := time.Now()
	_, err := (&SOCKS5{Open: c.Open, User: "user", Password: "secret", OneWrite: true}).
		Dial(ctx, "192.0.2.1:80")
	if readErr := <-read; readErr != nil {
		t.Fatalf("the server did not read the batch: %v", readErr)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	if !strings.Contains(err.Error(), "exit.one_write") {
		t.Errorf("%q does not name exit.one_write", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("Dial returned after %v", d)
	}
	if got := c.opened.Load(); got != 1 {
		t.Errorf("opened %d streams, want 1", got)
	}
	if got := c.closed.Load(); got != 1 {
		t.Errorf("closed %d streams, want 1", got)
	}
}
