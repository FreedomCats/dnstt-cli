// Added by dnstt-cli (scripts/vendorcore/_files): not part of the dnstt upstream.

package client

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// P4: a host name of a resolver goes through Config.LookupIP and nowhere else. Without
// LookupIP Dial fails before it opens a socket; with it, the socket goes to the address
// LookupIP gave. UDP reaches the in-process server and carries a stream; DoH and DoT reach
// a listener that drops the connection, which is enough to see where they went.
func TestResolverNamesUseLookupIP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	tcpPort := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)

	srv := startServer(t)
	udpPort := strconv.Itoa(srv.Addr.Port)
	loopback := netip.MustParseAddr("127.0.0.1")

	for _, c := range []struct {
		name, host, target string
		r                  Resolver
	}{
		{"DoH", "doh.example", "127.0.0.1:" + tcpPort, Resolver{DoH, "https://doh.example:" + tcpPort + "/dns-query"}},
		{"DoT", "dot.example", "127.0.0.1:" + tcpPort, Resolver{DoT, "dot.example:" + tcpPort}},
		{"UDP", "ns.example", "", Resolver{UDP, "ns.example:" + udpPort}},
	} {
		t.Run(c.name+" without LookupIP", func(t *testing.T) {
			cfg := udpConfig(srv)
			cfg.Resolver, cfg.UTLS = c.r, "none"
			var controls atomic.Int32
			cfg.Control = func(string, string, syscall.RawConn) error {
				controls.Add(1)
				return nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			s, err := Dial(ctx, cfg)
			if err == nil {
				s.Close()
				t.Fatal("Dial succeeded")
			}
			if !strings.Contains(err.Error(), "needs Config.LookupIP") {
				t.Errorf("Dial: %v", err)
			}
			if controls.Load() != 0 {
				t.Errorf("%d sockets opened", controls.Load())
			}
		})

		t.Run(c.name+" with LookupIP", func(t *testing.T) {
			cfg := udpConfig(srv)
			cfg.Resolver, cfg.UTLS = c.r, "none"
			var mu sync.Mutex
			asked := map[string]bool{}
			cfg.LookupIP = func(_ context.Context, host string) ([]netip.Addr, error) {
				mu.Lock()
				asked[host] = true
				mu.Unlock()
				return []netip.Addr{loopback}, nil
			}
			var controls atomic.Int32
			cfg.Control = func(network, address string, _ syscall.RawConn) error {
				if c.target != "" && address != c.target {
					t.Errorf("Control(%q, %q), want the address %s", network, address, c.target)
				}
				controls.Add(1)
				return nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			s, err := Dial(ctx, cfg)
			if c.r.Transport == UDP {
				if err != nil {
					t.Fatalf("Dial: %v", err)
				}
				defer s.Close()
				if err := echo(s, []byte("through ns.example")); err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				s.Close()
				t.Fatal("Dial succeeded against a listener that drops connections")
			}
			mu.Lock()
			if len(asked) != 1 || !asked[c.host] {
				t.Errorf("LookupIP asked for %v, want only %s", asked, c.host)
			}
			mu.Unlock()
			if controls.Load() == 0 {
				t.Error("no socket through Control")
			}
		})
	}
}
