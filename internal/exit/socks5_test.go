package exit

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"slices"
	"strings"
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
