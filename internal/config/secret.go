package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/FreedomCats/dnstt-cli/internal/app"
)

// SecretsFileName is the name of the secret store file. It lives in the config directory,
// next to profiles.toml.
const SecretsFileName = "secrets.json"

// SecretStore keeps secrets — the exit password and SSH keys — outside profiles.toml. The
// profile gets only a reference of the form <store>:<id>, which Put returns and Get
// resolves.
//
// What "store" means is up to the platform. On Windows it is DPAPI: the value is
// encrypted with CryptProtectData and bound to the user account, the reference scheme is
// dpapi. On Linux it is the session keyring (Secret Service), scheme secretservice, and
// where there is none (SSH, a server, a console without graphics) it is as on other
// systems: no encryption, the protection is mode 0600 on the store file itself, scheme
// file. A reference from one platform is not resolved on another: Get rejects a foreign
// scheme instead of returning garbage (see secret_windows.go, secret_unix.go,
// secret_linux.go).
type SecretStore interface {
	// Put stores the value under name and returns a reference to it. A repeated call with
	// the same name overwrites the previous value and returns the same reference, so that
	// `dnstt-cli profile set` does not pile up orphaned entries on every run.
	Put(name, value string) (ref string, err error)
	// Get resolves a reference back into the value.
	Get(ref string) (value string, err error)
	// Delete removes the value from the store. A reference that is not in the store is not
	// an error: `dnstt-cli profile rm` must carry the removal through even if the entry
	// disappeared earlier.
	Delete(ref string) error
}

// NewSecretStore opens the store in the platform's config directory, where Path looks for
// profiles.toml.
func NewSecretStore() (SecretStore, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	return NewSecretStoreFor(path), nil
}

// NewSecretStoreFor opens the store next to the given config file.
//
// It is needed by a command with the -config flag: secrets must live in the same directory
// as the profiles that refer to them, otherwise a moved config directory would only half
// work.
func NewSecretStoreFor(configPath string) SecretStore {
	s := newSecretStoreAt(filepath.Join(filepath.Dir(configPath), SecretsFileName))
	s.keyring = openKeyring
	return s
}

// newSecretStoreAt opens the store at an explicit path, without the keyring. It is needed
// by tests and in case a config directory flag appears; there is no public twin yet — it
// will be added when needed.
func newSecretStoreAt(path string) *store {
	return &store{path: path}
}

type store struct {
	path string
	// keyring opens the keyring; nil means there is none on this system.
	keyring func() (keyring, error)
	// refused is set once unlocking the keyring was refused in this store
	// (ErrKeyringLocked); see openKeyring.
	refused error
}

// keyring is a keyring: the values live in it, and the store file holds only the entry's
// name and id (secret_linux.go).
type keyring interface {
	Put(id, label, value string) error
	// Get: errKeyringNotFound if there is no such entry.
	Get(id string) (string, error)
	// Delete: errKeyringNotFound if there is no such entry.
	Delete(id string) error
	Close() error
}

var (
	// errKeyringUnavailable means there is no keyring in this session: Put writes to the
	// file.
	errKeyringUnavailable = errors.New("no keyring in this session")
	errKeyringNotFound    = errors.New("entry not found in the keyring")

	// ErrKeyringLocked means the secret was not read because the keyring was not unlocked:
	// the prompt was dismissed or not answered. The secret is intact; there is no need to
	// rewrite it.
	ErrKeyringLocked = errors.New("keyring is not unlocked")
)

// keyringScheme is the scheme of a reference to a secret in the keyring.
const keyringScheme = "secretservice"

// SecretRefKind says in words where the referenced secret lives, for the output of
// `dnstt-cli profile set`.
func SecretRefKind(ref string) string {
	if scheme, _, _ := strings.Cut(strings.TrimSpace(ref), ":"); scheme == keyringScheme {
		return "keyring (Secret Service)"
	}
	return SecretsFileName + ", " + secretStoreKind
}

// secretsData is the format of the store file. Entries are addressed by a short id that
// goes into the reference; name is a human-readable label for `dnstt-cli profile show` and
// the key by which Put finds an entry to overwrite.
type secretsData struct {
	Secrets map[string]secretEntry `json:"secrets"`
}

type secretEntry struct {
	Name string `json:"name"`
	// Data is base64 of protect(value). On Windows it is DPAPI ciphertext, on other systems
	// the value itself: there the file protects it, not a cipher. Empty for an entry in the
	// keyring.
	Data string `json:"data,omitempty"`
	// Store is where the value is if not in Data: keyringScheme. The entry stays in the file
	// for its name: by it Put finds the id to overwrite.
	Store string `json:"store,omitempty"`
}

func (s *store) Put(name, value string) (string, error) {
	if s.keyring != nil {
		ref, err := s.putKeyring(name, value)
		if !errors.Is(err, errKeyringUnavailable) {
			return ref, err
		}
	}

	sealed, err := protect([]byte(value))
	if err != nil {
		return "", fmt.Errorf("cannot protect secret %q: %w", name, err)
	}

	data, err := s.read()
	if err != nil {
		return "", err
	}

	id := idForName(data.Secrets, name)
	if id == "" {
		if id, err = newSecretID(); err != nil {
			return "", err
		}
	}
	data.Secrets[id] = secretEntry{
		Name: name,
		Data: base64.StdEncoding.EncodeToString(sealed),
	}

	if err := s.write(data); err != nil {
		return "", err
	}
	return secretScheme + ":" + id, nil
}

// putKeyring stores the value in the keyring. A previous value under the same name —
// whether in the keyring or in the file — is replaced: this is how a secret moves from the
// file to the keyring when rewritten.
func (s *store) putKeyring(name, value string) (string, error) {
	k, err := s.openKeyring()
	if err != nil {
		return "", err
	}
	defer k.Close()

	data, err := s.read()
	if err != nil {
		return "", err
	}
	id := idForName(data.Secrets, name)
	if id == "" {
		if id, err = newSecretID(); err != nil {
			return "", err
		}
	}

	if err := s.note(k.Put(id, app.Name+" "+name, value)); err != nil {
		return "", fmt.Errorf("cannot write secret %q: %w", name, err)
	}
	data.Secrets[id] = secretEntry{Name: name, Store: keyringScheme}
	if err := s.write(data); err != nil {
		return "", err
	}
	return keyringScheme + ":" + id, nil
}

func (s *store) Get(ref string) (string, error) {
	scheme, id, err := s.splitRef(ref)
	if err != nil {
		return "", err
	}
	if scheme == keyringScheme {
		return s.getKeyring(ref, id)
	}

	data, err := s.read()
	if err != nil {
		return "", err
	}
	entry, ok := data.Secrets[id]
	if !ok {
		return "", fmt.Errorf(
			"secret %q not found in the store %s — perhaps the profile was not created with `dnstt-cli profile set`", ref, s.path)
	}
	if entry.Store == keyringScheme {
		// The value moved to the keyring but the reference in the profile stayed the old
		// one: profiles.toml was not written after Put.
		if s.keyring == nil {
			return "", fmt.Errorf("secret %q is in the keyring, which is not available on this system", ref)
		}
		return s.getKeyring(ref, id)
	}

	sealed, err := base64.StdEncoding.DecodeString(entry.Data)
	if err != nil {
		return "", fmt.Errorf("secret %q in the store %s is corrupted: %w", ref, s.path, err)
	}
	plain, err := unprotect(sealed)
	if err != nil {
		return "", fmt.Errorf("cannot read secret %q (%s): %w", ref, secretStoreKind, err)
	}
	return string(plain), nil
}

func (s *store) getKeyring(ref, id string) (string, error) {
	k, err := s.openKeyring()
	if err != nil {
		return "", fmt.Errorf("secret %q: %w", ref, err)
	}
	defer k.Close()
	value, err := k.Get(id)
	s.note(err)
	if errors.Is(err, errKeyringNotFound) {
		return "", fmt.Errorf("secret %q not found in the keyring — perhaps it was removed from there by hand", ref)
	}
	if err != nil {
		return "", fmt.Errorf("secret %q: %w", ref, err)
	}
	return value, nil
}

func (s *store) Delete(ref string) error {
	scheme, id, err := s.splitRef(ref)
	if err != nil {
		return err
	}
	if scheme == keyringScheme {
		k, err := s.openKeyring()
		if err != nil {
			return fmt.Errorf("secret %q: %w", ref, err)
		}
		defer k.Close()
		if err := s.note(k.Delete(id)); err != nil && !errors.Is(err, errKeyringNotFound) {
			return fmt.Errorf("secret %q: %w", ref, err)
		}
	}

	data, err := s.read()
	if err != nil {
		return err
	}
	if _, ok := data.Secrets[id]; !ok {
		// Nothing to delete means it is already deleted. An error here would stop
		// `dnstt-cli profile rm` halfway, leaving the profile in the file.
		return nil
	}
	delete(data.Secrets, id)
	return s.write(data)
}

// openKeyring opens the keyring. If unlocking it was already refused in this store — the
// prompt was dismissed or not answered — the keyring is not opened and the same error is
// returned: otherwise a command that needs several secrets (`dnstt-cli profile show`)
// would show the unlock prompt for each of them.
func (s *store) openKeyring() (keyring, error) {
	if s.refused != nil {
		return nil, s.refused
	}
	return s.keyring()
}

// note remembers a refusal to unlock the keyring and returns err as is.
func (s *store) note(err error) error {
	if errors.Is(err, ErrKeyringLocked) {
		s.refused = err
	}
	return err
}

// splitRef parses the reference and makes sure it belongs to the local store: the file or,
// where there is one, the keyring.
//
// A foreign scheme is not garbage but a secret from another platform: DPAPI ciphertext
// cannot be read on unix anyway, and it is more honest to say so than to silently return
// nothing.
func (s *store) splitRef(ref string) (scheme, id string, err error) {
	scheme, id, ok := strings.Cut(strings.TrimSpace(ref), ":")
	if !ok || scheme == "" || id == "" {
		return "", "", fmt.Errorf("secret reference %q: expected the form <store>:<id>", ref)
	}
	if scheme == keyringScheme && s.keyring != nil {
		return scheme, id, nil
	}
	if scheme != secretScheme {
		return "", "", fmt.Errorf(
			"secret reference %q: store %q is not available on this system — here secrets use the scheme %q (%s)",
			ref, scheme, secretScheme, secretStoreKind)
	}
	return scheme, id, nil
}

// idForName looks up the id of the entry with the given name. Empty means there is no
// entry, and Put will create a new id. Duplicate names do not arise in the normal course
// (this lookup is what prevents them); when walking the map any one of them is taken.
func idForName(secrets map[string]secretEntry, name string) string {
	for id, entry := range secrets {
		if entry.Name == name {
			return id
		}
	}
	return ""
}

func (s *store) read() (*secretsData, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return &secretsData{Secrets: map[string]secretEntry{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("secret store %s: %w", s.path, err)
	}
	var data secretsData
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("secret store %s is corrupted: %w", s.path, err)
	}
	if data.Secrets == nil {
		data.Secrets = map[string]secretEntry{}
	}
	return &data, nil
}

func (s *store) write(data *secretsData) error {
	if err := makeDir(filepath.Dir(s.path)); err != nil {
		return err
	}

	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')

	// Write to a temporary file and rename it: an interrupted write must not leave the
	// store half written. Mode 0600 is all the protection secrets have on unix; on Windows
	// the mode bits do not matter, the values there are already encrypted by DPAPI.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("secret store %s: %w", tmp, err)
	}
	if err := giveToInvoker(tmp); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("secret store %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("secret store %s: %w", s.path, err)
	}
	return nil
}

func newSecretID() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("cannot create a secret id: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}
