package winsys

import (
	"errors"
	"fmt"
	"net/netip"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// AdapterGUID is the constant GUID of the tunnel adapter. With it Windows recognises the
// adapter on every run and does not create "Network 2", "Network 3" (WintunCreateAdapter
// documentation: the GUID determines the NLA record). It is our own, not inherited from
// the project the shell was carried over from: the adapters of two programs on one
// machine must not coincide. scripts/checktunnel finds the adapter by it as well.
var AdapterGUID = windows.GUID{
	Data1: 0xc46fad99, Data2: 0x13d1, Data3: 0x49bc,
	Data4: [8]byte{0xba, 0x19, 0xbd, 0x86, 0x16, 0xb5, 0x6f, 0x78},
}

// ConfigureAdapter sets the addresses and MTU of the tunnel adapter. The addresses are set
// with a prefix length of the whole address (/32, /128): the adapter has single addresses
// without a subnet mask, and the routes into the tunnel are set separately
// (SetTunnelRoutes).
//
// The interface is configured in both families even if there are no IPv6 addresses: the
// IPv6 routes go through the tunnel too (they block the machine's IPv6), and its MTU must
// be the same. If IPv6 is off on the adapter, this is not an error: the family is
// skipped.
//
// Router discovery and DAD are also switched off on the interface: the adapter is
// point-to-point, it has no neighbours, and DAD delays the address becoming ready by a
// second. wireguard-windows does the same (tunnel/addressconfig.go, configureInterface,
// MIT, v1.1.1).
//
// logf gets a line if an address had to be removed from another, disconnected interface
// (see cleanupAddressesOnDisconnectedInterfaces).
func ConfigureAdapter(luid uint64, addrs []netip.Addr, mtu int, logf func(string, ...any)) error {
	l := winipcfg.LUID(luid)
	for _, family := range []winipcfg.AddressFamily{windows.AF_INET, windows.AF_INET6} {
		var prefixes []netip.Prefix
		for _, a := range addrs {
			if a.Is4() == (family == windows.AF_INET) {
				prefixes = append(prefixes, netip.PrefixFrom(a, a.BitLen()))
			}
		}
		if len(prefixes) > 0 {
			err := l.SetIPAddressesForFamily(family, prefixes)
			if err == windows.ERROR_OBJECT_ALREADY_EXISTS {
				cleanupAddressesOnDisconnectedInterfaces(family, prefixes, logf)
				err = l.SetIPAddressesForFamily(family, prefixes)
			}
			if err != nil {
				return fmt.Errorf("setting addresses %v on the adapter: %w", prefixes, err)
			}
		}

		ipif, err := l.IPInterface(family)
		if len(prefixes) == 0 && family == windows.AF_INET6 && errors.Is(err, windows.ERROR_NOT_FOUND) {
			continue
		}
		if err != nil {
			return fmt.Errorf("%s interface parameters: %w", familyName(family), err)
		}
		ipif.RouterDiscoveryBehavior = winipcfg.RouterDiscoveryDisabled
		ipif.DadTransmits = 0
		ipif.ManagedAddressConfigurationSupported = false
		ipif.OtherStatefulConfigurationSupported = false
		// IPv6 does not accept an MTU below 1280 (RFC 8200), while the config allows a
		// smaller one. In that case IPv6 does not work through the tunnel anyway, it is
		// only blocked, so the IPv6 MTU stays as it is instead of failing the setup.
		if mtu > 0 && (family == windows.AF_INET || mtu >= 1280) {
			ipif.NLMTU = uint32(mtu)
		}
		if err := ipif.Set(); err != nil {
			return fmt.Errorf("%s interface MTU: %w", familyName(family), err)
		}
	}
	return nil
}

// cleanupAddressesOnDisconnectedInterfaces removes our addresses from interfaces that are
// not in the Up state: the address may have been left on a disconnected adapter, for
// example by a regular WireGuard with the same config, and then Windows does not let us
// set it (ERROR_OBJECT_ALREADY_EXISTS).
//
// A port of the function of the same name from golang.zx2c4.com/wireguard/windows
// tunnel/addressconfig.go (MIT, v1.1.1); log.Printf is replaced by logf.
func cleanupAddressesOnDisconnectedInterfaces(family winipcfg.AddressFamily, addresses []netip.Prefix, logf func(string, ...any)) {
	if len(addresses) == 0 {
		return
	}
	addrHash := make(map[netip.Addr]bool, len(addresses))
	for i := range addresses {
		addrHash[addresses[i].Addr()] = true
	}
	interfaces, err := winipcfg.GetAdaptersAddresses(family, winipcfg.GAAFlagDefault)
	if err != nil {
		return
	}
	for _, iface := range interfaces {
		if iface.OperStatus == winipcfg.IfOperStatusUp {
			continue
		}
		for address := iface.FirstUnicastAddress; address != nil; address = address.Next {
			// Unmap is our addition: net.IP can be 16 bytes long for IPv4 too.
			if ip, _ := netip.AddrFromSlice(address.Address.IP()); addrHash[ip.Unmap()] {
				ip = ip.Unmap()
				prefix := netip.PrefixFrom(ip, int(address.OnLinkPrefixLength))
				logf("[TUN] removing stale address %s from interface %q", prefix, iface.FriendlyName())
				iface.LUID.DeleteIPAddress(prefix)
			}
		}
	}
}

func familyName(family winipcfg.AddressFamily) string {
	if family == windows.AF_INET6 {
		return "IPv6"
	}
	return "IPv4"
}

// FlushAdapter removes the routes, addresses and DNS of both families from the tunnel
// adapter before the adapter is closed. wireguard-windows does this
// (tunnel/interfacewatcher.go, Destroy, MIT, v1.1.1): the Windows stack copes badly with
// destroying an interface that has active routes. It also keeps the interface's registry
// key (Tcpip\Parameters\Interfaces\{GUID}) from holding the tunnel's DNS after a stop.
//
// Errors are collected but stop nothing: the adapter is closed right after anyway. A
// family that is off on the adapter is absent, which is not an error.
func FlushAdapter(luid uint64) error {
	l := winipcfg.LUID(luid)
	var errs []error
	for _, family := range []winipcfg.AddressFamily{windows.AF_INET, windows.AF_INET6} {
		for _, flush := range []func(winipcfg.AddressFamily) error{l.FlushRoutes, l.FlushIPAddresses, l.FlushDNS} {
			if err := flush(family); err != nil && !errors.Is(err, windows.ERROR_NOT_FOUND) {
				errs = append(errs, fmt.Errorf("%s: %w", familyName(family), err))
			}
		}
	}
	return errors.Join(errs...)
}
