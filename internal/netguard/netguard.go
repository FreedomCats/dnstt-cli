// Package netguard keeps the tunnel core's own sockets out of the tunnel.
//
// When the datapath takes all of the system's traffic (tun), the core's sockets would
// follow the main routing table into the same tunnel, and the tunnel would depend on
// itself. Guard.Control goes into Config.Control of the in-process core (core/client),
// which puts it on every net.Dialer and net.ListenConfig it creates; it binds the socket
// to the physical interface: on Windows with IP_UNICAST_IF / IPV6_UNICAST_IF, on Linux
// with SO_BINDTOIFINDEX. On other systems Control does nothing yet.
package netguard

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
)

// Guard holds the physical interface indexes for IPv4 and IPv6. The zero value is ready
// to use but lets no socket through: no interface is chosen yet. The indexes change on
// the fly (network change): new sockets take the new index, already open ones live out
// their life with the old one.
type Guard struct {
	index4 atomic.Uint32
	index6 atomic.Uint32

	// mu and choice are the last interface choice (SelectInterfaces, Watch): Watch
	// compares a new one with it to tell whether the network has changed.
	mu     sync.Mutex
	choice Choice

	// Logf, if set, gets a line for every bound socket:
	// "[NETGUARD] tcp4 203.0.113.80:443 → interface 3". The main tool for debugging a
	// loop; the CLI sets it with -v. Set before the first socket. The line has no host
	// name: the address reaches Control already resolved.
	Logf func(format string, args ...any)

	// Mark, if not 0, is put on sockets with SO_MARK (Linux only): by it DNS protection
	// and the kill-switch recognise dnstt-cli's sockets and let them through, including
	// the core's queries to port 53 in UDP mode. The mark needs CAP_NET_ADMIN; without it
	// the socket is not created (EPERM), so the CLI sets it only when DNS protection is
	// on. Set before the first socket. Not used on Windows: there DNS protection lets the
	// whole dnstt-cli process through.
	Mark uint32

	// NoIPv6 denies IPv6 to the core's sockets: the IPv6 interface choice is dropped, an
	// IPv6 socket is not created, and Go falls back to IPv4. Needed when the reverse path
	// filter (firewalld) would drop IPv6 replies on the physical interface. Set before
	// SelectInterfaces and Watch.
	NoIPv6 bool
}

// SocketMark is the Mark value for dnstt-cli's sockets. It is clear of the known users of
// marks: wg-quick (0xca6c), kube-proxy (0x4000, 0x8000), Tailscale (mask 0xff0000),
// Calico (mask 0xffff0000), Cilium (mask 0xf00) — and of the marks of qwdtt-cli (0x1074)
// and csqtt-cli (0x1075): all three programs may be installed on one machine. dnstt-cli
// sets up no routing by the mark — it is only for recognition.
const SocketMark uint32 = 0x1076

// SelectInterfaces chooses the physical interface of the default route for IPv4 and IPv6
// and sets their indexes. exclude is our own adapter (the datapath's AdapterID: the LUID
// on Windows, the index on Linux); 0 means there is nothing to exclude. Returns the
// choice in words for output.
//
// No network at all is not an error: the indexes are zeroed, and the core's sockets fail
// with a clear message instead of going into the tunnel. The only error is the system
// refusing to give out the routing table.
func (g *Guard) SelectInterfaces(exclude uint64) (string, error) {
	if !supported {
		return "not needed on this system", nil
	}
	c, err := choose(exclude)
	if err != nil {
		return "", err
	}
	g.commit(c)
	return c.String(), nil
}

// SetInterfaces sets the physical interface indexes. 0 means there is no interface for
// that family, and sockets of that family are not created.
func (g *Guard) SetInterfaces(index4, index6 uint32) {
	g.index4.Store(index4)
	g.index6.Store(index6)
}

// Interfaces returns the current indexes for IPv4 and IPv6.
func (g *Guard) Interfaces() (index4, index6 uint32) {
	return g.index4.Load(), g.index6.Load()
}

// Choice is the physical interface choice for both families. Index 0 means the family
// has no default route.
type Choice struct {
	Index4, Index6 uint32
	// Alias4, Alias6 are the interface names for output ("Ethernet 3", "Wi-Fi",
	// "eth0").
	Alias4, Alias6 string
}

// Same reports the same choice: the indexes match. Names are not compared: renaming an
// interface does not change the network.
func (c Choice) Same(o Choice) bool {
	return c.Index4 == o.Index4 && c.Index6 == o.Index6
}

// Empty reports no network: neither family has a default route.
func (c Choice) Empty() bool { return c.Index4 == 0 && c.Index6 == 0 }

// String is the choice in words: "IPv4 — Ethernet 3 (index 3), IPv6 — no route".
func (c Choice) String() string {
	return "IPv4 — " + describe(c.Index4, c.Alias4) + ", IPv6 — " + describe(c.Index6, c.Alias6)
}

func describe(index uint32, alias string) string {
	if index == 0 {
		return "no route"
	}
	return fmt.Sprintf("%s (index %d)", alias, index)
}

// Choice is the last choice set by SelectInterfaces or Watch.
func (g *Guard) Choice() Choice {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.choice
}

// allowed is the choice without what the core's sockets must not get (NoIPv6).
func (g *Guard) allowed(c Choice) Choice {
	if g.NoIPv6 {
		c.Index6, c.Alias6 = 0, ""
	}
	return c
}

// commit sets the choice: the indexes for new sockets and the record for comparison.
// Returns the previous choice.
func (g *Guard) commit(c Choice) (before Choice) {
	c = g.allowed(c)
	g.mu.Lock()
	defer g.mu.Unlock()
	before, g.choice = g.choice, c
	g.SetInterfaces(c.Index4, c.Index6)
	return before
}

// Control is the hook for the Control field of net.Dialer and net.ListenConfig.
func (g *Guard) Control(network, address string, c syscall.RawConn) error {
	if !supported {
		return nil
	}
	return g.apply(network, address, c, g.setter())
}

// setter binds socket fd to interface index for the family. On Windows it is
// setUnicastIf, on Linux the binding and the mark, in tests a stub.
type setter func(fd uintptr, ipv6 bool, index uint32) error

// apply is the platform-independent part of Control: it decides which families to bind
// and checks that their index is known.
func (g *Guard) apply(network, address string, c syscall.RawConn, set setter) error {
	v4, v6, err := families(network, address)
	if err != nil {
		return err
	}
	if !v4 && !v6 {
		return nil
	}
	index4, index6 := g.Interfaces()

	// An IPv6 socket on the unspecified address may be dual-stack: Control gets "udp6" or
	// "tcp6" both for those networks, which Go opens with IPV6_V6ONLY, and for a
	// dual-stack "udp" or "tcp" socket. The option tells them apart; where it cannot be
	// read, the socket is taken as dual-stack. In the core such a socket is a per-query
	// UDP socket of UDP mode (P3): it is opened on ":0", "udp6" for an IPv6 resolver, so
	// Control does not see the destination. The other sockets of the core are dials, and
	// Control gets the destination address.
	if v4 && v6 {
		var only bool
		if err := c.Control(func(fd uintptr) { only, _ = v6Only(fd) }); err == nil && only {
			v4 = false
		}
	}

	// A dual-stack socket is bound to what is known: on a machine without IPv6 that is
	// IPv4 only.
	if v4 && v6 {
		if index4 == 0 && index6 == 0 {
			return noInterface("IPv4 and IPv6", network, address)
		}
		v4, v6 = index4 != 0, index6 != 0
		// On Linux there is one binding per socket, not per family. When the families
		// have different interfaces, the socket is bound to the IPv4 interface: IPv6
		// through it gets a route failure but does not go into the tunnel.
		if v4 && v6 && !bindPerFamily {
			v6 = false
		}
	}
	// The index is not known: the socket is not created. Silently letting it follow the
	// main table means a loop through the tunnel that nobody sees.
	if v4 && index4 == 0 {
		return noInterface("IPv4", network, address)
	}
	if v6 && index6 == 0 {
		return noInterface("IPv6", network, address)
	}

	var setErr error
	err = c.Control(func(fd uintptr) {
		if v4 {
			if setErr = set(fd, false, index4); setErr != nil {
				setErr = fmt.Errorf("netguard: %s=%d for %s %s: %w", option4, index4, network, address, setErr)
				return
			}
		}
		if v6 {
			if setErr = set(fd, true, index6); setErr != nil {
				setErr = fmt.Errorf("netguard: %s=%d for %s %s: %w", option6, index6, network, address, setErr)
			}
		}
	})
	if err != nil {
		return err
	}
	if setErr != nil {
		return setErr
	}
	if g.Logf != nil {
		switch {
		case v4 && v6:
			g.Logf("[NETGUARD] %s %s → interface %d (IPv4), %d (IPv6)", network, address, index4, index6)
		case v4:
			g.Logf("[NETGUARD] %s %s → interface %d", network, address, index4)
		default:
			g.Logf("[NETGUARD] %s %s → interface %d", network, address, index6)
		}
	}
	return nil
}

func noInterface(family, network, address string) error {
	return fmt.Errorf("netguard: no physical interface for %s (no network chosen or it has no %s), socket %s %s is not created",
		family, family, network, address)
}

// families decides which options to put on a socket. Loopback passes unchanged: this is
// a safety net, the hook is not put on listening sockets for local clients anyway.
//
// The family is given by the network's suffix: package net always passes tcp4, udp6 and
// so on to Control (net/sock_posix.go, ctrlNetwork); bare tcp/udp never occur there. The
// special case is a listening socket on an unspecified address (":0", "0.0.0.0:0",
// "[::]:0") with a *6 network: that is how a dual-stack socket looks, one that can also
// send to IPv4, so both options are set.
func families(network, address string) (v4, v6 bool, err error) {
	host, _, splitErr := net.SplitHostPort(address)
	if splitErr != nil {
		return false, false, fmt.Errorf("netguard: cannot parse address %q for %s: %w", address, network, splitErr)
	}
	addr, _ := netip.ParseAddr(host)
	if addr.IsValid() && addr.Unmap().IsLoopback() {
		return false, false, nil
	}
	unspecified := host == "" || addr.IsUnspecified()

	switch {
	case strings.HasSuffix(network, "4"):
		return true, false, nil
	case strings.HasSuffix(network, "6") && unspecified:
		return true, true, nil
	case strings.HasSuffix(network, "6"):
		return false, true, nil
	default:
		return false, false, fmt.Errorf("netguard: cannot tell the family of socket %s %s", network, address)
	}
}
