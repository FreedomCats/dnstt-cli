package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode"

	"github.com/FreedomCats/dnstt-cli/internal/config"
	"github.com/FreedomCats/dnstt-cli/internal/resolvers"
)

// errCancelled is the end of input in the wizard: the person stopped answering and nothing
// has been written. It is an ordinary error, not context.Canceled: `run` prints
// "cancelled, nothing written" and exits 1.
var errCancelled = errors.New("cancelled, nothing written")

// wizard asks the questions of architecture.md §10a and saves the profile. The menu (E4)
// builds one with its own prompter and calls add; `profile add` is the CLI caller.
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
	exit, password, err := w.askExit(ctx)
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
		Exit:       exit,
	}

	var ref string
	if password != "" {
		if ref, err = w.store.Put(name+".password", password); err != nil {
			return "", nil, fmt.Errorf("cannot store the exit password: %w", err)
		}
		prof.Exit.PasswordRef = ref
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
				fmt.Errorf("the exit password could not be removed from the secret store: %w", deleteErr))
		}
		return "", nil, errors.Join(err, errors.New("the exit password was removed from the secret store"))
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
	for {
		answer, err := w.readLine(ctx, "Tunnel domain: ")
		if err != nil {
			return "", err
		}
		domain := strings.TrimSpace(answer)
		if err := config.ValidDomainName(domain); err != nil {
			fmt.Fprintln(w.out, err)
			continue
		}
		return domain, nil
	}
}

// askPubkey asks the server public key; the reason names the expected form, never the value.
func (w *wizard) askPubkey(ctx context.Context) (string, error) {
	for {
		answer, err := w.readLine(ctx, "Server public key: ")
		if err != nil {
			return "", err
		}
		pubkey := strings.TrimSpace(answer)
		if !config.IsValidPubkey(pubkey) {
			fmt.Fprintln(w.out, "the server public key must be exactly 64 hex digits")
			continue
		}
		return pubkey, nil
	}
}

// askExit asks the exit type, the login and, when there is a login, the password twice.
// The password is returned separately: the caller puts it into the profile only after the
// secret store has accepted it.
func (w *wizard) askExit(ctx context.Context) (config.Exit, string, error) {
	exitType, err := w.askExitType(ctx)
	if err != nil {
		return config.Exit{}, "", err
	}

	question := "Exit login: "
	if exitType == "socks5" {
		question = "Exit login (Enter for no authentication): "
	}
	for {
		answer, err := w.readLine(ctx, question)
		if err != nil {
			return config.Exit{}, "", err
		}
		// The login is kept as typed: only its emptiness is judged on the trimmed value.
		// Trimming would silently change the credentials of the exit.
		user := answer
		if strings.TrimSpace(user) == "" {
			if exitType == "socks5" {
				return config.Exit{Type: exitType}, "", nil
			}
			fmt.Fprintln(w.out, "an HTTP exit needs a login")
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

		password, err := w.askPassword(ctx, exitType)
		if err != nil {
			return config.Exit{}, "", err
		}
		return config.Exit{Type: exitType, User: user}, password, nil
	}
}

// askExitType asks the exit type with socks5 as the default. SSH is refused by name: it is
// a later stage, not a typo.
func (w *wizard) askExitType(ctx context.Context) (string, error) {
	for {
		answer, err := w.readLine(ctx, "Exit type [socks5]: ")
		if err != nil {
			return "", err
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "", "socks5":
			return "socks5", nil
		case "http":
			return "http", nil
		case "ssh":
			fmt.Fprintln(w.out, "the SSH exit is not implemented yet (stage E5); choose socks5 or http")
		default:
			fmt.Fprintln(w.out, "the exit type must be socks5 or http")
		}
	}
}

// askPassword asks the exit password twice, hidden. The answer never reaches the output:
// only the reasons do.
func (w *wizard) askPassword(ctx context.Context, exitType string) (string, error) {
	for {
		first, err := w.readSecret(ctx, "Exit password: ")
		if err != nil {
			return "", err
		}
		if first == "" {
			fmt.Fprintln(w.out, "the exit password must not be empty")
			continue
		}
		if strings.ContainsFunc(first, unicode.IsControl) {
			fmt.Fprintln(w.out, "the exit password must not contain control characters")
			continue
		}
		if exitType == "socks5" && len(first) > 255 {
			fmt.Fprintln(w.out, "a SOCKS5 password must be at most 255 bytes")
			continue
		}

		again, err := w.readSecret(ctx, "Exit password again: ")
		if err != nil {
			return "", err
		}
		if again != first {
			fmt.Fprintln(w.out, "the passwords do not match")
			continue
		}
		return first, nil
	}
}

// askResolvers asks the resolvers one per line. An empty first answer is "network", an
// empty later answer finishes. The line is stored as typed, so a preset name stays short;
// a duplicate resolver is dropped by its normalized form.
func (w *wizard) askResolvers(ctx context.Context) ([]string, error) {
	const (
		first = `Resolver (Enter for "network"): `
		next  = "Another resolver (Enter to finish): "
	)

	question := first
	var entries []string
	var seen []resolvers.Resolver
	for {
		answer, err := w.readLine(ctx, question)
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
			fmt.Fprintln(w.out, err)
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
	answer, err := w.ask.Ask(ctx, question)
	return answer, readError(ctx, err)
}

// readSecret is readLine for a hidden answer.
func (w *wizard) readSecret(ctx context.Context, question string) (string, error) {
	answer, err := w.ask.AskSecret(ctx, question)
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
