package netguard

import (
	"errors"
	"fmt"
	"net"

	"golang.org/x/sys/unix"

	"github.com/FreedomCats/dnstt-cli/internal/linuxsys"
)

const supported = true

// bindPerFamily: on Linux there is one interface binding per socket (see apply).
const bindPerFamily = false

// The binding options, for error messages.
const (
	option4 = "SO_BINDTOIFINDEX"
	option6 = "SO_BINDTOIFINDEX"
)

// The platform readers of the system filters, variables for the tests as
// datapath.detectSystemDNS is.
var (
	firewalldStrictIPv6 = linuxsys.FirewalldStrictIPv6
	reversePathFilter   = linuxsys.ReversePathFilter
)

// CheckFirewall asks whether firewalld runs with the strict IPv6 reverse path filter: its
// rule drops IPv6 replies to the core's sockets on the physical interface just as a strict
// IPv4 rp_filter does. IPv6 is then denied to the core, which falls back to IPv4. It must
// run before SelectInterfaces and Watch: they read NoIPv6.
func (g *Guard) CheckFirewall() error {
	strict, err := firewalldStrictIPv6()
	if err != nil {
		return fmt.Errorf("netguard: firewalld: %w", err)
	}
	if !strict {
		return nil
	}
	g.NoIPv6 = true
	if g.Logf != nil {
		g.Logf("[NETGUARD] firewalld filters IPv6 replies strictly: the tunnel's sockets are IPv4 only")
	}
	return nil
}

// CheckReversePath checks the effective IPv4 rp_filter of the chosen interface
// (linuxsys.ReversePathFilter): the strict filter drops replies to the core's sockets,
// which are bound to the physical interface while the return route leads into the tunnel.
// It runs after SelectInterfaces, whose choice it checks.
func (g *Guard) CheckReversePath() error {
	c := g.Choice()
	if c.Index4 == 0 || c.Alias4 == "" {
		return nil
	}
	value, err := reversePathFilter(c.Alias4)
	if err != nil {
		return fmt.Errorf("netguard: rp_filter of %s: %w", c.Alias4, err)
	}
	if value != linuxsys.RPFilterStrict {
		return nil
	}
	return fmt.Errorf("netguard: strict IPv4 rp_filter on %s drops the tunnel's replies: "+
		"loosen it with sudo sysctl -w net.ipv4.conf.all.rp_filter=2 net.ipv4.conf.%s.rp_filter=2, "+
		"or connect with -datapath socks", c.Alias4, c.Alias4)
}

// setter binds the socket to the interface and, if Mark is set, marks it.
//
// The binding beats the /1 routes into the tunnel and needs no privileges. Without a
// route for the family on that interface the socket gets ENETUNREACH instead of going
// into the tunnel.
func (g *Guard) setter() setter {
	mark := g.Mark
	return func(fd uintptr, _ bool, index uint32) error {
		if err := bindToIndex(int(fd), index); err != nil {
			return err
		}
		if mark == 0 {
			return nil
		}
		if err := unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, int(mark)); err != nil {
			return fmt.Errorf("SO_MARK=%#x: %w", mark, err)
		}
		return nil
	}
}

// bindToIndex uses SO_BINDTOIFINDEX (kernel 5.0 and newer), and on an older kernel
// SO_BINDTODEVICE by the interface name.
func bindToIndex(fd int, index uint32) error {
	err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_BINDTOIFINDEX, int(index))
	if !errors.Is(err, unix.ENOPROTOOPT) {
		return err
	}
	iface, ierr := net.InterfaceByIndex(int(index))
	if ierr != nil {
		return fmt.Errorf("the kernel has no SO_BINDTOIFINDEX, and the interface name is unknown: %w", ierr)
	}
	return unix.BindToDevice(fd, iface.Name)
}

// choose chooses the interface (linuxsys.DefaultInterface) without setting it: Watch
// first compares it with the previous one. exclude is the index of our own adapter.
func choose(exclude uint64) (Choice, error) {
	v4, ok4, err := linuxsys.DefaultInterface(false, exclude)
	if err != nil {
		return Choice{}, fmt.Errorf("netguard: choosing the IPv4 interface: %w", err)
	}
	v6, ok6, err := linuxsys.DefaultInterface(true, exclude)
	if err != nil {
		return Choice{}, fmt.Errorf("netguard: choosing the IPv6 interface: %w", err)
	}
	var c Choice
	if ok4 {
		c.Index4, c.Alias4 = v4.Index, v4.Name
	}
	if ok6 {
		c.Index6, c.Alias6 = v6.Index, v6.Name
	}
	return c, nil
}

// subscribe gives netlink events about routes, interfaces and addresses
// (linuxsys.WatchNetwork).
func subscribe(notify func(id uint64)) (func(), error) {
	return linuxsys.WatchNetwork(notify)
}

// v6Only reads IPV6_V6ONLY of the socket.
func v6Only(fd uintptr) (bool, error) {
	v, err := unix.GetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_V6ONLY)
	return v == 1, err
}
