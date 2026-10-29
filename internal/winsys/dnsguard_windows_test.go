package winsys

import (
	"net/netip"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tailscale/wf"
	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// The rules, without WFP: on each of the four layers the block of port 53 over UDP and TCP
// is lighter than both permits, for our own process and for the tunnel's resolvers of the
// layer's family with loopback.
func TestDNSGuardRules(t *testing.T) {
	dns := []netip.Addr{
		netip.MustParseAddr("10.8.0.1"),
		netip.MustParseAddr("::ffff:1.1.1.1"), // IPv4 in IPv6 notation goes to IPv4
		netip.MustParseAddr("fd00::1"),
	}
	rules, err := dnsGuardRules(dns, []string{"app-id", "other-id"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 3*len(dnsGuardLayers) {
		t.Fatalf("%d rules, want %d", len(rules), 3*len(dnsGuardLayers))
	}

	ids := map[wf.RuleID]bool{}
	for i, layer := range dnsGuardLayers {
		block, self, allowed := rules[3*i], rules[3*i+1], rules[3*i+2]
		for _, r := range []*wf.Rule{block, self, allowed} {
			if r.Layer != layer.id || r.Sublayer != dnsGuardSublayer || r.Provider != DNSGuardProvider {
				t.Errorf("%s: rule %q is not on its layer, sublayer or provider", layer.name, r.Name)
			}
			if ids[r.ID] || r.ID.IsZero() {
				t.Errorf("%s: GUID of rule %q is empty or repeated", layer.name, r.Name)
			}
			ids[r.ID] = true
		}

		if block.Action != wf.ActionBlock || block.Weight >= self.Weight || block.Weight >= allowed.Weight {
			t.Errorf("%s: block %v with weight %d, permits %d and %d: the block must be lighter",
				layer.name, block.Action, block.Weight, self.Weight, allowed.Weight)
		}
		wantBlock := []wf.Match{
			{Field: wf.FieldIPRemotePort, Op: wf.MatchTypeEqual, Value: uint16(53)},
			{Field: wf.FieldIPProtocol, Op: wf.MatchTypeEqual, Value: wf.IPProtoUDP},
			{Field: wf.FieldIPProtocol, Op: wf.MatchTypeEqual, Value: wf.IPProtoTCP},
		}
		checkMatches(t, layer.name+": block", block.Conditions, wantBlock)

		if self.Action != wf.ActionPermit {
			t.Errorf("%s: own process rule is %v", layer.name, self.Action)
		}
		checkMatches(t, layer.name+": own process", self.Conditions, []wf.Match{
			{Field: wf.FieldALEAppID, Op: wf.MatchTypeEqual, Value: "app-id"},
			{Field: wf.FieldALEAppID, Op: wf.MatchTypeEqual, Value: "other-id"},
		})

		if allowed.Action != wf.ActionPermit {
			t.Errorf("%s: resolvers rule is %v", layer.name, allowed.Action)
		}
		var want []wf.Match
		if layer.ipv6 {
			want = []wf.Match{
				{Field: wf.FieldIPRemoteAddress, Op: wf.MatchTypeEqual, Value: netip.IPv6Loopback()},
				{Field: wf.FieldIPRemoteAddress, Op: wf.MatchTypeEqual, Value: netip.MustParseAddr("fd00::1")},
			}
		} else {
			want = []wf.Match{
				{Field: wf.FieldIPRemoteAddress, Op: wf.MatchTypeEqual, Value: netip.MustParsePrefix("127.0.0.0/8")},
				{Field: wf.FieldIPRemoteAddress, Op: wf.MatchTypeEqual, Value: netip.MustParseAddr("10.8.0.1")},
				{Field: wf.FieldIPRemoteAddress, Op: wf.MatchTypeEqual, Value: netip.MustParseAddr("1.1.1.1")},
			}
		}
		checkMatches(t, layer.name+": resolvers", allowed.Conditions, want)
	}
}

// IPv6 closed outward (BlockIPv6): the IPv4 layers have the same three rules, the IPv6
// layers additionally loopback, DHCPv6, ND and RA, the local network and block-all,
// lighter than the port 53 block, so DNS to the local network is closed.
func TestDNSGuardRulesBlockIPv6(t *testing.T) {
	rules, err := dnsGuardRules([]netip.Addr{netip.MustParseAddr("192.0.2.53")}, []string{"app-id"}, true)
	if err != nil {
		t.Fatal(err)
	}
	byLayer := map[wf.LayerID][]*wf.Rule{}
	for _, r := range rules {
		byLayer[r.Layer] = append(byLayer[r.Layer], r)
	}
	for _, layer := range dnsGuardLayers {
		got := byLayer[layer.id]
		if !layer.ipv6 {
			if len(got) != 3 {
				t.Errorf("%s: %d rules, want 3", layer.name, len(got))
			}
			continue
		}
		if len(got) != 3+5 {
			t.Fatalf("%s: %d rules, want 8", layer.name, len(got))
		}
		block53, all := got[0], got[len(got)-1]
		if all.Action != wf.ActionBlock || len(all.Conditions) != 0 || all.Weight != lockdownBlockWeight {
			t.Errorf("%s: last rule %q is %v, weight %d, %d conditions; want block-all",
				layer.name, all.Name, all.Action, all.Weight, len(all.Conditions))
		}
		for _, r := range got[3 : len(got)-1] {
			if r.Action != wf.ActionPermit || (r.Weight >= block53.Weight) != strings.Contains(r.Name, "loopback") {
				t.Errorf("%s: %q is %v with weight %d, DNS block %d", layer.name, r.Name, r.Action, r.Weight, block53.Weight)
			}
		}
	}
}

func checkMatches(t *testing.T, what string, got []*wf.Match, want []wf.Match) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: %d conditions, want %d: %v", what, len(got), len(want), got)
		return
	}
	for i := range want {
		if *got[i] != want[i] {
			t.Errorf("%s: condition %d = %v, want %v", what, i, got[i], want[i])
		}
	}
}

// Without administrator rights the session does not open, and that is an error, not a
// panic. With rights, a full round on real WFP: Enable, repeated Enable, Disable, Close.
func TestDNSGuardSession(t *testing.T) {
	g, err := OpenDNSGuard()
	if err != nil {
		t.Skipf("WFP session did not open (administrator rights required): %v", err)
	}
	dns := []netip.Addr{netip.MustParseAddr("1.1.1.1")}
	if err := g.Enable(dns); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if err := g.Enable(dns); err != nil {
		t.Fatalf("repeated Enable: %v", err)
	}
	if n := len(g.rules); n != 3*len(dnsGuardLayers) {
		t.Errorf("after two Enable calls %d rules, want %d", n, 3*len(dnsGuardLayers))
	}
	if err := g.Disable(); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if err := g.Disable(); err != nil {
		t.Fatalf("repeated Disable: %v", err)
	}
	if err := g.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := g.Enable(dns); err == nil {
		t.Error("Enable accepted after Close")
	}
}

// Rules are set while garbage collection runs. The arena of tailscale/wf handed out
// unaligned memory, and writing a struct with pointers during marking crashed the
// process: fatal error "bulkBarrierPreWrite: unaligned arguments", which recover does not
// catch (found in a live run, on the very first Enable).
//
// Whether it is aligned depends on the length of the application ID in the "own process"
// rule, that is the length of the path to the exe. dnstt-cli.exe has a constant path,
// while the test binary gets a different go-build directory every time, so the lengths
// are iterated: padding of 0-7 bytes covers the unaligned case too. Without the fix in the
// fork the test fails within a fraction of a second.
func TestDNSGuardUnderGC(t *testing.T) {
	g, err := OpenDNSGuard()
	if err != nil {
		t.Skipf("WFP session did not open (administrator rights required): %v", err)
	}
	defer g.Close()

	var stop atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		for !stop.Load() {
			runtime.GC()
		}
	}()
	defer func() {
		stop.Store(true)
		<-done
	}()

	appID := g.appIDs[0]
	dns := []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("1.0.0.1")}
	for i := range 50 {
		for pad := range 8 {
			g.appIDs[0] = appID + strings.Repeat("x", pad)
			if err := g.Enable(dns); err != nil {
				t.Fatalf("Enable %d, padding %d: %v", i, pad, err)
			}
		}
	}
	if err := g.Disable(); err != nil {
		t.Fatal(err)
	}
}

// The "closed" set, without WFP: on every layer, top to bottom by weight, our own process
// and loopback, the DNS block, service protocols and the local network of the layer's
// family, block-all.
func TestLockdownRules(t *testing.T) {
	rules, err := lockdownRules([]string{"app-id", "other-id"})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[wf.RuleID]bool{}
	byLayer := map[wf.LayerID][]*wf.Rule{}
	for _, r := range rules {
		if r.Sublayer != dnsGuardSublayer || r.Provider != DNSGuardProvider {
			t.Errorf("rule %q is not in its sublayer or provider", r.Name)
		}
		if ids[r.ID] || r.ID.IsZero() {
			t.Errorf("GUID of rule %q is empty or repeated", r.Name)
		}
		ids[r.ID] = true
		byLayer[r.Layer] = append(byLayer[r.Layer], r)
	}

	for _, layer := range dnsGuardLayers {
		got := byLayer[layer.id]
		want := 6
		if layer.ipv6 {
			want = 7 // plus ICMPv6 ND
		}
		if len(got) != want {
			t.Fatalf("%s: %d rules, want %d", layer.name, len(got), want)
		}
		for i := 1; i < len(got); i++ {
			if got[i].Weight > got[i-1].Weight {
				t.Errorf("%s: %q is heavier than %q above it", layer.name, got[i].Name, got[i-1].Name)
			}
		}
		self, loopback, dns, dhcp, local, block := got[0], got[1], got[2], got[3], got[len(got)-2], got[len(got)-1]

		for _, r := range []*wf.Rule{self, loopback, dhcp, local} {
			if r.Action != wf.ActionPermit {
				t.Errorf("%s: %q is %v, want permit", layer.name, r.Name, r.Action)
			}
		}
		for _, r := range []*wf.Rule{dns, block} {
			if r.Action != wf.ActionBlock {
				t.Errorf("%s: %q is %v, want block", layer.name, r.Name, r.Action)
			}
		}
		if self.Weight != dnsGuardPermitWeight || loopback.Weight != dnsGuardPermitWeight {
			t.Errorf("%s: own process and loopback with weights %d and %d", layer.name, self.Weight, loopback.Weight)
		}
		if dns.Weight != dnsGuardBlockWeight || dns.Weight <= local.Weight {
			t.Errorf("%s: DNS block with weight %d is not heavier than the local network (%d)", layer.name, dns.Weight, local.Weight)
		}
		if block.Weight >= local.Weight || block.Weight >= dhcp.Weight || len(block.Conditions) != 0 {
			t.Errorf("%s: block-all has weight %d, %d conditions", layer.name, block.Weight, len(block.Conditions))
		}

		checkMatches(t, layer.name+": own process", self.Conditions, []wf.Match{
			{Field: wf.FieldALEAppID, Op: wf.MatchTypeEqual, Value: "app-id"},
			{Field: wf.FieldALEAppID, Op: wf.MatchTypeEqual, Value: "other-id"},
		})
		checkMatches(t, layer.name+": loopback", loopback.Conditions, []wf.Match{
			{Field: wf.FieldFlags, Op: wf.MatchTypeFlagsAllSet, Value: wf.ConditionFlagIsLoopback},
		})
		checkMatches(t, layer.name+": DNS block", dns.Conditions, []wf.Match{
			{Field: wf.FieldIPRemotePort, Op: wf.MatchTypeEqual, Value: uint16(53)},
			{Field: wf.FieldIPProtocol, Op: wf.MatchTypeEqual, Value: wf.IPProtoUDP},
			{Field: wf.FieldIPProtocol, Op: wf.MatchTypeEqual, Value: wf.IPProtoTCP},
		})
		client, server := uint16(68), uint16(67)
		if layer.ipv6 {
			client, server = 546, 547
		}
		checkMatches(t, layer.name+": DHCP", dhcp.Conditions, []wf.Match{
			{Field: wf.FieldIPProtocol, Op: wf.MatchTypeEqual, Value: wf.IPProtoUDP},
			{Field: wf.FieldIPLocalPort, Op: wf.MatchTypeEqual, Value: client},
			{Field: wf.FieldIPRemotePort, Op: wf.MatchTypeEqual, Value: server},
		})
		if layer.ipv6 {
			ndp := got[4]
			want := []wf.Match{{Field: wf.FieldIPProtocol, Op: wf.MatchTypeEqual, Value: wf.IPProtoICMPV6}}
			for typ := uint16(133); typ <= 137; typ++ {
				want = append(want, wf.Match{Field: wf.FieldIPLocalPort, Op: wf.MatchTypeEqual, Value: typ})
			}
			if ndp.Action != wf.ActionPermit || ndp.Weight != local.Weight {
				t.Errorf("%s: ND is %v with weight %d", layer.name, ndp.Action, ndp.Weight)
			}
			checkMatches(t, layer.name+": ND", ndp.Conditions, want)
		}

		var wantLocal []wf.Match
		for _, p := range localNets {
			if p.Addr().Is6() == layer.ipv6 {
				wantLocal = append(wantLocal, wf.Match{Field: wf.FieldIPRemoteAddress, Op: wf.MatchTypeEqual, Value: p})
			}
		}
		if len(wantLocal) == 0 {
			t.Fatalf("%s: no local network", layer.name)
		}
		checkMatches(t, layer.name+": local network", local.Conditions, wantLocal)
	}
}

// Transitions between the sets on real WFP (administrator rights required): "closed" →
// DNS protection → "closed" → DNS protection with IPv6 closed → "closed" → no rules.
// After every transition the session holds exactly the rules of the new set: the old ones
// are removed, the new ones in place.
func TestDNSGuardLockdownSession(t *testing.T) {
	g, err := OpenDNSGuard()
	if err != nil {
		t.Skipf("WFP session did not open (administrator rights required): %v", err)
	}
	defer g.Close()
	lockdown, err := lockdownRules(g.appIDs)
	if err != nil {
		t.Fatal(err)
	}
	dns := []netip.Addr{netip.MustParseAddr("1.1.1.1")}

	check := func(what string, state guardState, want int) {
		t.Helper()
		if g.state != state || len(g.rules) != want {
			t.Errorf("%s: state %d, %d rules; want %d and %d", what, g.state, len(g.rules), state, want)
		}
		if inSession := sublayerRules(t, g); inSession != want {
			t.Errorf("%s: %d rules in the WFP sublayer, want %d", what, inSession, want)
		}
	}

	steps := []struct {
		what  string
		do    func() error
		state guardState
		want  int
	}{
		{"Lockdown", g.Lockdown, guardLockdown, len(lockdown)},
		{"repeated Lockdown", g.Lockdown, guardLockdown, len(lockdown)},
		{"Enable", func() error { return g.Enable(dns) }, guardTunnel, 3 * len(dnsGuardLayers)},
		{"Lockdown after Enable", g.Lockdown, guardLockdown, len(lockdown)},
		{"Enable with IPv6 closed", func() error {
			g.BlockIPv6(true)
			return g.Enable(dns)
		}, guardTunnel, 3*len(dnsGuardLayers) + 2*5},
		{"Lockdown after IPv6 closed", g.Lockdown, guardLockdown, len(lockdown)},
		{"Disable", g.Disable, guardOpen, 0},
	}
	for _, s := range steps {
		if err := s.do(); err != nil {
			t.Fatalf("%s: %v", s.what, err)
		}
		check(s.what, s.state, s.want)
	}
}

// A set that was not set in full is removed, and the previous one stays: here "closed"
// stays "closed". The failure is a rule with an invalid condition value: it is lighter
// than the others and is set last, after all the valid ones.
func TestDNSGuardReplaceRollback(t *testing.T) {
	g, err := OpenDNSGuard()
	if err != nil {
		t.Skipf("WFP session did not open (administrator rights required): %v", err)
	}
	defer g.Close()
	if err := g.Lockdown(); err != nil {
		t.Fatal(err)
	}
	want := len(g.rules)

	rules, err := dnsGuardRules([]netip.Addr{netip.MustParseAddr("1.1.1.1")}, g.appIDs, false)
	if err != nil {
		t.Fatal(err)
	}
	bad, err := newGuardRule(wf.LayerALEAuthConnectV4, "dnstt-cli: failure test", 1, wf.ActionBlock,
		[]*wf.Match{{Field: wf.FieldIPRemotePort, Op: wf.MatchTypeEqual, Value: "not a port"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.replaceLocked(guardTunnel, "test", append(rules, bad)); err == nil {
		t.Fatal("a set with an invalid rule was set")
	}
	if g.state != guardLockdown || len(g.rules) != want {
		t.Errorf("after the failure: state %d, %d rules; want \"closed\" and %d", g.state, len(g.rules), want)
	}
	if inSession := sublayerRules(t, g); inSession != want {
		t.Errorf("%d rules in the WFP sublayer, want %d: the new ones were not removed", inSession, want)
	}
}

// DNS changed on the fly on real WFP: the resolvers change twice in a row in the same
// order as in Tun.applyDNS, first allowing the previous and the new ones, then only the
// new ones. After every rule that was set or removed, every layer of the sublayer has the
// port 53 block, the permit for our own process and the resolvers the system's DNS leads
// to at that step: there is no moment without protection. At the end of the transition
// exactly the new resolvers are allowed.
//
// There are about a hundred steps, and listing the WFP filters after each takes seconds,
// so the system's current resolvers are added to the test resolvers: the machine's DNS is
// not closed for that time.
func TestDNSGuardChangeDNS(t *testing.T) {
	g, err := OpenDNSGuard()
	if err != nil {
		t.Skipf("WFP session did not open (administrator rights required): %v", err)
	}
	defer g.Close()
	a := netip.MustParseAddr("192.0.2.53")
	b := netip.MustParseAddr("198.51.100.53")
	c := netip.MustParseAddr("203.0.113.53")
	sys := systemResolvers(t)
	with := func(dns ...netip.Addr) []netip.Addr { return append(dns, sys...) }

	known := map[wf.RuleID]*wf.Rule{}
	var keep []netip.Addr
	checking, steps, failed := false, 0, false
	// No t.Fatal in the hook: g.mu is held, and the deferred Close would hang.
	g.step = func(r *wf.Rule, added bool) {
		if added {
			known[r.ID] = r
		}
		if !checking || failed {
			return
		}
		steps++
		if problem := guardGap(t, g, known, keep, false); problem != "" {
			failed = true
			t.Errorf("step %d (%q, added %v): %s", steps, r.Name, added, problem)
		}
	}

	if err := g.Enable(with(a)); err != nil {
		t.Fatalf("first Enable: %v", err)
	}
	checking = true
	for _, s := range []struct {
		what      string
		dns, keep []netip.Addr
	}{
		{"previous and new", with(a, b), with(a)},
		{"only new", with(b), with(b)},
		{"second time: previous and new", with(b, c), with(b)},
		{"second time: only new", with(c), with(c)},
	} {
		keep = s.keep
		if err := g.Enable(s.dns); err != nil {
			t.Fatalf("%s: Enable: %v", s.what, err)
		}
		if problem := guardGap(t, g, known, s.dns, true); problem != "" {
			t.Errorf("%s: after the transition %s", s.what, problem)
		}
		if n := sublayerRules(t, g); n != 3*len(dnsGuardLayers) {
			t.Errorf("%s: %d rules in the sublayer, want %d: the old ones were not removed", s.what, n, 3*len(dnsGuardLayers))
		}
	}
	if steps == 0 {
		t.Error("the transition hook was not called")
	}
}

// guardGap looks at what is in the WFP sublayer and returns what DNS protection is
// missing: on every layer the port 53 block, the permit for our own process, loopback and
// the resolvers keep of the layer's family; with exact, no others either. A sublayer rule
// that is not in known is an error too: this test did not set it.
func guardGap(t *testing.T, g *DNSGuard, known map[wf.RuleID]*wf.Rule, keep []netip.Addr, exact bool) string {
	t.Helper()
	all, err := g.session.Rules()
	if err != nil {
		return "listing WFP rules: " + err.Error()
	}
	type layerState struct {
		block, self, loopback bool
		permitted             map[netip.Addr]bool
	}
	layers := map[wf.LayerID]*layerState{}
	for _, l := range dnsGuardLayers {
		layers[l.id] = &layerState{permitted: map[netip.Addr]bool{}}
	}
	for _, inSession := range all {
		if inSession.Sublayer != dnsGuardSublayer {
			continue
		}
		r, ok := known[inSession.ID]
		if !ok {
			return "foreign rule in the sublayer: " + inSession.Name
		}
		st := layers[r.Layer]
		switch {
		case r.Action == wf.ActionBlock:
			st.block = true
		case strings.Contains(r.Name, "DNS of dnstt-cli itself"):
			st.self = true
		default:
			for _, m := range r.Conditions {
				switch v := m.Value.(type) {
				case netip.Addr:
					if v.IsLoopback() {
						st.loopback = true
					} else {
						st.permitted[v] = true
					}
				case netip.Prefix:
					if v.Addr().IsLoopback() {
						st.loopback = true
					}
				}
			}
		}
	}
	for _, l := range dnsGuardLayers {
		st := layers[l.id]
		switch {
		case !st.block:
			return l.name + ": no port 53 block"
		case !st.self:
			return l.name + ": no permit for our own process"
		case !st.loopback:
			return l.name + ": no loopback"
		}
		want := 0
		for _, a := range keep {
			if a.Is6() != l.ipv6 {
				continue
			}
			want++
			if !st.permitted[a] {
				return l.name + ": resolver not allowed: " + a.String()
			}
		}
		if exact && len(st.permitted) != want {
			return l.name + ": extra resolvers allowed"
		}
	}
	return ""
}

// systemResolvers returns the resolvers currently set on the system's interfaces, without
// the placeholders fec0:0:0:ffff::1-3 that Windows shows for an interface without IPv6
// DNS.
func systemResolvers(t *testing.T) []netip.Addr {
	t.Helper()
	placeholder := netip.MustParsePrefix("fec0:0:0:ffff::/64")
	ifaces, err := winipcfg.GetAdaptersAddresses(windows.AF_UNSPEC, winipcfg.GAAFlagDefault)
	if err != nil {
		t.Fatalf("system interfaces: %v", err)
	}
	var out []netip.Addr
	for _, iface := range ifaces {
		for d := iface.FirstDNSServerAddress; d != nil; d = d.Next {
			a, ok := netip.AddrFromSlice(d.Address.IP())
			if a = a.Unmap(); ok && !placeholder.Contains(a) && !slices.Contains(out, a) {
				out = append(out, a)
			}
		}
	}
	return out
}

// sublayerRules counts the rules in our WFP sublayer.
func sublayerRules(t *testing.T, g *DNSGuard) int {
	t.Helper()
	all, err := g.session.Rules()
	if err != nil {
		t.Fatalf("listing WFP rules: %v", err)
	}
	n := 0
	for _, r := range all {
		if r.Sublayer == dnsGuardSublayer {
			n++
		}
	}
	return n
}
