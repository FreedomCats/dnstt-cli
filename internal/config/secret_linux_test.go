package config

import (
	"errors"
	"fmt"
	"testing"

	"github.com/FreedomCats/dnstt-cli/internal/secretservice"
)

// The keyring was not unlocked: from outside this is ErrKeyringLocked, and the text stays
// what the keyring said, without repetition.
func TestKeyringErrorLocked(t *testing.T) {
	src := fmt.Errorf("%w: prompt dismissed without an answer", secretservice.ErrLocked)
	err := keyringError(src)
	if !errors.Is(err, ErrKeyringLocked) || !errors.Is(err, secretservice.ErrLocked) {
		t.Errorf("%v: marks lost", err)
	}
	if err.Error() != src.Error() {
		t.Errorf("text %q, expected %q", err, src)
	}
	if errors.Is(keyringError(errors.New("another problem")), ErrKeyringLocked) {
		t.Error("ErrKeyringLocked on an unrelated error")
	}
}
