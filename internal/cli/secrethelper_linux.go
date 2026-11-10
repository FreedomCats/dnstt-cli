//go:build linux

package cli

import (
	"io"

	"github.com/FreedomCats/dnstt-cli/internal/secretservice"
)

// runSecretHelper dispatches HelperCommand: the hidden command that performs one keyring
// operation on behalf of the user who invoked sudo (see internal/secretservice).
//
// It is called before run, and no flag is parsed before it: the helper is not a user
// command. It has no place in the usage or help text, and its input is a JSON request on
// stdin, not flags. Only the first argument takes part in the dispatch; the rest is left
// to the helper, so a request can never turn into an ordinary command.
func runSecretHelper(args []string, in io.Reader, out, errW io.Writer) (int, bool) {
	if len(args) == 0 || args[0] != secretservice.HelperCommand {
		return 0, false
	}
	return secretservice.RunHelper(in, out, errW), true
}
