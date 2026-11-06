//go:build linux

package app

import (
	"net/netip"

	"github.com/FreedomCats/dnstt-cli/internal/linuxsys"
)

func SystemNetworkDNS() ([]netip.Addr, error) {
	return linuxsys.NetworkDNS()
}
