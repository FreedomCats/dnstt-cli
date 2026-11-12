package cli

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
)

func runCLI(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	e := &env{stdout: &out, stderr: &errOut, stdin: strings.NewReader(""), build: Build{Version: "dev", Commit: "unknown"}}
	code = run(context.Background(), e, args)
	return code, out.String(), errOut.String()
}

func TestNoArgumentsPrintsCommands(t *testing.T) {
	code, out, _ := runCLI(t)
	if code != exitOK {
		t.Fatalf("exit code %d, want %d", code, exitOK)
	}
	for _, cmd := range commands() {
		if !strings.Contains(out, cmd.name) {
			t.Errorf("command list does not name %q:\n%s", cmd.name, out)
		}
	}
}

func TestVersion(t *testing.T) {
	code, out, _ := runCLI(t, "version")
	if code != exitOK {
		t.Fatalf("exit code %d, want %d", code, exitOK)
	}
	if !strings.HasPrefix(out, "dnstt-cli dev (unknown)\n") {
		t.Errorf("version output:\n%s", out)
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"nosuchcommand"},
		{"version", "extra"},
		{"version", "-nosuchflag"},
		{"help", "nosuchcommand"},
	} {
		code, _, errOut := runCLI(t, args...)
		if code != exitUsage {
			t.Errorf("%q: exit code %d, want %d; stderr:\n%s", args, code, exitUsage, errOut)
		}
	}
}

func TestHelpForCommand(t *testing.T) {
	code, out, _ := runCLI(t, "help", "version")
	if code != exitOK {
		t.Fatalf("exit code %d, want %d", code, exitOK)
	}
	if !strings.HasPrefix(out, "Usage: dnstt-cli version\n") {
		t.Errorf("help output:\n%s", out)
	}
}

// The menu is a command of its own right after connect: the list keeps its layout and gains
// one line, and help for it works through the same lookup.
func TestMenuInTheCommandList(t *testing.T) {
	names := make([]string, 0, len(commands()))
	for _, cmd := range commands() {
		names = append(names, cmd.name)
	}
	i := slices.Index(names, "menu")
	if i < 0 {
		t.Fatalf("the command list has no menu: %v", names)
	}
	if want := slices.Index(names, "connect") + 1; i != want {
		t.Fatalf("menu is at %d, want right after connect (%d): %v", i, want, names)
	}

	code, out, _ := runCLI(t, "help", "menu")
	if code != exitOK {
		t.Fatalf("exit code %d, want %d", code, exitOK)
	}
	if !strings.HasPrefix(out, "Usage: dnstt-cli menu [-connect profile]\n") {
		t.Fatalf("help output:\n%s", out)
	}
	if !strings.Contains(out, "connect to the profile at once") {
		t.Fatalf("help does not describe -connect:\n%s", out)
	}
}
