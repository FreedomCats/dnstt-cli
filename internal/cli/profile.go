package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/FreedomCats/dnstt-cli/internal/config"
	"github.com/FreedomCats/dnstt-cli/internal/datapath"
)

func profileCommand() *command {
	return &command{
		name:    "profile",
		args:    "<subcommand>",
		summary: "manage profiles",
		description: `Manage dnstt-cli profiles: add, set, import, show, rm, list.

Subcommands:
  add [name]      add a new profile with the interactive wizard
  set <name>      change a profile in the menu (not interactive yet); 'profile add' creates a new one
  import          import a profile from a slipnet:// link
  show <name>     show a profile
  rm <name>       remove a profile and its stored exit password or key
  list            list all profiles

The wizard asks for the profile name (a positional name skips the question; Enter takes
"home"), the tunnel domain, the server public key, the exit type (socks5, http or ssh; for
ssh the authentication method — the password twice, or the path of a private key with its
passphrase when it has one — and the pinned fingerprint), the exit login and password, the
resolvers — one per line, Enter for "network", presets by name — and the mode: tun where the
system supports it (it needs root to connect), otherwise socks.

The import subcommand asks for a slipnet:// link by hidden input in a terminal, re-asking
after a bad paste, and reads one link per line from stdin otherwise. A dnstt link becomes
a socks5 exit. A dnstt_ssh link becomes an ssh exit: the login and the password or the
private key come from the link, a passphrase only decrypts the key, and the pinned
fingerprint is asked in a terminal. From stdin such a link is a warning, because the
fingerprint cannot be asked there. NoizDNS (sayedns) and slipnet-enc:// links are refused
too.

The exit password and key go to the secret store; profiles.toml holds only a reference to
them. A hand-written SSH exit needs exit.user, exit.host_key and exactly one of
exit.password_ref and exit.key_ref.`,
		run: runProfile,
	}
}

func runProfile(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(profileCommand())
	if err := parse(e, fs, args); err != nil {
		return err
	}

	if fs.NArg() == 0 {
		return usagef("profile requires a subcommand: add, set, import, show, rm, list")
	}

	subcmd := fs.Arg(0)
	subArgs := fs.Args()[1:]

	switch subcmd {
	case "add":
		return runProfileAdd(ctx, e, subArgs)
	case "set":
		return runProfileSet(ctx, e, subArgs)
	case "import":
		return runProfileImport(ctx, e, subArgs)
	case "show":
		return runProfileShow(ctx, e, subArgs)
	case "rm":
		return runProfileRm(ctx, e, subArgs)
	case "list":
		return runProfileList(ctx, e, subArgs)
	default:
		return usagef("unknown profile subcommand %q", subcmd)
	}
}

func runProfileList(_ context.Context, e *env, args []string) error {
	if len(args) > 0 {
		return usagef("profile list takes no arguments")
	}

	profiles, err := config.LoadProfiles()
	if err != nil {
		return err
	}

	if len(profiles.Profile) == 0 {
		fmt.Fprintln(e.stdout, "No profiles configured.")
		return nil
	}

	fmt.Fprintln(e.stdout, "Profiles:")
	for name := range profiles.Profile {
		marker := ""
		if name == profiles.DefaultProfile {
			marker = " (default)"
		}
		fmt.Fprintf(e.stdout, "  %s%s\n", name, marker)
	}
	return nil
}

func runProfileShow(_ context.Context, e *env, args []string) error {
	var showFull bool
	var name string

	// Simple flag parsing: -full comes before or after the name, and any other argument
	// that starts with "-" is an unknown flag wherever it stands.
	for _, arg := range args {
		switch {
		case arg == "-full":
			showFull = true
		case strings.HasPrefix(arg, "-"):
			return usagef("unknown flag %q", arg)
		case name != "":
			return usagef("extra argument: profile show takes one profile name")
		default:
			name = arg
		}
	}

	if name == "" {
		return usagef("profile show requires a profile name")
	}

	profiles, err := config.LoadProfiles()
	if err != nil {
		return err
	}

	prof, ok := profiles.Profile[name]
	if !ok {
		return fmt.Errorf("profile %q not found", name)
	}

	fmt.Fprintf(e.stdout, "Profile: %s\n", name)
	if showFull {
		fmt.Fprintf(e.stdout, "  Domain: %s\n", prof.Domain)
		fmt.Fprintf(e.stdout, "  Public Key: %s\n", prof.Pubkey)
	} else {
		fmt.Fprintf(e.stdout, "  Domain: %s\n", maskSensitive(prof.Domain))
		fmt.Fprintf(e.stdout, "  Public Key: %s\n", maskSensitive(prof.Pubkey))
	}
	fmt.Fprintf(e.stdout, "  Resolvers: %v\n", prof.Resolvers)
	if len(prof.Bootstrap) > 0 {
		fmt.Fprintf(e.stdout, "  Bootstrap: %v\n", prof.Bootstrap)
	}
	if prof.UTLS != "" {
		fmt.Fprintf(e.stdout, "  uTLS: %s\n", prof.UTLS)
	}
	fmt.Fprintf(e.stdout, "  Datapath: %s\n", prof.Datapath)
	fmt.Fprintf(e.stdout, "  Kill Switch: %v\n", prof.KillSwitchOn())
	if prof.TunDNS != "" {
		fmt.Fprintf(e.stdout, "  Tun DNS: %s\n", prof.TunDNS)
	}
	if prof.PayloadSize != 0 {
		fmt.Fprintf(e.stdout, "  Payload Size: %d\n", prof.PayloadSize)
	}
	fmt.Fprintf(e.stdout, "  Exit Type: %s\n", prof.Exit.Type)
	if prof.Exit.User != "" {
		fmt.Fprintf(e.stdout, "  Exit User: %s\n", prof.Exit.User)
	}
	if prof.Exit.HostKey != "" {
		fmt.Fprintf(e.stdout, "  Exit Host Key: %s\n", prof.Exit.HostKey)
	}
	if prof.Exit.PasswordRef != "" {
		fmt.Fprintf(e.stdout, "  Exit Password: (stored in %s)\n", prof.Exit.PasswordRef)
	}
	if prof.Exit.KeyRef != "" {
		fmt.Fprintf(e.stdout, "  Exit Key: (stored in %s)\n", prof.Exit.KeyRef)
	}
	if prof.Socks.Listen != "" {
		fmt.Fprintf(e.stdout, "  SOCKS Listen: %s\n", prof.Socks.Listen)
	}

	return nil
}

func maskSensitive(s string) string {
	if len(s) <= 8 {
		return "***"
	}
	return s[:4] + "..." + s[len(s)-4:]
}

// Indirections for the tests: a failing store and a failing save are not reachable
// from an isolated temporary config otherwise.
var (
	newSecretStore = config.NewSecretStore
	saveProfiles   = config.SaveProfiles
)

func runProfileRm(_ context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return usagef("profile rm requires a profile name")
	}
	if len(args) > 1 {
		return usagef("extra argument: profile rm takes one profile name")
	}

	name := args[0]

	profiles, err := config.LoadProfiles()
	if err != nil {
		return err
	}

	prof, ok := profiles.Profile[name]
	if !ok {
		return fmt.Errorf("profile %q not found", name)
	}

	// The secrets go first: a secret whose profile is gone stays in the store with
	// nothing pointing at it, and no command can find it again. A profile left without
	// its secret is visible in profiles.toml, and the command can say so. An ssh profile
	// has exactly one of the two references and a socks5 or http profile at most the
	// password, so at most one secret is deleted here; the loop covers whichever
	// reference the profile carries.
	var store config.SecretStore
	var deleted []string
	for _, secret := range []struct {
		ref  string
		what string
		fail string
	}{
		{prof.Exit.PasswordRef, "exit password", "the exit password could not be deleted from the secret store"},
		{prof.Exit.KeyRef, "exit key", "the exit key could not be deleted from the secret store"},
	} {
		if secret.ref == "" {
			continue
		}
		if store == nil {
			store, err = newSecretStore()
			if err != nil {
				return fmt.Errorf("profile %q was not removed: cannot open the secret store: %w", name, err)
			}
		}
		if err := store.Delete(secret.ref); err != nil {
			return fmt.Errorf("profile %q was not removed: %s: %w", name, secret.fail, err)
		}
		deleted = append(deleted, secret.what)
	}

	delete(profiles.Profile, name)
	if profiles.DefaultProfile == name {
		profiles.DefaultProfile = ""
		// If there's only one profile left, make it the default.
		if len(profiles.Profile) == 1 {
			for n := range profiles.Profile {
				profiles.DefaultProfile = n
			}
		}
	}

	if err := saveProfiles(profiles); err != nil {
		switch len(deleted) {
		case 0:
			return err
		case 1:
			return errors.Join(err, fmt.Errorf(
				"profile %q was not removed from profiles.toml, but its %s was removed from the secret store", name, deleted[0]))
		default:
			return errors.Join(err, fmt.Errorf(
				"profile %q was not removed from profiles.toml, but its %s were removed from the secret store", name, strings.Join(deleted, " and ")))
		}
	}

	fmt.Fprintf(e.stdout, "Profile %q removed.\n", name)
	return nil
}

func runProfileAdd(ctx context.Context, e *env, args []string) error {
	var name string
	switch len(args) {
	case 0:
	case 1:
		name = args[0]
		if !config.ValidProfileName(name) {
			return usagef("invalid profile name %q: use ASCII letters, digits and '-', not starting with '-'", name)
		}
	default:
		return usagef("extra argument: profile add takes at most one profile name")
	}

	profiles, err := config.LoadProfiles()
	if err != nil {
		return err
	}

	store, err := config.NewSecretStore()
	if err != nil {
		return err
	}

	w := newWizard(e.stdout, newLinePrompter(e.stdin, e.stdout), store)
	name, prof, err := w.add(ctx, profiles, name)
	if err != nil {
		return err
	}

	fmt.Fprintf(e.stdout, "Profile %q added (exit %s, mode %s).\n", name, prof.Exit.Type, prof.Datapath)
	return nil
}

// newWizard builds the wizard the menu and `profile add` share: tun is offered when the
// datapath reports it works on this system, and its reason is shown when the answer is tun
// anyway.
func newWizard(out io.Writer, ask prompter, store config.SecretStore) *wizard {
	modes := []string{"tun", "socks"}
	var tunUnsupported error
	if requires := (&datapath.Tun{}).Requires(); requires.Unsupported != nil {
		modes = []string{"socks"}
		tunUnsupported = requires.Unsupported
	}
	return &wizard{
		ask:            ask,
		out:            out,
		store:          store,
		modes:          modes,
		tunUnsupported: tunUnsupported,
		save:           saveProfiles,
	}
}

func runProfileSet(_ context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return usagef("profile set requires a profile name")
	}
	if len(args) > 1 {
		return usagef("extra argument: profile set takes one profile name")
	}

	name := args[0]

	profiles, err := config.LoadProfiles()
	if err != nil {
		return err
	}

	_, ok := profiles.Profile[name]
	if !ok {
		return fmt.Errorf("profile %q not found; use 'profile add' to create it", name)
	}

	fmt.Fprintf(e.stdout, "profile set is not interactive yet. Change profile %q in the menu "+
		"(the profile items, \"another profile\" among them), or add a new profile with "+
		"'profile add', which asks every field.\n", name)
	return nil
}
