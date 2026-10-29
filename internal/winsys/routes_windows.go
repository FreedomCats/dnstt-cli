package winsys

import (
	"errors"
	"fmt"
	"net/netip"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// SetTunnelRoutes sets the routes through the tunnel adapter and makes its interfaces
// preferred: metric 0 instead of the automatic one, as wireguard-windows does for a full
// tunnel (tunnel/addressconfig.go, configureInterface, MIT, v1.1.1). The interface metric
// decides whose resolvers the Windows DNS client goes to first.
//
// The routes have no gateway (on-link): the adapter is point-to-point. A route that is
// already there is not an error, so a repeated call is harmless. The default route is not
// touched: the /1 halves win over it by prefix length, and they can be removed without
// touching anyone else's routes.
//
// If the adapter has no IPv6 (the protocol is unbound from it or off in the system), the
// IPv6 routes are skipped. routed6 tells whether the IPv6 routes were set: without them
// the machine's IPv6 bypasses the adapter, and closing it is the job of DNS protection.
func SetTunnelRoutes(luid uint64, routes []netip.Prefix) (routed6 bool, err error) {
	l := winipcfg.LUID(luid)
	for _, family := range routeFamilies(routes) {
		ipif, err := l.IPInterface(family)
		if family == windows.AF_INET6 && errors.Is(err, windows.ERROR_NOT_FOUND) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("%s interface parameters: %w", familyName(family), err)
		}
		for _, r := range routes {
			if r.Addr().Is4() != (family == windows.AF_INET) {
				continue
			}
			err := l.AddRoute(r.Masked(), unspecified(family), 0)
			if err != nil && !errors.Is(err, windows.ERROR_OBJECT_ALREADY_EXISTS) {
				return false, fmt.Errorf("route %s: %w", r, err)
			}
		}
		ipif.UseAutomaticMetric = false
		ipif.Metric = 0
		if err := ipif.Set(); err != nil {
			return false, fmt.Errorf("%s interface metric: %w", familyName(family), err)
		}
		if family == windows.AF_INET6 {
			routed6 = true
		}
	}
	return routed6, nil
}

// ClearTunnelRoutes removes what SetTunnelRoutes set and returns the automatic metric to
// the interfaces. The adapter stays. A route that is already gone is not an error, so a
// repeated call is harmless.
//
// There is no need to call it when closing the adapter: the routes hang on its LUID and
// go away with it.
func ClearTunnelRoutes(luid uint64, routes []netip.Prefix) error {
	l := winipcfg.LUID(luid)
	var errs []error
	for _, family := range routeFamilies(routes) {
		ipif, err := l.IPInterface(family)
		if family == windows.AF_INET6 && errors.Is(err, windows.ERROR_NOT_FOUND) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s interface parameters: %w", familyName(family), err))
			continue
		}
		for _, r := range routes {
			if r.Addr().Is4() != (family == windows.AF_INET) {
				continue
			}
			err := l.DeleteRoute(r.Masked(), unspecified(family))
			if err != nil && !errors.Is(err, windows.ERROR_NOT_FOUND) {
				errs = append(errs, fmt.Errorf("route %s: %w", r, err))
			}
		}
		ipif.UseAutomaticMetric = true
		if err := ipif.Set(); err != nil {
			errs = append(errs, fmt.Errorf("%s interface metric: %w", familyName(family), err))
		}
	}
	return errors.Join(errs...)
}

// routeFamilies returns the families that have routes in the list, IPv4 first.
func routeFamilies(routes []netip.Prefix) []winipcfg.AddressFamily {
	var has4, has6 bool
	for _, r := range routes {
		if r.Addr().Is4() {
			has4 = true
		} else {
			has6 = true
		}
	}
	var families []winipcfg.AddressFamily
	if has4 {
		families = append(families, windows.AF_INET)
	}
	if has6 {
		families = append(families, windows.AF_INET6)
	}
	return families
}

func unspecified(family winipcfg.AddressFamily) netip.Addr {
	if family == windows.AF_INET6 {
		return netip.IPv6Unspecified()
	}
	return netip.IPv4Unspecified()
}
