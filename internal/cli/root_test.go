package cli

import (
	"bytes"
	"context"
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
