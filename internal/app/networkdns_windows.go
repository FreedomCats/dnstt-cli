//go:build windows

package app

import (
	"net/netip"

	"github.com/FreedomCats/dnstt-cli/internal/winsys"
)

func SystemNetworkDNS() ([]netip.Addr, error) {
	return winsys.NetworkDNS(0)
}
