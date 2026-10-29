//go:build linux

package secretservice

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FreedomCats/dnstt-cli/internal/app"
	"github.com/FreedomCats/dnstt-cli/internal/sudo"
)

// Round trip: write, read, overwrite, delete.
func TestRoundTrip(t *testing.T) {
	f, c := newFakeService(t)

	if err := c.Put("0123abcd", "dnstt-cli home.password", "password"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if got, err := c.Get("0123abcd"); err != nil || got != "password" {
		t.Fatalf("Get: %q, %v", got, err)
	}
	if err := c.Put("0123abcd", "dnstt-cli home.password", "new"); err != nil {
		t.Fatalf("Put over: %v", err)
	}
	if got, _ := c.Get("0123abcd"); got != "new" {
		t.Errorf("after overwrite: %q", got)
	}
	f.with(func() {
		if len(f.items) != 1 {
			t.Errorf("%d items, expected one: overwriting must not multiply them", len(f.items))
		}
		for _, item := range f.items {
			if item.attrs[attrApp] != app.Name || item.label != "dnstt-cli home.password" {
				t.Errorf("item: label %q, attributes %v", item.label, item.attrs)
			}
		}
		if f.algorithm != "plain" {
			t.Errorf("session opened with algorithm %q", f.algorithm)
		}
	})

	if err := c.Delete("0123abcd"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := c.Get("0123abcd"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after delete: %v, expected ErrNotFound", err)
	}
	if err := c.Delete("0123abcd"); !errors.Is(err, ErrNotFound) {
		t.Errorf("repeated Delete: %v, expected ErrNotFound", err)
	}
}

// A locked keyring is unlocked through its prompt, and the person is told what is
// expected of them.
func TestUnlockPrompt(t *testing.T) {
	f, c := newFakeService(t)
	if err := c.Put("id1", "label", "value"); err != nil {
		t.Fatal(err)
	}
	f.with(func() { f.locked = true })
	var notice strings.Builder
	c.notify = &notice

	got, err := c.Get("id1")
	if err != nil || got != "value" {
		t.Fatalf("Get from a locked keyring: %q, %v", got, err)
	}
	f.with(func() {
		if f.prompts != 1 {
			t.Errorf("%d unlock prompts, expected one", f.prompts)
		}
	})
	if !strings.Contains(notice.String(), "locked") {
		t.Errorf("the person was not told about the prompt: %q", notice.String())
	}
}

// The prompt was dismissed without an answer: an error, not an empty value.
func TestUnlockDismissed(t *testing.T) {
	f, c := newFakeService(t)
	if err := c.Put("id1", "label", "value"); err != nil {
		t.Fatal(err)
	}
	f.with(func() { f.locked, f.dismiss = true, true })

	if _, err := c.Get("id1"); !errors.Is(err, ErrLocked) || !strings.Contains(err.Error(), "dismissed") {
		t.Errorf("Get: %v, expected ErrLocked \"prompt dismissed\"", err)
	}
	if err := c.Put("id2", "label", "x"); err == nil {
		t.Error("Put into a locked keyring succeeded")
	}
}

// The prompt does not answer (it cannot be seen): a timeout error, the prompt is removed.
func TestUnlockTimeout(t *testing.T) {
	f, c := newFakeService(t)
	if err := c.Put("id1", "label", "value"); err != nil {
		t.Fatal(err)
	}
	f.with(func() { f.locked, f.silent = true, true })
	c.timeout = 200 * time.Millisecond

	if _, err := c.Get("id1"); !errors.Is(err, ErrLocked) || !strings.Contains(err.Error(), "did not answer") {
		t.Errorf("Get: %v, expected ErrLocked on timeout", err)
	}
	f.with(func() {
		if f.dismissed != 1 {
			t.Errorf("prompt not removed: Dismiss called %d times", f.dismissed)
		}
	})
}

// No default collection means effectively no keyring: secrets will go to a file.
func TestNoDefaultCollection(t *testing.T) {
	f, c := newFakeService(t)
	f.with(func() { f.noDefault = true })
	if err := c.Put("id1", "label", "x"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Put: %v, expected ErrUnavailable", err)
	}
}

// The bus exists but the service is not on it: ErrUnavailable. It must not be started
// through D-Bus activation: without graphics it would ask for a password into the void.
func TestServiceNotRunning(t *testing.T) {
	addr := startBus(t)
	_, err := dial(func(key string) string {
		if key == "DBUS_SESSION_BUS_ADDRESS" {
			return addr
		}
		return ""
	}, io.Discard)
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("dial: %v, expected ErrUnavailable", err)
	}
}

// Without a session bus (SSH, a server): ErrUnavailable, not a bus of our own.
func TestNoSessionBus(t *testing.T) {
	_, err := dial(func(key string) string {
		if key == "XDG_RUNTIME_DIR" {
			return t.TempDir() // the directory exists, the bus socket in it does not
		}
		return ""
	}, io.Discard)
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("dial: %v, expected ErrUnavailable", err)
	}
}

func TestBusAddress(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{"XDG_RUNTIME_DIR": dir}
	getenv := func(key string) string { return env[key] }

	if got := busAddress(getenv); got != "" {
		t.Errorf("without a socket: %q", got)
	}
	bus := filepath.Join(dir, "bus")
	if err := writeEmpty(bus); err != nil {
		t.Fatal(err)
	}
	if got, want := busAddress(getenv), "unix:path="+bus; got != want {
		t.Errorf("socket in the session runtime directory: %q, expected %q", got, want)
	}
	env["DBUS_SESSION_BUS_ADDRESS"] = "unix:path=/elsewhere"
	if got := busAddress(getenv); got != "unix:path=/elsewhere" {
		t.Errorf("with DBUS_SESSION_BUS_ADDRESS: %q", got)
	}
}

// helper under sudo: the request goes to the stdin of a process run as the invoking user,
// the secret value does not get into its arguments; keyring errors arrive unchanged.
func TestHelper(t *testing.T) {
	mem := &memKeyring{values: map[string]string{}}
	u := &sudo.User{Name: "user", Uid: 4242, Gid: 4343, Groups: []uint32{4343}, HomeDir: "/home/user"}
	var cmds []*exec.Cmd
	h := &helper{user: u, exe: "/usr/bin/dnstt-cli", stderr: io.Discard, run: func(cmd *exec.Cmd) error {
		cmds = append(cmds, cmd)
		var req request
		raw, err := io.ReadAll(cmd.Stdin)
		if err != nil {
			return err
		}
		if err := jsonUnmarshal(raw, &req); err != nil {
			return err
		}
		answer(req, cmd.Stdout, cmd.Stderr, func() (Keyring, error) { return mem, nil })
		return nil
	}}

	if err := h.Put("id1", "label", "secret"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if got, err := h.Get("id1"); err != nil || got != "secret" {
		t.Fatalf("Get: %q, %v", got, err)
	}
	if err := h.Delete("id1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := h.Get("id1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after delete: %v, expected ErrNotFound", err)
	}

	cmd := cmds[0]
	if strings.Join(cmd.Args, " ") != "/usr/bin/dnstt-cli "+HelperCommand {
		t.Errorf("arguments: %q", cmd.Args)
	}
	cred := cmd.SysProcAttr.Credential
	if cred == nil || cred.Uid != 4242 || cred.Gid != 4343 {
		t.Errorf("not run as the invoking user: %+v", cred)
	}
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, "SUDO_") {
			t.Errorf("%s left in the environment", kv)
		}
	}

	mem.err = ErrUnavailable
	if err := h.Put("id2", "label", "x"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Put without a keyring: %v, expected ErrUnavailable", err)
	}
	mem.err = fmt.Errorf("%w: prompt dismissed without an answer", ErrLocked)
	if _, err := h.Get("id1"); !errors.Is(err, ErrLocked) || err.Error() != mem.err.Error() {
		t.Errorf("Get with a locked keyring: %v, expected ErrLocked with the same text", err)
	}
}

// The helper process failed to start: an error, not "item not found".
func TestHelperRunFails(t *testing.T) {
	h := &helper{user: &sudo.User{Name: "user"}, exe: "dnstt-cli", stderr: io.Discard,
		run: func(*exec.Cmd) error { return errors.New("exec: permission denied") }}
	_, err := h.Get("id1")
	if err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrUnavailable) {
		t.Errorf("Get: %v", err)
	}
}

// RunHelper is not silent on a malformed request.
func TestRunHelperBadRequest(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := RunHelper(strings.NewReader("not json"), &out, &errOut); code == 0 {
		t.Error("exit code 0 on a malformed request")
	}
	if errOut.Len() == 0 {
		t.Error("error not printed")
	}
}
