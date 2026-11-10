//go:build linux

package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreedomCats/dnstt-cli/internal/secretservice"
)

// noSessionBus points the helper at a session bus that does not exist. The request then
// gets an "unavailable" response without reaching the keyring of whoever runs the tests.
func noSessionBus(t *testing.T) {
	t.Helper()
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(t.TempDir(), "bus"))
}

// runMain calls the entry point with its own streams: the helper must see the same ones
// as the rest of the CLI, and the process-wide os.Stdin, os.Stdout and os.Stderr stay
// untouched.
func runMain(t *testing.T, args []string, stdin string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = mainWith(args, Build{Version: "dev", Commit: "unknown"}, strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

// The hidden command reaches secretservice.RunHelper through the ordinary entry point: the
// request is read from the injected stdin, the response goes to the injected stdout, and
// the exit code of the helper comes back. With no session bus the response is still valid
// JSON: the failure of the operation travels in it, not in the exit code.
func TestSecretHelperDispatched(t *testing.T) {
	noSessionBus(t)
	code, stdout, stderr := runMain(t, []string{secretservice.HelperCommand}, `{"op":"get","id":"home"}`)

	if code != exitOK {
		t.Fatalf("exit code %d, want %d; stderr:\n%s", code, exitOK, stderr)
	}
	var resp struct {
		Err         string `json:"err"`
		Unavailable bool   `json:"unavailable"`
	}
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("stdout is not a JSON response: %v\n%s", err, stdout)
	}
	if !resp.Unavailable {
		t.Errorf("response does not say unavailable:\n%s", stdout)
	}
	if resp.Err == "" {
		t.Errorf("response has no error text:\n%s", stdout)
	}
	for what, text := range map[string]string{"stdout": stdout, "stderr": stderr} {
		for _, unwanted := range []string{"unknown command", "Usage:"} {
			if strings.Contains(text, unwanted) {
				t.Errorf("%s contains %q, the helper was not dispatched:\n%s", what, unwanted, text)
			}
		}
	}
}

// A malformed request keeps the helper's own exit code and error message. This pins the
// order too: if the dispatch happened after run, the name would be looked up as a normal
// command and stderr would say "unknown command" instead.
func TestSecretHelperMalformedRequest(t *testing.T) {
	noSessionBus(t)
	code, stdout, stderr := runMain(t, []string{secretservice.HelperCommand}, "{")

	if code != 2 {
		t.Fatalf("exit code %d, want 2; stderr:\n%s", code, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout is not empty on a malformed request:\n%s", stdout)
	}
	for _, want := range []string{secretservice.HelperCommand, "request"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not contain %q:\n%s", want, stderr)
		}
	}
	for _, unwanted := range []string{"unknown command", "Usage:"} {
		if strings.Contains(stderr, unwanted) {
			t.Errorf("stderr contains %q, the helper was not dispatched:\n%s", unwanted, stderr)
		}
	}
}

// Anything but the helper's own first argument is left to the ordinary CLI: nothing is
// read from the streams and nothing is written to them.
func TestRunSecretHelperNotHandled(t *testing.T) {
	for _, args := range [][]string{nil, {}, {"version"}} {
		var out, errOut bytes.Buffer
		code, ok := runSecretHelper(args, strings.NewReader(`{"op":"get","id":"home"}`), &out, &errOut)
		if ok {
			t.Errorf("%q: handled as the helper", args)
		}
		if code != 0 {
			t.Errorf("%q: exit code %d, want 0", args, code)
		}
		if out.Len() != 0 || errOut.Len() != 0 {
			t.Errorf("%q: wrote stdout %q, stderr %q", args, out.String(), errOut.String())
		}
	}
}

// Without arguments the entry point still prints the command list, and the hidden command
// is not in it.
func TestMainWithoutArgumentsPrintsCommands(t *testing.T) {
	code, stdout, stderr := runMain(t, nil, "")

	if code != exitOK {
		t.Fatalf("exit code %d, want %d; stderr:\n%s", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, "Commands:") {
		t.Errorf("stdout is not the command list:\n%s", stdout)
	}
	if strings.Contains(stdout, secretservice.HelperCommand) {
		t.Errorf("the command list names the hidden helper:\n%s", stdout)
	}
}
