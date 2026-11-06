package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/FreedomCats/dnstt-cli/core/client"
	"github.com/FreedomCats/dnstt-cli/core/dns"
	"github.com/FreedomCats/dnstt-cli/internal/config"
	"github.com/FreedomCats/dnstt-cli/internal/resolvers"
)

func TestResolverChecksNetworkAndBootstrap(t *testing.T) {
	list, err := resolvers.Expand([]string{"network", "dot:dot.example"})
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("injected network DNS failure")
	calls := 0
	checks := resolverChecks(list, nil, func() ([]netip.Addr, error) {
		calls++
		if calls == 1 {
			return []netip.Addr{
				netip.MustParseAddr("198.51.100.53"),
				netip.MustParseAddr("2001:db8::53"),
			}, nil
		}
		return nil, want
	})
	if len(checks) != 3 || checks[0].name != "network (198.51.100.53)" || checks[0].resolver.String() != "udp:198.51.100.53:53" {
		t.Fatalf("checks: %+v", checks)
	}
	if checks[1].name != "network (2001:db8::53)" || checks[1].resolver.String() != "udp:[2001:db8::53]:53" {
		t.Fatalf("second network check: %+v", checks[1])
	}
	if checks[0].err != nil {
		t.Fatalf("network resolver setup error %v", checks[0].err)
	}
	if _, err := checks[2].lookup(nil, "dot.example"); !errors.Is(err, want) {
		t.Fatalf("bootstrap lookup error %v, want %v", err, want)
	}
}

func TestResolverChecksExplicitBootstrapAndPinnedBypass(t *testing.T) {
	list, err := resolvers.Expand([]string{"doh:https://doh.example/dns-query@192.0.2.1", "udp:192.0.2.53"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	checks := resolverChecks(list, []string{"203.0.113.53:5353"}, func() ([]netip.Addr, error) {
		calls++
		return nil, errors.New("must not read network DNS")
	})
	if calls != 0 {
		t.Fatalf("network DNS read %d times with explicit bootstrap", calls)
	}
	got, err := checks[0].lookup(context.Background(), "doh.example")
	if err != nil || !slices.Equal(got, []netip.Addr{netip.MustParseAddr("192.0.2.1")}) {
		t.Fatalf("pinned lookup %v, %v", got, err)
	}
	if calls != 0 {
		t.Fatalf("pinned resolver triggered network DNS %d times", calls)
	}
	if checks[1].resolver.Host() != "192.0.2.53" || checks[1].resolver.Core().Address != "192.0.2.53:53" {
		t.Fatalf("resolver address changed: %+v", checks[1].resolver)
	}
}

func TestResolverChecksReportsNetworkDNSErrors(t *testing.T) {
	want := errors.New("system DNS reader failed")
	list, err := resolvers.Expand([]string{"network", "dot:dot.example@192.0.2.1", "udp:192.0.2.53"})
	if err != nil {
		t.Fatal(err)
	}
	checks := resolverChecks(list, nil, func() ([]netip.Addr, error) { return nil, want })
	if len(checks) != 3 || checks[0].err == nil || !errors.Is(checks[0].err, want) {
		t.Fatalf("network check errors: %+v", checks)
	}
	if checks[1].err != nil || checks[2].err != nil {
		t.Fatalf("pinned/address resolver setup errors: %v, %v", checks[1].err, checks[2].err)
	}
}

func TestRunResolverChecksReportsNetworkDNSError(t *testing.T) {
	list, err := resolvers.Expand([]string{"network", "doh:https://doh.example/dns-query@192.0.2.1", "udp:192.0.2.53"})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	e := &env{stdout: &stdout, stderr: &stderr}
	prof := &config.Profile{}
	domain, err := dns.ParseName("t.example.com")
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("system DNS reader failed")
	networkCalls := 0
	dials := 0
	err = runResolverChecks(context.Background(), e, "test", prof, domain, make([]byte, 32), list,
		func() ([]netip.Addr, error) { networkCalls++; return nil, want },
		func(ctx context.Context, cfg client.Config) (*client.Session, error) {
			dials++
			if cfg.Resolver.Transport == client.DoH {
				got, err := cfg.LookupIP(ctx, "doh.example")
				if err != nil || !slices.Equal(got, []netip.Addr{netip.MustParseAddr("192.0.2.1")}) {
					t.Errorf("pinned resolver lookup %v, %v", got, err)
				}
			}
			return nil, errors.New("injected handshake failure")
		})
	var exitErr *exitCodeError
	if !errors.As(err, &exitErr) || exitErr.code != exitError {
		t.Fatalf("runResolverChecks error %v, want exit code %d", err, exitError)
	}
	if got := stdout.String(); !strings.Contains(got, fmt.Sprintf("network: FAILED (network DNS: %v)", want)) {
		t.Errorf("network DNS error not reported as FAILED:\n%s", got)
	}
	for _, name := range []string{"doh:https://doh.example/dns-query@192.0.2.1", "udp:192.0.2.53:53"} {
		if !strings.Contains(stdout.String(), name+": FAILED (injected handshake failure)") {
			t.Errorf("resolver %q was not checked:\n%s", name, stdout.String())
		}
	}
	if dials != 2 {
		t.Errorf("dial called %d times, want 2", dials)
	}
	if networkCalls != 1 {
		t.Errorf("network DNS called %d times; the pinned resolver should bypass bootstrap", networkCalls)
	}
}

func TestRunResolverChecksDialsEveryNetworkResolver(t *testing.T) {
	list, err := resolvers.Expand([]string{"network"})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	e := &env{stdout: &stdout, stderr: &stderr}
	prof := &config.Profile{}
	domain, err := dns.ParseName("t.example.com")
	if err != nil {
		t.Fatal(err)
	}
	servers := []netip.Addr{netip.MustParseAddr("198.51.100.53"), netip.MustParseAddr("2001:db8::53")}
	var dialed []string
	err = runResolverChecks(context.Background(), e, "test", prof, domain, make([]byte, 32), list,
		func() ([]netip.Addr, error) { return servers, nil },
		func(_ context.Context, cfg client.Config) (*client.Session, error) {
			dialed = append(dialed, cfg.Resolver.Address)
			return nil, errors.New("injected handshake failure")
		})
	var exitErr *exitCodeError
	if !errors.As(err, &exitErr) || exitErr.code != exitError {
		t.Fatalf("runResolverChecks error %v, want exit code %d", err, exitError)
	}
	want := []string{"198.51.100.53:53", "[2001:db8::53]:53"}
	if !slices.Equal(dialed, want) {
		t.Errorf("dialed resolvers %v, want %v", dialed, want)
	}
	for _, name := range []string{"network (198.51.100.53)", "network (2001:db8::53)"} {
		if !strings.Contains(stdout.String(), name+": FAILED (injected handshake failure)") {
			t.Errorf("network resolver %q result missing:\n%s", name, stdout.String())
		}
	}
}
