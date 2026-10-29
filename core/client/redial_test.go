// Added by dnstt-cli (scripts/vendorcore/_files): not part of the dnstt upstream.

package client

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FreedomCats/dnstt-cli/core/turbotunnel"
)

// dotEcho is a resolver for TLSPacketConn without TLS: it reads length-prefixed messages
// and writes them back. drop breaks the connections open now.
type dotEcho struct {
	ln    net.Listener
	mu    sync.Mutex
	conns []net.Conn
}

func newDoTEcho(t *testing.T) *dotEcho {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	e := &dotEcho{ln: ln}
	t.Cleanup(func() { ln.Close(); e.drop() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			e.mu.Lock()
			e.conns = append(e.conns, conn)
			e.mu.Unlock()
			go func() {
				defer conn.Close()
				for {
					var n uint16
					if binary.Read(conn, binary.BigEndian, &n) != nil {
						return
					}
					p := make([]byte, n)
					if _, err := io.ReadFull(conn, p); err != nil {
						return
					}
					if binary.Write(conn, binary.BigEndian, n) != nil {
						return
					}
					if _, err := conn.Write(p); err != nil {
						return
					}
				}
			}()
		}
	}()
	return e
}

func (e *dotEcho) drop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, c := range e.conns {
		c.Close()
	}
	e.conns = nil
}

// flakyDial dials the resolver unless it is told to fail, and notes the time of every
// attempt.
type flakyDial struct {
	addr     string
	mu       sync.Mutex
	failures int // how many dials fail from now on; -1 — all
	attempts []time.Time
}

func (d *flakyDial) fail(n int) {
	d.mu.Lock()
	d.failures = n
	d.attempts = nil
	d.mu.Unlock()
}

func (d *flakyDial) dial(ctx context.Context, network, _ string) (net.Conn, error) {
	d.mu.Lock()
	d.attempts = append(d.attempts, time.Now())
	failing := d.failures != 0
	if d.failures > 0 {
		d.failures--
	}
	d.mu.Unlock()
	if failing {
		return nil, errors.New("refused by the test")
	}
	var nd net.Dialer
	return nd.DialContext(ctx, network, d.addr)
}

func (d *flakyDial) times() []time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]time.Time(nil), d.attempts...)
}

// roundTrip writes msg until it comes back: messages sent while the connection is down
// are lost, as on the network.
func roundTrip(t *testing.T, c *TLSPacketConn, msg []byte) {
	t.Helper()
	got := make(chan []byte, 16)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			buf := make([]byte, 512)
			n, _, err := c.ReadFrom(buf)
			if err != nil {
				return
			}
			select {
			case got <- buf[:n]:
			case <-stop:
				return
			}
		}
	}()
	deadline := time.After(10 * time.Second)
	for {
		if _, err := c.WriteTo(msg, turbotunnel.DummyAddr{}); err != nil {
			t.Fatalf("WriteTo: %v", err)
		}
		select {
		case p := <-got:
			if !bytes.Equal(p, msg) {
				t.Fatalf("got %q, want %q", p, msg)
			}
			return
		case <-time.After(100 * time.Millisecond):
		case <-deadline:
			t.Fatal("the message did not come back")
		}
	}
}

func shortRedial(t *testing.T) {
	minDelay, maxDelay := redialMinDelay, redialMaxDelay
	redialMinDelay, redialMaxDelay = 50*time.Millisecond, 200*time.Millisecond
	t.Cleanup(func() { redialMinDelay, redialMaxDelay = minDelay, maxDelay })
}

// P2: failed redials are retried with a growing delay up to its cap, and the transport
// carries messages again once a dial succeeds.
func TestDoTRedialBackoff(t *testing.T) {
	shortRedial(t)
	e := newDoTEcho(t)
	d := &flakyDial{addr: e.ln.Addr().String()}
	var logMu sync.Mutex
	var logged []string
	logf := func(format string, args ...any) {
		logMu.Lock()
		logged = append(logged, fmt.Sprintf(format, args...))
		logMu.Unlock()
	}
	c, err := NewTLSPacketConn(e.ln.Addr().String(), d.dial, logf)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	roundTrip(t, c, []byte("before"))

	d.fail(5)
	e.drop()
	roundTrip(t, c, []byte("after"))

	at := d.times()
	if len(at) != 6 {
		t.Fatalf("%d dials after the drop, want 5 failed and 1 good", len(at))
	}
	for i, want := range []time.Duration{50, 100, 200, 200, 200} {
		want *= time.Millisecond
		if gap := at[i+1].Sub(at[i]); gap < want || gap > want+time.Second {
			t.Errorf("gap after failed dial %d: %v, want %v", i+1, gap, want)
		}
	}

	// The failed dials reach the logf of the transport, not the log package.
	logMu.Lock()
	defer logMu.Unlock()
	if n := strings.Count(strings.Join(logged, "\n"), "dial tls: refused by the test"); n != 5 {
		t.Errorf("logf got %d failed dials, want 5:\n%s", n, strings.Join(logged, "\n"))
	}
}

// P2: closing the transport stops the redials, and its goroutines end.
func TestDoTRedialStopsOnClose(t *testing.T) {
	shortRedial(t)
	e := newDoTEcho(t)
	d := &flakyDial{addr: e.ln.Addr().String()}
	c, err := NewTLSPacketConn(e.ln.Addr().String(), d.dial, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	d.fail(-1)
	e.drop()
	// The send loop notices the broken connection on its next write; in a session
	// DNSPacketConn polls all the time.
	for len(d.times()) < 3 {
		c.WriteTo([]byte("poll"), turbotunnel.DummyAddr{})
		time.Sleep(10 * time.Millisecond)
	}
	c.Close()
	n := len(d.times())
	deadline := time.Now().Add(5 * time.Second)
	for goroutinesOf("(*TLSPacketConn)") != "" {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines left after Close:\n\n%s", goroutinesOf("(*TLSPacketConn)"))
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
	if got := len(d.times()); got != n {
		t.Errorf("%d dials after Close", got-n)
	}
}

// goroutinesOf gives the stacks of the client's goroutines that mention s, outside tests.
func goroutinesOf(s string) string {
	var found []string
	for _, g := range clientGoroutines() {
		if strings.Contains(g, s) {
			found = append(found, g)
		}
	}
	return strings.Join(found, "\n\n")
}
