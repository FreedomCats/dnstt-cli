package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func tempStore(t *testing.T) *store {
	t.Helper()
	return newSecretStoreAt(filepath.Join(t.TempDir(), SecretsFileName))
}

// Round trip: put in, take out. The test runs on the current platform, so on Windows it
// goes through the real DPAPI, elsewhere through the file.
func TestSecretRoundTrip(t *testing.T) {
	s := tempStore(t)

	ref, err := s.Put("home.password", "exit-password")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if !strings.HasPrefix(ref, secretScheme+":") {
		t.Errorf("reference %q does not start with scheme %q", ref, secretScheme)
	}

	got, err := s.Get(ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "exit-password" {
		t.Errorf("got %q, expected %q", got, "exit-password")
	}
}

// A reference survives a restart: written by one store instance, read by another over the
// same file.
func TestSecretPersistsAcrossInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), SecretsFileName)

	ref, err := newSecretStoreAt(path).Put("home.ssh_key", "a1, b2, c3")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := newSecretStoreAt(path).Get(ref)
	if err != nil {
		t.Fatalf("Get from a new instance: %v", err)
	}
	if got != "a1, b2, c3" {
		t.Errorf("got %q", got)
	}
}

// A repeated Put with the same name overwrites the value and returns the same reference —
// otherwise `dnstt-cli profile set` would pile up orphaned entries.
func TestSecretPutReusesRefByName(t *testing.T) {
	s := tempStore(t)

	first, err := s.Put("home.password", "old")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Put("home.password", "new")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("reference changed on overwrite: %q → %q", first, second)
	}

	got, err := s.Get(second)
	if err != nil {
		t.Fatal(err)
	}
	if got != "new" {
		t.Errorf("got %q, expected \"new\"", got)
	}

	data, err := s.read()
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Secrets) != 1 {
		t.Errorf("%d entries in the store, expected one", len(data.Secrets))
	}
}

// Different names give different references, and both values come back.
func TestSecretMultiple(t *testing.T) {
	s := tempStore(t)

	pref, err := s.Put("home.password", "p")
	if err != nil {
		t.Fatal(err)
	}
	href, err := s.Put("home.ssh_key", "h")
	if err != nil {
		t.Fatal(err)
	}
	if pref == href {
		t.Fatal("different secrets got the same reference")
	}
	if v, _ := s.Get(pref); v != "p" {
		t.Errorf("password = %q", v)
	}
	if v, _ := s.Get(href); v != "h" {
		t.Errorf("SSH key = %q", v)
	}
}

func TestSecretGetMalformedRef(t *testing.T) {
	s := tempStore(t)
	for _, ref := range []string{"", "noscheme", secretScheme + ":", ":id"} {
		if _, err := s.Get(ref); err == nil {
			t.Errorf("reference %q accepted, expected an error", ref)
		}
	}
}

func TestSecretGetUnknownID(t *testing.T) {
	s := tempStore(t)
	if _, err := s.Get(secretScheme + ":deadbeef"); err == nil {
		t.Error("nonexistent id accepted")
	}
}

// A reference with a foreign scheme (a profile from Windows opened on unix or vice versa)
// is not resolved silently: otherwise the secret would "vanish" without explanation.
func TestSecretGetForeignScheme(t *testing.T) {
	s := tempStore(t)

	foreign := "file"
	if secretScheme == "file" {
		foreign = "dpapi"
	}

	_, err := s.Get(foreign + ":abc123")
	if err == nil {
		t.Fatal("foreign scheme accepted")
	}
	if !strings.Contains(err.Error(), foreign) {
		t.Errorf("the message does not name the foreign scheme: %v", err)
	}
}

// On unix the only protection of a secret is the file mode. The store must be created with
// 0600. On Windows the mode bits mean nothing — there DPAPI protects the secret.
func TestSecretFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("on Windows the file mode does not protect the secret — DPAPI does")
	}
	s := tempStore(t)
	if _, err := s.Put("home.password", "p"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode %o, expected 600", perm)
	}
}

// What exactly is in the file: on Windows DPAPI ciphertext (the value cannot be seen
// inside), on unix the value itself (the protection is the file mode, not a cipher). This
// checks that DPAPI is really applied on Windows.
func TestSecretStorageForm(t *testing.T) {
	s := tempStore(t)
	const secret = "very-secret-value-42"

	if _, err := s.Put("home.password", secret); err != nil {
		t.Fatal(err)
	}
	data, err := s.read()
	if err != nil {
		t.Fatal(err)
	}

	var stored secretEntry
	for _, entry := range data.Secrets {
		stored = entry
	}
	sealed, err := base64.StdEncoding.DecodeString(stored.Data)
	if err != nil {
		t.Fatalf("data is not base64: %v", err)
	}

	if runtime.GOOS == "windows" {
		if string(sealed) == secret {
			t.Error("on Windows the secret is stored in clear text — DPAPI was not applied")
		}
	} else {
		if string(sealed) != secret {
			t.Errorf("on unix expected the value itself, stored %q", string(sealed))
		}
	}
}

// Delete removes one entry and leaves its neighbour alone.
func TestSecretDelete(t *testing.T) {
	s := tempStore(t)

	password, err := s.Put("home.password", "password")
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.Put("home.ssh_key", "key")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Delete(password); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(password); err == nil {
		t.Error("deleted secret is still readable")
	}
	if got, err := s.Get(key); err != nil || got != "key" {
		t.Errorf("the neighbouring entry was damaged: %q, %v", got, err)
	}
}

// Deleting what is not there is not an error: `dnstt-cli profile rm` must carry the
// profile removal through even if the entry disappeared earlier.
func TestSecretDeleteMissing(t *testing.T) {
	s := tempStore(t)

	if err := s.Delete(secretScheme + ":0123456789abcdef"); err != nil {
		t.Errorf("deleting a missing entry returned an error: %v", err)
	}

	ref, err := s.Put("home.password", "password")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ref); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ref); err != nil {
		t.Errorf("repeated delete returned an error: %v", err)
	}
}

// A foreign scheme is rejected here too: Delete by a reference from another platform
// would remove something other than what the caller meant.
func TestSecretDeleteForeignScheme(t *testing.T) {
	s := tempStore(t)

	foreign := "unheard-of-store:0123456789abcdef"
	err := s.Delete(foreign)
	if err == nil {
		t.Fatal("foreign scheme accepted")
	}
	if !strings.Contains(err.Error(), secretScheme) {
		t.Errorf("the message does not name the local scheme: %v", err)
	}
}
