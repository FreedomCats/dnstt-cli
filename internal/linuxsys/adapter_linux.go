package linuxsys

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// minMTU6 is the smallest MTU at which the kernel keeps IPv6 on an interface
// (RFC 8200). With a smaller MTU the kernel removes IPv6 from the interface entirely.
const minMTU6 = 1280

// LinkKind returns the type of interface name as ip -d link calls it ("tuntap",
// "wireguard", "dummy"); ok = false means there is no such interface.
func LinkKind(name string) (kind string, ok bool, err error) {
	link, err := netlink.LinkByName(name)
	if err != nil {
		var notFound netlink.LinkNotFoundError
		if errors.As(err, &notFound) {
			return "", false, nil
		}
		return "", false, err
	}
	return link.Type(), true, nil
}

// LinkIndex returns the index of interface name.
func LinkIndex(name string) (int, error) {
	link, err := netlink.LinkByName(name)
	if err != nil {
		return 0, err
	}
	return link.Attrs().Index, nil
}

// LinkName returns the name of the interface with index index; ok = false means there
// is none.
func LinkName(index int) (name string, ok bool, err error) {
	link, err := netlink.LinkByIndex(index)
	if err != nil {
		var notFound netlink.LinkNotFoundError
		if errors.As(err, &notFound) {
			return "", false, nil
		}
		return "", false, err
	}
	return link.Attrs().Name, true, nil
}

// ConfigureAdapter sets the MTU and addresses of our own adapter and brings it up.
// Counterpart of winsys.ConfigureAdapter: every address goes in as a full-length prefix
// (/32, /128); the caller installs the routes. Calling it again reconfigures: addresses
// of global scope that are not in addrs are removed (on our own adapter they are all
// ours), the automatic fe80:: is left alone.
//
// An MTU below 1280 leaves no IPv6 on the interface: IPv6 addresses are then not set,
// which is reported through logf. IPv6 through the tunnel would not work anyway, and
// bringing the adapter up does not fail because of it (as on Windows).
func ConfigureAdapter(index int, addrs []netip.Addr, mtu int, logf func(string, ...any)) error {
	link, err := netlink.LinkByIndex(index)
	if err != nil {
		return fmt.Errorf("interface %d: %w", index, err)
	}
	name := link.Attrs().Name
	if mtu > 0 && link.Attrs().MTU != mtu {
		if err := netlink.LinkSetMTU(link, mtu); err != nil {
			return fmt.Errorf("MTU %d on %s: %w", mtu, name, err)
		}
	}

	want := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		a = a.Unmap()
		if a.Is6() && mtu > 0 && mtu < minMTU6 {
			logf("[TUN] address %v not set: with MTU %d the kernel does not keep IPv6 on the interface", a, mtu)
			continue
		}
		want = append(want, a)
	}

	have, err := netlink.AddrList(link, netlink.FAMILY_ALL)
	if err != nil {
		return fmt.Errorf("addresses of %s: %w", name, err)
	}
	for _, h := range have {
		if h.Scope != unix.RT_SCOPE_UNIVERSE {
			continue
		}
		a, ok := netip.AddrFromSlice(h.IP)
		if ok && slices.Contains(want, a.Unmap()) {
			continue
		}
		if err := netlink.AddrDel(link, &h); err != nil {
			return fmt.Errorf("removing address %v from %s: %w", h.IPNet, name, err)
		}
	}

	for _, a := range want {
		addr := &netlink.Addr{IPNet: &net.IPNet{
			IP:   a.AsSlice(),
			Mask: net.CIDRMask(a.BitLen(), a.BitLen()),
		}}
		if a.Is6() {
			// Duplicate address detection is pointless on a point-to-point link, and
			// while it runs the address is "tentative" and sockets cannot bind to it.
			addr.Flags = unix.IFA_F_NODAD
		}
		if err := netlink.AddrReplace(link, addr); err != nil {
			return fmt.Errorf("address %v on %s: %w", a, name, err)
		}
	}

	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("bringing up %s: %w", name, err)
	}
	return nil
}
