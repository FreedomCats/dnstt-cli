//go:build !linux

package datapath

import (
	"errors"
	"net/netip"

	"github.com/FreedomCats/dnstt-cli/internal/exit"
)

// errTunUnsupported is the refusal on systems where the system adapter is not
// implemented yet. The Windows stage replaces this file; the build tag is the seam.
var errTunUnsupported = errors.New("tun is not implemented on this system yet; use -datapath socks")

// tunUnsupported gives Requires the reason before the caller checks privileges.
func tunUnsupported() error { return errTunUnsupported }

// openTunAdapter creates nothing.
func openTunAdapter(string, exit.Dialer, netip.Addr, func(string, ...any)) (*tunAdapter, error) {
	return nil, errTunUnsupported
}

// acquireTunLock refuses at once: "not supported" is found out before connecting, not
// after it.
func acquireTunLock() (func(), error) { return nil, errTunUnsupported }
