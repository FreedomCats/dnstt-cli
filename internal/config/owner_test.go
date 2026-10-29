package config

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/FreedomCats/dnstt-cli/internal/sudo"
)

// fakeSudo replaces the sudo invoker, and chown with a recorder of calls: a real chown
// requires root.
func fakeSudo(t *testing.T, owner *sudo.User, fail error) *[]string {
	t.Helper()
	var calls []string
	oldInvoker, oldChown := invoker, chown
	t.Cleanup(func() { invoker, chown = oldInvoker, oldChown })

	invoker = func() (*sudo.User, error) { return owner, nil }
	chown = func(path string, uid, gid int) error {
		if uid != 4242 || gid != 4343 {
			t.Errorf("chown %s: %d:%d, expected 4242:4343", path, uid, gid)
		}
		calls = append(calls, path)
		return fail
	}
	return &calls
}

var sudoUser = &sudo.User{Name: "user", Uid: 4242, Gid: 4343, HomeDir: "/home/user"}

// Under sudo everything dnstt-cli created is given to the invoker: the missing directories
// top down and files before the rename. What existed before is left alone.
func TestWriteUnderSudoGivesFilesAway(t *testing.T) {
	calls := fakeSudo(t, sudoUser, nil)
	root := t.TempDir()
	dir := filepath.Join(root, "a", "b")
	store := NewSecretStoreFor(filepath.Join(dir, FileName))

	if _, err := store.Put("home/password", "secret"); err != nil {
		t.Fatal(err)
	}
	secrets := filepath.Join(dir, SecretsFileName)
	want := []string{filepath.Join(root, "a"), dir, secrets + ".tmp"}
	if !slices.Equal(*calls, want) {
		t.Errorf("chown: %q, expected %q", *calls, want)
	}

	*calls = nil
	if _, err := store.Put("home/ssh_key", "another"); err != nil {
		t.Fatal(err)
	}
	want = []string{secrets + ".tmp"}
	if !slices.Equal(*calls, want) {
		t.Errorf("chown on the second write: %q, expected %q", *calls, want)
	}
}

func TestWriteWithoutSudoKeepsOwner(t *testing.T) {
	calls := fakeSudo(t, nil, nil)
	path := filepath.Join(t.TempDir(), "a", FileName)

	if _, err := NewSecretStoreFor(path).Put("home/password", "secret"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Errorf("chown called without sudo: %q", *calls)
	}
}

// Giving the file away failed: the write did not happen, and no temporary file of root's
// is left behind.
func TestWriteUnderSudoChownFails(t *testing.T) {
	fakeSudo(t, sudoUser, errors.New("not permitted"))
	dir := t.TempDir()

	if _, err := NewSecretStoreFor(filepath.Join(dir, FileName)).Put("home/password", "secret"); err == nil {
		t.Fatal("chown error swallowed")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("files left in the directory after the error: %v", entries)
	}
}
