package supervisor

import (
	"bytes"
	"context"
	"flag"
	"io"
	"log"
	"net"
	"os"
	"testing"
	"time"

	"github.com/FreedomCats/dnstt-cli/core/client"
	"github.com/FreedomCats/dnstt-cli/core/dns"
	"github.com/FreedomCats/dnstt-cli/core/server"
	"github.com/FreedomCats/dnstt-cli/internal/resolvers"
)

func TestMain(m *testing.M) {
	// The upstream server logs through the log package; only -v shows it.
	flag.Parse()
	if !testing.Verbose() {
		log.SetOutput(io.Discard)
	}
	os.Exit(m.Run())
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

func echoThrough(t *testing.T, tun *Tunnel, msg []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := tun.OpenStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := c.Write(msg); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatal("echo differs")
	}
}

// The server restarts: the session dies of silence, the dials fail while the server is
// down, and a new session comes up once it is back, with no Restart from outside.
func TestServerRestart(t *testing.T) {
	domain, err := dns.ParseName("t.example.com")
	if err != nil {
		t.Fatal(err)
	}
	ts, err := server.StartTest(domain, echoServer(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ts.Close() })
	r, err := resolvers.Parse("udp:" + ts.Addr.String())
	if err != nil {
		t.Fatal(err)
	}

	states := make(chan Transition, 1000)
	tun, err := New(Options{
		Session: client.Config{
			Domain:           domain,
			PubKey:           ts.PubKey,
			KeepAliveTimeout: 2 * time.Second,
		},
		Resolvers:        resolvers.NewPool([]resolvers.Resolver{r}),
		HandshakeTimeout: time.Second,
		DegradedAfter:    time.Second,
		OnTransition:     func(tr Transition) { states <- tr },
	})
	if err != nil {
		t.Fatal(err)
	}
	// The delays stay short: the test is about the recovery, not the backoff.
	tun.sleep = func(ctx context.Context, d time.Duration) bool {
		return sleepCtx(ctx, min(d, 100*time.Millisecond))
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- tun.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-result; err != nil {
			t.Error(err)
		}
	}()

	waitFor := func(state State) Transition {
		t.Helper()
		timeout := time.After(30 * time.Second)
		for {
			select {
			case tr := <-states:
				if tr.To == state {
					return tr
				}
			case <-timeout:
				t.Fatalf("no %s", state)
			}
		}
	}
	waitFor(StateConnected)
	echoThrough(t, tun, []byte("before the restart"))

	if err := ts.Close(); err != nil {
		t.Fatal(err)
	}
	tr := waitFor(StateReconnecting)
	t.Logf("session ended: %v", tr.Cause)
	// A dial or two fail while the server is down.
	waitFor(StateReconnecting)
	if err := ts.Reopen(); err != nil {
		t.Fatal(err)
	}
	waitFor(StateConnected)
	echoThrough(t, tun, []byte("after the restart"))
}
