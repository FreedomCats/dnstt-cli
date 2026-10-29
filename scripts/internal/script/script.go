// Package script holds what the service commands in scripts/ share: the repository root
// and colored output.
package script

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/FreedomCats/dnstt-cli/internal/app"
)

// Root is the root of the dnstt-cli module: the directory of the go.mod that go sees
// from the current directory. Commands are run as `go run ./scripts/<name>` from the
// root, so that is it.
func Root() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOMOD: %w", err)
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == os.DevNull {
		return "", errors.New("run from the " + app.Name + " module")
	}
	return filepath.Dir(gomod), nil
}

// Terminal colors.
const (
	Red    = "31"
	Green  = "32"
	Yellow = "33"
)

// Say prints a line to stdout.
func Say(a ...any) { fmt.Fprintln(os.Stdout, a...) }

// Sayf is Say with a format.
func Sayf(format string, a ...any) { fmt.Fprintf(os.Stdout, format+"\n", a...) }

// Color prints a line to stdout in color — only if it is a terminal.
func Color(color, line string) { colorLine(os.Stdout, color, line) }

// Fail prints the error in red to stderr and ends the command with exit code 1.
func Fail(name string, err error) {
	colorLine(os.Stderr, Red, name+": "+err.Error())
	os.Exit(1)
}

func colorLine(w *os.File, color, line string) {
	if term.IsTerminal(int(w.Fd())) && enableColor(w) {
		fmt.Fprintf(w, "\x1b[%sm%s\x1b[0m\n", color, line)
		return
	}
	fmt.Fprintln(w, line)
}
