package winsys

import (
	"net/netip"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// The subscription sees a route change and names its interface. The route goes to
// TEST-NET-2 (198.51.100.0/24, RFC 5737); nobody goes there, so the test does not affect
// the machine's network. Routes can be added only with administrator rights; without them
// the test is skipped.
func TestWatchNetworkSeesRouteChange(t *testing.T) {
	iface, ok, err := DefaultInterface(false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Skip("no IPv4 default route")
	}

	seen := make(chan uint64, 64)
	stop, err := WatchNetwork(func(luid uint64) {
		select {
		case seen <- luid:
		default:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	luid := winipcfg.LUID(iface.LUID)
	prefix := netip.MustParsePrefix("198.51.100.0/24")
	if err := luid.AddRoute(prefix, netip.IPv4Unspecified(), 9999); err != nil {
		if err == windows.ERROR_ACCESS_DENIED {
			t.Skipf("cannot add a route (administrator rights required): %v", err)
		}
		t.Fatalf("AddRoute: %v", err)
	}
	removed := false
	defer func() {
		if !removed {
			_ = luid.DeleteRoute(prefix, netip.IPv4Unspecified())
		}
	}()

	expect := func(what string) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case got := <-seen:
				if got == iface.LUID {
					return
				}
			case <-deadline:
				t.Fatalf("%s: no event with the LUID of %s arrived", what, iface.Alias)
			}
		}
	}
	expect("route added")

	if err := luid.DeleteRoute(prefix, netip.IPv4Unspecified()); err != nil {
		t.Fatalf("DeleteRoute: %v", err)
	}
	removed = true
	expect("route removed")

	// No events after stop.
	stop()
	for len(seen) > 0 {
		<-seen
	}
	if err := luid.AddRoute(prefix, netip.IPv4Unspecified(), 9999); err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	removed = false
	select {
	case got := <-seen:
		t.Fatalf("event after stop: LUID %d", got)
	case <-time.After(300 * time.Millisecond):
	}
}
