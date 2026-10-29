package linuxsys

import (
	"net"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// Interface is the chosen physical interface.
type Interface struct {
	Index uint32 // index for netguard (SO_BINDTOIFINDEX)
	Name  string // name as in ip link: "eth0", "wlan0"
}

// DefaultInterface chooses the interface of the default route (0.0.0.0/0 or ::/0) of
// table main for the family: the one with the lowest route metric, only interfaces that
// are UP, never interface exclude (the index of our own adapter; 0 means nothing to
// exclude). The tunnel routes are two /1 halves and do not take part in the choice, and
// our own adapter is also excluded explicitly, so the choice works with the tunnel up
// too. Counterpart of winsys.DefaultInterface.
//
// ok = false means the family has no default route (for example, a network without
// IPv6). That is not an error.
func DefaultInterface(ipv6 bool, exclude uint64) (iface Interface, ok bool, err error) {
	family := netlink.FAMILY_V4
	if ipv6 {
		family = netlink.FAMILY_V6
	}
	routes, err := netlink.RouteListFiltered(family,
		&netlink.Route{Table: unix.RT_TABLE_MAIN}, netlink.RT_FILTER_TABLE)
	if err != nil {
		return Interface{}, false, err
	}
	iface, ok = pickDefault(routes, exclude, linkState)
	return iface, ok, nil
}

// linkState returns the name of the interface and whether it qualifies for the choice:
// administratively up and not lost its link. An interface that is gone (it disappeared
// between the route list and this call) does not qualify.
func linkState(index int) (name string, up bool) {
	link, err := netlink.LinkByIndex(index)
	if err != nil {
		return "", false
	}
	attrs := link.Attrs()
	// OperUnknown is reported by interfaces with no notion of carrier (tun, some VPNs):
	// for them only the UP flag counts.
	oper := attrs.OperState == netlink.OperUp || attrs.OperState == netlink.OperUnknown
	return attrs.Name, attrs.Flags&net.FlagUp != 0 && oper
}

// pickDefault makes the choice from a given table: kept apart from netlink so that it
// can be tested on a fake table. On a metric tie the first one in the table wins.
func pickDefault(routes []netlink.Route, exclude uint64, link func(index int) (name string, up bool)) (Interface, bool) {
	var (
		best   Interface
		ok     bool
		lowest int
	)
	for _, r := range routes {
		if !isDefault(r) || r.Type != unix.RTN_UNICAST {
			// A default unreachable or blackhole route is no way out.
			continue
		}
		for _, index := range nexthops(r) {
			if index == 0 || uint64(index) == exclude {
				continue
			}
			if ok && r.Priority >= lowest {
				continue
			}
			name, up := link(index)
			if !up {
				continue
			}
			best, ok, lowest = Interface{Index: uint32(index), Name: name}, true, r.Priority
		}
	}
	return best, ok
}

// isDefault reports a route covering the whole address space of the family. netlink
// returns it with Dst = nil or with a zero prefix length, depending on version and
// family.
func isDefault(r netlink.Route) bool {
	if r.Dst == nil {
		return true
	}
	ones, _ := r.Dst.Mask.Size()
	return ones == 0
}

// nexthops returns the interfaces of a route: one for an ordinary route, several for a
// route with several gateways (ip route add default nexthop … nexthop …).
func nexthops(r netlink.Route) []int {
	if len(r.MultiPath) == 0 {
		return []int{r.LinkIndex}
	}
	indexes := make([]int, 0, len(r.MultiPath))
	for _, nh := range r.MultiPath {
		indexes = append(indexes, nh.LinkIndex)
	}
	return indexes
}
