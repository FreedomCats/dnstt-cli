package linuxsys

import (
	"net/netip"

	"golang.org/x/sys/unix"
)

// Lockdown installs the "closed" set, the kill switch while the tunnel is not up: the
// system is allowed only dnstt-cli sockets, loopback, service protocols (DHCP, ICMPv6 ND
// and RA) and the local network, and DNS goes nowhere except from dnstt-cli sockets and
// loopback, not even to the local network: the router's resolver would hand the queries
// to the ISP. Everything else is dropped; sending UDP then ends in EPERM, TCP in a
// timeout.
//
// The rules are replaced in one batch, as in Enable: there is no passage through an
// empty table. If the kernel refuses, it applies nothing, and the previous set stays.
func (g *DNSGuard) Lockdown() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.applyLocked(guardLockdown, lockdownRules(false), lockdownRules(true))
}

// localNets is the local network the kill switch lets through: private and link-local
// ranges, multicast, broadcast.
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

// lockdownRules returns the rules of the "closed" chain in order. The port 53 ban comes
// before the accepts: otherwise DNS would go to a resolver on the local network.
// forward is transit traffic of containers and VMs: the mark, loopback and the host's
// service protocols do not apply there.
func lockdownRules(forward bool) []guardRule {
	var rules []guardRule
	if !forward {
		rules = append(rules,
			guardRule{mark: true, accept: true},
			guardRule{loopback: true, accept: true})
	}
	rules = append(rules, dnsDenyRules()...)
	if !forward {
		rules = append(rules, hostServiceRules(0)...)
	}
	rules = append(rules, localNetRules(0)...)
	return append(rules, guardRule{})
}

// dnsDenyRules bans port 53, UDP and TCP, to any address.
func dnsDenyRules() []guardRule {
	return []guardRule{
		{proto: unix.IPPROTO_UDP, dport: 53},
		{proto: unix.IPPROTO_TCP, dport: 53},
	}
}

// hostServiceRules returns accepts for the host's service protocols of family family
// (unix.NFPROTO_*; 0 means both). DHCP also renews a lease by unicast, and the server
// may be outside the local network; ND and RA also go to neighbours' global addresses.
func hostServiceRules(family byte) []guardRule {
	var rules []guardRule
	for _, r := range []guardRule{
		{family: unix.NFPROTO_IPV4, proto: unix.IPPROTO_UDP, sport: 68, dport: 67, accept: true},
		{family: unix.NFPROTO_IPV6, proto: unix.IPPROTO_UDP, sport: 546, dport: 547, accept: true},
		{family: unix.NFPROTO_IPV6, ndp: true, accept: true},
	} {
		if family == 0 || r.family == family {
			rules = append(rules, r)
		}
	}
	return rules
}

// localNetRules returns accepts for the local network (localNets) of family family
// (unix.NFPROTO_*; 0 means both).
func localNetRules(family byte) []guardRule {
	var rules []guardRule
	for _, p := range localNets {
		f := byte(unix.NFPROTO_IPV4)
		if p.Addr().Is6() {
			f = unix.NFPROTO_IPV6
		}
		if family == 0 || f == family {
			rules = append(rules, guardRule{family: f, daddr: p, accept: true})
		}
	}
	return rules
}
