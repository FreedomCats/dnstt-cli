package linuxsys

import (
	"net"
	"testing"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func prefix(t *testing.T, s string) *net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// fakeLinks is the interface state for a fake table: index → name; the ones in down are
// down.
func fakeLinks(down ...int) func(int) (string, bool) {
	names := map[int]string{1: "lo", 2: "eth0", 3: "wlan0", 4: "tun0", 7: "dnstt-cli"}
	return func(index int) (string, bool) {
		name, ok := names[index]
		if !ok {
			return "", false
		}
		for _, d := range down {
			if d == index {
				return name, false
			}
		}
		return name, true
	}
}

func TestPickDefault(t *testing.T) {
	route := func(dst *net.IPNet, link, priority int) netlink.Route {
		return netlink.Route{Dst: dst, LinkIndex: link, Priority: priority, Type: unix.RTN_UNICAST}
	}
	all4 := prefix(t, "0.0.0.0/0")
	half1, half2 := prefix(t, "0.0.0.0/1"), prefix(t, "128.0.0.0/1")
	corp := prefix(t, "10.0.0.0/15")

	for _, tc := range []struct {
		name    string
		routes  []netlink.Route
		exclude uint64
		down    []int
		want    string // name, or "" for none
	}{
		{"one route, Dst nil", []netlink.Route{route(nil, 2, 20100)}, 0, nil, "eth0"},
		{"one route, Dst /0", []netlink.Route{route(all4, 2, 20100)}, 0, nil, "eth0"},
		{"lowest metric", []netlink.Route{route(nil, 2, 20100), route(nil, 3, 600)}, 0, nil, "wlan0"},
		{"tie goes to the first", []netlink.Route{route(nil, 3, 100), route(nil, 2, 100)}, 0, nil, "wlan0"},
		{"down interface is skipped", []netlink.Route{route(nil, 3, 600), route(nil, 2, 20100)}, 0, []int{3}, "eth0"},
		{"vanished interface", []netlink.Route{route(nil, 9, 1), route(nil, 2, 20100)}, 0, nil, "eth0"},
		{"tunnel halves do not count", []netlink.Route{route(half1, 7, 0), route(half2, 7, 0), route(nil, 2, 20100)}, 0, nil, "eth0"},
		{"own adapter is excluded", []netlink.Route{route(nil, 7, 0), route(nil, 2, 20100)}, 7, nil, "eth0"},
		{"corporate networks do not count", []netlink.Route{route(corp, 4, 50), route(nil, 2, 20100)}, 0, nil, "eth0"},
		{"no default route", []netlink.Route{route(corp, 4, 50)}, 0, nil, ""},
		{"the only one is down", []netlink.Route{route(nil, 2, 100)}, 0, []int{2}, ""},
		{"default unreachable", []netlink.Route{
			{LinkIndex: 1, Priority: 1, Type: unix.RTN_UNREACHABLE}, route(nil, 2, 20100)}, 0, nil, "eth0"},
		{"several gateways", []netlink.Route{{Priority: 5, Type: unix.RTN_UNICAST,
			MultiPath: []*netlink.NexthopInfo{{LinkIndex: 3}, {LinkIndex: 2}}}}, 0, []int{3}, "eth0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := pickDefault(tc.routes, tc.exclude, fakeLinks(tc.down...))
			if tc.want == "" {
				if ok {
					t.Fatalf("chose %+v, want none", got)
				}
				return
			}
			if !ok || got.Name != tc.want {
				t.Fatalf("chose %+v (ok=%v), want %s", got, ok, tc.want)
			}
		})
	}
}

// A live choice on this machine: needs no privileges and changes nothing. No default
// route is an outcome too, not an error.
func TestDefaultInterfaceLive(t *testing.T) {
	for _, ipv6 := range []bool{false, true} {
		iface, ok, err := DefaultInterface(ipv6, 0)
		if err != nil {
			t.Fatalf("ipv6=%v: %v", ipv6, err)
		}
		if !ok {
			t.Logf("ipv6=%v: no default route", ipv6)
			continue
		}
		t.Logf("ipv6=%v: %s (index %d)", ipv6, iface.Name, iface.Index)
		if got, err := net.InterfaceByIndex(int(iface.Index)); err != nil || got.Name != iface.Name {
			t.Errorf("ipv6=%v: index %d does not match name %q: %v %v", ipv6, iface.Index, iface.Name, got, err)
		}
		// The chosen interface, once excluded, is not chosen again.
		if again, ok, _ := DefaultInterface(ipv6, uint64(iface.Index)); ok && again.Index == iface.Index {
			t.Errorf("ipv6=%v: excluded %d chosen again", ipv6, iface.Index)
		}
	}
}
