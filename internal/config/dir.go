// Package config holds the dnstt-cli profiles (profiles.toml) and the secret store next
// to them.
//
// A profile replaces the core's flags: a named set of settings switched between by one
// word on the command line (§10 doc/architecture.md). profiles.toml holds no secrets: the
// exit password and SSH keys live in the secret store (secret.go), and the profile holds
// only references to them.
package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/FreedomCats/dnstt-cli/internal/app"
	"github.com/FreedomCats/dnstt-cli/internal/sudo"
)

// Names of the config directory and file. The directory is shared by the whole program:
// the secret store (secret.go) lives next to profiles.toml.
const (
	AppDir   = app.Name
	FileName = "profiles.toml"
)

// Path returns the path to profiles.toml by the platform's rules:
// %APPDATA%\dnstt-cli\profiles.toml on Windows and
// $XDG_CONFIG_HOME/dnstt-cli/profiles.toml (~/.config by default) elsewhere.
// Under sudo it is ~/.config/dnstt-cli of the user who invoked sudo, not root's.
//
// os.UserConfigDir does not fit here: on macOS it gives ~/Library/Application Support,
// but the client is a console program, and its settings belong where other console
// programs keep theirs.
func Path() (string, error) {
	dir, err := appDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// appDir is the config directory: the platform directory plus AppDir. It holds
// profiles.toml and the secret store (secret.go), so the path is computed in one place.
func appDir() (string, error) {
	invoker, err := sudo.Invoker()
	if err != nil {
		return "", err
	}
	dir, err := configDir(runtime.GOOS, os.Getenv, invoker)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, AppDir), nil
}

// configDir is separate from Path and takes the platform and the environment as
// arguments: otherwise there would be no way to test another system's branch.
//
// invoker is who invoked sudo (nil if not run through sudo). Under sudo the environment is
// already root's: HOME=/root, and XDG_CONFIG_HOME, if it survived at all, may belong to
// anyone. So the invoking user's home directory is taken from the user database; sudo does
// not pass on their own XDG_CONFIG_HOME, and there is no way to learn it.
func configDir(goos string, env func(string) string, invoker *sudo.User) (string, error) {
	if goos == "windows" {
		if appData := strings.TrimSpace(env("APPDATA")); appData != "" {
			return appData, nil
		}
		return "", errors.New("environment variable APPDATA is not set: cannot tell where to look for the config")
	}

	if invoker != nil {
		return filepath.Join(invoker.HomeDir, ".config"), nil
	}

	if xdg := strings.TrimSpace(env("XDG_CONFIG_HOME")); xdg != "" {
		return xdg, nil
	}
	if home := strings.TrimSpace(env("HOME")); home != "" {
		return filepath.Join(home, ".config"), nil
	}
	return "", errors.New("environment variables XDG_CONFIG_HOME and HOME are not set: cannot tell where to look for the config")
}
