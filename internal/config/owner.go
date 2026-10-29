package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/FreedomCats/dnstt-cli/internal/sudo"
)

// Under sudo dnstt-cli writes to the config directory of the user who invoked it (see
// configDir). Files and directories created by root would stay root's: without sudo the
// person could not read or rewrite them later. So everything dnstt-cli creates under sudo
// is given to the invoking user.
//
// Both variables are replaced in tests: a real chown requires root.
var (
	invoker = sudo.Invoker
	chown   = os.Chown
)

// makeDir creates the directory with all missing parents, like MkdirAll, and under sudo
// gives every created one to the invoking user — including, say, ~/.config if it did not
// exist. Directories that already existed are left alone.
func makeDir(dir string) error {
	owner, err := invoker()
	if err != nil {
		return err
	}

	var created []string
	if owner != nil {
		for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
			if _, err := os.Lstat(d); !errors.Is(err, fs.ErrNotExist) {
				break
			}
			created = append(created, d)
			if parent := filepath.Dir(d); parent == d {
				break
			}
		}
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("config directory %s: %w", dir, err)
	}

	// Top down: this way a directory is given away before what is inside it.
	for i := len(created) - 1; i >= 0; i-- {
		if err := giveTo(owner, created[i]); err != nil {
			return fmt.Errorf("config directory %s: %w", created[i], err)
		}
	}
	return nil
}

// giveToInvoker gives the file to the user who invoked sudo; without sudo it does nothing.
// It is called for the temporary file before Rename: after the rename the same file is
// already in place of the real one, and there is no window when it belongs to root.
func giveToInvoker(path string) error {
	owner, err := invoker()
	if err != nil {
		return err
	}
	return giveTo(owner, path)
}

func giveTo(owner *sudo.User, path string) error {
	if owner == nil {
		return nil
	}
	if err := chown(path, int(owner.Uid), int(owner.Gid)); err != nil {
		return fmt.Errorf("cannot give the file to user %s: %w", owner.Name, err)
	}
	return nil
}
