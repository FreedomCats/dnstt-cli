//go:build !linux

package cli

import "io"

// runSecretHelper is the stub for platforms without the helper: the keyring bridge exists
// only on Linux, where Secret Service lives. The name stays an ordinary unknown command,
// and nothing is read from the streams before the command line is parsed.
func runSecretHelper(_ []string, _ io.Reader, _, _ io.Writer) (int, bool) {
	return 0, false
}
