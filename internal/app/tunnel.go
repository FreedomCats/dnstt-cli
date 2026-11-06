// Package app builds the tunnel from a profile: the supervisor, the exit dialer and the
// datapath, for commands to use.
package app

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"syscall"

	"github.com/FreedomCats/dnstt-cli/core/client"
	"github.com/FreedomCats/dnstt-cli/core/dns"
	"github.com/FreedomCats/dnstt-cli/internal/config"
	"github.com/FreedomCats/dnstt-cli/internal/datapath"
	"github.com/FreedomCats/dnstt-cli/internal/exit"
	"github.com/FreedomCats/dnstt-cli/internal/resolvers"
	"github.com/FreedomCats/dnstt-cli/internal/supervisor"
)

// TunnelOptions are the settings for building a tunnel from a profile.
type TunnelOptions struct {
	// Profile is the profile to connect with; required.
	Profile *config.Profile
	// ProfileName is the name of the profile, for logs.
	ProfileName string
	// Logf receives the lines of the tunnel and the datapath; nil — silent.
	Logf func(format string, args ...any)
	// OnTransition receives every state change of the supervisor; nil — silent.
	OnTransition func(supervisor.Transition)
}

// Tunnel is a dnstt tunnel built from a profile: the supervisor, the exit dialer and the
// datapath ready to be run.
type Tunnel struct {
	Supervisor *supervisor.Tunnel
	Datapath   interface {
		Run(context.Context) error
		Close() error
	}
	Exit exit.Dialer
}

// BuildTunnel creates the supervisor and the exit dialer from the profile, ready to be run.
// It does not start the datapath: that is created separately after the tunnel is built.
func BuildTunnel(opts TunnelOptions) (*Tunnel, error) {
	if opts.Profile == nil {
		return nil, errors.New("no profile")
	}
	prof := opts.Profile

	// Parse the domain.
	domain, err := dns.ParseName(prof.Domain)
	if err != nil {
		return nil, fmt.Errorf("domain: %w", err)
	}

	// Parse the public key.
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
	pool := resolvers.NewPool(expanded)

	// Read profile bootstrap servers once; network servers remain dynamic.
	networkDNS := SystemNetworkDNS
	bootstrap, err := newProfileBootstrap(prof.Bootstrap, expanded, networkDNS)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: %w", err)
	}

	// Build the session config for the supervisor.
	sessionCfg := client.Config{
		Domain: domain,
		PubKey: pubkey,
		UTLS:   prof.UTLS,
		Control: func(network, address string, c syscall.RawConn) error {
			// TODO: bind to the physical interface when netguard is integrated.
			return nil
		},
		Logf: opts.Logf,
	}

	// Build the exit dialer.
	exitType := prof.Exit.Type
	if exitType == "" {
		exitType = "socks5"
	}
	var exitDialer exit.Dialer
	switch exitType {
	case "socks5":
		var password string
		if prof.Exit.PasswordRef != "" {
			password, err = config.GetSecret(prof.Exit.PasswordRef)
			if err != nil {
				return nil, fmt.Errorf("exit password: %w", err)
			}
		}
		exitDialer = &exit.SOCKS5{
			User:     prof.Exit.User,
			Password: password,
			// Open will be set by BuildDatapath when the supervisor is available.
		}
	case "ssh":
		return nil, errors.New("SSH exit is not implemented yet (stage E5)")
	default:
		return nil, fmt.Errorf("unknown exit type %q", exitType)
	}

	// Build the supervisor.
	tun, err := supervisor.New(supervisor.Options{
		Session:    sessionCfg,
		Resolvers:  pool,
		Bootstrap:  bootstrap,
		NetworkDNS: networkDNS,
		CheckExit: func(ctx context.Context, open exit.OpenStream) error {
			if s5, ok := exitDialer.(*exit.SOCKS5); ok {
				// Temporarily set Open for the check.
				s5.Open = open
				return s5.Check(ctx)
			}
			return nil
		},
		OnTransition: opts.OnTransition,
		Logf:         opts.Logf,
	})
	if err != nil {
		return nil, fmt.Errorf("supervisor: %w", err)
	}

	return &Tunnel{
		Supervisor: tun,
		Exit:       exitDialer,
	}, nil
}

func newProfileBootstrap(entries []string, list []resolvers.Resolver, networkDNS func() ([]netip.Addr, error)) (*resolvers.Bootstrap, error) {
	var own []netip.AddrPort
	var err error
	if len(entries) > 0 {
		own, err = resolvers.ParseBootstrap(entries)
		if err != nil {
			return nil, err
		}
	}
	return resolvers.NewProfileBootstrap(own, list, networkDNS), nil
}

// BuildDatapath creates the datapath (socks or tun) for the tunnel. It must be called after
// the supervisor is built, because the datapath needs the supervisor's OpenStream.
// The datapath is already running when this returns (for socks, the listener is active).
func BuildDatapath(tun *Tunnel, prof *config.Profile, logf func(format string, args ...any)) (interface {
	Close() error
}, error) {
	datapathType := prof.Datapath
	if datapathType == "" {
		datapathType = "socks"
	}

	switch datapathType {
	case "socks":
		listen := prof.Socks.Listen
		if listen == "" {
			listen = datapath.DefaultSocksListen
		}
		// Set the Open function on the SOCKS5 exit.
		if s5, ok := tun.Exit.(*exit.SOCKS5); ok {
			s5.Open = tun.Supervisor.OpenStream
		}
		socks, err := datapath.NewSocks(datapath.SocksOptions{
			Listen: listen,
			Dialer: tun.Exit,
			Up: func() bool {
				return tun.Supervisor.State() == supervisor.StateConnected ||
					tun.Supervisor.State() == supervisor.StateDegraded
			},
			Logf: logf,
		})
		if err != nil {
			return nil, fmt.Errorf("socks datapath: %w", err)
		}
		return socks, nil
	case "tun":
		return nil, errors.New("tun datapath is not implemented yet (stage E2)")
	default:
		return nil, fmt.Errorf("unknown datapath type %q", datapathType)
	}
}
