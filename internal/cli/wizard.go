package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/FreedomCats/dnstt-cli/internal/config"
	"github.com/FreedomCats/dnstt-cli/internal/exit"
	"github.com/FreedomCats/dnstt-cli/internal/resolvers"
)

// errCancelled is the end of input in the wizard: the person stopped answering and nothing
// has been written. It is an ordinary error, not context.Canceled: `run` prints
// "cancelled, nothing written" and exits 1.
var errCancelled = errors.New("cancelled, nothing written")

// exitSecret is one exit credential as the authoring paths handle it: the password of a
// password exit or the private key of an SSH exit, decrypted. The wizard, the import and
// the menu put it into the secret store through the same names and the same words and set
// the same profile field, so that the three paths cannot disagree.
type exitSecret struct {
	password string
	key      string
}

// secretName is the name of the store entry: <profile>.password or <profile>.key.
func (s exitSecret) secretName(profile string) string {
	if s.key != "" {
		return profile + ".key"
	}
	return profile + ".password"
}

// kind is the credential in the words of the messages, without the article: "exit
// password" or "exit key".
func (s exitSecret) kind() string {
	if s.key != "" {
		return "exit key"
	}
	return "exit password"
}

// value is what the store keeps: the password itself or the decrypted key in PEM.
func (s exitSecret) value() string {
	if s.key != "" {
		return s.key
	}
	return s.password
}

// apply records the reference the store returned in the profile: key_ref for a key,
// password_ref for a password. Only the field of the shape is set, so a key exit never
// gains password_ref and the other way round.
func (s exitSecret) apply(prof *config.Exit, ref string) {
	if s.key != "" {
		prof.KeyRef = ref
		return
	}
	prof.PasswordRef = ref
}

// wizard asks the questions of architecture.md §10a and saves the profile. The menu and
// `profile add` build one with newWizard and call add.
type wizard struct {
	ask   prompter
	out   io.Writer
	store config.SecretStore
	// modes are the datapaths offered, the first is the default.
	modes []string
	// tunUnsupported is why tun is not offered; nil when it is.
	tunUnsupported error
	save           func(*config.Profiles) error
}

// add runs the questions and saves the profile. A non-empty name skips the name question;
// otherwise it is asked. It returns the profile name and the profile it built, so that the
// caller can report both without reaching into its own Profiles — which add does not
// change. Nothing is written before the last question.
func (w *wizard) add(ctx context.Context, profiles *config.Profiles, name string) (string, *config.Profile, error) {
	if profiles == nil {
		profiles = &config.Profiles{}
	}
	if name != "" {
		if !config.ValidProfileName(name) {
			return "", nil, fmt.Errorf("invalid profile name %q: use ASCII letters, digits and '-', not starting with '-'", name)
		}
		if _, exists := profiles.Profile[name]; exists {
			return "", nil, fmt.Errorf("profile %q already exists; use 'profile set' to edit it", name)
		}
	} else {
		var err error
		if name, err = w.askName(ctx, profiles); err != nil {
			return "", nil, err
		}
	}

	domain, err := w.askDomain(ctx)
	if err != nil {
		return "", nil, err
	}
	pubkey, err := w.askPubkey(ctx)
	if err != nil {
		return "", nil, err
	}
	exitConf, secret, err := w.askExit(ctx)
	if err != nil {
		return "", nil, err
	}
	resolverList, err := w.askResolvers(ctx)
	if err != nil {
		return "", nil, err
	}
	datapath, err := w.askMode(ctx)
	if err != nil {
		return "", nil, err
	}

	// The kill-switch default is named explicitly, so the profile keeps it on even if the
	// default changes later. Bootstrap, UTLS, TunDNS and Socks stay empty: their defaults
	// are the product defaults.
	killSwitch := true
	prof := &config.Profile{
		Domain:     domain,
		Pubkey:     pubkey,
		Resolvers:  resolverList,
		Datapath:   datapath,
		KillSwitch: &killSwitch,
		Exit:       exitConf,
	}

	// The store is written only now, after the last question: an end of input above leaves
	// nothing behind, in the file or in the store.
	var ref string
	if secret.value() != "" {
		if ref, err = w.store.Put(secret.secretName(name), secret.value()); err != nil {
			return "", nil, fmt.Errorf("cannot store the %s: %w", secret.kind(), err)
		}
		secret.apply(&prof.Exit, ref)
	}

	// The caller's Profiles stays as it was: on a failed save there is no profile anywhere
	// but in this function. The values are shared, the map is not.
	next := &config.Profiles{
		DefaultProfile: profiles.DefaultProfile,
		Profile:        make(map[string]*config.Profile, len(profiles.Profile)+1),
	}
	for existing, p := range profiles.Profile {
		next.Profile[existing] = p
	}
	next.Profile[name] = prof
	if next.DefaultProfile == "" {
		next.DefaultProfile = name
	}

	if err := w.save(next); err != nil {
		if ref == "" {
			return "", nil, err
		}
		if deleteErr := w.store.Delete(ref); deleteErr != nil {
			return "", nil, errors.Join(err,
				fmt.Errorf("the %s could not be removed from the secret store: %w", secret.kind(), deleteErr))
		}
		return "", nil, errors.Join(err, fmt.Errorf("the %s was removed from the secret store", secret.kind()))
	}
	return name, prof, nil
}

// askName asks the profile name until it is valid and not taken.
func (w *wizard) askName(ctx context.Context, profiles *config.Profiles) (string, error) {
	for {
		answer, err := w.readLine(ctx, "Profile name [home]: ")
		if err != nil {
			return "", err
		}
		name := strings.TrimSpace(answer)
		if name == "" {
			name = "home"
		}
		if !config.ValidProfileName(name) {
			fmt.Fprintln(w.out, "the profile name may contain only ASCII letters, digits and '-', and must not start with '-'")
			continue
		}
		if _, exists := profiles.Profile[name]; exists {
			fmt.Fprintf(w.out, "profile %q already exists; choose another name\n", name)
			continue
		}
		return name, nil
	}
}

// askDomain asks the tunnel domain and returns the trimmed value.
func (w *wizard) askDomain(ctx context.Context) (string, error) {
	return askDomainValue(ctx, w.ask, w.out, "")
}

// askDomainValue asks the tunnel domain and returns the trimmed value. With a current value
// the question carries it as the default, masked as `profile show` does, and an empty answer
// keeps it. The menu's editor and the wizard share the wording and the validator.
func askDomainValue(ctx context.Context, ask prompter, out io.Writer, current string) (string, error) {
	question := "Tunnel domain: "
	if current != "" {
		question = fmt.Sprintf("Domain [%s]: ", maskSensitive(current))
	}
	for {
		answer, err := readLineFrom(ctx, ask, question)
		if err != nil {
			return "", err
		}
		domain := strings.TrimSpace(answer)
		if domain == "" && current != "" {
			return current, nil
		}
		if err := config.ValidDomainName(domain); err != nil {
			fmt.Fprintln(out, err)
			continue
		}
		return domain, nil
	}
}

// askPubkey asks the server public key; the reason names the expected form, never the value.
func (w *wizard) askPubkey(ctx context.Context) (string, error) {
	return askPubkeyValue(ctx, w.ask, w.out, "")
}

// askPubkeyValue is askDomainValue for the server public key: with a current value the
// question carries it masked, and an empty answer keeps it.
func askPubkeyValue(ctx context.Context, ask prompter, out io.Writer, current string) (string, error) {
	question := "Server public key: "
	if current != "" {
		question = fmt.Sprintf("Server public key [%s]: ", maskSensitive(current))
	}
	for {
		answer, err := readLineFrom(ctx, ask, question)
		if err != nil {
			return "", err
		}
		pubkey := strings.TrimSpace(answer)
		if pubkey == "" && current != "" {
			return current, nil
		}
		if !config.IsValidPubkey(pubkey) {
			fmt.Fprintln(out, "the server public key must be exactly 64 hex digits")
			continue
		}
		return pubkey, nil
	}
}

// askExit asks the exit type, the login, the credential and, for an SSH exit, the pinned
// fingerprint: the password of a password exit twice, or the path of the private key with
// its passphrase when the key needs one. The credential is returned separately: the caller
// puts it into the profile only after the secret store has accepted it.
func (w *wizard) askExit(ctx context.Context) (config.Exit, exitSecret, error) {
	exitType, err := w.askExitType(ctx)
	if err != nil {
		return config.Exit{}, exitSecret{}, err
	}

	question := "Exit login: "
	if exitType == "socks5" {
		question = "Exit login (Enter for no authentication): "
	}
	for {
		answer, err := w.readLine(ctx, question)
		if err != nil {
			return config.Exit{}, exitSecret{}, err
		}
		// The login is kept as typed: only its emptiness is judged on the trimmed value.
		// Trimming would silently change the credentials of the exit.
		user := answer
		if strings.TrimSpace(user) == "" {
			if exitType == "socks5" {
				return config.Exit{Type: exitType}, exitSecret{}, nil
			}
			fmt.Fprintln(w.out, "an HTTP or SSH exit needs a login")
			continue
		}
		if strings.ContainsFunc(user, unicode.IsControl) {
			fmt.Fprintln(w.out, "the login must not contain control characters")
			continue
		}
		if exitType == "http" && strings.Contains(user, ":") {
			fmt.Fprintln(w.out, "an HTTP login must not contain ':'")
			continue
		}
		if exitType == "socks5" && len(user) > 255 {
			fmt.Fprintln(w.out, "a SOCKS5 login must be at most 255 bytes")
			continue
		}

		prof := config.Exit{Type: exitType, User: user}
		var secret exitSecret
		if exitType == "ssh" {
			if secret, err = askSSHAuth(ctx, w.ask, w.out); err != nil {
				return config.Exit{}, exitSecret{}, err
			}
			if prof.HostKey, err = askFingerprint(ctx, w.ask, w.out, ""); err != nil {
				return config.Exit{}, exitSecret{}, err
			}
		} else if secret.password, err = w.askPassword(ctx, exitType); err != nil {
			return config.Exit{}, exitSecret{}, err
		}
		return prof, secret, nil
	}
}

// askExitType asks the exit type with socks5 as the default.
func (w *wizard) askExitType(ctx context.Context) (string, error) {
	for {
		answer, err := w.readLine(ctx, "Exit type [socks5]: ")
		if err != nil {
			return "", err
		}
		switch exitType := strings.ToLower(strings.TrimSpace(answer)); exitType {
		case "":
			return "socks5", nil
		case "socks5", "http", "ssh":
			return exitType, nil
		default:
			fmt.Fprintln(w.out, "the exit type must be socks5, http or ssh")
		}
	}
}

// askSSHAuth asks the SSH authentication method and its credential: the password twice, or
// the key file. An empty answer is a password, the method of most links.
func askSSHAuth(ctx context.Context, ask prompter, out io.Writer) (exitSecret, error) {
	for {
		answer, err := readLineFrom(ctx, ask, "SSH authentication (password, key) [password]: ")
		if err != nil {
			return exitSecret{}, err
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "", "password":
			password, err := askExitPassword(ctx, ask, out, "ssh")
			if err != nil {
				return exitSecret{}, err
			}
			return exitSecret{password: password}, nil
		case "key":
			key, err := askExitKey(ctx, ask, out)
			if err != nil {
				return exitSecret{}, err
			}
			return exitSecret{key: key}, nil
		default:
			fmt.Fprintln(out, `the SSH authentication must be "password" or "key"`)
		}
	}
}

// askExitKey asks the path of a private key, reads it and decrypts it with its passphrase
// when it has one: the store keeps the key in PEM without a passphrase. A path that cannot
// be read, a damaged key and a wrong passphrase all print the reason and ask the path
// again, so a typo does not end the wizard. Neither the key nor the passphrase reaches the
// output, and a read failure never names the path: an *os.PathError prints it.
func askExitKey(ctx context.Context, ask prompter, out io.Writer) (string, error) {
	for {
		answer, err := readLineFrom(ctx, ask, "Private key file: ")
		if err != nil {
			return "", err
		}
		path := expandHome(strings.TrimSpace(answer))
		pemBytes, readErr := os.ReadFile(path)
		if readErr != nil {
			fmt.Fprintln(out, "the private key file cannot be read")
			continue
		}
		key, err := exit.DecryptKey(pemBytes, "")
		if errors.Is(err, exit.ErrKeyNeedsPassphrase) {
			passphrase, readErr := readSecretFrom(ctx, ask, "Key passphrase: ")
			if readErr != nil {
				return "", readErr
			}
			key, err = exit.DecryptKey(pemBytes, passphrase)
		}
		if err != nil {
			fmt.Fprintln(out, err)
			continue
		}
		return string(key), nil
	}
}

// expandHome replaces a leading `~/` or a bare `~` with the home directory: a shell expands
// it before the program starts, but an answer typed at a question is not a shell. A path
// without it, and a home directory that cannot be found, come back as they are.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[len("~/"):])
}

// askFingerprint asks the pinned host key fingerprint plainly: it is public, not a secret,
// so it is neither hidden nor masked. The answer goes through exit.ParseFingerprint, a bad
// one prints the reason and the question repeats; with a current value an empty answer
// keeps it. The menu's fingerprint item and the import share the question and the
// validator.
func askFingerprint(ctx context.Context, ask prompter, out io.Writer, current string) (string, error) {
	const hint = "Pinned host key fingerprint (SHA256:…, as ssh-keygen -lf prints it)"
	question := hint + ": "
	if current != "" {
		question = hint + " [" + current + "]: "
	}
	for {
		answer, err := readLineFrom(ctx, ask, question)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(answer) == "" && current != "" {
			return current, nil
		}
		fingerprint, err := exit.ParseFingerprint(answer)
		if err != nil {
			fmt.Fprintln(out, err)
			continue
		}
		return fingerprint, nil
	}
}

// askPassword asks the exit password twice, hidden. The answer never reaches the output:
// only the reasons do.
func (w *wizard) askPassword(ctx context.Context, exitType string) (string, error) {
	return askExitPassword(ctx, w.ask, w.out, exitType)
}

// askExitPassword asks the exit password twice, hidden. The answer never reaches the output:
// only the reasons do. The menu's editor and the wizard share the rules.
func askExitPassword(ctx context.Context, ask prompter, out io.Writer, exitType string) (string, error) {
	for {
		first, err := readSecretFrom(ctx, ask, "Exit password: ")
		if err != nil {
			return "", err
		}
		if first == "" {
			fmt.Fprintln(out, "the exit password must not be empty")
			continue
		}
		if strings.ContainsFunc(first, unicode.IsControl) {
			fmt.Fprintln(out, "the exit password must not contain control characters")
			continue
		}
		if exitType == "socks5" && len(first) > 255 {
			fmt.Fprintln(out, "a SOCKS5 password must be at most 255 bytes")
			continue
		}

		again, err := readSecretFrom(ctx, ask, "Exit password again: ")
		if err != nil {
			return "", err
		}
		if again != first {
			fmt.Fprintln(out, "the passwords do not match")
			continue
		}
		return first, nil
	}
}

// askResolvers asks the resolvers one per line. An empty first answer is "network", an
// empty later answer finishes. The line is stored as typed, so a preset name stays short;
// a duplicate resolver is dropped by its normalized form.
func (w *wizard) askResolvers(ctx context.Context) ([]string, error) {
	return askResolverList(ctx, w.ask, w.out)
}

// askResolverList asks the resolvers one per line. An empty first answer is "network", an
// empty later answer finishes. The line is stored as typed, so a preset name stays short;
// a duplicate resolver is dropped by its normalized form. The menu's editor and the wizard
// share the questions and the rules.
func askResolverList(ctx context.Context, ask prompter, out io.Writer) ([]string, error) {
	const (
		first = `Resolver (Enter for "network"): `
		next  = "Another resolver (Enter to finish): "
	)

	question := first
	var entries []string
	var seen []resolvers.Resolver
	for {
		answer, err := readLineFrom(ctx, ask, question)
		if err != nil {
			return nil, err
		}
		line := strings.TrimSpace(answer)
		if line == "" {
			if len(entries) == 0 {
				return []string{"network"}, nil
			}
			return entries, nil
		}
		parsed, err := resolvers.Expand([]string{line})
		if err != nil {
			fmt.Fprintln(out, err)
			continue
		}
		if slices.ContainsFunc(seen, func(r resolvers.Resolver) bool { return r.String() == parsed[0].String() }) {
			question = next
			continue
		}
		seen = append(seen, parsed[0])
		entries = append(entries, line)
		question = next
	}
}

// askMode asks the datapath, offering modes in order; an empty answer takes the first.
func (w *wizard) askMode(ctx context.Context) (string, error) {
	modes := w.modes
	if len(modes) == 0 {
		modes = []string{"socks"}
	}
	question := fmt.Sprintf("Mode (%s) [%s]: ", strings.Join(modes, ", "), modes[0])
	for {
		answer, err := w.readLine(ctx, question)
		if err != nil {
			return "", err
		}
		mode := strings.ToLower(strings.TrimSpace(answer))
		if mode == "" {
			return modes[0], nil
		}
		if slices.Contains(modes, mode) {
			return mode, nil
		}
		if mode == "tun" {
			if w.tunUnsupported != nil {
				fmt.Fprintln(w.out, w.tunUnsupported)
			} else {
				fmt.Fprintln(w.out, "tun is not available on this system yet")
			}
			continue
		}
		fmt.Fprintf(w.out, "the mode must be one of: %s\n", strings.Join(modes, ", "))
	}
}

// readLine asks a question and turns the end of input into errCancelled. A cancelled
// context is returned as itself, so `run` reports "interrupted".
func (w *wizard) readLine(ctx context.Context, question string) (string, error) {
	return readLineFrom(ctx, w.ask, question)
}

// readSecret is readLine for a hidden answer.
func (w *wizard) readSecret(ctx context.Context, question string) (string, error) {
	return readSecretFrom(ctx, w.ask, question)
}

// readLineFrom is readLine over any prompter: the free questions of the menu and the
// wizard's methods share it.
func readLineFrom(ctx context.Context, ask prompter, question string) (string, error) {
	answer, err := ask.Ask(ctx, question)
	return answer, readError(ctx, err)
}

// readSecretFrom is readLineFrom for a hidden answer.
func readSecretFrom(ctx context.Context, ask prompter, question string) (string, error) {
	answer, err := ask.AskSecret(ctx, question)
	return answer, readError(ctx, err)
}

// readError separates the ways a read can end: a cancelled context as itself, the end of
// input as the wizard's cancel, anything else as it came.
func readError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, io.EOF) {
		return errCancelled
	}
	return err
}
