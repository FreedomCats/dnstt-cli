package cli

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/FreedomCats/dnstt-cli/core/client"
	"github.com/FreedomCats/dnstt-cli/core/dns"
	"github.com/FreedomCats/dnstt-cli/internal/app"
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

	return runResolverChecks(ctx, e, profileName, prof, domain, pubkey, expanded, app.SystemNetworkDNS, client.Dial)
}

type resolverDialFunc func(context.Context, client.Config) (*client.Session, error)

func runResolverChecks(ctx context.Context, e *env, profileName string, prof *config.Profile, domain dns.Name, pubkey []byte, expanded []resolvers.Resolver, networkDNS func() ([]netip.Addr, error), dial resolverDialFunc) error {
	checks := resolverChecks(expanded, prof.Bootstrap, networkDNS)
	fmt.Fprintf(e.stdout, "Checking %d resolver(s) for profile %q...\n", len(checks), profileName)

	anyPassed := false
	for _, check := range checks {
		r := check.resolver
		if check.err != nil {
			fmt.Fprintf(e.stdout, "  %s: FAILED (%v)\n", check.name, check.err)
			continue
		}

		// Build the session config for this resolver.
		cfg := client.Config{
			Domain:   domain,
			PubKey:   pubkey,
			Resolver: r.Core(),
			UTLS:     prof.UTLS,
			LookupIP: check.lookup,
		}

		// Try the handshake with a timeout.
		checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		start := time.Now()
		session, err := dial(checkCtx, cfg)
		elapsed := time.Since(start)
		cancel()

		if err != nil {
			fmt.Fprintf(e.stdout, "  %s: FAILED (%v)\n", check.name, err)
		} else {
			fmt.Fprintf(e.stdout, "  %s: OK (%v)\n", check.name, elapsed.Truncate(time.Millisecond))
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

type resolverCheck struct {
	name     string
	resolver resolvers.Resolver
	lookup   resolvers.LookupFunc
	err      error
}

func resolverChecks(list []resolvers.Resolver, own []string, networkDNS func() ([]netip.Addr, error)) []resolverCheck {
	var ownServers []netip.AddrPort
	if len(own) > 0 {
		var err error
		ownServers, err = resolvers.ParseBootstrap(own)
		if err != nil {
			return []resolverCheck{{name: "bootstrap", err: err}}
		}
	}
	bootstrap := resolvers.NewProfileBootstrap(ownServers, list, networkDNS)

	var checks []resolverCheck
	for _, r := range list {
		if !r.Network {
			checks = append(checks, resolverCheck{name: r.Name(), resolver: r, lookup: r.LookupIP(bootstrap.LookupIP)})
			continue
		}
		servers, err := networkDNS()
		if err != nil {
			checks = append(checks, resolverCheck{name: r.Name(), err: fmt.Errorf("network DNS: %w", err)})
			continue
		}
		if len(servers) == 0 {
			checks = append(checks, resolverCheck{name: r.Name(), err: errors.New("network DNS: the system has no DNS server")})
			continue
		}
		for _, server := range servers {
			networkResolver := resolvers.NetworkResolver(server)
			checks = append(checks, resolverCheck{name: fmt.Sprintf("network (%s)", server), resolver: networkResolver, lookup: networkResolver.LookupIP(bootstrap.LookupIP)})
		}
	}
	return checks
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
