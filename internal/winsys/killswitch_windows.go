package winsys

import (
	"net/netip"

	"github.com/tailscale/wf"
)

// The weights of the "closed" rules are below the DNS protection weights: the port 53
// block (14) is heavier than the local network permit, and our own process and loopback
// (15) are heavier than all blocks.
const (
	lockdownLocalWeight = 13 // DHCP, ICMPv6 ND and RA, local network
	lockdownBlockWeight = 12 // everything else
)

// Lockdown sets the "closed" set, the kill-switch while the tunnel is not up: the system
// is allowed only the dnstt-cli process, loopback, service protocols (DHCP, ICMPv6 ND and
// RA) and the local network, and DNS goes nowhere except from dnstt-cli and to loopback,
// not even to the local network: the router's resolver would hand the queries to the
// ISP. Everything else is forbidden, as in linuxsys.
//
// The rules are replaced as in Enable (replaceLocked): there is no moment without rules
// during the transition. On failure the previous set stays.
func (g *DNSGuard) Lockdown() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	rules, err := lockdownRules(g.appIDs)
	if err != nil {
		return err
	}
	return g.replaceLocked(guardLockdown, "kill-switch", rules)
}

// localNets is the local network the kill-switch lets through to: private and link-local
// ranges, multicast, broadcast. The same list as in linuxsys.
var localNets = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("255.255.255.255/32"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("ff00::/8"),
}

// lockdownRules builds the rules for Lockdown; it is separate for a test without rights.
// The set follows tunnel/firewall/rules.go of wireguard-windows (MIT, v1.1.1:
// permitLoopback, permitDHCPIPv4/IPv6, permitNdp, blockAll), but DHCP goes to any
// address, as in linuxsys: a lease is renewed by unicast, and the server is not always on
// the local network. On every layer, top to bottom by weight: our own processes and
// loopback, the port 53 block, service protocols and the local network, block-all.
func lockdownRules(appIDs []string) ([]*wf.Rule, error) {
	var rules []*wf.Rule
	for _, layer := range dnsGuardLayers {
		self, err := newGuardRule(layer.id, "dnstt-cli: kill-switch, dnstt-cli itself, "+layer.name,
			dnsGuardPermitWeight, wf.ActionPermit, selfMatches(appIDs))
		if err != nil {
			return nil, err
		}
		block53, err := newGuardRule(layer.id, "dnstt-cli: kill-switch, block DNS, "+layer.name,
			dnsGuardBlockWeight, wf.ActionBlock, dnsPortMatches())
		if err != nil {
			return nil, err
		}
		local, err := lockdownLocalRules(layer, "dnstt-cli: kill-switch")
		if err != nil {
			return nil, err
		}
		// local[0] is loopback: the weight of our own processes, above the DNS block.
		rules = append(append(rules, self, local[0], block53), local[1:]...)
	}
	return rules, nil
}

// lockdownLocalRules is the part of "closed" for one layer without our own processes and
// the DNS block: loopback, DHCP, ICMPv6 ND and RA, the local network, block-all. DNS
// protection closes IPv6 outward with it as well (BlockIPv6). prefix is the start of the
// rule names.
func lockdownLocalRules(layer guardLayer, prefix string) ([]*wf.Rule, error) {
	var rules []*wf.Rule
	add := func(name string, weight uint64, action wf.Action, conds ...*wf.Match) error {
		r, err := newGuardRule(layer.id, prefix+", "+name+", "+layer.name, weight, action, conds)
		if err != nil {
			return err
		}
		rules = append(rules, r)
		return nil
	}

	if err := add("loopback", dnsGuardPermitWeight, wf.ActionPermit,
		&wf.Match{Field: wf.FieldFlags, Op: wf.MatchTypeFlagsAllSet, Value: wf.ConditionFlagIsLoopback}); err != nil {
		return nil, err
	}

	// DHCP: the client is port 68 (546 for DHCPv6), the server 67 (547). On the incoming
	// layer "local" is the client too.
	client, server := uint16(68), uint16(67)
	if layer.ipv6 {
		client, server = 546, 547
	}
	if err := add("DHCP", lockdownLocalWeight, wf.ActionPermit,
		&wf.Match{Field: wf.FieldIPProtocol, Op: wf.MatchTypeEqual, Value: wf.IPProtoUDP},
		&wf.Match{Field: wf.FieldIPLocalPort, Op: wf.MatchTypeEqual, Value: client},
		&wf.Match{Field: wf.FieldIPRemotePort, Op: wf.MatchTypeEqual, Value: server}); err != nil {
		return nil, err
	}
	if layer.ipv6 {
		// ND and RA also go to global addresses of neighbours. On the ALE layers the ICMP
		// type sits in the local port field.
		ndp := []*wf.Match{{Field: wf.FieldIPProtocol, Op: wf.MatchTypeEqual, Value: wf.IPProtoICMPV6}}
		for typ := uint16(133); typ <= 137; typ++ {
			ndp = append(ndp, &wf.Match{Field: wf.FieldIPLocalPort, Op: wf.MatchTypeEqual, Value: typ})
		}
		if err := add("ICMPv6 ND and RA", lockdownLocalWeight, wf.ActionPermit, ndp...); err != nil {
			return nil, err
		}
	}

	var local []*wf.Match
	for _, p := range localNets {
		if p.Addr().Is6() == layer.ipv6 {
			local = append(local, &wf.Match{Field: wf.FieldIPRemoteAddress, Op: wf.MatchTypeEqual, Value: p})
		}
	}
	if err := add("local network", lockdownLocalWeight, wf.ActionPermit, local...); err != nil {
		return nil, err
	}
	if err := add("block all", lockdownBlockWeight, wf.ActionBlock); err != nil {
		return nil, err
	}
	return rules, nil
}
