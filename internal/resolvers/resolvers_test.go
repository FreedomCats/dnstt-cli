package resolvers

import (
	"context"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/FreedomCats/dnstt-cli/core/client"
)

func addrs(s ...string) []netip.Addr {
	var a []netip.Addr
	for _, x := range s {
		a = append(a, netip.MustParseAddr(x))
	}
	return a
}

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		entry string
		want  Resolver
		str   string // String(); "" — the entry itself
	}{
		{"doh:https://doh.example/dns-query",
			Resolver{Transport: client.DoH, Address: "https://doh.example/dns-query"}, ""},
		{"doh:https://doh.example/dns-query@192.0.2.1",
			Resolver{Transport: client.DoH, Address: "https://doh.example/dns-query", Pins: addrs("192.0.2.1")}, ""},
		{"doh:https://doh.example:8443/q?x=1@192.0.2.1,[2001:db8::1], 192.0.2.1",
			Resolver{Transport: client.DoH, Address: "https://doh.example:8443/q?x=1", Pins: addrs("192.0.2.1", "2001:db8::1")},
			"doh:https://doh.example:8443/q?x=1@192.0.2.1,2001:db8::1"},
		{"DoH:https://192.0.2.1/dns-query",
			Resolver{Transport: client.DoH, Address: "https://192.0.2.1/dns-query"},
			"doh:https://192.0.2.1/dns-query"},
		{"dot:dot.example",
			Resolver{Transport: client.DoT, Address: "dot.example:853"}, "dot:dot.example:853"},
		{"dot:dot.example:8853@192.0.2.2",
			Resolver{Transport: client.DoT, Address: "dot.example:8853", Pins: addrs("192.0.2.2")}, ""},
		{"udp:192.0.2.53",
			Resolver{Transport: client.UDP, Address: "192.0.2.53:53"}, "udp:192.0.2.53:53"},
		{"  udp:192.0.2.53:5353 ",
			Resolver{Transport: client.UDP, Address: "192.0.2.53:5353"}, "udp:192.0.2.53:5353"},
		{"udp:2001:db8::53",
			Resolver{Transport: client.UDP, Address: "[2001:db8::53]:53"}, "udp:[2001:db8::53]:53"},
		{"udp:[2001:db8::53]",
			Resolver{Transport: client.UDP, Address: "[2001:db8::53]:53"}, "udp:[2001:db8::53]:53"},
		{"udp:[::ffff:192.0.2.53]:53",
			Resolver{Transport: client.UDP, Address: "192.0.2.53:53"}, "udp:192.0.2.53:53"},
		{"dot:[2001:db8::853]:853",
			Resolver{Transport: client.DoT, Address: "[2001:db8::853]:853"}, ""},
		{"udp:dns.example@192.0.2.53",
			Resolver{Transport: client.UDP, Address: "dns.example:53", Pins: addrs("192.0.2.53")},
			"udp:dns.example:53@192.0.2.53"},
	} {
		got, err := Parse(tc.entry)
		if err != nil {
			t.Errorf("%q: %v", tc.entry, err)
			continue
		}
		if got.Transport != tc.want.Transport || got.Address != tc.want.Address || !slices.Equal(got.Pins, tc.want.Pins) {
			t.Errorf("%q: got %+v, want %+v", tc.entry, got, tc.want)
		}
		str := tc.str
		if str == "" {
			str = tc.entry
		}
		if got.String() != str {
			t.Errorf("%q: String() = %q, want %q", tc.entry, got.String(), str)
		}
		// String reads back to the same resolver.
		again, err := Parse(got.String())
		if err != nil || again.String() != got.String() {
			t.Errorf("%q: Parse(String()) = %v, %v", tc.entry, again, err)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, tc := range []struct{ entry, want string }{
		{"", "no transport"},
		{"doh-cloudflare", "no transport"},
		{"tcp:192.0.2.53:53", "not supported yet"},
		{"doq:dns.example", "unknown transport"},
		{"doh:http://doh.example/dns-query", "https://"},
		{"doh:https://user@doh.example/dns-query@192.0.2.1", "user name"},
		{"doh:https:///dns-query", "bad host name"},
		{"doh:https://doh.example:0/q", "bad port"},
		{"doh:https://doh.example/q#frag", "fragment"},
		{"doh:https://doh.example/dns-query@", "nothing after @"},
		{"doh:https://doh.example/dns-query@doh.example", "not an IP address"},
		{"doh:https://192.0.2.1/dns-query@192.0.2.2", "pins are for host names"},
		{"dot:dot.example:0", "bad port"},
		{"dot:dot.example:65536", "bad port"},
		{"dot:dot.example:", "bad port"},
		{"udp:", "bad host name"},
		{"udp:bad host:53", "bad host name"},
		{"udp:a..b:53", "bad host name"},
		{"udp:[fe80::1%eth0]:53", "zone"},
		{"udp:" + strings.Repeat("a", 64) + ".example", "bad host name"},
	} {
		_, err := Parse(tc.entry)
		if err == nil {
			t.Errorf("%q: no error", tc.entry)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: %q does not say %q", tc.entry, err, tc.want)
		}
	}
}

func TestPresets(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Presets {
		if seen[p.Name] {
			t.Errorf("preset %s twice", p.Name)
		}
		seen[p.Name] = true
		r, ok := Preset(p.Name)
		if !ok {
			t.Fatalf("Preset(%q) not found", p.Name)
		}
		// Every preset works without the bootstrap resolver.
		if _, err := netip.ParseAddr(r.Host()); err != nil && len(r.Pins) == 0 {
			t.Errorf("%s: a host name without pins", p.Name)
		}
		if r.Name() != p.Name || r.Preset != p.Name {
			t.Errorf("%s: Name() = %q", p.Name, r.Name())
		}
		if !strings.HasPrefix(p.Name, string(r.Transport)+"-") {
			t.Errorf("%s: transport %s", p.Name, r.Transport)
		}
	}
	if _, ok := Preset("DOH-Cloudflare"); !ok {
		t.Error("preset names are not case-insensitive")
	}
	if _, ok := Preset("doh-nonexistent"); ok {
		t.Error("an unknown preset found")
	}
}

func TestExpand(t *testing.T) {
	list, err := Expand([]string{
		"doh-google",
		"udp:192.0.2.53",
		"doh:https://dns.google/dns-query@8.8.8.8,8.8.4.4", // the same as the preset
		"udp:192.0.2.53:53", // the same as the second
		"dot-quad9",
	})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range list {
		names = append(names, r.Name())
	}
	want := []string{"doh-google", "udp:192.0.2.53:53", "dot-quad9"}
	if !slices.Equal(names, want) {
		t.Errorf("got %q, want %q", names, want)
	}

	_, err = Expand([]string{"doh-cloudfare", "udp:bad host", "doh-google"})
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{`"doh-cloudfare": no such preset (doh-cloudflare,`, `"udp:bad host"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q does not say %q", err, want)
		}
	}
	if _, err := Expand(nil); err == nil {
		t.Error("an empty list is accepted")
	}
}

func TestLookupIP(t *testing.T) {
	r, err := Parse("doh:https://doh.example/dns-query@192.0.2.1,192.0.2.2")
	if err != nil {
		t.Fatal(err)
	}
	var asked []string
	fallback := func(ctx context.Context, host string) ([]netip.Addr, error) {
		asked = append(asked, host)
		return addrs("198.51.100.1"), nil
	}
	lookup := r.LookupIP(fallback)
	got, err := lookup(context.Background(), "DOH.example")
	if err != nil || !slices.Equal(got, addrs("192.0.2.1", "192.0.2.2")) {
		t.Errorf("pinned host: %v, %v", got, err)
	}
	got[0] = netip.Addr{} // the caller may change what it got
	if got, _ := lookup(context.Background(), "doh.example"); !slices.Equal(got, addrs("192.0.2.1", "192.0.2.2")) {
		t.Errorf("pins changed by the caller: %v", got)
	}
	if got, err := lookup(context.Background(), "other.example"); err != nil || !slices.Equal(got, addrs("198.51.100.1")) {
		t.Errorf("other host: %v, %v", got, err)
	}
	if !slices.Equal(asked, []string{"other.example"}) {
		t.Errorf("fallback asked %q", asked)
	}
	if _, err := r.LookupIP(nil)(context.Background(), "other.example"); err == nil {
		t.Error("no fallback: a name that is not pinned is resolved")
	}

	// Without pins every name goes to the fallback.
	r, _ = Parse("dot:dot.example")
	asked = nil
	if _, err := r.LookupIP(fallback)(context.Background(), "dot.example"); err != nil || len(asked) != 1 {
		t.Errorf("no pins: %v, asked %q", err, asked)
	}
}

func TestCore(t *testing.T) {
	r, _ := Parse("dot:dot.example@192.0.2.2")
	if got, want := r.Core(), (client.Resolver{Transport: client.DoT, Address: "dot.example:853"}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if r.Host() != "dot.example" {
		t.Errorf("Host() = %q", r.Host())
	}
}
