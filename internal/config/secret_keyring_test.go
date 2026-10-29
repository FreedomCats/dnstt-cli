package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeKeyring is an in-memory keyring.
type fakeKeyring struct {
	values map[string]string
	labels map[string]string
	// unavailable: no keyring in the session; err: there is one, but the operation failed.
	unavailable bool
	err         error
	opened      int
}

func newFakeKeyring() *fakeKeyring {
	return &fakeKeyring{values: map[string]string{}, labels: map[string]string{}}
}

func (f *fakeKeyring) open() (keyring, error) {
	f.opened++
	if f.unavailable {
		return nil, errKeyringUnavailable
	}
	return f, nil
}

func (f *fakeKeyring) Put(id, label, value string) error {
	if f.err != nil {
		return f.err
	}
	f.values[id], f.labels[id] = value, label
	return nil
}

func (f *fakeKeyring) Get(id string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	v, ok := f.values[id]
	if !ok {
		return "", errKeyringNotFound
	}
	return v, nil
}

func (f *fakeKeyring) Delete(id string) error {
	if f.err != nil {
		return f.err
	}
	if _, ok := f.values[id]; !ok {
		return errKeyringNotFound
	}
	delete(f.values, id)
	return nil
}

func (f *fakeKeyring) Close() error { return nil }

// keyringStore is a store with a fake keyring over the file path.
func keyringStore(path string, k *fakeKeyring) *store {
	s := newSecretStoreAt(path)
	s.keyring = k.open
	return s
}

// With a keyring the value goes into it, and only the name and id stay in the file.
func TestKeyringPut(t *testing.T) {
	path := filepath.Join(t.TempDir(), SecretsFileName)
	k := newFakeKeyring()
	s := keyringStore(path, k)

	ref, err := s.Put("home.password", "exit-password")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	id, ok := strings.CutPrefix(ref, keyringScheme+":")
	if !ok {
		t.Fatalf("reference %q, expected scheme %s", ref, keyringScheme)
	}
	if k.values[id] != "exit-password" || k.labels[id] != "dnstt-cli home.password" {
		t.Errorf("in the keyring: %q with label %q", k.values[id], k.labels[id])
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "exit-password") || strings.Contains(string(raw), `"data"`) {
		t.Errorf("the value got into the file:\n%s", raw)
	}
	if !strings.Contains(string(raw), "home.password") {
		t.Errorf("the file lacks the entry name:\n%s", raw)
	}

	got, err := keyringStore(path, k).Get(ref)
	if err != nil || got != "exit-password" {
		t.Errorf("Get: %q, %v", got, err)
	}
	if SecretRefKind(ref) != "keyring (Secret Service)" {
		t.Errorf("SecretRefKind(%q) = %q", ref, SecretRefKind(ref))
	}
}

// A secret written to the file earlier is still read and moves to the keyring when
// rewritten — under the same id, with no copy left in the file.
func TestKeyringMovesOnRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), SecretsFileName)
	old, err := newSecretStoreAt(path).Put("home.ssh_key", "a1, b2")
	if err != nil {
		t.Fatal(err)
	}

	k := newFakeKeyring()
	s := keyringStore(path, k)
	if got, err := s.Get(old); err != nil || got != "a1, b2" {
		t.Fatalf("old reference with a keyring: %q, %v", got, err)
	}

	ref, err := s.Put("home.ssh_key", "c3")
	if err != nil {
		t.Fatal(err)
	}
	_, oldID, _ := strings.Cut(old, ":")
	if ref != keyringScheme+":"+oldID {
		t.Errorf("reference after rewrite %q, expected %s:%s", ref, keyringScheme, oldID)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), `"data"`) {
		t.Errorf("a value is left in the file:\n%s", raw)
	}

	// The profile was not written with the new reference yet: the old one leads to the keyring.
	if got, err := s.Get(old); err != nil || got != "c3" {
		t.Errorf("old reference after the move: %q, %v", got, err)
	}
}

// No keyring: the file, as before.
func TestKeyringUnavailableFallsBackToFile(t *testing.T) {
	k := newFakeKeyring()
	k.unavailable = true
	s := keyringStore(filepath.Join(t.TempDir(), SecretsFileName), k)

	ref, err := s.Put("home.password", "x")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ref, secretScheme+":") {
		t.Errorf("reference %q, expected scheme %s", ref, secretScheme)
	}
	if got, err := s.Get(ref); err != nil || got != "x" {
		t.Errorf("Get: %q, %v", got, err)
	}
	if !strings.HasPrefix(SecretRefKind(ref), SecretsFileName) {
		t.Errorf("SecretRefKind(%q) = %q", ref, SecretRefKind(ref))
	}
}

// There is a keyring but the write failed (the unlock prompt was dismissed): an error, not
// a quiet fallback to the file — the person refused, and that must not be bypassed
// silently.
func TestKeyringPutErrorIsNotFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), SecretsFileName)
	k := newFakeKeyring()
	k.err = errors.New("prompt dismissed without an answer")

	if _, err := keyringStore(path, k).Put("home.password", "x"); err == nil {
		t.Fatal("Put succeeded")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("store file created: %v", err)
	}
}

// The unlock prompt was dismissed: the next secrets of the same store do not open the
// keyring, so no second prompt appears for the same refusal. Other errors (no such entry)
// are not remembered.
func TestKeyringRefusalSticks(t *testing.T) {
	k := newFakeKeyring()
	s := keyringStore(filepath.Join(t.TempDir(), SecretsFileName), k)
	password, err := s.Put("home.password", "x")
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.Put("home.ssh_key", "y")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(keyringScheme + ":0123456789abcdef"); err == nil {
		t.Fatal("Get of a nonexistent entry succeeded")
	}

	k.err = fmt.Errorf("%w: prompt dismissed without an answer", ErrKeyringLocked)
	opened := k.opened
	if _, err := s.Get(password); !errors.Is(err, ErrKeyringLocked) {
		t.Fatalf("Get: %v", err)
	}
	// Meanwhile the keyring was unlocked, but the store still does not open it: the refusal
	// holds until the end of the command.
	k.err = nil
	if _, err := s.Get(key); !errors.Is(err, ErrKeyringLocked) {
		t.Errorf("second Get: %v", err)
	}
	if err := s.Delete(key); !errors.Is(err, ErrKeyringLocked) {
		t.Errorf("Delete: %v", err)
	}
	if k.opened != opened+1 {
		t.Errorf("keyring opened %d times after the refusal, expected 1", k.opened-opened)
	}
}

// Delete removes from the keyring and from the file; an entry gone from the keyring is not
// an error.
func TestKeyringDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), SecretsFileName)
	k := newFakeKeyring()
	s := keyringStore(path, k)

	ref, err := s.Put("home.password", "x")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ref); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(k.values) != 0 {
		t.Errorf("left in the keyring: %v", k.values)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "home.password") {
		t.Errorf("an entry is left in the file:\n%s", raw)
	}
	if err := s.Delete(ref); err != nil {
		t.Errorf("repeated Delete: %v", err)
	}
}

// The entry was removed from the keyring by hand: Get explains instead of returning nothing.
func TestKeyringGetMissing(t *testing.T) {
	k := newFakeKeyring()
	s := keyringStore(filepath.Join(t.TempDir(), SecretsFileName), k)
	if _, err := s.Get(keyringScheme + ":0123456789abcdef"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("Get: %v", err)
	}
}

// A keyring reference where there is no keyring (Windows, macOS, tests) is a foreign scheme.
func TestKeyringRefWithoutKeyring(t *testing.T) {
	_, err := tempStore(t).Get(keyringScheme + ":0123456789abcdef")
	if err == nil || !strings.Contains(err.Error(), keyringScheme) {
		t.Errorf("Get: %v", err)
	}
}
