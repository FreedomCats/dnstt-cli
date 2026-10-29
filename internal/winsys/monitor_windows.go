package winsys

import (
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// WatchNetwork subscribes to changes of routes (NotifyRouteChange2) and IP interfaces
// (NotifyIpInterfaceChange) of both families and calls notify with the LUID of the
// interface the change touched.
//
// The events themselves decide nothing: switching Wi-Fi → Ethernet produces dozens of
// them, and changes of our own adapter (the tunnel routes) do not change the choice of
// the physical interface. The caller (netguard.Guard.Watch) collapses the burst and
// decides whether the network has changed.
//
// notify is called from winipcfg goroutines, possibly concurrently. stop removes the
// subscription and waits for calls of notify already in progress.
func WatchNetwork(notify func(luid uint64)) (stop func(), err error) {
	routes, err := winipcfg.RegisterRouteChangeCallback(func(_ winipcfg.MibNotificationType, route *winipcfg.MibIPforwardRow2) {
		notify(uint64(route.InterfaceLUID))
	})
	if err != nil {
		return nil, err
	}
	ifaces, err := winipcfg.RegisterInterfaceChangeCallback(func(_ winipcfg.MibNotificationType, iface *winipcfg.MibIPInterfaceRow) {
		notify(uint64(iface.InterfaceLUID))
	})
	if err != nil {
		_ = routes.Unregister()
		return nil, err
	}
	return func() {
		_ = ifaces.Unregister()
		_ = routes.Unregister()
	}, nil
}
