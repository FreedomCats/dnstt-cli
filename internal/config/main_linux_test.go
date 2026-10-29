package config

import (
	"os"
	"testing"
)

// Tests must not reach the real keyring of whoever runs them: test profiles would write
// their secrets there. Without a session bus there is no keyring, and secrets go to a
// file, as on a machine without graphics.
func TestMain(m *testing.M) {
	os.Unsetenv("DBUS_SESSION_BUS_ADDRESS")
	os.Unsetenv("XDG_RUNTIME_DIR")
	os.Exit(m.Run())
}
