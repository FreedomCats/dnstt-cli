package config

import (
	"path/filepath"
	"testing"

	"github.com/FreedomCats/dnstt-cli/internal/sudo"
)

func TestConfigDir(t *testing.T) {
	underSudo := &sudo.User{Name: "user", Uid: 4242, Gid: 4242, HomeDir: "/home/user"}

	tests := []struct {
		name    string
		goos    string
		env     map[string]string
		invoker *sudo.User
		want    string
	}{
		{
			name: "windows takes APPDATA",
			goos: "windows",
			env:  map[string]string{"APPDATA": `C:\Users\user\AppData\Roaming`},
			want: `C:\Users\user\AppData\Roaming`,
		},
		{
			name: "unix takes XDG_CONFIG_HOME",
			goos: "linux",
			env:  map[string]string{"XDG_CONFIG_HOME": "/home/user/.config-2", "HOME": "/home/user"},
			want: "/home/user/.config-2",
		},
		{
			name: "unix without XDG falls back to ~/.config",
			goos: "linux",
			env:  map[string]string{"HOME": "/home/user"},
			want: filepath.Join("/home/user", ".config"),
		},
		{
			// On macOS the client is a console program, and its settings live where other
			// console programs keep theirs, not in Library/Application Support.
			name: "macOS counts as unix",
			goos: "darwin",
			env:  map[string]string{"HOME": "/Users/user"},
			want: filepath.Join("/Users/user", ".config"),
		},
		{
			// Under sudo the environment is root's: the config is looked up for the invoker.
			name:    "sudo takes the invoker's directory",
			goos:    "linux",
			env:     map[string]string{"HOME": "/root"},
			invoker: underSudo,
			want:    filepath.Join("/home/user", ".config"),
		},
		{
			// An XDG_CONFIG_HOME that survived into sudo (sudo -E or root's settings) does
			// not belong to the invoker and is ignored.
			name:    "sudo ignores XDG_CONFIG_HOME",
			goos:    "linux",
			env:     map[string]string{"XDG_CONFIG_HOME": "/root/.config", "HOME": "/root"},
			invoker: underSudo,
			want:    filepath.Join("/home/user", ".config"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := configDir(tt.goos, func(key string) string { return tt.env[key] }, tt.invoker)
			if err != nil {
				t.Fatalf("configDir: %v", err)
			}
			if got != tt.want {
				t.Errorf("configDir = %q, expected %q", got, tt.want)
			}
		})
	}
}

func TestConfigDirWithoutEnv(t *testing.T) {
	empty := func(string) string { return "" }

	for _, goos := range []string{"windows", "linux"} {
		if _, err := configDir(goos, empty, nil); err == nil {
			t.Errorf("%s: an empty environment was accepted as valid", goos)
		}
	}
}

// Path joins the platform directory with the directory and file names.
func TestPath(t *testing.T) {
	path, err := Path()
	if err != nil {
		t.Skipf("environment without a config directory: %v", err)
	}
	if filepath.Base(path) != FileName || filepath.Base(filepath.Dir(path)) != AppDir {
		t.Errorf("Path = %q, expected suffix %s", path, filepath.Join(AppDir, FileName))
	}
}
