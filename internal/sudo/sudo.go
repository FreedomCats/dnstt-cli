// Package sudo finds out on whose behalf dnstt-cli is run through sudo.
//
// On Linux the tun mode needs root, and dnstt-cli is run through sudo. But the settings
// and secrets belong to whoever invoked it, not to root, and the keyring to use is that
// person's too. The package answers one question — who this person is — and is needed
// by config (the settings directory, the owner of written files) and secretservice (the
// keyring helper run as that user).
package sudo

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"strconv"
	"strings"
	"sync"
)

// User is the user who invoked sudo.
type User struct {
	Name    string
	Uid     uint32
	Gid     uint32
	Groups  []uint32 // supplementary groups; without them, only Gid
	HomeDir string
}

// Invoker returns the user who invoked sudo, or nil if dnstt-cli is not run through sudo:
// without root, by root directly, or on Windows, where there is no sudo and Geteuid
// returns -1. An error means sudo is there but the user could not be found; silently
// putting root in their place is not allowed: the settings would be the wrong ones, and
// root's keyring would be used.
//
// The answer does not change during the life of the process and is computed once.
func Invoker() (*User, error) {
	return invokerOnce()
}

var invokerOnce = sync.OnceValues(func() (*User, error) {
	return invoker(os.Geteuid(), os.Getenv, user.Lookup)
})

// invoker is split out for tests: the caller supplies the privileges, the environment and
// the user database.
func invoker(euid int, env func(string) string, lookup func(string) (*user.User, error)) (*User, error) {
	if euid != 0 {
		return nil, nil
	}
	name := strings.TrimSpace(env("SUDO_USER"))
	if name == "" || name == "root" {
		return nil, nil
	}

	u, err := lookup(name)
	if err != nil {
		return nil, fmt.Errorf("dnstt-cli is run through sudo by user %s, but the user cannot be found: %w; "+
			"%s", name, err, hint)
	}
	if strings.TrimSpace(u.HomeDir) == "" {
		return nil, fmt.Errorf("user %s who invoked sudo has no home directory; %s", name, hint)
	}

	uid, err := parseID(u.Uid)
	if err != nil {
		return nil, fmt.Errorf("user %s: uid: %w", name, err)
	}
	gid, err := parseID(u.Gid)
	if err != nil {
		return nil, fmt.Errorf("user %s: gid: %w", name, err)
	}

	// Supplementary groups are given to the programs run as the user (the keyring
	// helper), but their absence is no reason to refuse: the primary one is enough.
	groups := []uint32{gid}
	if ids, err := u.GroupIds(); err == nil {
		groups = groups[:0]
		for _, id := range ids {
			if g, err := parseID(id); err == nil {
				groups = append(groups, g)
			}
		}
		if len(groups) == 0 {
			groups = append(groups, gid)
		}
	}

	return &User{Name: name, Uid: uid, Gid: gid, Groups: groups, HomeDir: u.HomeDir}, nil
}

// hint is how to do without looking up the user: profiles are read by -config under sudo
// too, and writing them does not need sudo at all.
const hint = "give the settings file with the -config flag, and change profiles without sudo"

func parseID(s string) (uint32, error) {
	id, err := strconv.ParseUint(strings.TrimSpace(s), 10, 32)
	if err != nil {
		return 0, errors.New("not a number: " + s)
	}
	return uint32(id), nil
}
