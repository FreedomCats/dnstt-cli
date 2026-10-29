package sudo

import (
	"os"
	"path"
	"strconv"
	"strings"
)

// SessionEnv is the session environment of user u built from the sudo environment: for
// programs that dnstt-cli runs on the user's behalf (the keyring helper).
//
// sudo keeps DISPLAY and XAUTHORITY, but HOME, USER and LOGNAME are already root's, and
// XDG_RUNTIME_DIR and DBUS_SESSION_BUS_ADDRESS do not survive at all. Without them a
// program finds neither the user's settings nor their D-Bus session bus, and without the
// bus there is no keyring. The session directory is /run/user/<uid> (systemd-logind); if
// it does not exist, what was there stays. The SUDO_* variables are of no use there and
// are removed.
func SessionEnv(environ []string, u *User) []string {
	return sessionEnv(environ, u, exists)
}

func sessionEnv(environ []string, u *User, exists func(string) bool) []string {
	set := map[string]string{
		"HOME":    u.HomeDir,
		"USER":    u.Name,
		"LOGNAME": u.Name,
	}
	// The paths are Linux ones on any system: filepath on Windows would give
	// \run\user\….
	runtimeDir := path.Join("/run/user", strconv.FormatUint(uint64(u.Uid), 10))
	if exists(runtimeDir) {
		set["XDG_RUNTIME_DIR"] = runtimeDir
		if bus := path.Join(runtimeDir, "bus"); exists(bus) {
			set["DBUS_SESSION_BUS_ADDRESS"] = "unix:path=" + bus
		}
	}

	env := make([]string, 0, len(environ)+len(set))
	for _, kv := range environ {
		key, _, _ := strings.Cut(kv, "=")
		if _, replaced := set[key]; replaced || strings.HasPrefix(key, "SUDO_") {
			continue
		}
		env = append(env, kv)
	}
	for _, key := range []string{"HOME", "USER", "LOGNAME", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS"} {
		if value, ok := set[key]; ok {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func exists(name string) bool {
	_, err := os.Stat(name)
	return err == nil
}
