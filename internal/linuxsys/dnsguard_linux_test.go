package linuxsys

import (
	"bytes"
	"net/netip"
	"testing"

	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"
)

// Order of the output rules: mark, loopback, accepts for the resolvers over both
// protocols, then the port 53 bans. In forward there is no mark or loopback.
func TestDNSGuardRules(t *testing.T) {
	v4 := netip.MustParsePrefix("192.0.2.53/32")
	v6 := netip.MustParsePrefix("2001:db8::53/128")
	dns := []netip.Addr{v4.Addr(), v6.Addr()}

	out := dnsGuardRules(dns, false, false)
	want := []guardRule{
		{mark: true, accept: true},
		{loopback: true, accept: true},
		{family: unix.NFPROTO_IPV4, daddr: v4, proto: unix.IPPROTO_UDP, dport: 53, accept: true},
		{family: unix.NFPROTO_IPV4, daddr: v4, proto: unix.IPPROTO_TCP, dport: 53, accept: true},
		{family: unix.NFPROTO_IPV6, daddr: v6, proto: unix.IPPROTO_UDP, dport: 53, accept: true},
		{family: unix.NFPROTO_IPV6, daddr: v6, proto: unix.IPPROTO_TCP, dport: 53, accept: true},
		{proto: unix.IPPROTO_UDP, dport: 53},
		{proto: unix.IPPROTO_TCP, dport: 53},
	}
	assertRules(t, "output", out, want)
	assertRules(t, "forward", dnsGuardRules(dns, false, true), want[2:])
}

// With IPv6 closed an IPv6 resolver is not accepted: there is no route into the tunnel
// for it. After the port 53 ban comes local IPv6, as in "closed": the host's service
// protocols (not in forward) and the local network, then a drop of all other IPv6.
func TestDNSGuardRulesBlockIPv6(t *testing.T) {
	v4 := netip.MustParsePrefix("192.0.2.53/32")
	v6 := byte(unix.NFPROTO_IPV6)
	dns := []netip.Addr{v4.Addr(), netip.MustParseAddr("2001:db8::53")}

	head := []guardRule{
		{mark: true, accept: true},
		{loopback: true, accept: true},
		{family: unix.NFPROTO_IPV4, daddr: v4, proto: unix.IPPROTO_UDP, dport: 53, accept: true},
		{family: unix.NFPROTO_IPV4, daddr: v4, proto: unix.IPPROTO_TCP, dport: 53, accept: true},
		{proto: unix.IPPROTO_UDP, dport: 53},
		{proto: unix.IPPROTO_TCP, dport: 53},
	}
	services := []guardRule{
		{family: v6, proto: unix.IPPROTO_UDP, sport: 546, dport: 547, accept: true},
		{family: v6, ndp: true, accept: true},
	}
	local := []guardRule{
		{family: v6, daddr: netip.MustParsePrefix("fe80::/10"), accept: true},
		{family: v6, daddr: netip.MustParsePrefix("fc00::/7"), accept: true},
		{family: v6, daddr: netip.MustParsePrefix("ff00::/8"), accept: true},
		{family: v6},
	}

	var out []guardRule
	out = append(out, head...)
	out = append(out, services...)
	out = append(out, local...)
	assertRules(t, "output", dnsGuardRules(dns, true, false), out)

	var fwd []guardRule
	fwd = append(fwd, head[2:]...)
	fwd = append(fwd, local...)
	assertRules(t, "forward", dnsGuardRules(dns, true, true), fwd)
}

// An IPv4 address in IPv6 form (::ffff:a.b.c.d) is compared as IPv4, an empty one is
// skipped; with no resolvers only the bans remain.
func TestDNSGuardRulesOddAddresses(t *testing.T) {
	mapped := netip.MustParseAddr("::ffff:192.0.2.53")
	rules := dnsGuardRules([]netip.Addr{mapped, {}}, false, true)
	if len(rules) != 4 || rules[0].family != unix.NFPROTO_IPV4 || rules[0].daddr != netip.PrefixFrom(mapped.Unmap(), 32) {
		t.Errorf("rules: %+v", rules)
	}
	if rules := dnsGuardRules(nil, false, true); len(rules) != 2 || rules[0].accept || rules[1].accept {
		t.Errorf("no resolvers: %+v", rules)
	}
}

func assertRules(t *testing.T, chain string, got, want []guardRule) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d rules, want %d:\n%+v", chain, len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s, rule %d: %+v, want %+v", chain, i, got[i], want[i])
		}
	}
}

// Expressions: what is compared with what. The address is in network byte order, the
// port big-endian, the interface name IFNAMSIZ bytes, the verdict last.
func TestGuardRuleExprs(t *testing.T) {
	r := guardRule{family: unix.NFPROTO_IPV6, daddr: netip.MustParsePrefix("2001:db8::53/128"),
		proto: unix.IPPROTO_TCP, dport: 53, accept: true}
	e := r.exprs(0x1076)
	if len(e) != 9 {
		t.Fatalf("%d expressions, want 9: %#v", len(e), e)
	}
	if p, ok := e[2].(*expr.Payload); !ok || p.Base != expr.PayloadBaseNetworkHeader || p.Offset != 24 || p.Len != 16 {
		t.Errorf("IPv6 destination address: %#v", e[2])
	}
	if c := e[3].(*expr.Cmp); !bytes.Equal(c.Data, r.daddr.Addr().AsSlice()) {
		t.Errorf("address: % x", c.Data)
	}
	if p, ok := e[6].(*expr.Payload); !ok || p.Base != expr.PayloadBaseTransportHeader || p.Offset != 2 || p.Len != 2 {
		t.Errorf("destination port: %#v", e[6])
	}
	if c := e[7].(*expr.Cmp); !bytes.Equal(c.Data, []byte{0, 53}) {
		t.Errorf("port: % x", c.Data)
	}
	if v := e[8].(*expr.Verdict); v.Kind != expr.VerdictAccept {
		t.Errorf("verdict: %v", v.Kind)
	}

	v4 := guardRule{daddr: netip.MustParsePrefix("192.0.2.53/32")}.exprs(0)
	if p := v4[0].(*expr.Payload); p.Offset != 16 || p.Len != 4 {
		t.Errorf("IPv4 destination address: %#v", p)
	}
	if v := v4[2].(*expr.Verdict); v.Kind != expr.VerdictDrop {
		t.Errorf("verdict without accept: %v", v.Kind)
	}

	lo := guardRule{loopback: true}.exprs(0)
	if c := lo[1].(*expr.Cmp); len(c.Data) != unix.IFNAMSIZ || !bytes.HasPrefix(c.Data, []byte("lo\x00")) {
		t.Errorf("interface name: % x", c.Data)
	}
	mark := guardRule{mark: true}.exprs(0x1076)
	if m := mark[0].(*expr.Meta); m.Key != expr.MetaKeyMARK {
		t.Errorf("mark: %#v", m)
	}
}
