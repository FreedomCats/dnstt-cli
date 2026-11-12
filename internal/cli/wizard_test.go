package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/FreedomCats/dnstt-cli/internal/config"
	"github.com/FreedomCats/dnstt-cli/internal/exit"
)

const (
	wizardTestPubkey   = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	wizardTestPassword = "synthetic-exit-password"
	// testKeyPassphrase is the passphrase of the generated encrypted key of these tests.
	testKeyPassphrase = "synthetic-key-passphrase"
)

// testKeys is one generated ed25519 key of a test, written into the test's temporary
// directory: the same key in PEM unencrypted and encrypted, the passphrase of the second
// and the fingerprint of the public part. No fixture and no key of the machine.
type testKeys struct {
	plainPath     string
	encryptedPath string
	plain         []byte
	encrypted     []byte
	passphrase    string
	fingerprint   string
}

func writeTestKeys(t *testing.T) testKeys {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	plainBlock, err := ssh.MarshalPrivateKey(priv, "synthetic-test-key")
	if err != nil {
		t.Fatal(err)
	}
	encryptedBlock, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "synthetic-test-key", []byte(testKeyPassphrase))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	keys := testKeys{
		plainPath:     filepath.Join(dir, "id_ed25519"),
		encryptedPath: filepath.Join(dir, "id_ed25519_encrypted"),
		plain:         pem.EncodeToMemory(plainBlock),
		encrypted:     pem.EncodeToMemory(encryptedBlock),
		passphrase:    testKeyPassphrase,
		fingerprint:   ssh.FingerprintSHA256(signer.PublicKey()),
	}
	if err := os.WriteFile(keys.plainPath, keys.plain, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keys.encryptedPath, keys.encrypted, 0o600); err != nil {
		t.Fatal(err)
	}
	return keys
}

// keyMarker is a recognizable run of the key's Base64 body: a message that echoed the key
// would carry it, while the PEM header lines alone are not key material.
func keyMarker(key string) string {
	for _, line := range strings.Split(key, "\n") {
		if len(line) >= 40 && !strings.HasPrefix(line, "-----") {
			return line[:40]
		}
	}
	return key
}

// scriptedAnswer is one answer of a scripted prompter, with the method the wizard must
// use to ask for it.
type scriptedAnswer struct {
	secret bool
	value  string
	err    error
}

// scriptedPrompter plays the person: a queue of answers and a record of the questions. A
// question asked with the other method than the answer was marked for fails the test; an
// empty queue is the end of input.
type scriptedPrompter struct {
	t       *testing.T
	answers []scriptedAnswer
	asked   []string
}

func (p *scriptedPrompter) Ask(_ context.Context, question string) (string, error) {
	return p.answer(false, question)
}

func (p *scriptedPrompter) AskSecret(_ context.Context, question string) (string, error) {
	return p.answer(true, question)
}

func (p *scriptedPrompter) answer(secret bool, question string) (string, error) {
	p.t.Helper()
	p.asked = append(p.asked, question)
	if len(p.answers) == 0 {
		return "", io.EOF
	}
	answer := p.answers[0]
	p.answers = p.answers[1:]
	if answer.secret != secret {
		p.t.Fatalf("question %q: asked with %s, the answer is marked %s",
			question, askedBy(secret), askedBy(answer.secret))
	}
	return answer.value, answer.err
}

func askedBy(secret bool) string {
	if secret {
		return "AskSecret"
	}
	return "Ask"
}

// fakeSecretStore is a SecretStore in memory, with the calls it saw.
type fakeSecretStore struct {
	values    map[string]string
	refs      map[string]string
	puts      int
	deletes   int
	deleted   []string
	putErr    error
	deleteErr error
}

func newFakeSecretStore() *fakeSecretStore {
	return &fakeSecretStore{
		values: make(map[string]string),
		refs:   make(map[string]string),
	}
}

func (s *fakeSecretStore) Put(name, value string) (string, error) {
	s.puts++
	if s.putErr != nil {
		return "", s.putErr
	}
	ref := "file:" + name
	s.values[name] = value
	s.refs[ref] = name
	return ref, nil
}

func (s *fakeSecretStore) Get(ref string) (string, error) {
	name, ok := s.refs[ref]
	if !ok {
		return "", errors.New("no such secret")
	}
	return s.values[name], nil
}

func (s *fakeSecretStore) Delete(ref string) error {
	s.deletes++
	s.deleted = append(s.deleted, ref)
	if s.deleteErr != nil {
		return s.deleteErr
	}
	name, ok := s.refs[ref]
	if !ok {
		return nil
	}
	delete(s.values, name)
	delete(s.refs, ref)
	return nil
}

// wizardTestHarness wires a wizard to a scripted prompter, a fake store and a save
// function that records what it was given.
type wizardTestHarness struct {
	wizard   *wizard
	store    *fakeSecretStore
	prompter *scriptedPrompter
	out      *bytes.Buffer
	profiles *config.Profiles
	saved    []*config.Profiles
	saveErr  error
}

func newWizardTestHarness(t *testing.T, answers []scriptedAnswer, modes []string, tunUnsupported error) *wizardTestHarness {
	t.Helper()
	h := &wizardTestHarness{
		store:    newFakeSecretStore(),
		prompter: &scriptedPrompter{t: t, answers: answers},
		out:      &bytes.Buffer{},
		profiles: &config.Profiles{Profile: make(map[string]*config.Profile)},
	}
	h.wizard = &wizard{
		ask:            h.prompter,
		out:            h.out,
		store:          h.store,
		modes:          modes,
		tunUnsupported: tunUnsupported,
		save: func(p *config.Profiles) error {
			if h.saveErr != nil {
				return h.saveErr
			}
			h.saved = append(h.saved, p)
			return nil
		},
	}
	return h
}

// baseWizardAnswers is a complete valid answer script for the given exit type: the name
// "home", the test domain and key, the exit's own questions, the "network" resolver and the
// first mode. "ssh-password" and "ssh-key" are the two shapes of an SSH exit; the second
// writes a generated key into the test's temporary directory.
func baseWizardAnswers(t *testing.T, exitType string) []scriptedAnswer {
	t.Helper()
	kind := exitType
	if strings.HasPrefix(exitType, "ssh-") {
		kind = "ssh"
	}
	answers := []scriptedAnswer{
		{value: "home"},
		{value: "t.example.com"},
		{value: wizardTestPubkey},
		{value: kind},
	}
	switch exitType {
	case "http":
		answers = append(answers,
			scriptedAnswer{value: "synthetic-user"},
			scriptedAnswer{secret: true, value: wizardTestPassword},
			scriptedAnswer{secret: true, value: wizardTestPassword},
		)
	case "ssh-password":
		answers = append(answers,
			scriptedAnswer{value: "synthetic-user"},
			scriptedAnswer{value: ""}, // method: password
			scriptedAnswer{secret: true, value: wizardTestPassword},
			scriptedAnswer{secret: true, value: wizardTestPassword},
			scriptedAnswer{value: cliTestHostKey},
		)
	case "ssh-key":
		keys := writeTestKeys(t)
		answers = append(answers,
			scriptedAnswer{value: "synthetic-user"},
			scriptedAnswer{value: "key"},
			scriptedAnswer{value: keys.encryptedPath},
			scriptedAnswer{secret: true, value: keys.passphrase},
			scriptedAnswer{value: cliTestHostKey},
		)
	default:
		answers = append(answers, scriptedAnswer{value: ""})
	}
	return append(answers,
		scriptedAnswer{value: ""}, // resolver: "network"
		scriptedAnswer{value: ""}, // mode: the first
	)
}

// insertAnswers puts extra answers before position at.
func insertAnswers(answers []scriptedAnswer, at int, extra ...scriptedAnswer) []scriptedAnswer {
	out := make([]scriptedAnswer, 0, len(answers)+len(extra))
	out = append(out, answers[:at]...)
	out = append(out, extra...)
	out = append(out, answers[at:]...)
	return out
}

func TestWizardSocks5NoAuth(t *testing.T) {
	answers := []scriptedAnswer{
		{value: "home"},
		{value: "t.example.com"},
		{value: wizardTestPubkey},
		{value: "socks5"},
		{value: ""}, // login: no authentication
		{value: "udp:192.0.2.1:53"},
		{value: ""}, // finish the resolvers
		{value: "socks"},
	}
	h := newWizardTestHarness(t, answers, []string{"tun", "socks"}, nil)

	name, prof, err := h.wizard.add(context.Background(), h.profiles, "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "home" {
		t.Fatalf("name = %q, want home", name)
	}
	if prof.Exit.Type != "socks5" || prof.Exit.User != "" || prof.Exit.PasswordRef != "" {
		t.Fatalf("exit = %+v, want a SOCKS5 exit without authentication", prof.Exit)
	}
	if h.store.puts != 0 || h.store.deletes != 0 {
		t.Fatalf("store calls: puts %d, deletes %d; want none", h.store.puts, h.store.deletes)
	}
	if len(h.saved) != 1 {
		t.Fatalf("saves = %d, want 1", len(h.saved))
	}
	saved := h.saved[0]
	if saved.DefaultProfile != "home" || saved.Profile["home"] != prof {
		t.Fatalf("saved profiles do not carry home as the default: %+v", saved)
	}
	if len(h.profiles.Profile) != 0 {
		t.Fatal("the caller's profiles were changed")
	}
	if prof.Domain != "t.example.com" || prof.Pubkey != wizardTestPubkey {
		t.Fatalf("domain or key wrong: %+v", prof)
	}
	if len(prof.Resolvers) != 1 || prof.Resolvers[0] != "udp:192.0.2.1:53" {
		t.Fatalf("resolvers = %v", prof.Resolvers)
	}
	if prof.Datapath != "socks" || !prof.KillSwitchOn() {
		t.Fatalf("datapath %q, kill-switch %v", prof.Datapath, prof.KillSwitchOn())
	}
	for _, question := range h.prompter.asked {
		if question == "Exit password: " || question == "Exit password again: " {
			t.Fatal("a SOCKS5 exit without a login asked for a password")
		}
	}
}

func TestWizardHTTPProfile(t *testing.T) {
	answers := []scriptedAnswer{
		{value: "work"},
		{value: "t.example.com"},
		{value: wizardTestPubkey},
		{value: "http"},
		{value: "synthetic-user"},
		{secret: true, value: wizardTestPassword},
		{secret: true, value: wizardTestPassword},
		{value: "doh-cloudflare"},
		{value: "udp:192.0.2.1:53"},
		{value: ""}, // finish the resolvers
		{value: "socks"},
	}
	h := newWizardTestHarness(t, answers, []string{"tun", "socks"}, nil)

	name, prof, err := h.wizard.add(context.Background(), h.profiles, "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "work" {
		t.Fatalf("name = %q, want work", name)
	}
	if prof.Exit.Type != "http" || prof.Exit.User != "synthetic-user" {
		t.Fatalf("exit = %+v", prof.Exit)
	}
	if prof.Exit.PasswordRef != "file:work.password" {
		t.Fatalf("password_ref = %q", prof.Exit.PasswordRef)
	}
	if got := h.store.values["work.password"]; got != wizardTestPassword {
		t.Fatalf("stored password = %q", got)
	}
	if len(prof.Resolvers) != 2 || prof.Resolvers[0] != "doh-cloudflare" || prof.Resolvers[1] != "udp:192.0.2.1:53" {
		t.Fatalf("resolvers = %v; the preset must stay short and the entry as typed", prof.Resolvers)
	}
	if prof.Datapath != "socks" {
		t.Fatalf("datapath = %q, want socks", prof.Datapath)
	}
	if len(h.saved) != 1 || h.saved[0].DefaultProfile != "work" {
		t.Fatal("the profile was not saved as the default")
	}
	if !strings.Contains(strings.Join(h.prompter.asked, "\n"), "Exit password again: ") {
		t.Fatal("the password was not asked twice")
	}
	if strings.Contains(h.out.String(), wizardTestPassword) {
		t.Fatal("the wizard printed the password")
	}
	for _, question := range h.prompter.asked {
		if strings.Contains(question, wizardTestPassword) {
			t.Fatal("the password reached a question")
		}
	}
}

// An SSH exit with a password: the method question, the password twice and the fingerprint,
// and the saved set is a profile that loads.
func TestWizardSSHPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.toml")
	h := newWizardTestHarness(t, baseWizardAnswers(t, "ssh-password"), []string{"tun", "socks"}, nil)

	name, prof, err := h.wizard.add(context.Background(), h.profiles, "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "home" {
		t.Fatalf("name = %q", name)
	}
	if prof.Exit.Type != "ssh" || prof.Exit.User != "synthetic-user" {
		t.Fatalf("exit = %+v", prof.Exit)
	}
	if prof.Exit.HostKey != cliTestHostKey {
		t.Fatalf("host_key = %q, want %q", prof.Exit.HostKey, cliTestHostKey)
	}
	if prof.Exit.PasswordRef != "file:home.password" || prof.Exit.KeyRef != "" {
		t.Fatalf("references = %+v; want only the password", prof.Exit)
	}
	if got := h.store.values["home.password"]; got != wizardTestPassword {
		t.Fatalf("stored password = %q", got)
	}
	asked := strings.Join(h.prompter.asked, "\n")
	if !strings.Contains(asked, "SSH authentication (password, key) [password]: ") ||
		!strings.Contains(asked, "Pinned host key fingerprint") {
		t.Fatalf("the SSH questions were not asked:\n%s", asked)
	}

	// The wizard's profile is a profile: it saves and loads through internal/config.
	if len(h.saved) != 1 {
		t.Fatalf("saves = %d, want 1", len(h.saved))
	}
	if err := config.SaveProfilesTo(path, h.saved[0]); err != nil {
		t.Fatalf("the wizard's profile does not save: %v", err)
	}
	loaded, err := config.LoadProfilesFrom(path)
	if err != nil {
		t.Fatalf("the wizard's profile does not load: %v", err)
	}
	if got := loaded.Profile["home"].Exit; got.Type != "ssh" || got.PasswordRef != "file:home.password" ||
		got.HostKey != cliTestHostKey {
		t.Fatalf("the loaded exit = %+v", got)
	}

	if strings.Contains(h.out.String(), wizardTestPassword) {
		t.Fatal("the wizard printed the password")
	}
	for _, question := range h.prompter.asked {
		if strings.Contains(question, wizardTestPassword) {
			t.Fatal("the password reached a question")
		}
	}
}

// An SSH exit with a private key: the key file is read and decrypted with its passphrase,
// and the store keeps the plain PEM under <profile>.key.
func TestWizardSSHKey(t *testing.T) {
	keys := writeTestKeys(t)

	t.Run("an encrypted key", func(t *testing.T) {
		answers := []scriptedAnswer{
			{value: "home"},
			{value: "t.example.com"},
			{value: wizardTestPubkey},
			{value: "ssh"},
			{value: "synthetic-user"},
			{value: "key"},
			{value: keys.encryptedPath},
			{secret: true, value: "the wrong passphrase"}, // the reason is printed, the path re-asked
			{value: keys.encryptedPath},
			{secret: true, value: keys.passphrase},
			{value: cliTestHostKey},
			{value: ""}, // resolver: network
			{value: ""}, // mode: the first
		}
		h := newWizardTestHarness(t, answers, []string{"tun", "socks"}, nil)

		_, prof, err := h.wizard.add(context.Background(), h.profiles, "")
		if err != nil {
			t.Fatal(err)
		}
		if prof.Exit.Type != "ssh" || prof.Exit.KeyRef != "file:home.key" {
			t.Fatalf("exit = %+v", prof.Exit)
		}
		if prof.Exit.PasswordRef != "" {
			t.Fatalf("a key exit gained password_ref %q", prof.Exit.PasswordRef)
		}
		stored := h.store.values["home.key"]
		if stored == "" {
			t.Fatal("the key was not stored under home.key")
		}
		if _, err := exit.ParseKey([]byte(stored), ""); err != nil {
			t.Fatalf("the stored key does not parse without a passphrase: %v", err)
		}
		if !strings.Contains(h.out.String(), "the passphrase is wrong or the key is damaged") {
			t.Fatalf("the wrong passphrase was not explained:\n%s", h.out.String())
		}
		asked := strings.Join(h.prompter.asked, "\n")
		if got := strings.Count(asked, "Key passphrase: "); got != 2 {
			t.Fatalf("the passphrase question was asked %d times, want 2:\n%s", got, asked)
		}
		assertKeyNotLeaked(t, h, stored)
	})

	t.Run("an unencrypted key", func(t *testing.T) {
		answers := []scriptedAnswer{
			{value: "home"},
			{value: "t.example.com"},
			{value: wizardTestPubkey},
			{value: "ssh"},
			{value: "synthetic-user"},
			{value: "key"},
			{value: keys.plainPath},
			{value: cliTestHostKey},
			{value: ""}, // resolver: network
			{value: ""}, // mode: the first
		}
		h := newWizardTestHarness(t, answers, []string{"tun", "socks"}, nil)

		_, prof, err := h.wizard.add(context.Background(), h.profiles, "")
		if err != nil {
			t.Fatal(err)
		}
		if prof.Exit.KeyRef != "file:home.key" {
			t.Fatalf("key_ref = %q", prof.Exit.KeyRef)
		}
		for _, question := range h.prompter.asked {
			if strings.Contains(question, "passphrase") {
				t.Fatalf("an unencrypted key asked for a passphrase: %q", h.prompter.asked)
			}
		}
		raw, err := os.ReadFile(keys.plainPath)
		if err != nil {
			t.Fatal(err)
		}
		if got := h.store.values["home.key"]; got != string(raw) {
			t.Fatal("the store did not keep the file's PEM unchanged")
		}
		assertKeyNotLeaked(t, h, h.store.values["home.key"])
	})
}

// A key path that cannot be read prints the reason without the path and asks again.
func TestWizardSSHKeyMissingFile(t *testing.T) {
	keys := writeTestKeys(t)
	const marker = "synthetic-key-path-marker"
	missing := filepath.Join(t.TempDir(), marker)
	answers := []scriptedAnswer{
		{value: "home"},
		{value: "t.example.com"},
		{value: wizardTestPubkey},
		{value: "ssh"},
		{value: "synthetic-user"},
		{value: "key"},
		{value: missing},
		{value: keys.plainPath},
		{value: cliTestHostKey},
		{value: ""}, // resolver: network
		{value: ""}, // mode: the first
	}
	h := newWizardTestHarness(t, answers, []string{"tun", "socks"}, nil)

	_, prof, err := h.wizard.add(context.Background(), h.profiles, "")
	if err != nil {
		t.Fatal(err)
	}
	if prof.Exit.KeyRef != "file:home.key" {
		t.Fatalf("key_ref = %q", prof.Exit.KeyRef)
	}
	if !strings.Contains(h.out.String(), "the private key file cannot be read") {
		t.Fatalf("the read failure was not explained:\n%s", h.out.String())
	}
	if strings.Contains(h.out.String(), marker) {
		t.Fatalf("the message names the path of the file:\n%s", h.out.String())
	}
	if got := strings.Count(strings.Join(h.prompter.asked, "\n"), "Private key file: "); got != 2 {
		t.Fatalf("the path question was asked %d times, want 2", got)
	}
}

// The fingerprint is asked plainly and normalized: an MD5 answer prints the accepted form
// and asks again, and a bare 43-character body is stored with the prefix.
func TestWizardFingerprint(t *testing.T) {
	keys := writeTestKeys(t)
	answers := []scriptedAnswer{
		{value: "home"},
		{value: "t.example.com"},
		{value: wizardTestPubkey},
		{value: "ssh"},
		{value: "synthetic-user"},
		{value: "key"},
		{value: keys.plainPath},
		{value: "MD5:aa:bb:cc:dd"},
		{value: strings.TrimPrefix(keys.fingerprint, "SHA256:")},
		{value: ""}, // resolver: network
		{value: ""}, // mode: the first
	}
	h := newWizardTestHarness(t, answers, []string{"tun", "socks"}, nil)

	_, prof, err := h.wizard.add(context.Background(), h.profiles, "")
	if err != nil {
		t.Fatal(err)
	}
	if prof.Exit.HostKey != keys.fingerprint {
		t.Fatalf("host_key = %q, want %q", prof.Exit.HostKey, keys.fingerprint)
	}
	if !strings.Contains(h.out.String(), "must be SHA256") {
		t.Fatalf("the refused fingerprint does not print the accepted form:\n%s", h.out.String())
	}
	if got := strings.Count(strings.Join(h.prompter.asked, "\n"), "Pinned host key fingerprint"); got != 2 {
		t.Fatalf("the fingerprint question was asked %d times, want 2", got)
	}
}

// assertKeyNotLeaked checks that the key did not reach the output, a question or the file
// the profile is saved into.
func assertKeyNotLeaked(t *testing.T, h *wizardTestHarness, key string) {
	t.Helper()
	marker := keyMarker(key)
	for _, text := range []string{h.out.String(), strings.Join(h.prompter.asked, "\n")} {
		if strings.Contains(text, marker) || strings.Contains(text, key) {
			t.Fatalf("the key reached the terminal:\n%s", text)
		}
	}
	if len(h.saved) == 0 {
		t.Fatal("nothing was saved to check")
	}
	path := filepath.Join(t.TempDir(), "profiles.toml")
	if err := config.SaveProfilesTo(path, h.saved[0]); err != nil {
		t.Fatalf("the wizard's profile does not save: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), marker) || strings.Contains(string(raw), key) {
		t.Fatalf("the key reached profiles.toml:\n%s", raw)
	}
}

// The defaults are home, "network" and the first offered mode.
func TestWizardDefaults(t *testing.T) {
	answers := []scriptedAnswer{
		{value: ""}, // name: home
		{value: "t.example.com"},
		{value: wizardTestPubkey},
		{value: ""}, // exit type: socks5
		{value: ""}, // login: no authentication
		{value: ""}, // resolver: network
		{value: ""}, // mode: the first
	}
	h := newWizardTestHarness(t, answers, []string{"tun", "socks"}, nil)

	name, prof, err := h.wizard.add(context.Background(), h.profiles, "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "home" {
		t.Fatalf("name = %q, want home", name)
	}
	if len(prof.Resolvers) != 1 || prof.Resolvers[0] != "network" {
		t.Fatalf("resolvers = %v, want network", prof.Resolvers)
	}
	if prof.Datapath != "tun" {
		t.Fatalf("datapath = %q, want the first mode tun", prof.Datapath)
	}
	if got := strings.Join(h.prompter.asked, "\n"); strings.Contains(got, "Another resolver") {
		t.Fatal("the empty first resolver answer asked for another resolver")
	}
	if h.prompter.asked[0] != "Profile name [home]: " {
		t.Fatalf("first question = %q", h.prompter.asked[0])
	}
	if !strings.Contains(strings.Join(h.prompter.asked, "\n"), `Resolver (Enter for "network"): `) {
		t.Fatal("the resolver question does not name the default")
	}
}

func TestWizardPositionalNameSkipsQuestion(t *testing.T) {
	answers := baseWizardAnswers(t, "socks5")[1:] // everything after the name
	h := newWizardTestHarness(t, answers, []string{"tun", "socks"}, nil)

	name, _, err := h.wizard.add(context.Background(), h.profiles, "home")
	if err != nil {
		t.Fatal(err)
	}
	if name != "home" {
		t.Fatalf("name = %q", name)
	}
	if len(h.prompter.asked) == 0 || h.prompter.asked[0] != "Tunnel domain: " {
		t.Fatalf("first question = %q, want the domain: the positional name must skip the name question",
			h.prompter.asked[0])
	}
}

func TestWizardNameCollision(t *testing.T) {
	t.Run("asked name", func(t *testing.T) {
		answers := append([]scriptedAnswer{{value: "home"}, {value: "other"}}, baseWizardAnswers(t, "socks5")[1:]...)
		h := newWizardTestHarness(t, answers, []string{"tun", "socks"}, nil)
		h.profiles.Profile["home"] = &config.Profile{Domain: "old.example.com"}

		name, _, err := h.wizard.add(context.Background(), h.profiles, "")
		if err != nil {
			t.Fatal(err)
		}
		if name != "other" {
			t.Fatalf("name = %q, want other after the collision", name)
		}
		asked := strings.Join(h.prompter.asked, "\n")
		if strings.Count(asked, "Profile name [home]: ") != 2 {
			t.Fatalf("the name question was not re-asked:\n%s", asked)
		}
		if !strings.Contains(h.out.String(), `profile "home" already exists`) {
			t.Fatal("the collision was not explained")
		}
	})

	t.Run("positional name", func(t *testing.T) {
		h := newWizardTestHarness(t, nil, []string{"tun", "socks"}, nil)
		h.profiles.Profile["home"] = &config.Profile{Domain: "old.example.com"}

		_, _, err := h.wizard.add(context.Background(), h.profiles, "home")
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("error = %v, want a collision error", err)
		}
		if len(h.prompter.asked) != 0 {
			t.Fatalf("questions asked before the collision error: %v", h.prompter.asked)
		}
		if h.store.puts != 0 || len(h.saved) != 0 {
			t.Fatal("a collision wrote something")
		}
	})
}

func TestWizardReasks(t *testing.T) {
	socks := baseWizardAnswers(t, "socks5")
	httpAnswers := baseWizardAnswers(t, "http")
	sshPassword := baseWizardAnswers(t, "ssh-password")
	tunReason := errors.New("synthetic: no TUN device")
	socksAuth := []scriptedAnswer{
		{value: "home"},
		{value: "t.example.com"},
		{value: wizardTestPubkey},
		{value: "socks5"},
		{value: "synthetic-user"},
		{secret: true, value: wizardTestPassword},
		{secret: true, value: wizardTestPassword},
		{value: ""}, // resolver: network
		{value: ""}, // mode: the first
	}

	for _, tc := range []struct {
		name           string
		answers        []scriptedAnswer
		modes          []string
		tunUnsupported error
		wantExit       string
		wantUser       string
		wantMode       string
		wantOut        string
	}{
		{
			name:     "bad name",
			answers:  insertAnswers(socks, 0, scriptedAnswer{value: "bad name"}),
			wantExit: "socks5", wantMode: "tun", wantOut: "only ASCII letters",
		},
		{
			name:     "bad domain",
			answers:  insertAnswers(socks, 1, scriptedAnswer{value: "bad_domain"}),
			wantExit: "socks5", wantMode: "tun", wantOut: "only letters, digits and '-'",
		},
		{
			name:     "short key",
			answers:  insertAnswers(socks, 2, scriptedAnswer{value: "0123"}),
			wantExit: "socks5", wantMode: "tun", wantOut: "64 hex digits",
		},
		{
			name:     "non-hex key",
			answers:  insertAnswers(socks, 2, scriptedAnswer{value: strings.Repeat("zz", 32)}),
			wantExit: "socks5", wantMode: "tun", wantOut: "64 hex digits",
		},
		{
			name:     "unknown exit type",
			answers:  insertAnswers(socks, 3, scriptedAnswer{value: "ftp"}),
			wantExit: "socks5", wantMode: "tun", wantOut: "must be socks5, http or ssh",
		},
		{
			name:     "empty HTTP login",
			answers:  insertAnswers(httpAnswers, 4, scriptedAnswer{value: ""}),
			wantExit: "http", wantUser: "synthetic-user", wantMode: "tun", wantOut: "needs a login",
		},
		{
			name:     "empty SSH login",
			answers:  insertAnswers(sshPassword, 4, scriptedAnswer{value: ""}),
			wantExit: "ssh", wantUser: "synthetic-user", wantMode: "tun", wantOut: "needs a login",
		},
		{
			name:     "unknown SSH authentication",
			answers:  insertAnswers(sshPassword, 5, scriptedAnswer{value: "agent"}),
			wantExit: "ssh", wantUser: "synthetic-user", wantMode: "tun",
			wantOut: `the SSH authentication must be "password" or "key"`,
		},
		{
			name:     "empty password",
			answers:  insertAnswers(httpAnswers, 5, scriptedAnswer{secret: true, value: ""}),
			wantExit: "http", wantUser: "synthetic-user", wantMode: "tun", wantOut: "must not be empty",
		},
		{
			name:     "colon in HTTP login",
			answers:  insertAnswers(httpAnswers, 4, scriptedAnswer{value: "synthetic:user"}),
			wantExit: "http", wantUser: "synthetic-user", wantMode: "tun", wantOut: "must not contain ':'",
		},
		{
			name:     "control character in login",
			answers:  insertAnswers(httpAnswers, 4, scriptedAnswer{value: "synthetic\x01user"}),
			wantExit: "http", wantUser: "synthetic-user", wantMode: "tun", wantOut: "must not contain control characters",
		},
		{
			name:     "control character in password",
			answers:  insertAnswers(httpAnswers, 5, scriptedAnswer{secret: true, value: "synthetic\x01password"}),
			wantExit: "http", wantUser: "synthetic-user", wantMode: "tun", wantOut: "must not contain control characters",
		},
		{
			name:     "long SOCKS5 login",
			answers:  insertAnswers(socks, 4, scriptedAnswer{value: strings.Repeat("u", 256)}),
			wantExit: "socks5", wantMode: "tun", wantOut: "a SOCKS5 login must be at most 255 bytes",
		},
		{
			name:     "long SOCKS5 password",
			answers:  insertAnswers(socksAuth, 5, scriptedAnswer{secret: true, value: strings.Repeat("p", 256)}),
			wantExit: "socks5", wantUser: "synthetic-user", wantMode: "tun", wantOut: "a SOCKS5 password must be at most 255 bytes",
		},
		{
			name: "login keeps spaces",
			answers: []scriptedAnswer{
				{value: "home"},
				{value: "t.example.com"},
				{value: wizardTestPubkey},
				{value: "socks5"},
				{value: " synthetic-user "},
				{secret: true, value: wizardTestPassword},
				{secret: true, value: wizardTestPassword},
				{value: ""}, // resolver: network
				{value: ""}, // mode: the first
			},
			wantExit: "socks5", wantUser: " synthetic-user ", wantMode: "tun",
		},
		{
			name:     "bad resolver",
			answers:  insertAnswers(socks, 5, scriptedAnswer{value: "not-a-resolver"}),
			wantExit: "socks5", wantMode: "tun", wantOut: "no such preset",
		},
		{
			name:     "unknown mode",
			answers:  insertAnswers(socks, 6, scriptedAnswer{value: "wireguard"}),
			wantExit: "socks5", wantMode: "tun", wantOut: "the mode must be one of: tun, socks",
		},
		{
			name:           "tun not offered with a reason",
			answers:        insertAnswers(socks, 6, scriptedAnswer{value: "tun"}),
			modes:          []string{"socks"},
			tunUnsupported: tunReason,
			wantExit:       "socks5", wantMode: "socks", wantOut: tunReason.Error(),
		},
		{
			name:     "tun not offered without a reason",
			answers:  insertAnswers(socks, 6, scriptedAnswer{value: "tun"}),
			modes:    []string{"socks"},
			wantExit: "socks5", wantMode: "socks", wantOut: "not available on this system yet",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			modes := tc.modes
			if modes == nil {
				modes = []string{"tun", "socks"}
			}
			h := newWizardTestHarness(t, tc.answers, modes, tc.tunUnsupported)

			name, prof, err := h.wizard.add(context.Background(), h.profiles, "")
			if err != nil {
				t.Fatal(err)
			}
			if name != "home" {
				t.Fatalf("name = %q", name)
			}
			if prof.Exit.Type != tc.wantExit || prof.Exit.User != tc.wantUser {
				t.Fatalf("exit = %+v, want type %s user %q", prof.Exit, tc.wantExit, tc.wantUser)
			}
			if tc.wantUser != "" {
				if h.store.values["home.password"] != wizardTestPassword {
					t.Fatal("the password was not stored after the re-ask")
				}
			} else if h.store.puts != 0 {
				t.Fatal("a SOCKS5 exit without authentication used the secret store")
			}
			if prof.Datapath != tc.wantMode {
				t.Fatalf("datapath = %q, want %q", prof.Datapath, tc.wantMode)
			}
			if tc.wantOut != "" && !strings.Contains(h.out.String(), tc.wantOut) {
				t.Fatalf("reason not printed; want %q, got:\n%s", tc.wantOut, h.out.String())
			}
			if len(h.saved) != 1 {
				t.Fatalf("saves = %d, want 1", len(h.saved))
			}
		})
	}
}

func TestWizardMismatchedPasswordReasksBoth(t *testing.T) {
	answers := baseWizardAnswers(t, "http")
	answers[6] = scriptedAnswer{secret: true, value: "synthetic-other-password"}
	answers = insertAnswers(answers, 7,
		scriptedAnswer{secret: true, value: wizardTestPassword},
		scriptedAnswer{secret: true, value: wizardTestPassword})
	h := newWizardTestHarness(t, answers, []string{"tun", "socks"}, nil)

	_, prof, err := h.wizard.add(context.Background(), h.profiles, "")
	if err != nil {
		t.Fatal(err)
	}
	if prof.Exit.PasswordRef != "file:home.password" {
		t.Fatalf("password_ref = %q", prof.Exit.PasswordRef)
	}
	asked := strings.Join(h.prompter.asked, "\n")
	if strings.Count(asked, "Exit password: ") != 2 || strings.Count(asked, "Exit password again: ") != 2 {
		t.Fatalf("the mismatched password did not re-ask both entries:\n%s", asked)
	}
	if !strings.Contains(h.out.String(), "do not match") {
		t.Fatal("the mismatch was not explained")
	}
	if strings.Contains(h.out.String(), "synthetic-other-password") {
		t.Fatal("the wizard printed the rejected password")
	}
}

// The end of input at any question writes nothing: every script, the passphrase and the
// fingerprint questions of an SSH exit included.
func TestWizardEOFAtEveryQuestion(t *testing.T) {
	for _, exitType := range []string{"http", "ssh-password", "ssh-key"} {
		answers := baseWizardAnswers(t, exitType)
		for cut := 0; cut < len(answers); cut++ {
			t.Run(fmt.Sprintf("%s after %d answers", exitType, cut), func(t *testing.T) {
				h := newWizardTestHarness(t, answers[:cut], []string{"tun", "socks"}, nil)
				_, _, err := h.wizard.add(context.Background(), h.profiles, "")
				if !errors.Is(err, errCancelled) {
					t.Fatalf("error = %v, want errCancelled", err)
				}
				if h.store.puts != 0 || h.store.deletes != 0 {
					t.Fatal("a cancelled run touched the secret store")
				}
				if len(h.saved) != 0 || len(h.profiles.Profile) != 0 {
					t.Fatal("a cancelled run saved a profile")
				}
			})
		}
	}
}

// contextPrompter returns the context error: the wizard must not turn it into errCancelled.
type contextPrompter struct{}

func (contextPrompter) Ask(ctx context.Context, _ string) (string, error) {
	return "", ctx.Err()
}

func (contextPrompter) AskSecret(ctx context.Context, _ string) (string, error) {
	return "", ctx.Err()
}

func TestWizardContextCancellationIsNotEOF(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	h := newWizardTestHarness(t, nil, []string{"tun", "socks"}, nil)
	h.wizard.ask = contextPrompter{}

	_, _, err := h.wizard.add(ctx, h.profiles, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if errors.Is(err, errCancelled) {
		t.Fatal("context cancellation was turned into the end of input")
	}
}

func TestWizardReadErrorPropagates(t *testing.T) {
	injected := errors.New("synthetic read failure")
	answers := baseWizardAnswers(t, "socks5")
	answers[1] = scriptedAnswer{err: injected}
	h := newWizardTestHarness(t, answers, []string{"tun", "socks"}, nil)

	_, _, err := h.wizard.add(context.Background(), h.profiles, "")
	if !errors.Is(err, injected) {
		t.Fatalf("error = %v, want the injected error", err)
	}
	if errors.Is(err, errCancelled) {
		t.Fatal("the injected error was turned into the end of input")
	}
	if h.store.puts != 0 || len(h.saved) != 0 {
		t.Fatal("a failed read wrote something")
	}
}

func TestWizardFailedSaveRollsBackSecret(t *testing.T) {
	h := newWizardTestHarness(t, baseWizardAnswers(t, "http"), []string{"tun", "socks"}, nil)
	h.saveErr = errors.New("synthetic save failure")

	_, _, err := h.wizard.add(context.Background(), h.profiles, "")
	if err == nil {
		t.Fatal("a failing save returned no error")
	}
	if !strings.Contains(err.Error(), "synthetic save failure") ||
		!strings.Contains(err.Error(), "removed from the secret store") {
		t.Fatalf("error = %v, want the save failure and the rollback", err)
	}
	if strings.Contains(err.Error(), wizardTestPassword) {
		t.Fatal("the error contains the password")
	}
	if h.store.deletes != 1 || len(h.store.deleted) != 1 || h.store.deleted[0] != "file:home.password" {
		t.Fatalf("deletes = %v, want the one reference", h.store.deleted)
	}
	if len(h.store.values) != 0 {
		t.Fatal("the secret stayed in the store")
	}
	if len(h.saved) != 0 || len(h.profiles.Profile) != 0 {
		t.Fatal("a failed save left the profile somewhere")
	}
}

func TestWizardPutFailureDoesNotSave(t *testing.T) {
	h := newWizardTestHarness(t, baseWizardAnswers(t, "http"), []string{"tun", "socks"}, nil)
	h.store.putErr = errors.New("synthetic store failure")

	_, _, err := h.wizard.add(context.Background(), h.profiles, "")
	if err == nil || !strings.Contains(err.Error(), "synthetic store failure") {
		t.Fatalf("error = %v, want the store failure", err)
	}
	if strings.Contains(err.Error(), wizardTestPassword) {
		t.Fatal("the error contains the password")
	}
	if len(h.saved) != 0 {
		t.Fatal("the profile was saved after the store failed")
	}
	if h.store.deletes != 0 {
		t.Fatal("the secret was deleted although Put failed")
	}
	if len(h.profiles.Profile) != 0 {
		t.Fatal("the caller's profiles were changed")
	}
}

// The key shape rolls back the same way as the password: the put entry is deleted and no
// message mentions a password.
func TestWizardFailedSaveRollsBackKey(t *testing.T) {
	h := newWizardTestHarness(t, baseWizardAnswers(t, "ssh-key"), []string{"tun", "socks"}, nil)
	h.saveErr = errors.New("synthetic save failure")

	_, _, err := h.wizard.add(context.Background(), h.profiles, "")
	if err == nil {
		t.Fatal("a failing save returned no error")
	}
	if !strings.Contains(err.Error(), "synthetic save failure") ||
		!strings.Contains(err.Error(), "the exit key was removed from the secret store") {
		t.Fatalf("error = %v, want the save failure and the rollback", err)
	}
	if strings.Contains(err.Error(), "password") {
		t.Fatalf("the rollback of a key mentions a password: %v", err)
	}
	if h.store.deletes != 1 || len(h.store.deleted) != 1 || h.store.deleted[0] != "file:home.key" {
		t.Fatalf("deletes = %v, want the one reference", h.store.deleted)
	}
	if len(h.store.values) != 0 {
		t.Fatal("the key stayed in the store")
	}
	if len(h.saved) != 0 || len(h.profiles.Profile) != 0 {
		t.Fatal("a failed save left the profile somewhere")
	}
}

// A store that refuses the key saves nothing.
func TestWizardKeyPutFailureDoesNotSave(t *testing.T) {
	h := newWizardTestHarness(t, baseWizardAnswers(t, "ssh-key"), []string{"tun", "socks"}, nil)
	h.store.putErr = errors.New("synthetic store failure")

	_, _, err := h.wizard.add(context.Background(), h.profiles, "")
	if err == nil || !strings.Contains(err.Error(), "cannot store the exit key") ||
		!strings.Contains(err.Error(), "synthetic store failure") {
		t.Fatalf("error = %v, want the store failure", err)
	}
	if len(h.saved) != 0 {
		t.Fatal("the profile was saved after the store failed")
	}
	if h.store.deletes != 0 {
		t.Fatal("the key was deleted although Put failed")
	}
}

// The non-terminal AskSecret reads an ordinary line from the same reader as Ask.
func TestLinePrompter(t *testing.T) {
	var out bytes.Buffer
	p := newLinePrompter(strings.NewReader("answer\nsecret\n"), &out)

	got, err := p.Ask(context.Background(), "Question: ")
	if err != nil || got != "answer" {
		t.Fatalf("Ask = %q, %v", got, err)
	}
	got, err = p.AskSecret(context.Background(), "Password: ")
	if err != nil || got != "secret" {
		t.Fatalf("AskSecret = %q, %v", got, err)
	}
	if out.String() != "Question: Password: " {
		t.Fatalf("questions = %q", out.String())
	}
	if _, err := p.Ask(context.Background(), "Again: "); !errors.Is(err, io.EOF) {
		t.Fatalf("after the end of input: %v, want io.EOF", err)
	}
}
