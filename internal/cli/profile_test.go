package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FreedomCats/dnstt-cli/core/dns"
	"github.com/FreedomCats/dnstt-cli/core/server"
	"github.com/FreedomCats/dnstt-cli/internal/config"
)

// runCLIInput runs the command line with the given standard input.
func runCLIInput(t *testing.T, input string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	e := &env{
		stdout: &out,
		stderr: &errOut,
		stdin:  strings.NewReader(input),
		build:  Build{Version: "dev", Commit: "unknown"},
	}
	code = run(context.Background(), e, args)
	return code, out.String(), errOut.String()
}

func TestProfileAddEndToEnd(t *testing.T) {
	path := isolatedConnectConfig(t)
	const password = "synthetic-exit-password"
	input := strings.Join([]string{
		"", // name: Enter takes home
		"t.example.com",
		wizardTestPubkey,
		"socks5",
		"synthetic-user",
		password,
		password,
		"udp:192.0.2.1:53",
		"", // finish the resolvers
		"socks",
	}, "\n") + "\n"

	code, stdout, stderr := runCLIInput(t, input, "profile", "add")
	if code != exitOK {
		t.Fatalf("exit code %d, want %d; stderr:\n%s", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, `Profile "home" added (exit socks5, mode socks).`) {
		t.Fatalf("output does not report the new profile:\n%s", stdout)
	}

	profiles, err := config.LoadProfilesFrom(path)
	if err != nil {
		t.Fatalf("the saved profile does not load: %v", err)
	}
	prof := profiles.Profile["home"]
	if prof == nil {
		t.Fatal("profile home not found")
	}
	if profiles.DefaultProfile != "home" {
		t.Fatalf("default_profile = %q, want home", profiles.DefaultProfile)
	}
	if prof.Domain != "t.example.com" || prof.Pubkey != wizardTestPubkey {
		t.Fatalf("domain or key incomplete: %+v", prof)
	}
	if len(prof.Resolvers) != 1 || prof.Resolvers[0] != "udp:192.0.2.1:53" {
		t.Fatalf("resolvers = %v", prof.Resolvers)
	}
	if prof.Datapath != "socks" || !prof.KillSwitchOn() {
		t.Fatalf("datapath %q, kill-switch %v", prof.Datapath, prof.KillSwitchOn())
	}
	if prof.Exit.Type != "socks5" || prof.Exit.User != "synthetic-user" || prof.Exit.PasswordRef == "" {
		t.Fatalf("exit incomplete: %+v", prof.Exit)
	}

	secret, err := config.NewSecretStoreFor(path).Get(prof.Exit.PasswordRef)
	if err != nil {
		t.Fatalf("the password is not in the secret store: %v", err)
	}
	if secret != password {
		t.Fatalf("stored password = %q", secret)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), password) {
		t.Fatal("profiles.toml contains the password")
	}
	if !strings.Contains(string(raw), "password_ref") {
		t.Fatal("profiles.toml does not reference the secret")
	}
}

func TestProfileAddCancelWritesNothing(t *testing.T) {
	path := isolatedConnectConfig(t)
	if err := config.SaveProfilesTo(path, &config.Profiles{
		DefaultProfile: "old",
		Profile: map[string]*config.Profile{"old": {
			Domain: "old.example.com", Pubkey: wizardTestPubkey,
			Resolvers: []string{"network"}, Exit: config.Exit{Type: "socks5"},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := config.NewSecretStoreFor(path).Put("old.password", "synthetic-old-password"); err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(filepath.Dir(path), config.SecretsFileName)
	beforeProfiles, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeSecrets, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatal(err)
	}

	for _, input := range []string{
		"",                  // no answers at all
		"\nt.example.com\n", // stop after the domain
	} {
		code, _, stderr := runCLIInput(t, input, "profile", "add")
		if code != exitError {
			t.Fatalf("exit code %d, want %d; stderr:\n%s", code, exitError, stderr)
		}
		if !strings.Contains(stderr, "cancelled") {
			t.Fatalf("stderr does not report the cancellation:\n%s", stderr)
		}

		afterProfiles, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		afterSecrets, err := os.ReadFile(secretPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(beforeProfiles, afterProfiles) {
			t.Fatal("a cancelled run changed profiles.toml")
		}
		if !bytes.Equal(beforeSecrets, afterSecrets) {
			t.Fatal("a cancelled run changed the secret store")
		}
	}
}

func TestProfileAddDuplicateName(t *testing.T) {
	path := isolatedConnectConfig(t)
	if err := config.SaveProfilesTo(path, &config.Profiles{
		DefaultProfile: "home",
		Profile: map[string]*config.Profile{"home": {
			Domain: "t.example.com", Pubkey: wizardTestPubkey,
			Resolvers: []string{"network"}, Exit: config.Exit{Type: "socks5"},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	code, _, stderr := runCLIInput(t, "", "profile", "add", "home")
	if code != exitError {
		t.Fatalf("exit code %d, want %d; stderr:\n%s", code, exitError, stderr)
	}
	if !strings.Contains(stderr, "already exists") {
		t.Fatalf("stderr does not name the collision:\n%s", stderr)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("the refused run changed profiles.toml")
	}
}

func TestProfileAddRefusesInvalidProfilesFile(t *testing.T) {
	path := isolatedConnectConfig(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `[profile.home]
pubkey = "` + wizardTestPubkey + `"
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := runCLIInput(t, "", "profile", "add")
	if code != exitError {
		t.Fatalf("exit code %d, want %d; stderr:\n%s", code, exitError, stderr)
	}
	if !strings.Contains(stderr, "domain is required") {
		t.Fatalf("stderr does not report the invalid profile:\n%s", stderr)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != raw {
		t.Fatal("an invalid profiles.toml was overwritten")
	}
}

func TestProfileAddUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"extra arguments", []string{"profile", "add", "one", "two"}, "at most one"},
		{"bad name", []string{"profile", "add", "bad/name"}, "invalid profile name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolatedConnectConfig(t)
			code, _, stderr := runCLIInput(t, "", tc.args...)
			if code != exitUsage {
				t.Fatalf("exit code %d, want %d; stderr:\n%s", code, exitUsage, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Fatalf("stderr does not say %q:\n%s", tc.want, stderr)
			}
		})
	}
}

func TestProfileHelpDescribesWizard(t *testing.T) {
	code, out, _ := runCLI(t, "profile", "-h")
	if code != exitOK {
		t.Fatalf("exit code %d, want %d", code, exitOK)
	}
	for _, want := range []string{"add [name]", "wizard", "socks5", "http", "secret store", "its stored exit password"} {
		if !strings.Contains(out, want) {
			t.Errorf("help does not mention %q:\n%s", want, out)
		}
	}
}

// twoProfileFixture writes profiles.toml with an authenticated default "home" whose exit
// password is in the store, and a no-auth "work". It returns the profiles path and the
// reference; the scheme is the platform's, so the reference is what Put returned.
func twoProfileFixture(t *testing.T) (path, ref string) {
	t.Helper()
	path = isolatedConnectConfig(t)
	var err error
	ref, err = config.NewSecretStoreFor(path).Put("home.password", wizardTestPassword)
	if err != nil {
		t.Fatalf("store.Put: %v", err)
	}
	if err := config.SaveProfilesTo(path, &config.Profiles{
		DefaultProfile: "home",
		Profile: map[string]*config.Profile{
			"home": {
				Domain:    "t.example.com",
				Pubkey:    wizardTestPubkey,
				Resolvers: []string{"network"},
				Exit:      config.Exit{Type: "socks5", User: "synthetic-user", PasswordRef: ref},
			},
			"work": {
				Domain:    "w.example.com",
				Pubkey:    wizardTestPubkey,
				Resolvers: []string{"network"},
				Exit:      config.Exit{Type: "socks5"},
			},
		},
	}); err != nil {
		t.Fatalf("SaveProfilesTo: %v", err)
	}
	return path, ref
}

// assertRemovalLeftProfilesUnchanged checks that a failed removal left profiles.toml
// byte-identical, no temporary file behind and no password in the message.
func assertRemovalLeftProfilesUnchanged(t *testing.T, path string, before []byte, stderr string) {
	t.Helper()
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("profiles.toml: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("a failed removal changed profiles.toml")
	}
	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("profiles.toml.tmp was left behind: %v", err)
	}
	if strings.Contains(stderr, wizardTestPassword) {
		t.Fatal("stderr contains the stored password")
	}
}

func TestProfileRmDeletesSecret(t *testing.T) {
	path, ref := twoProfileFixture(t)

	code, stdout, stderr := runCLIInput(t, "", "profile", "rm", "home")
	if code != exitOK {
		t.Fatalf("exit code %d, want %d; stderr:\n%s", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, `Profile "home" removed.`) {
		t.Fatalf("stdout does not report the removal:\n%s", stdout)
	}

	profiles, err := config.LoadProfilesFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := profiles.Profile["home"]; ok {
		t.Fatal("profile home is still in profiles.toml")
	}
	if profiles.DefaultProfile != "work" {
		t.Fatalf("default_profile = %q, want work", profiles.DefaultProfile)
	}
	if _, err := config.NewSecretStoreFor(path).Get(ref); err == nil {
		t.Fatal("the exit password is still in the secret store")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(path), config.SecretsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "home.password") {
		t.Fatal("secrets.json still names the removed profile's secret")
	}
}

func TestProfileRmWithoutPasswordRefLeavesStoreClosed(t *testing.T) {
	path := isolatedConnectConfig(t)
	if err := config.SaveProfilesTo(path, &config.Profiles{
		DefaultProfile: "work",
		Profile: map[string]*config.Profile{"work": {
			Domain: "w.example.com", Pubkey: wizardTestPubkey,
			Resolvers: []string{"network"}, Exit: config.Exit{Type: "socks5"},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	calls := 0
	old := newSecretStore
	newSecretStore = func() (config.SecretStore, error) {
		calls++
		return newFakeSecretStore(), nil
	}
	t.Cleanup(func() { newSecretStore = old })

	code, _, stderr := runCLIInput(t, "", "profile", "rm", "work")
	if code != exitOK {
		t.Fatalf("exit code %d, want %d; stderr:\n%s", code, exitOK, stderr)
	}
	if calls != 0 {
		t.Fatalf("the secret store constructor was called %d times, want 0", calls)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("profiles.toml still exists after removing the last profile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), config.SecretsFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("secrets.json exists: %v", err)
	}
}

func TestProfileRmMissingProfileFailsBeforeStore(t *testing.T) {
	path, ref := twoProfileFixture(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	calls := 0
	old := newSecretStore
	newSecretStore = func() (config.SecretStore, error) {
		calls++
		return newFakeSecretStore(), nil
	}
	t.Cleanup(func() { newSecretStore = old })

	code, _, stderr := runCLIInput(t, "", "profile", "rm", "missing")
	if code != exitError {
		t.Fatalf("exit code %d, want %d; stderr:\n%s", code, exitError, stderr)
	}
	if !strings.Contains(stderr, "not found") {
		t.Fatalf("stderr does not report the missing profile:\n%s", stderr)
	}
	if calls != 0 {
		t.Fatalf("the secret store constructor was called %d times, want 0", calls)
	}
	assertRemovalLeftProfilesUnchanged(t, path, before, stderr)

	got, err := config.NewSecretStoreFor(path).Get(ref)
	if err != nil || got != wizardTestPassword {
		t.Fatalf("the secret was disturbed: %q, %v", got, err)
	}
}

func TestProfileRmKeepsProfileWhenSecretDeleteFails(t *testing.T) {
	t.Run("cannot open the store", func(t *testing.T) {
		path, _ := twoProfileFixture(t)
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		old := newSecretStore
		newSecretStore = func() (config.SecretStore, error) {
			return nil, errors.New("synthetic store failure")
		}
		t.Cleanup(func() { newSecretStore = old })

		code, _, stderr := runCLIInput(t, "", "profile", "rm", "home")
		if code != exitError {
			t.Fatalf("exit code %d, want %d; stderr:\n%s", code, exitError, stderr)
		}
		if !strings.Contains(stderr, "was not removed") || !strings.Contains(stderr, "cannot open the secret store") {
			t.Fatalf("stderr does not report the store failure:\n%s", stderr)
		}
		assertRemovalLeftProfilesUnchanged(t, path, before, stderr)
	})

	t.Run("delete fails", func(t *testing.T) {
		path, ref := twoProfileFixture(t)
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		fake := newFakeSecretStore()
		fake.deleteErr = errors.New("synthetic delete failure")
		old := newSecretStore
		newSecretStore = func() (config.SecretStore, error) { return fake, nil }
		t.Cleanup(func() { newSecretStore = old })

		code, _, stderr := runCLIInput(t, "", "profile", "rm", "home")
		if code != exitError {
			t.Fatalf("exit code %d, want %d; stderr:\n%s", code, exitError, stderr)
		}
		if !strings.Contains(stderr, "was not removed") ||
			!strings.Contains(stderr, "could not be deleted from the secret store") {
			t.Fatalf("stderr does not report the delete failure:\n%s", stderr)
		}
		if fake.deletes != 1 || len(fake.deleted) != 1 || fake.deleted[0] != ref {
			t.Fatalf("Delete calls = %d %v, want exactly %q", fake.deletes, fake.deleted, ref)
		}
		assertRemovalLeftProfilesUnchanged(t, path, before, stderr)
	})
}

func TestProfileRmReportsPartialStateWhenSaveFails(t *testing.T) {
	path, ref := twoProfileFixture(t)

	old := saveProfiles
	saveProfiles = func(*config.Profiles) error {
		return errors.New("synthetic save failure")
	}
	t.Cleanup(func() { saveProfiles = old })

	code, _, stderr := runCLIInput(t, "", "profile", "rm", "home")
	if code != exitError {
		t.Fatalf("exit code %d, want %d; stderr:\n%s", code, exitError, stderr)
	}
	for _, want := range []string{
		"synthetic save failure",
		"was not removed from profiles.toml, but its exit password was removed from the secret store",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr does not contain %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, wizardTestPassword) {
		t.Fatal("stderr contains the stored password")
	}

	profiles, err := config.LoadProfilesFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	home := profiles.Profile["home"]
	if home == nil {
		t.Fatal("the profile was removed although the save failed")
	}
	if home.Exit.PasswordRef != ref {
		t.Fatalf("password_ref = %q, want %q", home.Exit.PasswordRef, ref)
	}
	if _, err := config.NewSecretStoreFor(path).Get(ref); err == nil {
		t.Fatal("the exit password was not deleted")
	}
}

func TestProfileRmToleratesMissingSecret(t *testing.T) {
	path := isolatedConnectConfig(t)
	store := config.NewSecretStoreFor(path)
	ref, err := store.Put("gone.password", wizardTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ref); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveProfilesTo(path, &config.Profiles{
		DefaultProfile: "home",
		Profile: map[string]*config.Profile{"home": {
			Domain: "t.example.com", Pubkey: wizardTestPubkey,
			Resolvers: []string{"network"},
			Exit:      config.Exit{Type: "socks5", User: "synthetic-user", PasswordRef: ref},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := runCLIInput(t, "", "profile", "rm", "home")
	if code != exitOK {
		t.Fatalf("exit code %d, want %d; stderr:\n%s", code, exitOK, stderr)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("profiles.toml still exists after removing the last profile: %v", err)
	}
}

func TestProfileRmLastProfileRemovesFile(t *testing.T) {
	path := isolatedConnectConfig(t)
	store := config.NewSecretStoreFor(path)
	ref, err := store.Put("home.password", wizardTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.SaveProfilesTo(path, &config.Profiles{
		DefaultProfile: "home",
		Profile: map[string]*config.Profile{"home": {
			Domain: "t.example.com", Pubkey: wizardTestPubkey,
			Resolvers: []string{"network"},
			Exit:      config.Exit{Type: "socks5", User: "synthetic-user", PasswordRef: ref},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLIInput(t, "", "profile", "rm", "home")
	if code != exitOK {
		t.Fatalf("exit code %d, want %d; stderr:\n%s", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, `Profile "home" removed.`) {
		t.Fatalf("stdout does not report the removal:\n%s", stdout)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("profiles.toml still exists after removing the last profile: %v", err)
	}
	if _, err := store.Get(ref); err == nil {
		t.Fatal("the exit password is still in the secret store")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(path), config.SecretsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "home.password") {
		t.Fatal("secrets.json still names the removed profile's secret")
	}

	code, stdout, stderr = runCLIInput(t, "", "profile", "list")
	if code != exitOK {
		t.Fatalf("profile list exit code %d, want %d; stderr:\n%s", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, "No profiles configured.") {
		t.Fatalf("profile list does not report the empty state:\n%s", stdout)
	}

	code, _, stderr = runCLIInput(t, "", "connect")
	if code != exitError {
		t.Fatalf("connect exit code %d, want %d; stderr:\n%s", code, exitError, stderr)
	}
	if !strings.Contains(stderr, "no default profile set") {
		t.Fatalf("connect does not report the missing default:\n%s", stderr)
	}

	input := strings.Join([]string{
		"", // name: Enter takes home
		"t.example.com",
		wizardTestPubkey,
		"socks5",
		"", // login: no authentication
		"", // resolver: network
		"socks",
	}, "\n") + "\n"
	code, stdout, stderr = runCLIInput(t, input, "profile", "add")
	if code != exitOK {
		t.Fatalf("profile add exit code %d, want %d; stderr:\n%s", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, `Profile "home" added`) {
		t.Fatalf("profile add does not report the new profile:\n%s", stdout)
	}

	profiles, err := config.LoadProfilesFrom(path)
	if err != nil {
		t.Fatalf("the re-created profile does not load: %v", err)
	}
	if profiles.Profile["home"] == nil {
		t.Fatal("profile add did not create home from the empty state")
	}
	if profiles.DefaultProfile != "home" {
		t.Fatalf("default_profile = %q, want home", profiles.DefaultProfile)
	}
}

// httpConnectUpstream is an HTTP CONNECT proxy with Basic authentication in a loopback
// listener: after the 200 it echoes what it reads. It replaces a live exit in the test.
type httpConnectUpstream struct {
	net.Listener
	accepted atomic.Int32
}

func startHTTPConnectUpstream(t *testing.T, user, password string) *httpConnectUpstream {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	upstream := &httpConnectUpstream{Listener: ln}
	token := "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			upstream.accepted.Add(1)
			go func() {
				defer c.Close()
				c.SetDeadline(time.Now().Add(20 * time.Second))
				r := bufio.NewReader(c)
				req, err := http.ReadRequest(r)
				if err != nil {
					return
				}
				if req.Method != "CONNECT" || req.Header.Get("Proxy-Authorization") != token {
					io.WriteString(c, "HTTP/1.1 407 Proxy Authentication Required\r\n"+
						"Proxy-Authenticate: Basic realm=\"synthetic\"\r\n\r\n")
					return
				}
				if _, err := io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
					return
				}
				io.Copy(c, r)
			}()
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return upstream
}

// The profile is created by the wizard, then connect uses it end to end: the local SOCKS5
// listener carries one request through the tunnel to the HTTP CONNECT exit.
func TestProfileAddThenConnect(t *testing.T) {
	path := isolatedConnectConfig(t)
	const user, password = "synthetic-user", "synthetic-exit-password"
	upstream := startHTTPConnectUpstream(t, user, password)

	domain, err := dns.ParseName("t.example.com")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := server.StartTest(domain, upstream.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })

	input := strings.Join([]string{
		"", // name: home
		domain.String(),
		fmt.Sprintf("%x", srv.PubKey),
		"http",
		user,
		password,
		password,
		"udp:" + srv.Addr.String(),
		"", // finish the resolvers
		"socks",
	}, "\n") + "\n"
	code, _, stderr := runCLIInput(t, input, "profile", "add")
	if code != exitOK {
		t.Fatalf("profile add exit code %d, want %d; stderr:\n%s", code, exitOK, stderr)
	}

	profiles, err := config.LoadProfilesFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	home := profiles.Profile["home"]
	if home.Exit.Type != "http" || home.Domain != domain.String() {
		t.Fatal("the profile saved by the wizard is not the one being connected")
	}
	ref := home.Exit.PasswordRef
	if ref == "" {
		t.Fatal("the wizard did not store the exit password")
	}

	// The wizard leaves socks empty, which is the product default 127.0.0.1:1080; a client
	// already running there would make the test bind a busy port. Only the listener
	// changes: the rest of the profile is still what the wizard created.
	home.Socks = config.Socks{Listen: "127.0.0.1:0"}
	if err := config.SaveProfilesTo(path, profiles); err != nil {
		t.Fatal(err)
	}

	output := &connectOutput{lines: make(chan string, 100)}
	e := &env{stdout: io.Discard, stderr: output, stdin: strings.NewReader("")}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- run(ctx, e, []string{"connect", "-v"}) }()
	defer func() { cancel(); waitConnectResult(t, done) }()

	var address string
	for address == "" {
		select {
		case line := <-output.lines:
			if after, ok := strings.CutPrefix(line, "[SOCKS] listening on "); ok {
				address = strings.TrimSpace(after)
			}
		case <-ctx.Done():
			t.Fatal("connect did not open its local listener")
		}
	}

	var c net.Conn
	var reply byte
	for {
		c, reply = connectSOCKSRequest(t, address)
		if reply != 3 { // the tunnel is not ready yet
			break
		}
		c.Close()
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal("tunnel did not become ready")
		}
	}
	defer c.Close()
	if reply != 0 {
		t.Fatalf("CONNECT returned SOCKS5 code %d", reply)
	}
	msg := []byte("CLI application through the wizard profile")
	if _, err := c.Write(msg); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(c, got); err != nil || !bytes.Equal(got, msg) {
		t.Fatalf("application echo = %q, %v", got, err)
	}

	cancel()
	if code := waitConnectResult(t, done); code != exitOK {
		t.Fatalf("connect exit = %d, want %d", code, exitOK)
	}
	if upstream.accepted.Load() != 1 {
		t.Fatalf("HTTP exit streams = %d, want the one application request", upstream.accepted.Load())
	}
	for _, private := range []string{user, password, ref, path} {
		if strings.Contains(output.String(), private) {
			t.Fatalf("connect diagnostics exposed %q", private)
		}
	}
}
