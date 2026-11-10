package exit

import (
	"context"
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/FreedomCats/dnstt-cli/core/client"
	"github.com/FreedomCats/dnstt-cli/core/dns"
	"github.com/FreedomCats/dnstt-cli/core/server"
)

func TestMain(m *testing.M) {
	// The upstream server logs through the log package; only -v shows it.
	flag.Parse()
	if !testing.Verbose() {
		log.SetOutput(io.Discard)
	}
	os.Exit(m.Run())
}

// tunnel runs the upstream dnstt server in-process with the SOCKS5 server as its upstream
// address and dials a session to it: the whole path of a connection, as with danted
// behind dnstt-server.
func tunnel(t *testing.T, srv *socksServer) *client.Session {
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

func TestThroughTunnel(t *testing.T) {
	srv := &socksServer{
		user:     "user",
		password: "secret",
		names:    map[string]string{"echo.example.com": echoServer(t)},
	}
	s := tunnel(t, srv)
	open := func(context.Context) (net.Conn, error) { return s.OpenStream() }
	d := &SOCKS5{Open: open, User: "user", Password: "secret"}

	if err := d.Check(dialCtx(t)); err != nil {
		t.Fatalf("Check: %v", err)
	}
	conn, err := d.Dial(dialCtx(t), "echo.example.com:7")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	echo(t, conn, make([]byte, 50000))

	// A closed port behind the exit: the exit's refusal, the session goes on.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := ln.Addr().String()
	ln.Close()
	_, err = d.Dial(dialCtx(t), closed)
	var ce *ConnectError
	if !errors.As(err, &ce) || ce.Code != 0x05 {
		t.Fatalf("to a closed port: %v, want a *ConnectError with code 5", err)
	}
	echo(t, conn, []byte("after the refusal"))

	bad := &SOCKS5{Open: open, User: "user", Password: "wrong"}
	if err := bad.Check(dialCtx(t)); !IsLogin(err) {
		t.Fatalf("wrong password: %v, want a login error", err)
	}

	// The one-write exchange: Check stays sequential, Dial sends the batch in one Write.
	one := &SOCKS5{Open: open, User: "user", Password: "secret", OneWrite: true}
	if err := one.Check(dialCtx(t)); err != nil {
		t.Fatalf("one-write Check: %v", err)
	}
	oneConn, err := one.Dial(dialCtx(t), "echo.example.com:7")
	if err != nil {
		t.Fatalf("one-write Dial: %v", err)
	}
	defer oneConn.Close()
	echo(t, oneConn, make([]byte, 50000))

	_, err = one.Dial(dialCtx(t), closed)
	var ceOne *ConnectError
	if !errors.As(err, &ceOne) || ceOne.Code != 0x05 {
		t.Fatalf("one-write to a closed port: %v, want a *ConnectError with code 5", err)
	}
	echo(t, oneConn, []byte("after the one-write refusal"))

	// An upstream that reads one message and drops the rest of the batch: the one-write
	// Dial fails with the key, and no second stream is opened.
	drop := &socksServer{user: "user", password: "secret", singleRead: true, dropBatch: true}
	dropSession := tunnel(t, drop)
	opened := 0
	dropOpen := func(context.Context) (net.Conn, error) {
		opened++
		return dropSession.OpenStream()
	}
	dropped := &SOCKS5{Open: dropOpen, User: "user", Password: "secret", OneWrite: true}
	_, err = dropped.Dial(dialCtx(t), "echo.example.com:7")
	if err == nil || !strings.Contains(err.Error(), "exit.one_write") {
		t.Fatalf("a dropped batch: %v, want an error naming exit.one_write", err)
	}
	if IsLogin(err) {
		t.Errorf("a dropped batch: %v is a login error", err)
	}
	if opened != 1 {
		t.Errorf("opened %d streams, want 1", opened)
	}
	if ev := drop.events(); len(ev) != 0 {
		t.Errorf("the dropped upstream saw %q", ev)
	}
}
