package winsys

import (
	"errors"
	"fmt"
	"net/netip"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// SetAdapterDNS sets the resolvers of the tunnel adapter: the Windows DNS client goes to
// them first while the adapter has the lowest metric (SetTunnelRoutes). The previous list
// of resolvers is replaced, not extended, so a repeated call is harmless.
//
// The setting lives in the registry under the interface GUID and outlives the adapter,
// but without the interface it has no effect; the adapter's GUID is constant, and the
// next run overwrites it.
//
// IPv6 resolvers are not set when the adapter has no IPv6: that family is skipped.
func SetAdapterDNS(luid uint64, servers []netip.Addr) error {
	l := winipcfg.LUID(luid)
	for _, family := range []winipcfg.AddressFamily{windows.AF_INET, windows.AF_INET6} {
		if family == windows.AF_INET6 {
			if _, err := l.IPInterface(family); errors.Is(err, windows.ERROR_NOT_FOUND) {
				continue
			}
		}
		// SetDNS picks the addresses of its own family by itself.
		if err := l.SetDNS(family, servers, nil); err != nil {
			return fmt.Errorf("setting %s DNS on the adapter: %w", familyName(family), err)
		}
	}
	return nil
}
