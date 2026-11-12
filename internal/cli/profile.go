package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/FreedomCats/dnstt-cli/internal/config"
	"github.com/FreedomCats/dnstt-cli/internal/datapath"
)

func profileCommand() *command {
	return &command{
		name:    "profile",
		args:    "<subcommand>",
		summary: "manage profiles",
		description: `Manage dnstt-cli profiles: add, set, show, rm, list.

Subcommands:
  add [name]      add a new profile with the interactive wizard
  set <name>      edit an existing profile (interactive)
  show <name>     show a profile
  rm <name>       remove a profile and its stored exit password
  list            list all profiles

The wizard asks for the profile name (a positional name skips the question; Enter takes
"home"), the tunnel domain, the server public key, the exit type (socks5 or http; the SSH
exit is not implemented yet), the exit login and password, the resolvers — one per line,
Enter for "network", presets by name — and the mode: tun where the system supports it (it
needs root to connect), otherwise socks.

The exit password goes to the secret store; profiles.toml holds only a reference to it.`,
		run: runProfile,
	}
}

func runProfile(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(profileCommand())
	if err := parse(e, fs, args); err != nil {
		return err
	}

	if fs.NArg() == 0 {
		return usagef("profile requires a subcommand: add, set, show, rm, list")
	}

	subcmd := fs.Arg(0)
	subArgs := fs.Args()[1:]

	switch subcmd {
	case "add":
		return runProfileAdd(ctx, e, subArgs)
	case "set":
		return runProfileSet(ctx, e, subArgs)
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
	fmt.Fprintf(e.stdout, "  Exit Type: %s\n", prof.Exit.Type)
	if prof.Exit.User != "" {
		fmt.Fprintf(e.stdout, "  Exit User: %s\n", prof.Exit.User)
	}
	if prof.Exit.PasswordRef != "" {
		fmt.Fprintf(e.stdout, "  Exit Password: (stored in %s)\n", prof.Exit.PasswordRef)
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

	// The secret goes first: a secret whose profile is gone stays in the store with
	// nothing pointing at it, and no command can find it again. A profile left without
	// its secret is visible in profiles.toml, and the command can say so.
	ref := prof.Exit.PasswordRef
	if ref != "" {
		store, err := newSecretStore()
		if err != nil {
			return fmt.Errorf("profile %q was not removed: cannot open the secret store: %w", name, err)
		}
		if err := store.Delete(ref); err != nil {
			return fmt.Errorf("profile %q was not removed: the exit password could not be deleted from the secret store: %w", name, err)
		}
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
		if ref == "" {
			return err
		}
		return errors.Join(err, fmt.Errorf(
			"profile %q was not removed from profiles.toml, but its exit password was removed from the secret store", name))
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
		if !validProfileName(name) {
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

	// tun is offered when the datapath reports it works on this system; the reason is
	// shown when the answer is tun anyway.
	modes := []string{"tun", "socks"}
	var tunUnsupported error
	if requires := (&datapath.Tun{}).Requires(); requires.Unsupported != nil {
		modes = []string{"socks"}
		tunUnsupported = requires.Unsupported
	}

	w := &wizard{
		ask:            newLinePrompter(e.stdin, e.stdout),
		out:            e.stdout,
		store:          store,
		modes:          modes,
		tunUnsupported: tunUnsupported,
		save:           config.SaveProfiles,
	}
	name, prof, err := w.add(ctx, profiles, name)
	if err != nil {
		return err
	}

	fmt.Fprintf(e.stdout, "Profile %q added (exit %s, mode %s).\n", name, prof.Exit.Type, prof.Datapath)
	return nil
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

	fmt.Fprintf(e.stdout, "Editing profile %q. This command is not yet interactive.\n", name)
	fmt.Fprintf(e.stdout, "Edit the file directly: %s\n", mustPath())
	return nil
}

func mustPath() string {
	p, err := config.Path()
	if err != nil {
		return "<config path>"
	}
	return p
}
