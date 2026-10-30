package resolvers

import (
	"context"
	"flag"
	"io"
	"log"
	"os"
	"strconv"
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

// A session through a resolver given by a host name: the core gets the address from the
// pin, or from the bootstrap resolver, and never from the system.
func TestSessionThroughName(t *testing.T) {
	domain, err := dns.ParseName("t.example.com")
	if err != nil {
		t.Fatal(err)
	}
	ts, err := server.StartTest(domain, "127.0.0.1:9") // no streams are opened
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ts.Close() })
	port := strconv.Itoa(ts.Addr.Port)
	srv := startDNS(t)
	boot := NewBootstrap(fixed(srv.addr), nil)

	for _, tc := range []struct {
		entry   string
		queries int // to the bootstrap resolver
	}{
		{"udp:pinned.example:" + port + "@127.0.0.1", 0},
		{"udp:lo.example:" + port, 1},
	} {
		r, err := Parse(tc.entry)
		if err != nil {
			t.Fatal(err)
		}
		before := len(srv.queries())
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		s, err := client.Dial(ctx, client.Config{
			Domain:   domain,
			PubKey:   ts.PubKey,
			Resolver: r.Core(),
			LookupIP: r.LookupIP(boot.LookupIP),
		})
		cancel()
		if err != nil {
			t.Fatalf("%s: %v", tc.entry, err)
		}
		s.Close()
		if got := len(srv.queries()) - before; got != tc.queries {
			t.Errorf("%s: %d queries to the bootstrap resolver, want %d", tc.entry, got, tc.queries)
		}
	}
}
