//go:build !linux

package config

// There is no keyring known to dnstt-cli here: on Windows secrets are encrypted by DPAPI,
// on macOS there is only the file (Keychain — once there is something to test it on).
var openKeyring func() (keyring, error)
