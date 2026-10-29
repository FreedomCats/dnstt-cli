package winsys

import (
	"net/netip"
	"testing"

	"golang.org/x/sys/windows"
)

// The choice is checked against GetBestInterfaceEx: without our own tunnel the best route
// to an external address is the default route.
func TestDefaultInterfaceMatchesBestRoute(t *testing.T) {
	for _, tc := range []struct {
		name string
		ipv6 bool
		dst  netip.Addr
	}{
		{"IPv4", false, netip.MustParseAddr("1.1.1.1")},
		{"IPv6", true, netip.MustParseAddr("2606:4700:4700::1111")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			iface, ok, err := DefaultInterface(tc.ipv6, 0)
			if err != nil {
				t.Fatal(err)
			}
			best, bestErr := bestInterface(tc.dst)
			t.Logf("DefaultInterface: ok=%v %+v; GetBestInterfaceEx: %d, %v", ok, iface, best, bestErr)
			if !ok {
				if bestErr == nil {
					t.Fatalf("no default route, but GetBestInterfaceEx found interface %d", best)
				}
				return
			}
			if bestErr != nil {
				t.Fatalf("chose %+v, but GetBestInterfaceEx: %v", iface, bestErr)
			}
			if iface.Index != best {
				t.Fatalf("chose interface %d (%s), but GetBestInterfaceEx gives %d", iface.Index, iface.Alias, best)
			}
		})
	}
}

func TestDefaultInterfaceExclude(t *testing.T) {
	iface, ok, err := DefaultInterface(false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Skip("no IPv4 default route")
	}
	again, ok, err := DefaultInterface(false, iface.LUID)
	if err != nil {
		t.Fatal(err)
	}
	if ok && again.LUID == iface.LUID {
		t.Fatalf("excluded interface %+v chosen again", iface)
	}
}

func bestInterface(dst netip.Addr) (uint32, error) {
	var sa windows.Sockaddr
	if dst.Is4() {
		sa = &windows.SockaddrInet4{Addr: dst.As4()}
	} else {
		sa = &windows.SockaddrInet6{Addr: dst.As16()}
	}
	var index uint32
	err := windows.GetBestInterfaceEx(sa, &index)
	return index, err
}
