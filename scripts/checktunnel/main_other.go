//go:build !windows && !linux

// The state snapshot is for Windows and Linux only.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "checktunnel: Windows and Linux only")
	os.Exit(2)
}
