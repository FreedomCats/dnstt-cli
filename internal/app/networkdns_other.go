//go:build !linux && !windows

package app

import (
	"errors"
	"net/netip"
)

func SystemNetworkDNS() ([]netip.Addr, error) {
	return nil, errors.New("network DNS is not supported on this platform")
}
