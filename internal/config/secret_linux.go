package config

import (
	"errors"
	"fmt"
	"os"

	"github.com/FreedomCats/dnstt-cli/internal/secretservice"
)

// On Linux secrets go to the session keyring (Secret Service): there they are encrypted
// with a key unlocked by logging in, like DPAPI on Windows. Without a keyring, a file with
// mode 0600 (secret_unix.go). References of both kinds are read: secrets written to the
// file earlier move to the keyring only when they are rewritten.
var openKeyring = func() (keyring, error) {
	k, err := secretservice.Open(os.Stderr)
	if err != nil {
		return nil, keyringError(err)
	}
	return linuxKeyring{k}, nil
}

// linuxKeyring translates errors of the secretservice package into store errors.
type linuxKeyring struct{ k secretservice.Keyring }

func (l linuxKeyring) Put(id, label, value string) error {
	return keyringError(l.k.Put(id, label, value))
}
func (l linuxKeyring) Delete(id string) error { return keyringError(l.k.Delete(id)) }
func (l linuxKeyring) Close() error           { return l.k.Close() }

func (l linuxKeyring) Get(id string) (string, error) {
	value, err := l.k.Get(id)
	return value, keyringError(err)
}

func keyringError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, secretservice.ErrUnavailable):
		return fmt.Errorf("%w: %w", errKeyringUnavailable, err)
	case errors.Is(err, secretservice.ErrNotFound):
		return fmt.Errorf("%w: %w", errKeyringNotFound, err)
	case errors.Is(err, secretservice.ErrLocked):
		// Their text is the same, so it is not repeated a second time.
		return marked{err, ErrKeyringLocked}
	}
	return err
}

// marked is an error with its own text that errors.Is also recognises by the mark.
type marked struct {
	error
	mark error
}

func (m marked) Unwrap() error        { return m.error }
func (m marked) Is(target error) bool { return target == m.mark }
