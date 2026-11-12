package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/FreedomCats/dnstt-cli/internal/config"
	"github.com/FreedomCats/dnstt-cli/internal/exit"
)

// sshTestHostKey is a syntactically valid pinned fingerprint: SHA256: and 43 base64
// characters, the encoding of 32 zero bytes. The tests build a dialer but never reach a
// server, so no real host key is involved.
var sshTestHostKey = "SHA256:" + strings.Repeat("A", 43)

// sshTestKey returns a generated ed25519 private key in OpenSSH PEM, unencrypted or
// encrypted with passphrase. A key exists only to prove the wiring: no fixture, no value
// from anywhere.
func sshTestKey(t *testing.T, passphrase string) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, "")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(block))
}

// The password of an SSH exit comes from password_ref through the injected getter; the
// fingerprint is normalized on the way, and the dialer sits behind the fatal wrapper.
func TestBuildSSHExitPassword(t *testing.T) {
	p := testProfile(config.Exit{
		Type: "ssh", User: "synthetic-user",
		HostKey:     strings.Repeat("A", 43), // the bare body: the build adds SHA256:
		PasswordRef: "file:synthetic-password-id",
	})
	var refs []string
	tun, err := buildTunnel(TunnelOptions{Profile: p}, func(ref string) (string, error) {
		refs = append(refs, ref)
		return "synthetic-password", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0] != p.Exit.PasswordRef {
		t.Fatalf("secret lookups = %v, want only %q", refs, p.Exit.PasswordRef)
	}
	w, ok := tun.Exit.(*fatalExit)
	if !ok {
		t.Fatalf("exit = %T, want *fatalExit", tun.Exit)
	}
	d, ok := w.dialer.(*exit.SSH)
	if !ok {
		t.Fatalf("dialer = %T, want *exit.SSH", w.dialer)
	}
	if d.Open == nil || d.User != "synthetic-user" || d.Password != "synthetic-password" || d.Signer != nil {
		t.Fatal("the SSH exit was not bound from the profile")
	}
	if d.HostKey != sshTestHostKey {
		t.Fatalf("host key = %q, want the normalized %q", d.HostKey, sshTestHostKey)
	}
}

// The key of an SSH exit comes from key_ref through the getter and exit.ParseKey; no
// password is offered then.
func TestBuildSSHExitKey(t *testing.T) {
	key := sshTestKey(t, "")
	p := testProfile(config.Exit{
		Type: "ssh", User: "synthetic-user",
		HostKey: sshTestHostKey,
		KeyRef:  "file:synthetic-key-id",
	})
	var refs []string
	tun, err := buildTunnel(TunnelOptions{Profile: p}, func(ref string) (string, error) {
		refs = append(refs, ref)
		return key, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0] != p.Exit.KeyRef {
		t.Fatalf("secret lookups = %v, want only %q", refs, p.Exit.KeyRef)
	}
	w, ok := tun.Exit.(*fatalExit)
	if !ok {
		t.Fatalf("exit = %T, want *fatalExit", tun.Exit)
	}
	d, ok := w.dialer.(*exit.SSH)
	if !ok {
		t.Fatalf("dialer = %T, want *exit.SSH", w.dialer)
	}
	if d.Signer == nil || d.Password != "" || d.User != "synthetic-user" || d.HostKey != sshTestHostKey {
		t.Fatal("the SSH exit was not bound from the key profile")
	}
}

// An SSH profile that internal/config would refuse is refused here too, before the secret
// store is opened: a profile built in code has not been through the profile rules.
func TestBuildSSHRefusesInvalidProfile(t *testing.T) {
	for _, tc := range []struct {
		name string
		exit config.Exit
		want string
	}{
		{"missing user", config.Exit{Type: "ssh", HostKey: sshTestHostKey, PasswordRef: "file:synthetic-id"}, `exit.user is required for exit.type "ssh"`},
		{"control in user", config.Exit{Type: "ssh", User: "synthetic\ruser", HostKey: sshTestHostKey, PasswordRef: "file:synthetic-id"}, "exit.user must not contain control characters"},
		{"missing host key", config.Exit{Type: "ssh", User: "synthetic-user", PasswordRef: "file:synthetic-id"}, "exit.host_key"},
		{"MD5 fingerprint", config.Exit{Type: "ssh", User: "synthetic-user", HostKey: "aa:bb:cc:dd", PasswordRef: "file:synthetic-id"}, "exit.host_key"},
		{"both references", config.Exit{Type: "ssh", User: "synthetic-user", HostKey: sshTestHostKey, PasswordRef: "file:synthetic-id", KeyRef: "file:synthetic-key-id"}, "takes exit.password_ref or exit.key_ref, not both"},
		{"neither reference", config.Exit{Type: "ssh", User: "synthetic-user", HostKey: sshTestHostKey}, "needs exactly one of exit.password_ref and exit.key_ref"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tun, err := buildTunnel(TunnelOptions{Profile: testProfile(tc.exit)}, func(string) (string, error) {
				t.Fatal("an invalid SSH profile opened the secret store")
				return "", nil
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("BuildTunnel = %v, %v; want %q", tun, err, tc.want)
			}
			if strings.Contains(err.Error(), "synthetic") {
				t.Fatalf("the error repeated a value: %v", err)
			}
		})
	}
}

// A store failure and a bad stored value fail the build with a message that names the field
// and repeats neither the reference nor the value.
func TestBuildSSHSecretErrors(t *testing.T) {
	const (
		passwordRef = "file:synthetic-password-ref"
		keyRef      = "file:synthetic-key-ref"
	)
	encrypted := sshTestKey(t, "synthetic-passphrase")
	for _, tc := range []struct {
		name      string
		password  bool
		ref       string
		value     string
		lookupErr error
		want      string
	}{
		{"password store error", true, passwordRef, "", errors.New("synthetic store failure: " + passwordRef), "exit.password_ref: cannot read the password from the secret store"},
		{"empty password", true, passwordRef, "", nil, "exit.password_ref: the stored password is empty"},
		{"key store error", false, keyRef, "", errors.New("synthetic store failure: " + keyRef), "exit.key_ref: cannot read the private key from the secret store"},
		{"empty key", false, keyRef, "", nil, "exit.key_ref: the stored private key is empty"},
		{"damaged key", false, keyRef, "synthetic-key-material", nil, "exit.key_ref: the private key cannot be parsed"},
		{"encrypted key", false, keyRef, encrypted, nil, "exit.key_ref: the private key needs a passphrase"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ex := config.Exit{Type: "ssh", User: "synthetic-user", HostKey: sshTestHostKey}
			if tc.password {
				ex.PasswordRef = tc.ref
			} else {
				ex.KeyRef = tc.ref
			}
			tun, err := buildTunnel(TunnelOptions{Profile: testProfile(ex)}, func(ref string) (string, error) {
				if ref != tc.ref {
					t.Fatalf("lookup used %q, want %q", ref, tc.ref)
				}
				return tc.value, tc.lookupErr
			})
			if err == nil || err.Error() != tc.want {
				t.Fatalf("BuildTunnel = %v, %v; want %q", tun, err, tc.want)
			}
			for _, private := range []string{tc.ref, tc.value, "synthetic"} {
				if private != "" && strings.Contains(err.Error(), private) {
					t.Fatalf("construction leaked private input %q: %v", private, err)
				}
			}
		})
	}
}

// A host key mismatch and a refused login stop the tunnel: each reaches the caller
// unchanged and signals FatalExit exactly once. An ordinary failure of one connection does
// not stop it.
func TestSSHFatalExitSignalsOnce(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int32
	}{
		{"host key mismatch", &exit.HostKeyError{Got: sshTestHostKey, Want: sshTestHostKey}, 1},
		{"login refusal", &exit.LoginError{}, 1},
		{"ordinary failure", errors.New("synthetic channel failure"), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var signals atomic.Int32
			f := &fatalExit{dialer: appDialerFunc(func(context.Context, string) (net.Conn, error) {
				return nil, tc.err
			}), fatal: func(err error) {
				if err != tc.err {
					t.Error("fatal signal lost the error")
				}
				signals.Add(1)
			}}
			var wg sync.WaitGroup
			for range 64 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if _, err := f.Dial(context.Background(), "echo.example:7"); err != tc.err {
						t.Error("the wrapper lost the SSH error")
					}
				}()
			}
			wg.Wait()
			if got := signals.Load(); got != tc.want {
				t.Fatalf("fatal signals = %d, want %d", got, tc.want)
			}
		})
	}
}
