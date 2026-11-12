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
	"github.com/FreedomCats/dnstt-cli/internal/supervisor"
)

func resolversCommand() *command {
	return &command{
		name:    "resolvers",
		args:    "<subcommand>",
		summary: "check resolvers of a profile",
		description: `Check resolvers: perform a real Noise handshake through every resolver of the
profile and print the time or the reason for failure. Exit code 1 if none passes.

A resolver is doh:URL, dot:host[:port], udp:host[:port] or tcp:host[:port], or a preset
name.

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

	_, err := checkProfileResolvers(ctx, e, profileName)
	return err
}

// checkProfileResolvers checks the resolvers of one profile: the load, the default profile
// when the name is empty, the domain and key parse, the resolvers' expansion and the
// checks themselves. The command and the menu share it.
func checkProfileResolvers(ctx context.Context, e *env, profileName string) ([]resolverOutcome, error) {
	// Load profiles.
	profiles, err := config.LoadProfiles()
	if err != nil {
		return nil, err
	}

	// Find the profile to use.
	if profileName == "" {
		profileName = profiles.DefaultProfile
		if profileName == "" {
			return nil, errors.New("no default profile set")
		}
	}
	prof, ok := profiles.Profile[profileName]
	if !ok {
		return nil, fmt.Errorf("profile %q not found", profileName)
	}

	// Parse the domain and public key.
	domain, err := dns.ParseName(prof.Domain)
	if err != nil {
		return nil, fmt.Errorf("domain: %w", err)
	}
	pubkey, err := hex.DecodeString(prof.Pubkey)
	if err != nil {
		return nil, fmt.Errorf("pubkey: %w", err)
	}
	if len(pubkey) != 32 {
		return nil, fmt.Errorf("pubkey: expected 32 bytes, got %d", len(pubkey))
	}

	// Parse resolvers.
	resolverList := prof.Resolvers
	if len(resolverList) == 0 {
		resolverList = []string{"network"}
	}
	expanded, err := resolvers.Expand(resolverList)
	if err != nil {
		return nil, fmt.Errorf("resolvers: %w", err)
	}

	return checkResolvers(ctx, e, profileName, prof, domain, pubkey, expanded, app.SystemNetworkDNS, client.Dial)
}

type resolverDialFunc func(context.Context, client.Config) (*client.Session, error)

func runResolverChecks(ctx context.Context, e *env, profileName string, prof *config.Profile, domain dns.Name, pubkey []byte, expanded []resolvers.Resolver, networkDNS func() ([]netip.Addr, error), dial resolverDialFunc) error {
	_, err := checkResolvers(ctx, e, profileName, prof, domain, pubkey, expanded, networkDNS, dial)
	return err
}

// resolverOutcome is one checked entry: the profile entry it came from, the name that was
// printed, and whether the handshake passed.
type resolverOutcome struct {
	entry string
	name  string
	ok    bool
}

// checkResolvers runs the checks and prints their lines. It returns the outcome of every
// check, or no outcomes when nothing passed: there is nothing to drop from a run where
// every resolver failed, and the exitCodeError carries that result.
func checkResolvers(ctx context.Context, e *env, profileName string, prof *config.Profile, domain dns.Name, pubkey []byte, expanded []resolvers.Resolver, networkDNS func() ([]netip.Addr, error), dial resolverDialFunc) ([]resolverOutcome, error) {
	checks := resolverChecks(expanded, prof.Bootstrap, networkDNS)
	fmt.Fprintf(e.stdout, "Checking %d resolver(s) for profile %q...\n", len(checks), profileName)

	var outcomes []resolverOutcome
	anyPassed := false
	for _, check := range checks {
		r := check.resolver
		if check.err != nil {
			fmt.Fprintf(e.stdout, "  %s: FAILED (%v)\n", check.name, check.err)
			outcomes = append(outcomes, resolverOutcome{entry: check.entry, name: check.name})
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

		// Try the handshake within the supervisor's handshake timeout: the policy value
		// is shared, and a shorter caller context still bounds it.
		checkCtx, cancel := context.WithTimeout(ctx, supervisor.DefaultHandshakeTimeout)
		start := time.Now()
		session, err := dial(checkCtx, cfg)
		elapsed := time.Since(start)
		cancel()

		if err != nil {
			fmt.Fprintf(e.stdout, "  %s: FAILED (%v)\n", check.name, err)
			outcomes = append(outcomes, resolverOutcome{entry: check.entry, name: check.name})
		} else {
			fmt.Fprintf(e.stdout, "  %s: OK (%v)\n", check.name, elapsed.Truncate(time.Millisecond))
			session.Close()
			anyPassed = true
			outcomes = append(outcomes, resolverOutcome{entry: check.entry, name: check.name, ok: true})
		}
	}

	if !anyPassed {
		fmt.Fprintf(e.stderr, "\nNo resolver passed the check.\n")
		return nil, &exitCodeError{code: exitError}
	}

	return outcomes, nil
}

type resolverCheck struct {
	// entry is the profile entry the check came from: the resolver's name, the network
	// entry for one of the system servers, or "" for the bootstrap parse error, which is
	// not a resolver of the profile.
	entry    string
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
	// resolvers check runs outside a tunnel: its sockets need no Control.
	bootstrap := resolvers.NewProfileBootstrap(ownServers, list, networkDNS, nil)

	var checks []resolverCheck
	for _, r := range list {
		if !r.Network {
			checks = append(checks, resolverCheck{entry: r.Name(), name: r.Name(), resolver: r, lookup: r.LookupIP(bootstrap.LookupIP)})
			continue
		}
		servers, err := networkDNS()
		if err != nil {
			checks = append(checks, resolverCheck{entry: resolvers.NetworkEntry, name: r.Name(), err: fmt.Errorf("network DNS: %w", err)})
			continue
		}
		if len(servers) == 0 {
			checks = append(checks, resolverCheck{entry: resolvers.NetworkEntry, name: r.Name(), err: errors.New("network DNS: the system has no DNS server")})
			continue
		}
		for _, server := range servers {
			networkResolver := resolvers.NetworkResolver(server)
			checks = append(checks, resolverCheck{entry: resolvers.NetworkEntry, name: fmt.Sprintf("network (%s)", server), resolver: networkResolver, lookup: networkResolver.LookupIP(bootstrap.LookupIP)})
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
