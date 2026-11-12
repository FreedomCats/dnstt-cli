package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/FreedomCats/dnstt-cli/internal/config"
)

// Synthetic values only: a documentation domain, documentation addresses and the shared
// synthetic key. No value here comes from a real link.
const importTestSSHPassword = "synthetic-ssh-password"

// importLinkText builds a synthetic link with the standard alphabet, the way the
// generators write it. Field 0 is the version; empty fields fill the gaps.
func importLinkText(fields map[int]string) string {
	values := map[int]string{0: strconv.Itoa(22)}
	highest := 0
	for i, v := range fields {
		values[i] = v
		if i > highest {
			highest = i
		}
	}
	parts := make([]string, highest+1)
	for i := range parts {
		parts[i] = values[i]
	}
	return "slipnet://" + base64.StdEncoding.EncodeToString([]byte(strings.Join(parts, "|")))
}

// importDnsttLink is a valid dnstt link with the given fields on top.
func importDnsttLink(over map[int]string) string {
	fields := map[int]string{
		1:  "dnstt",
		2:  "home",
		3:  "t.example.com",
		4:  "192.0.2.53:53:0",
		11: wizardTestPubkey,
	}
	for i, v := range over {
		fields[i] = v
	}
	return importLinkText(fields)
}

// importSSHLink is a valid password-based dnstt_ssh link with the given fields on top.
func importSSHLink(over map[int]string) string {
	fields := map[int]string{
		1:  "dnstt_ssh",
		2:  "home",
		3:  "t.example.com",
		4:  "192.0.2.53:53:0",
		11: wizardTestPubkey,
		15: "synthetic-ssh-user",
		16: importTestSSHPassword,
		23: "password",
	}
	for i, v := range over {
		fields[i] = v
	}
	return importLinkText(fields)
}

// withFakeStore replaces the store seam for one test.
func withFakeStore(t *testing.T, store *fakeSecretStore) {
	t.Helper()
	previous := newSecretStore
	newSecretStore = func() (config.SecretStore, error) { return store, nil }
	t.Cleanup(func() { newSecretStore = previous })
}

// withFailingSave replaces the save seam for one test.
func withFailingSave(t *testing.T, saveErr error) {
	t.Helper()
	previous := saveProfiles
	saveProfiles = func(*config.Profiles) error { return saveErr }
	t.Cleanup(func() { saveProfiles = previous })
}

// importEnv is an env for the direct importInteractive tests.
func importEnv() (*env, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	return &env{stdout: &out, stderr: &errOut, stdin: strings.NewReader("")}, &out, &errOut
}

func TestProfileImportStdinDnstt(t *testing.T) {
	path := isolatedConnectConfig(t)
	store := newFakeSecretStore()
	withFakeStore(t, store)

	input := importDnsttLink(map[int]string{
		2:  "home",
		12: "synthetic-user",
		13: wizardTestPassword,
		22: "udp",
		39: "120",
	}) + "\n"
	code, stdout, stderr := runCLIInput(t, input, "profile", "import")
	if code != exitOK {
		t.Fatalf("exit code %d, want %d; stderr:\n%s", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, `Profile "home" imported (exit socks5).`) {
		t.Fatalf("output does not report the import:\n%s", stdout)
	}

	profiles, err := config.LoadProfilesFrom(path)
	if err != nil {
		t.Fatalf("the saved profile does not load: %v", err)
	}
	if profiles.DefaultProfile != "home" {
		t.Fatalf("default_profile = %q, want home", profiles.DefaultProfile)
	}
	prof := profiles.Profile["home"]
	if prof == nil {
		t.Fatal("profile home not found")
	}
	if prof.Domain != "t.example.com" || prof.Pubkey != wizardTestPubkey {
		t.Fatalf("domain or key incomplete: %+v", prof)
	}
	if len(prof.Resolvers) != 1 || prof.Resolvers[0] != "udp:192.0.2.53:53" {
		t.Fatalf("resolvers = %v", prof.Resolvers)
	}
	if prof.PayloadSize != 120 {
		t.Fatalf("payload_size = %d, want 120", prof.PayloadSize)
	}
	if prof.Exit.Type != "socks5" || prof.Exit.User != "synthetic-user" || prof.Exit.PasswordRef != "file:home.password" {
		t.Fatalf("exit incomplete: %+v", prof.Exit)
	}
	if !prof.KillSwitchOn() || prof.Datapath != "" {
		t.Fatalf("kill-switch %v, datapath %q", prof.KillSwitchOn(), prof.Datapath)
	}
	if got := store.values["home.password"]; got != wizardTestPassword {
		t.Fatalf("the store did not get the password")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), wizardTestPassword) {
		t.Fatal("profiles.toml contains the password")
	}
	if !strings.Contains(string(raw), "password_ref") {
		t.Fatal("profiles.toml does not reference the secret")
	}

	code, show, showErr := runCLIInput(t, "", "profile", "show", "home")
	if code != exitOK {
		t.Fatalf("profile show failed: %d, %s", code, showErr)
	}
	if !strings.Contains(show, "stored in file:home.password") {
		t.Fatalf("profile show does not print the reference:\n%s", show)
	}
	if strings.Contains(show, wizardTestPassword) {
		t.Fatal("profile show prints the password")
	}
}

func TestProfileImportStdinSeveralLinks(t *testing.T) {
	path := isolatedConnectConfig(t)
	store := newFakeSecretStore()
	withFakeStore(t, store)

	input := strings.Join([]string{
		importDnsttLink(map[int]string{2: "first"}),
		"not a link",
		importDnsttLink(map[int]string{2: "second"}),
	}, "\n") + "\n"
	code, stdout, stderr := runCLIInput(t, input, "profile", "import")
	if code != exitOK {
		t.Fatalf("exit code %d, want %d; stderr:\n%s", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, `Profile "first" imported`) || !strings.Contains(stdout, `Profile "second" imported`) {
		t.Fatalf("output does not report both imports:\n%s", stdout)
	}
	if !strings.Contains(stderr, "line 2") || !strings.Contains(stderr, "not a slipnet:// link") {
		t.Fatalf("stderr does not warn about line 2:\n%s", stderr)
	}

	profiles, err := config.LoadProfilesFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles.Profile) != 2 || profiles.Profile["first"] == nil || profiles.Profile["second"] == nil {
		t.Fatalf("saved profiles = %v", profiles.Profile)
	}
}

func TestProfileImportStdinSameNameTwice(t *testing.T) {
	path := isolatedConnectConfig(t)
	store := newFakeSecretStore()
	withFakeStore(t, store)

	input := importDnsttLink(nil) + "\n" + importDnsttLink(map[int]string{3: "other.example.com"}) + "\n"
	code, _, stderr := runCLIInput(t, input, "profile", "import")
	if code != exitOK {
		t.Fatalf("exit code %d, want %d; stderr:\n%s", code, exitOK, stderr)
	}
	if !strings.Contains(stderr, `profile "home" already exists; the link was skipped`) {
		t.Fatalf("the second link was not skipped with a warning:\n%s", stderr)
	}

	profiles, err := config.LoadProfilesFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if profiles.Profile["home"].Domain != "t.example.com" {
		t.Fatalf("the second link overwrote the first: %+v", profiles.Profile["home"])
	}
}

func TestProfileImportStdinSSHPassword(t *testing.T) {
	path := isolatedConnectConfig(t)
	store := newFakeSecretStore()
	withFakeStore(t, store)

	code, stdout, stderr := runCLIInput(t, importSSHLink(map[int]string{2: "ssh-home"})+"\n", "profile", "import")
	if code != exitOK {
		t.Fatalf("exit code %d, want %d; stderr:\n%s", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, `Profile "ssh-home" imported (exit ssh).`) {
		t.Fatalf("output does not report the SSH import:\n%s", stdout)
	}
	if !strings.Contains(stdout, "06-ssh-exit") {
		t.Fatalf("output does not say when the profile connects:\n%s", stdout)
	}

	profiles, err := config.LoadProfilesFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	prof := profiles.Profile["ssh-home"]
	if prof == nil {
		t.Fatal("profile ssh-home not found")
	}
	if prof.Exit.Type != "ssh" || prof.Exit.User != "synthetic-ssh-user" || prof.Exit.PasswordRef != "file:ssh-home.password" {
		t.Fatalf("exit incomplete: %+v", prof.Exit)
	}
	if got := store.values["ssh-home.password"]; got != importTestSSHPassword {
		t.Fatal("the store did not get the SSH password")
	}
}

func TestProfileImportStdinSSHKeyRefused(t *testing.T) {
	path := isolatedConnectConfig(t)
	store := newFakeSecretStore()
	withFakeStore(t, store)

	const keyMaterial = "synthetic-private-key-material"
	link := importSSHLink(map[int]string{
		2:  "ssh-home",
		23: "key",
		24: base64.StdEncoding.EncodeToString([]byte(keyMaterial)),
	})
	code, _, stderr := runCLIInput(t, link+"\n", "profile", "import")
	if code != exitError {
		t.Fatalf("exit code %d, want %d; stderr:\n%s", code, exitError, stderr)
	}
	if !strings.Contains(stderr, "06-ssh-exit") {
		t.Fatalf("the refusal does not name the stage:\n%s", stderr)
	}
	if strings.Contains(stderr, importTestSSHPassword) || strings.Contains(stderr, keyMaterial) {
		t.Fatalf("the refusal repeats the link's values:\n%s", stderr)
	}
	if store.puts != 0 || len(store.values) != 0 {
		t.Fatal("the refused link wrote a secret")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the refused link wrote profiles.toml: %v", err)
	}
}

func TestProfileImportStdinTakenName(t *testing.T) {
	path := isolatedConnectConfig(t)
	store := newFakeSecretStore()
	withFakeStore(t, store)

	if err := config.SaveProfilesTo(path, &config.Profiles{
		DefaultProfile: "home",
		Profile: map[string]*config.Profile{"home": {
			Domain:    "old.example.com",
			Pubkey:    wizardTestPubkey,
			Resolvers: []string{"udp:192.0.2.1:53"},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := runCLIInput(t, importDnsttLink(nil)+"\n", "profile", "import")
	if code != exitError {
		t.Fatalf("exit code %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr, `profile "home" already exists; the link was skipped`) {
		t.Fatalf("no warning about the taken name:\n%s", stderr)
	}
	if !strings.Contains(stderr, "no profile was imported") {
		t.Fatalf("the command did not report that nothing was imported:\n%s", stderr)
	}
	if store.puts != 0 {
		t.Fatal("the skipped link wrote a secret")
	}

	profiles, err := config.LoadProfilesFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if profiles.Profile["home"].Domain != "old.example.com" {
		t.Fatalf("the taken profile was overwritten: %+v", profiles.Profile["home"])
	}
}

func TestProfileImportPutFailure(t *testing.T) {
	path := isolatedConnectConfig(t)
	store := newFakeSecretStore()
	store.putErr = errors.New("synthetic store failure")
	withFakeStore(t, store)

	link := importDnsttLink(map[int]string{12: "synthetic-user", 13: wizardTestPassword})
	code, _, stderr := runCLIInput(t, link+"\n", "profile", "import")
	if code != exitError {
		t.Fatalf("exit code %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr, "cannot store the exit password") || !strings.Contains(stderr, "synthetic store failure") {
		t.Fatalf("stderr does not report the store failure:\n%s", stderr)
	}
	if strings.Contains(stderr, wizardTestPassword) {
		t.Fatal("the error contains the password")
	}
	if store.deletes != 0 {
		t.Fatal("a failed Put led to Delete")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a failed Put saved a profile: %v", err)
	}
}

func TestProfileImportSaveFailure(t *testing.T) {
	path := isolatedConnectConfig(t)
	store := newFakeSecretStore()
	withFakeStore(t, store)
	withFailingSave(t, errors.New("synthetic save failure"))

	link := importDnsttLink(map[int]string{12: "synthetic-user", 13: wizardTestPassword})
	code, _, stderr := runCLIInput(t, link+"\n", "profile", "import")
	if code != exitError {
		t.Fatalf("exit code %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr, "synthetic save failure") || !strings.Contains(stderr, "removed from the secret store") {
		t.Fatalf("stderr does not report the save failure and the rollback:\n%s", stderr)
	}
	if strings.Contains(stderr, wizardTestPassword) {
		t.Fatal("the error contains the password")
	}
	if store.deletes != 1 || len(store.deleted) != 1 || store.deleted[0] != "file:home.password" {
		t.Fatalf("deletes = %v, want the one reference", store.deleted)
	}
	if len(store.values) != 0 {
		t.Fatal("the secret stayed in the store")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a failed save left a file: %v", err)
	}
}

func TestProfileImportEmptyAndNoLink(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  string
	}{
		{"empty input", "", "no slipnet:// link in the input"},
		{"no link in the input", "not a link\n", "no profile was imported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := isolatedConnectConfig(t)
			store := newFakeSecretStore()
			withFakeStore(t, store)

			code, _, stderr := runCLIInput(t, tc.input, "profile", "import")
			if code != exitError {
				t.Fatalf("exit code %d, want %d", code, exitError)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Fatalf("stderr does not contain %q:\n%s", tc.want, stderr)
			}
			if store.puts != 0 {
				t.Fatal("nothing was imported, but the store was written")
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("nothing was imported, but a file exists: %v", err)
			}
		})
	}
}

func TestProfileImportTakesNoArguments(t *testing.T) {
	isolatedConnectConfig(t)
	code, _, stderr := runCLIInput(t, "", "profile", "import", "slipnet://example")
	if code != exitUsage {
		t.Fatalf("exit code %d, want %d; stderr:\n%s", code, exitUsage, stderr)
	}
	if !strings.Contains(stderr, "takes no arguments") {
		t.Fatalf("stderr does not report the extra argument:\n%s", stderr)
	}
}

func TestProfileImportInteractive(t *testing.T) {
	path := isolatedConnectConfig(t)
	store := newFakeSecretStore()
	withFakeStore(t, store)

	e, out, _ := importEnv()
	p := &scriptedPrompter{t: t, answers: []scriptedAnswer{
		{secret: true, value: importDnsttLink(map[int]string{12: "synthetic-user", 13: wizardTestPassword})},
		{secret: true, value: ""},
	}}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if err := importInteractive(context.Background(), e, profiles, store, p); err != nil {
		t.Fatalf("importInteractive = %v", err)
	}
	if !strings.Contains(out.String(), `Profile "home" imported (exit socks5).`) {
		t.Fatalf("output does not report the import:\n%s", out.String())
	}
	if strings.Join(p.asked, " ") != "slipnet:// link:  Another link (Enter to finish): " {
		t.Fatalf("questions = %q", p.asked)
	}
	if strings.Contains(strings.Join(p.asked, " "), "Profile name") {
		t.Fatal("the name question was asked although field 2 is usable")
	}

	saved, err := config.LoadProfilesFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Profile["home"] == nil {
		t.Fatal("profile home not saved")
	}
	if !strings.Contains(string(mustRead(t, path)), "password_ref") {
		t.Fatal("the saved profile has no password reference")
	}
}

func TestProfileImportInteractiveAsksName(t *testing.T) {
	path := isolatedConnectConfig(t)
	store := newFakeSecretStore()
	withFakeStore(t, store)

	e, _, _ := importEnv()
	p := &scriptedPrompter{t: t, answers: []scriptedAnswer{
		{secret: true, value: importDnsttLink(map[int]string{2: ""})},
		{value: "term-home"},
		{secret: true, value: ""},
	}}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if err := importInteractive(context.Background(), e, profiles, store, p); err != nil {
		t.Fatalf("importInteractive = %v", err)
	}
	if !strings.Contains(strings.Join(p.asked, " "), "Profile name [home]: ") {
		t.Fatalf("the name was not asked: %q", p.asked)
	}

	saved, err := config.LoadProfilesFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Profile["term-home"] == nil {
		t.Fatal("the chosen name was not used")
	}
}

func TestProfileImportInteractiveTakenName(t *testing.T) {
	path := isolatedConnectConfig(t)
	store := newFakeSecretStore()
	withFakeStore(t, store)

	if err := config.SaveProfilesTo(path, &config.Profiles{
		DefaultProfile: "home",
		Profile: map[string]*config.Profile{"home": {
			Domain:    "old.example.com",
			Pubkey:    wizardTestPubkey,
			Resolvers: []string{"udp:192.0.2.1:53"},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	e, _, _ := importEnv()
	p := &scriptedPrompter{t: t, answers: []scriptedAnswer{
		{secret: true, value: importDnsttLink(nil)},
		{value: "second"},
		{secret: true, value: ""},
	}}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if err := importInteractive(context.Background(), e, profiles, store, p); err != nil {
		t.Fatalf("importInteractive = %v", err)
	}
	if !strings.Contains(strings.Join(p.asked, " "), "Profile name [home]: ") {
		t.Fatalf("the name was not asked after a taken one: %q", p.asked)
	}

	saved, err := config.LoadProfilesFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Profile["home"].Domain != "old.example.com" {
		t.Fatalf("the taken profile was overwritten: %+v", saved.Profile["home"])
	}
	if saved.Profile["second"] == nil {
		t.Fatal("the chosen name was not imported")
	}
}

func TestProfileImportInteractiveRetriesBadLink(t *testing.T) {
	path := isolatedConnectConfig(t)
	store := newFakeSecretStore()
	withFakeStore(t, store)

	e, out, _ := importEnv()
	p := &scriptedPrompter{t: t, answers: []scriptedAnswer{
		{secret: true, value: "not a link"},
		{secret: true, value: importDnsttLink(nil)},
		{secret: true, value: ""},
	}}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if err := importInteractive(context.Background(), e, profiles, store, p); err != nil {
		t.Fatalf("importInteractive = %v", err)
	}
	if !strings.Contains(out.String(), "not a slipnet:// link") {
		t.Fatalf("the parse error was not reported:\n%s", out.String())
	}
	if len(p.asked) != 3 || p.asked[0] != p.asked[1] {
		t.Fatalf("the question did not repeat after the bad link: %q", p.asked)
	}
	saved, err := config.LoadProfilesFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Profile["home"] == nil {
		t.Fatal("the good link was not imported")
	}
}

func TestProfileImportInteractiveEOF(t *testing.T) {
	isolatedConnectConfig(t)
	store := newFakeSecretStore()
	withFakeStore(t, store)

	e, _, _ := importEnv()
	p := &scriptedPrompter{t: t}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if err := importInteractive(context.Background(), e, profiles, store, p); !errors.Is(err, errCancelled) {
		t.Fatalf("importInteractive = %v, want errCancelled", err)
	}
	if store.puts != 0 {
		t.Fatal("the end of input wrote a secret")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
