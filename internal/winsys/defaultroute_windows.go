package winsys

import (
	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// Interface is the chosen physical interface.
type Interface struct {
	Index uint32 // index for netguard (IP_UNICAST_IF)
	LUID  uint64
	Alias string // name as in Get-NetAdapter: "Ethernet 3", "Wi-Fi"
}

// DefaultInterface chooses the interface of the default route (0.0.0.0/0 or ::/0) for the
// family: the one with the lowest sum of route and interface metrics, only interfaces in
// the Up state, excluding the interface exclude (the LUID of our own adapter; 0 means
// nothing to exclude). The tunnel routes are two /1 halves, so they do not take part in
// the choice, and our own adapter is also excluded explicitly, so the choice works with
// the tunnel up as well.
//
// ok = false means there is no default route for the family (for example, a network
// without IPv6). This is not an error.
//
// A port of findDefaultLUID from golang.zx2c4.com/wireguard/windows
// tunnel/mtumonitor.go (MIT, v1.1.1).
func DefaultInterface(ipv6 bool, exclude uint64) (iface Interface, ok bool, err error) {
	family := winipcfg.AddressFamily(windows.AF_INET)
	if ipv6 {
		family = windows.AF_INET6
	}
	rows, err := winipcfg.GetIPForwardTable2(family)
	if err != nil {
		return Interface{}, false, err
	}
	lowestMetric := ^uint64(0)
	for i := range rows {
		if rows[i].DestinationPrefix.PrefixLength != 0 || uint64(rows[i].InterfaceLUID) == exclude {
			continue
		}
		ifrow, err := rows[i].InterfaceLUID.Interface()
		if err != nil || ifrow.OperStatus != winipcfg.IfOperStatusUp {
			continue
		}
		ipif, err := rows[i].InterfaceLUID.IPInterface(family)
		if err != nil {
			continue
		}
		combinedMetric := uint64(rows[i].Metric) + uint64(ipif.Metric)
		if combinedMetric < lowestMetric {
			lowestMetric = combinedMetric
			iface = Interface{
				Index: rows[i].InterfaceIndex,
				LUID:  uint64(rows[i].InterfaceLUID),
				Alias: ifrow.Alias(),
			}
			ok = true
		}
	}
	return iface, ok, nil
}
