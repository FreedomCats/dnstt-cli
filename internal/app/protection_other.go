//go:build !linux

package app

import "errors"

// openSystemDNSGuard is the backstop of a system without the protection: the tun
// datapath is unsupported here, so this is reached only by a mistake. The Windows WFP
// session is stage 4.
func openSystemDNSGuard() (dnsGuardSession, error) {
	return nil, errors.New("DNS protection is not implemented on this system")
}
