package cli

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/FreedomCats/dnstt-cli/core/client"
	"github.com/FreedomCats/dnstt-cli/core/dns"
	"github.com/FreedomCats/dnstt-cli/internal/config"
	"github.com/FreedomCats/dnstt-cli/internal/resolvers"
)

func resolversCommand() *command {
	return &command{
		name:    "resolvers",
		args:    "<subcommand>",
		summary: "check resolvers of a profile",
		description: `Check resolvers: perform a real Noise handshake through every resolver of the
profile and print the time or the reason for failure. Exit code 1 if none passes.

Subcommands:
  check [profile]   check all resolvers of the profile`,
		run: runResolvers,
	}
}

func runResolvers(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(resolversCommand())
	if err := parse(e, fs, args); err != nil {
		return err
	}

	if fs.NArg() == 0 {
		return usagef("resolvers requires a subcommand: check")
	}

	subcmd := fs.Arg(0)
	subArgs := fs.Args()[1:]

	switch subcmd {
	case "check":
		return runResolversCheck(ctx, e, subArgs)
	default:
		return usagef("unknown resolvers subcommand %q", subcmd)
	}
}

func runResolversCheck(ctx context.Context, e *env, args []string) error {
	var profileName string
	switch len(args) {
	case 0:
		// Use the default profile.
	case 1:
		profileName = args[0]
	default:
		return usagef("extra argument: resolvers check takes at most one profile name")
	}

	// Load profiles.
	profiles, err := config.LoadProfiles()
	if err != nil {
		return err
	}

	// Find the profile to use.
	if profileName == "" {
		profileName = profiles.DefaultProfile
		if profileName == "" {
			return errors.New("no default profile set")
		}
	}
	prof, ok := profiles.Profile[profileName]
	if !ok {
		return fmt.Errorf("profile %q not found", profileName)
	}

	// Parse the domain and public key.
	domain, err := dns.ParseName(prof.Domain)
	if err != nil {
		return fmt.Errorf("domain: %w", err)
	}
	pubkey, err := hex.DecodeString(prof.Pubkey)
	if err != nil {
		return fmt.Errorf("pubkey: %w", err)
	}
	if len(pubkey) != 32 {
		return fmt.Errorf("pubkey: expected 32 bytes, got %d", len(pubkey))
	}

	// Parse resolvers.
	resolverList := prof.Resolvers
	if len(resolverList) == 0 {
		resolverList = []string{"network"}
	}
	expanded, err := resolvers.Expand(resolverList)
	if err != nil {
		return fmt.Errorf("resolvers: %w", err)
	}

	fmt.Fprintf(e.stdout, "Checking %d resolver(s) for profile %q...\n", len(expanded), profileName)

	anyPassed := false
	for _, r := range expanded {
		// Skip network entry for now (it needs network DNS lookup).
		if r.Network {
			fmt.Fprintf(e.stdout, "  %s: SKIPPED (network DNS not implemented)\n", r.Name())
			continue
		}

		// Build the session config for this resolver.
		cfg := client.Config{
			Domain:   domain,
			PubKey:   pubkey,
			Resolver: r.Core(),
			UTLS:     prof.UTLS,
			LookupIP: r.LookupIP(nil), // No bootstrap for now.
		}

		// Try the handshake with a timeout.
		checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		start := time.Now()
		session, err := client.Dial(checkCtx, cfg)
		elapsed := time.Since(start)
		cancel()

		if err != nil {
			fmt.Fprintf(e.stdout, "  %s: FAILED (%v)\n", r.Name(), err)
		} else {
			fmt.Fprintf(e.stdout, "  %s: OK (%v)\n", r.Name(), elapsed.Truncate(time.Millisecond))
			session.Close()
			anyPassed = true
		}
	}

	if !anyPassed {
		fmt.Fprintf(e.stderr, "\nNo resolver passed the check.\n")
		return &exitCodeError{code: exitError}
	}

	return nil
}

// exitCodeError is an error that carries a specific exit code.
type exitCodeError struct {
	code int
	msg  string
}

func (e *exitCodeError) Error() string {
	if e.msg != "" {
		return e.msg
	}
	return fmt.Sprintf("exit code %d", e.code)
}
