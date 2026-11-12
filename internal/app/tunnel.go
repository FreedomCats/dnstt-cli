// Package app builds the tunnel from a profile: the supervisor, the exit dialer and the
// datapath, for commands to use.
package app

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"syscall"
	"unicode"

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
	// Notice receives the user-facing lines about system traffic between sessions; nil —
	// Logf.
	Notice func(format string, args ...any)
	// Control is the socket hook of the core: netguard.Guard.Control for a datapath that
	// takes all system traffic. nil — the core's sockets are left as they are (socks).
	Control func(network, address string, c syscall.RawConn) error
}

// Tunnel is a dnstt tunnel built from a profile: the supervisor and the exit dialer,
// ready to be run. The datapath is built separately by BuildDatapath.
type Tunnel struct {
	Supervisor *supervisor.Tunnel
	Exit       exit.Dialer
}

// BuildTunnel creates the supervisor and the exit dialer from the profile, ready to be run.
// It does not start the datapath: that is created separately after the tunnel is built.
func BuildTunnel(opts TunnelOptions) (*Tunnel, error) {
	return buildTunnel(opts, config.GetSecret)
}

func buildTunnel(opts TunnelOptions, getSecret func(string) (string, error)) (*Tunnel, error) {
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

	// Read profile bootstrap servers once; network servers remain dynamic. The bootstrap
	// sockets carry the same Control as the core's: under a closed kill-switch they are
	// the core's sockets too.
	networkDNS := SystemNetworkDNS
	bootstrap, err := newProfileBootstrap(prof.Bootstrap, expanded, networkDNS, opts.Control)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: %w", err)
	}

	// Build the session config for the supervisor.
	sessionCfg := sessionConfig(prof, domain, pubkey, opts)

	// Build the exit dialer.
	exitType := prof.Exit.Type
	if exitType == "" {
		exitType = "socks5"
	}
	user := prof.Exit.User
	var password string
	var checkExit func(context.Context, exit.OpenStream) error
	switch exitType {
	case "socks5":
		if prof.Exit.PasswordRef != "" {
			if user == "" {
				return nil, errors.New("SOCKS5 exit: password_ref requires a user")
			}
			password, err = getSecret(prof.Exit.PasswordRef)
			if err != nil {
				return nil, fmt.Errorf("exit password: %w", err)
			}
		}
		checkExit = func(ctx context.Context, open exit.OpenStream) error {
			// The startup check stays sequential: it has no target and must work against
			// any server, so it sends no CONNECT and never batches.
			return (&exit.SOCKS5{Open: open, User: user, Password: password}).Check(ctx)
		}
	case "http":
		if strings.Contains(user, ":") || strings.ContainsFunc(user, unicode.IsControl) {
			return nil, errors.New("HTTP exit: invalid user")
		}
		if prof.Exit.PasswordRef != "" {
			if user == "" {
				return nil, errors.New("HTTP exit: password_ref requires a user")
			}
			password, err = getSecret(prof.Exit.PasswordRef)
			if err != nil {
				// Store errors can contain references, paths or private payloads.
				return nil, errors.New("HTTP exit: cannot read password from the secret store")
			}
		}
		if strings.ContainsFunc(password, unicode.IsControl) {
			return nil, errors.New("HTTP exit: invalid password")
		}
	case "ssh":
		return nil, errors.New("SSH exit is not implemented yet (stage E5)")
	default:
		return nil, fmt.Errorf("unknown exit type %q", exitType)
	}

	// Build the supervisor.
	tun, err := supervisor.New(supervisor.Options{
		Session:      sessionCfg,
		Resolvers:    pool,
		Bootstrap:    bootstrap,
		NetworkDNS:   networkDNS,
		CheckExit:    checkExit,
		OnTransition: opts.OnTransition,
		Notice:       opts.Notice,
		Logf:         opts.Logf,
	})
	if err != nil {
		return nil, fmt.Errorf("supervisor: %w", err)
	}
	var exitDialer exit.Dialer
	if exitType == "http" {
		exitDialer = &httpExit{
			dialer: &exit.HTTP{Open: tun.OpenStream, User: user, Password: password},
			fatal:  tun.FatalExit,
		}
	} else {
		// The profile's one_write reaches the datapath only: this dialer is its sole
		// consumer, and the startup check above never sees it.
		exitDialer = &exit.SOCKS5{Open: tun.OpenStream, User: user, Password: password, OneWrite: prof.Exit.OneWrite}
	}

	return &Tunnel{
		Supervisor: tun,
		Exit:       exitDialer,
	}, nil
}

// sessionConfig is the client.Config of every session of a connect. Its keepalive is the
// approved policy timeout: the supervisor copies Options.Session on every dial, so the first
// connection and every redial carry it. No profile key sets it.
func sessionConfig(prof *config.Profile, domain dns.Name, pubkey []byte, opts TunnelOptions) client.Config {
	return client.Config{
		Domain:           domain,
		PubKey:           pubkey,
		UTLS:             prof.UTLS,
		Control:          opts.Control,
		Logf:             opts.Logf,
		KeepAliveTimeout: supervisor.DefaultKeepAliveTimeout,
	}
}

// httpExit stops the tunnel on the first complete authentication refusal. Other HTTP
// failures belong to their application request, not to the resolver's health.
type httpExit struct {
	dialer exit.Dialer
	fatal  func(error)
	once   sync.Once
}

func (h *httpExit) Dial(ctx context.Context, address string) (net.Conn, error) {
	conn, err := h.dialer.Dial(ctx, address)
	if exit.IsLogin(err) {
		h.once.Do(func() { h.fatal(err) })
	}
	return conn, err
}

func newProfileBootstrap(entries []string, list []resolvers.Resolver, networkDNS func() ([]netip.Addr, error), control func(network, address string, c syscall.RawConn) error) (*resolvers.Bootstrap, error) {
	var own []netip.AddrPort
	var err error
	if len(entries) > 0 {
		own, err = resolvers.ParseBootstrap(entries)
		if err != nil {
			return nil, err
		}
	}
	return resolvers.NewProfileBootstrap(own, list, networkDNS, control), nil
}

// Datapath is what BuildDatapath returns: the datapath of the run, socks or tun. For tun
// only the object is built here: the adapter is opened by the first Start of the
// supervisor, when the exit is known to serve.
type Datapath interface {
	Close() error
	Requires() datapath.Requirements
}

var (
	_ Datapath = (*datapath.Tun)(nil)
	_ Datapath = (*datapath.Socks)(nil)
)

// defaultTunDNS is the DNS behind the exit when the profile has no tun_dns.
var defaultTunDNS = netip.MustParseAddr("1.1.1.1")

// parseTunDNS reads the profile's tun_dns: the DNS behind the exit, an IPv4 address
// without a port (the netstack dials port 53). Empty means defaultTunDNS.
func parseTunDNS(s string) (netip.Addr, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return defaultTunDNS, nil
	}
	addr, err := netip.ParseAddr(s)
	if err != nil || !addr.Is4() {
		return netip.Addr{}, fmt.Errorf("tun_dns %q is not an IPv4 address", s)
	}
	return addr, nil
}

// BuildDatapath creates the datapath (socks or tun) for the tunnel. It must be called after
// the supervisor is built, because the datapath needs the supervisor's OpenStream.
//
// For socks the listener is already open when this returns: a program may be pointed at
// it while the tunnel is still dialing. For tun nothing is opened until a session: the
// adapter and its routes are the supervisor's Start business, because the system traffic
// may lead into the tunnel only when the exit serves.
func BuildDatapath(tun *Tunnel, prof *config.Profile, logf func(format string, args ...any)) (Datapath, error) {
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
		// A stop drains the datapath before the session is closed: the relay of an open
		// connection ends while the tunnel is still up (architecture.md §08). SetDrain is
		// called before Run, which happens after this function returns.
		tun.Supervisor.SetDrain(func(ctx context.Context) { _ = socks.Drain(ctx) })
		return socks, nil
	case "tun":
		dns, err := parseTunDNS(prof.TunDNS)
		if err != nil {
			return nil, err
		}
		t, err := datapath.NewTun(datapath.TunOptions{
			Dialer: tun.Exit,
			TunDNS: dns,
			Logf:   logf,
		})
		if err != nil {
			return nil, fmt.Errorf("tun datapath: %w", err)
		}
		return t, nil
	default:
		return nil, fmt.Errorf("unknown datapath type %q", datapathType)
	}
}
