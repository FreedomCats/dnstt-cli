package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/FreedomCats/dnstt-cli/internal/config"
)

func profileCommand() *command {
	return &command{
		name:    "profile",
		args:    "<subcommand>",
		summary: "manage profiles",
		description: `Manage dnstt-cli profiles: add, set, show, rm, list.

Subcommands:
  add <name>      add a new profile (interactive)
  set <name>      edit an existing profile (interactive)
  show <name>     show a profile
  rm <name>       remove a profile
  list            list all profiles`,
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

	// Simple flag parsing: -full comes before the name.
	for i, arg := range args {
		if arg == "-full" {
			showFull = true
		} else if !strings.HasPrefix(arg, "-") {
			if name != "" {
				return usagef("extra argument: profile show takes one profile name")
			}
			name = arg
		} else if i > 0 {
			return usagef("unknown flag %q", arg)
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
	fmt.Fprintf(e.stdout, "  Kill Switch: %v\n", prof.KillSwitch)
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

	if _, ok := profiles.Profile[name]; !ok {
		return fmt.Errorf("profile %q not found", name)
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

	if err := config.SaveProfiles(profiles); err != nil {
		return err
	}

	fmt.Fprintf(e.stdout, "Profile %q removed.\n", name)
	return nil
}

func runProfileAdd(_ context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return usagef("profile add requires a profile name")
	}
	if len(args) > 1 {
		return usagef("extra argument: profile add takes one profile name")
	}

	name := args[0]

	profiles, err := config.LoadProfiles()
	if err != nil {
		// If the file doesn't exist, create a new profiles structure.
		profiles = &config.Profiles{
			Profile: make(map[string]*config.Profile),
		}
	}

	if _, exists := profiles.Profile[name]; exists {
		return fmt.Errorf("profile %q already exists; use 'profile set' to edit it", name)
	}

	// Create a minimal profile.
	prof := &config.Profile{
		Domain:     "",
		Pubkey:     "",
		Resolvers:  []string{"network"},
		Datapath:   "socks",
		KillSwitch: true,
		Exit: config.Exit{
			Type: "socks5",
		},
	}

	profiles.Profile[name] = prof
	if profiles.DefaultProfile == "" {
		profiles.DefaultProfile = name
	}

	if err := config.SaveProfiles(profiles); err != nil {
		return err
	}

	fmt.Fprintf(e.stdout, "Profile %q added. Use 'profile set %s' to configure it.\n", name, name)
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
