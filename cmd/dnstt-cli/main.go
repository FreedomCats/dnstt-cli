// Command dnstt-cli is a desktop console client for dnstt.
//
// Only build information and the exit code live here: command parsing and the commands
// themselves are in internal/cli.
package main

import (
	"os"

	"github.com/FreedomCats/dnstt-cli/internal/cli"
)

// version and commit are set at build time through -ldflags.
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], cli.Build{Version: version, Commit: commit}))
}
