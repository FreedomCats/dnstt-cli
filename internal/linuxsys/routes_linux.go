package linuxsys

import (
	"errors"
	"fmt"
	"net"
	"net/netip"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// SetTunnelRoutes installs routes for prefixes via interface index in table main: no
// gateway, scope link, as wg-quick does. Calling it again is harmless (RouteReplace).
// The routes hang on the interface and go away with it. Counterpart of
// winsys.SetTunnelRoutes.
func SetTunnelRoutes(index int, prefixes []netip.Prefix) error {
	for _, p := range prefixes {
		if err := netlink.RouteReplace(tunnelRoute(index, p)); err != nil {
			return fmt.Errorf("route %v: %w", p, err)
		}
	}
	return nil
}

// ClearTunnelRoutes removes routes for prefixes from interface index; a route that is
// already gone is not an error. It removes everything it can and returns the first
// error.
func ClearTunnelRoutes(index int, prefixes []netip.Prefix) error {
	var first error
	for _, p := range prefixes {
		err := netlink.RouteDel(tunnelRoute(index, p))
		if err != nil && !errors.Is(err, unix.ESRCH) && first == nil {
			first = fmt.Errorf("removing route %v: %w", p, err)
		}
	}
	return first
}

func tunnelRoute(index int, p netip.Prefix) *netlink.Route {
	p = p.Masked()
	return &netlink.Route{
		LinkIndex: index,
		Dst:       &net.IPNet{IP: p.Addr().AsSlice(), Mask: net.CIDRMask(p.Bits(), p.Addr().BitLen())},
		Scope:     netlink.SCOPE_LINK,
		Table:     unix.RT_TABLE_MAIN,
	}
}
