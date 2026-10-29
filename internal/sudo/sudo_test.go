package sudo

import (
	"errors"
	"os/user"
	"testing"
)

func envOf(m map[string]string) func(string) string {
	return func(key string) string { return m[key] }
}

func lookupOf(users ...*user.User) func(string) (*user.User, error) {
	return func(name string) (*user.User, error) {
		for _, u := range users {
			if u.Username == name {
				return u, nil
			}
		}
		return nil, user.UnknownUserError(name)
	}
}

var someone = &user.User{Username: "user", Uid: "4242", Gid: "4343", HomeDir: "/home/user"}

func TestInvokerUnderSudo(t *testing.T) {
	got, err := invoker(0, envOf(map[string]string{"SUDO_USER": "user", "HOME": "/root"}), lookupOf(someone))
	if err != nil {
		t.Fatalf("invoker: %v", err)
	}
	if got == nil {
		t.Fatal("user not found under sudo")
	}
	if got.Name != "user" || got.Uid != 4242 || got.Gid != 4343 || got.HomeDir != "/home/user" {
		t.Errorf("invoker = %+v", got)
	}
	if len(got.Groups) == 0 {
		t.Error("no groups at all: at least the primary one must remain")
	}
}

// Without sudo the package stays silent: neither a user nor an error.
func TestInvokerWithoutSudo(t *testing.T) {
	tests := []struct {
		name string
		euid int
		env  map[string]string
	}{
		{"regular user", 1000, map[string]string{"SUDO_USER": "user"}},
		{"root without sudo", 0, map[string]string{}},
		{"sudo by root", 0, map[string]string{"SUDO_USER": "root"}},
		{"windows", -1, map[string]string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lookup := func(string) (*user.User, error) {
				t.Fatal("the user database must not be queried without sudo")
				return nil, nil
			}
			got, err := invoker(tt.euid, envOf(tt.env), lookup)
			if got != nil || err != nil {
				t.Errorf("invoker = %+v, %v; want nil, nil", got, err)
			}
		})
	}
}

// There is a user under sudo, but they cannot be found: an error, not a silent root.
func TestInvokerUnknownUser(t *testing.T) {
	_, err := invoker(0, envOf(map[string]string{"SUDO_USER": "ghost"}), lookupOf(someone))
	var unknown user.UnknownUserError
	if !errors.As(err, &unknown) {
		t.Errorf("err = %v, want UnknownUserError", err)
	}
}

func TestInvokerBadRecord(t *testing.T) {
	tests := []struct {
		name string
		u    *user.User
	}{
		{"no home directory", &user.User{Username: "user", Uid: "4242", Gid: "4343"}},
		{"uid not a number", &user.User{Username: "user", Uid: "S-1-5", Gid: "4343", HomeDir: "/home/user"}},
		{"gid not a number", &user.User{Username: "user", Uid: "4242", Gid: "", HomeDir: "/home/user"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := invoker(0, envOf(map[string]string{"SUDO_USER": "user"}), lookupOf(tt.u)); err == nil {
				t.Error("broken record accepted")
			}
		})
	}
}
