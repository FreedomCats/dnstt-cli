package exit

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/FreedomCats/dnstt-cli/core/client"
	"github.com/FreedomCats/dnstt-cli/core/dns"
	"github.com/FreedomCats/dnstt-cli/core/server"
)

func TestHTTPThroughTunnel(t *testing.T) {
	srv := &httpProxy{
		user:     "synthetic-user",
		password: "synthetic-password",
		targets:  map[string]string{"echo.example:7": echoServer(t)},
	}
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
	session, err := client.Dial(ctx, client.Config{
		Domain:   domain,
		PubKey:   ts.PubKey,
		Resolver: client.Resolver{Transport: client.UDP, Address: ts.Addr.String()},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	open := func(context.Context) (net.Conn, error) { return session.OpenStream() }
	d := &HTTP{Open: open, User: srv.user, Password: srv.password}
	conn, err := d.Dial(dialCtx(t), "echo.example:7")
	if err != nil {
		t.Fatal(err)
	}
	echo(t, conn, make([]byte, 50000))
	conn.Close()

	_, err = d.Dial(dialCtx(t), "blocked.example:443")
	var refusal *HTTPConnectError
	if !errors.As(err, &refusal) || refusal.StatusCode != 403 || IsLogin(err) {
		t.Fatalf("blocked target: %v, want an HTTP 403 refusal", err)
	}
	bad := &HTTP{Open: open, User: srv.user, Password: "synthetic-wrong-password"}
	if _, err := bad.Dial(dialCtx(t), "echo.example:7"); !IsLogin(err) {
		t.Fatalf("wrong password: %v, want a login error", err)
	}
	// Both refusals close only their stream. A new CONNECT on the same session succeeds.
	conn, err = d.Dial(dialCtx(t), "echo.example:7")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	echo(t, conn, []byte("a new stream after refusals"))
	if got := len(srv.seen()); got != 4 {
		t.Errorf("proxy saw %d streams, want 4", got)
	}
}
