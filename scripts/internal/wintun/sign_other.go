//go:build !windows

package wintun

import "github.com/FreedomCats/dnstt-cli/scripts/internal/script"

// verifySignature does not check the signature outside Windows: WinVerifyTrust exists only
// there. By this time the archive hash has already been checked.
func verifySignature(string) error {
	script.Say("Signature not checked: WinVerifyTrust exists only on Windows (the archive hash was checked).")
	return nil
}
