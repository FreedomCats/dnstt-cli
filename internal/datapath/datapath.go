// Package datapath is how traffic gets into the tunnel: a local SOCKS5 proxy (socks) or a
// system adapter (tun). The interfaces between a datapath and the code that runs it live
// here; the datapaths themselves come in E1 (socks) and E2 (tun).
package datapath

import "net/netip"

// Requirements are the conditions a datapath works on. The zero value is "nothing
// needed": that is socks, run by an ordinary user.
type Requirements struct {
	// Unsupported is why the datapath does not work on this system; nil — it works. If
	// set, the caller does not check the other fields: there is no point in advising
	// privileges for what will not start anyway.
	Unsupported error

	// Admin: administrator (root) privileges are needed. The caller checks them: the
	// datapath knows nothing about the privileges of the process.
	Admin bool

	// Netguard: the datapath takes all system traffic, and the core's own sockets must be
	// taken past the tunnel (bound to the physical interface, architecture.md §06),
	// otherwise the tunnel depends on itself.
	Netguard bool

	// DNSGuard: the datapath sets DNS leak protection and expects its session in its
	// options. The caller opens it before connecting: a refusal of nftables or WFP is a
	// clear error at once, not a loop of reconnections.
	DNSGuard bool
}

// DNSGuard is the DNS leak protection, one for the whole dnstt-cli connect
// (linuxsys.DNSGuard, winsys.DNSGuard). The datapath enables it when the routes are set,
// switches it to new resolvers when DNS changes and removes it in Suspend and Close; both
// calls are harmless when repeated.
type DNSGuard interface {
	// Enable lets DNS go only to the resolvers dns (and to the queries of dnstt-cli
	// itself); a repeated call replaces the previous list.
	Enable(dns []netip.Addr) error
	// Disable removes the restriction.
	Disable() error
}

// IPv6Blocker is an optional part of DNSGuard (Linux): protection that can also close the
// system's IPv6 outward. The tunnel does not carry IPv6 (architecture.md R7), and on
// Linux, where packets go to the kernel past the shell, there is nobody to drop them —
// without the block IPv6 would go past the tunnel. The local IPv6 network stays open.
type IPv6Blocker interface {
	// BlockIPv6 sets whether to close IPv6 outward at the next Enable.
	BlockIPv6(block bool)
}

// Reserver is an optional part of a datapath: an implementation that has to take
// something machine-wide before connecting (tun: the adapter). The caller calls Reserve
// before the first attempt and holds the reservation the whole run: a refusal here is a
// clear error before any connection, not a failed attempt that the supervisor would take
// for a reason to reconnect.
type Reserver interface {
	Reserve() (release func(), err error)
}

// Persistent is an optional part of a datapath: an implementation that lives the whole
// dnstt-cli connect, not one session. For tun it is the adapter: there is no point in
// recreating it on every reconnection, and Windows would also make "Network 2",
// "Network 3".
//
// Start of such an implementation is called for every session and reconfigures it
// instead of refusing with "already started".
type Persistent interface {
	// Suspend removes what sends system traffic into the tunnel — routes, system DNS and
	// the DNS leak protection — and leaves the rest. The next successful Start brings
	// them back. With the kill-switch on, the caller closes system traffic instead.
	// Harmless before Start and when repeated.
	Suspend() error
}

// Failing is an optional part of a datapath: an implementation that can break for good in
// the middle of a run. For tun on Windows it is the adapter that vanished by itself: it
// was disabled or removed in the system, the network stack was reset, the driver was
// updated. Reconnection does not cure that: the tunnel would lead nowhere while the run
// counted itself as up.
type Failing interface {
	// Failed delivers the reason once, when the datapath broke; until then it is silent.
	// One channel for the whole run.
	Failed() <-chan error
}
