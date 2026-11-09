//go:build linux

package app

import (
	"github.com/FreedomCats/dnstt-cli/internal/linuxsys"
	"github.com/FreedomCats/dnstt-cli/internal/netguard"
)

// openSystemDNSGuard opens the Linux protection session: the nftables table inet
// dnstt-cli with the owner flag. The mark is the one netguard.Control puts on the core's
// sockets, so DNS protection and the kill-switch let them through under sudo.
func openSystemDNSGuard() (dnsGuardSession, error) {
	guard, err := linuxsys.OpenDNSGuard(netguard.SocketMark)
	if err != nil {
		return nil, err
	}
	return guard, nil
}
