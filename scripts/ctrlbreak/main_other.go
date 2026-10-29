//go:build !windows

// Ctrl+Break by PID is Windows only: on Linux kill -INT does the same.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "ctrlbreak: Windows only; on Linux use kill -INT <pid>")
	os.Exit(2)
}
