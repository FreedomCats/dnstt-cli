package winsys

import (
	"net/netip"
	"slices"

	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// NetworkDNS returns the DNS servers the network gave the system: those of the interface
// of the default route (IPv4, then IPv6 if that is another interface), the ISP's resolver
// set by DHCP. The bootstrap resolver asks them for the names of resolvers
// (architecture.md R5): in a network that lets through only some resolvers, the ISP's is
// the one that answers. exclude is the LUID of our own adapter (0 — none), as in
// DefaultInterface.
//
// Windows fills an interface without DNS with the site-local fec0:0:0:ffff::1–3, which
// answer nowhere; they are left out, and so are loopback addresses.
func NetworkDNS(exclude uint64) ([]netip.Addr, error) {
	var out []netip.Addr
	var seen []uint64
	for _, ipv6 := range []bool{false, true} {
		iface, ok, err := DefaultInterface(ipv6, exclude)
		if err != nil {
			return nil, err
		}
		if !ok || slices.Contains(seen, iface.LUID) {
			continue
		}
		seen = append(seen, iface.LUID)
		servers, err := winipcfg.LUID(iface.LUID).DNS()
		if err != nil {
			return nil, err
		}
		for _, a := range servers {
			a = a.Unmap()
			if a.IsLoopback() || a.IsUnspecified() || siteLocalDNS.Contains(a) || slices.Contains(out, a) {
				continue
			}
			out = append(out, a)
		}
	}
	return out, nil
}

// siteLocalDNS holds the placeholder DNS servers of Windows, fec0:0:0:ffff::1–3.
var siteLocalDNS = netip.MustParsePrefix("fec0:0:0:ffff::/64")
