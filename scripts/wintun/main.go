// Command wintun downloads wintun.dll of a fixed version, verifies it and puts it next to
// dnstt-cli.exe: the archive hash, and on Windows the Authenticode signature (WireGuard
// LLC). Details are in the scripts/internal/wintun package.
//
//	go run ./scripts/wintun
//	go run ./scripts/wintun -arch arm64 -dest C:\tools\dnstt-cli
//	go run ./scripts/wintun -archive %USERPROFILE%\Downloads\wintun-0.14.1.zip
//
// -arch is the architecture in Go terms: amd64 (default), arm64, 386, arm.
// -dest is where to put wintun.dll; by default build/out in the repository root, the
// build output directory where dnstt-cli.exe is meant to be built too.
// -archive is an already downloaded wintun-<version>.zip: then the command does not go to
// the network. The hash is checked anyway.
//
// Ported from PowerShell (scripts/fetch-wintun.ps1).
package main

import (
	"flag"
	"os"
	"path/filepath"

	"github.com/FreedomCats/dnstt-cli/scripts/internal/script"
	"github.com/FreedomCats/dnstt-cli/scripts/internal/wintun"
)

const self = "wintun"

func main() {
	var o wintun.Options
	flag.StringVar(&o.Arch, "arch", "amd64", "architecture: amd64, arm64, 386, arm")
	flag.StringVar(&o.Dest, "dest", "", "where to put wintun.dll (default build/out in the repository root)")
	flag.StringVar(&o.Archive, "archive", "", "an already downloaded wintun-"+wintun.Version+".zip")
	flag.Parse()
	if flag.NArg() > 0 {
		flag.Usage()
		os.Exit(2)
	}
	if o.Dest == "" {
		root, err := script.Root()
		if err != nil {
			script.Fail(self, err)
		}
		o.Dest = filepath.Join(root, "build", "out")
	}
	if err := wintun.Fetch(o); err != nil {
		script.Fail(self, err)
	}
}
