package app

import (
	"errors"
	"fmt"

	"github.com/FreedomCats/dnstt-cli/internal/netguard"
)

// NetguardOptions are the settings of the netguard wiring of a datapath that takes all
// system traffic.
type NetguardOptions struct {
	// Guard is the guard whose Control is already given to the session; required.
	Guard *netguard.Guard
	// Exclude is the id of the datapath's own adapter at the time of the question
	// (datapath.Adapter.AdapterID): netguard must not choose it as the physical
	// interface. nil means 0: nothing to exclude.
	Exclude func() uint64
	// Restart ends the session and redials after a network change; required.
	Restart func(error)
	// Logf receives the check results and the network changes; nil — silent.
	Logf func(format string, args ...any)
}

// netguardHooks are the steps of EnableNetguard, replaceable in the tests: the real ones
// ask /proc, firewalld, the routing table and the kernel.
type netguardHooks struct {
	checkFirewall    func() error
	selectInterfaces func(exclude uint64) (string, error)
	checkReversePath func() error
	watch            func(netguard.WatchOptions) (func(), error)
}

// EnableNetguard checks the system filters that would drop the core's replies, chooses the
// physical interface without the datapath's adapter and watches the network for changes.
// The order is the contract: the firewalld decision is taken before the interface is
// chosen, and the chosen interface is checked before the watcher starts. An error at any
// step leaves no watcher behind.
//
// The returned stop stops the watcher; the caller defers it.
func EnableNetguard(opts NetguardOptions) (func(), error) {
	return enableNetguard(opts, netguardHooks{})
}

func enableNetguard(opts NetguardOptions, hooks netguardHooks) (func(), error) {
	if opts.Guard == nil {
		return nil, errors.New("netguard: no guard")
	}
	if opts.Restart == nil {
		return nil, errors.New("netguard: no restart")
	}
	exclude := opts.Exclude
	if exclude == nil {
		exclude = func() uint64 { return 0 }
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if hooks.checkFirewall == nil {
		hooks.checkFirewall = opts.Guard.CheckFirewall
	}
	if hooks.selectInterfaces == nil {
		hooks.selectInterfaces = opts.Guard.SelectInterfaces
	}
	if hooks.checkReversePath == nil {
		hooks.checkReversePath = opts.Guard.CheckReversePath
	}
	if hooks.watch == nil {
		hooks.watch = opts.Guard.Watch
	}

	if err := hooks.checkFirewall(); err != nil {
		return nil, err
	}
	choice, err := hooks.selectInterfaces(exclude())
	if err != nil {
		return nil, err
	}
	logf("[NETGUARD] physical interface: %s", choice)
	if err := hooks.checkReversePath(); err != nil {
		return nil, err
	}
	return hooks.watch(netguard.WatchOptions{
		Exclude: exclude,
		Changed: func(now, before netguard.Choice) {
			// No network at all: there is nothing to reconnect to; the next change,
			// when a network appears, is the one to follow.
			if now.Empty() {
				logf("[NETGUARD] no network: no default route, waiting for the next change")
				return
			}
			opts.Restart(fmt.Errorf("network changed: %s", now))
		},
	})
}
