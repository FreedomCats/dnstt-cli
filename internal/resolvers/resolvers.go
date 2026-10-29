// Package resolvers is the list of resolvers a profile tunnels through (architecture.md
// R5): parsing of its entries, the built-in presets, the bootstrap resolver for their host
// names and the health that puts a bad resolver aside for a while.
package resolvers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/FreedomCats/dnstt-cli/core/client"
)

// Resolver is one entry of the list: a resolver the core can send its queries through,
// and the addresses of its host name if they are pinned.
type Resolver struct {
	Transport client.Transport
	// Address is what the core takes: the URL for DoH, host:port for DoT and UDP.
	Address string
	// Pins are the addresses of the host name, in the order to try them; empty — the
	// name is resolved by the bootstrap resolver. Never set for a host given as an
	// address.
	Pins []netip.Addr
	// Preset is the name of the preset the entry came from; "" for an entry of its own.
	Preset string
}

// Core is the resolver for core.Config.
func (r Resolver) Core() client.Resolver {
	return client.Resolver{Transport: r.Transport, Address: r.Address}
}

// Host is the host of Address: a name or an address.
func (r Resolver) Host() string {
	if r.Transport == client.DoH {
		u, err := url.Parse(r.Address)
		if err != nil {
			return ""
		}
		return u.Hostname()
	}
	host, _, _ := net.SplitHostPort(r.Address)
	return host
}

// String is the entry as it is written in a profile, the port always given; Parse reads it
// back to the same Resolver (without Preset).
func (r Resolver) String() string {
	s := string(r.Transport) + ":" + r.Address
	for i, a := range r.Pins {
		if i == 0 {
			s += "@"
		} else {
			s += ","
		}
		s += a.String()
	}
	return s
}

// Name is how the resolver is shown: the preset name or the entry.
func (r Resolver) Name() string {
	if r.Preset != "" {
		return r.Preset
	}
	return r.String()
}

// LookupFunc resolves a host name: core.Config.LookupIP.
type LookupFunc func(ctx context.Context, host string) ([]netip.Addr, error)

// LookupIP is the core.Config.LookupIP of a session through r: the pins for the host of r,
// fallback for anything else. A nil fallback refuses a name that is not pinned.
func (r Resolver) LookupIP(fallback LookupFunc) LookupFunc {
	host := r.Host()
	pins := slices.Clone(r.Pins)
	return func(ctx context.Context, name string) ([]netip.Addr, error) {
		if len(pins) > 0 && strings.EqualFold(name, host) {
			return slices.Clone(pins), nil
		}
		if fallback == nil {
			return nil, fmt.Errorf("%s is neither pinned nor resolvable without a bootstrap resolver", name)
		}
		return fallback(ctx, name)
	}
}

// Default ports of DoT (RFC 7858) and plain DNS.
const (
	portDoT = "853"
	portUDP = "53"
)

// Parse reads one entry: doh:URL, dot:host[:port], udp:host[:port], each optionally with
// @address[,address…] that pins the addresses of a host name. A preset name is not an
// entry: Expand takes those.
func Parse(entry string) (Resolver, error) {
	r, err := parse(strings.TrimSpace(entry))
	if err != nil {
		return Resolver{}, fmt.Errorf("resolver %q: %w", entry, err)
	}
	return r, nil
}

func parse(entry string) (Resolver, error) {
	prefix, rest, ok := strings.Cut(entry, ":")
	if !ok {
		return Resolver{}, errors.New("no transport: doh:, dot: or udp:, or a preset name")
	}
	var r Resolver
	switch t := client.Transport(strings.ToLower(prefix)); t {
	case client.DoH, client.DoT, client.UDP:
		r.Transport = t
	case "tcp":
		return Resolver{}, errors.New("transport tcp is not supported yet")
	default:
		return Resolver{}, fmt.Errorf("unknown transport %q: doh, dot or udp", prefix)
	}

	if i := strings.LastIndexByte(rest, '@'); i >= 0 {
		pins, err := parsePins(rest[i+1:])
		if err != nil {
			return Resolver{}, err
		}
		rest, r.Pins = rest[:i], pins
	}

	var host string
	var err error
	if r.Transport == client.DoH {
		r.Address, host, err = parseURL(rest)
	} else {
		port := portUDP
		if r.Transport == client.DoT {
			port = portDoT
		}
		r.Address, host, err = parseHostPort(rest, port)
	}
	if err != nil {
		return Resolver{}, err
	}
	if _, err := netip.ParseAddr(host); err == nil && len(r.Pins) > 0 {
		return Resolver{}, errors.New("an address is pinned to an address; pins are for host names")
	}
	return r, nil
}

func parsePins(s string) ([]netip.Addr, error) {
	if s == "" {
		return nil, errors.New("nothing after @: a pin is an address")
	}
	var pins []netip.Addr
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(p), "["), "]")
		a, err := netip.ParseAddr(p)
		if err != nil || a.Zone() != "" {
			return nil, fmt.Errorf("pin %q is not an IP address", p)
		}
		a = a.Unmap()
		if !slices.Contains(pins, a) {
			pins = append(pins, a)
		}
	}
	return pins, nil
}

func parseURL(s string) (address, host string, err error) {
	u, err := url.Parse(s)
	if err != nil {
		return "", "", errors.New("not a URL")
	}
	switch {
	case !strings.EqualFold(u.Scheme, "https"):
		return "", "", errors.New("a DoH URL starts with https://")
	case u.User != nil:
		return "", "", errors.New("a DoH URL with a user name")
	case u.Fragment != "":
		return "", "", errors.New("a DoH URL with a fragment")
	}
	host = u.Hostname()
	if err := checkHost(host); err != nil {
		return "", "", err
	}
	if p := u.Port(); p != "" {
		if _, err := parsePort(p); err != nil {
			return "", "", err
		}
	}
	return s, host, nil
}

// parseHostPort reads host[:port], host[:port] with an IPv6 host in brackets, or a bare
// IPv6 address.
func parseHostPort(s, defaultPort string) (address, host string, err error) {
	if a, err := netip.ParseAddr(s); err == nil {
		if err := checkAddr(a); err != nil {
			return "", "", err
		}
		host = a.Unmap().String()
		return net.JoinHostPort(host, defaultPort), host, nil
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		// No port: a host name or a bracketed address.
		host, port = s, defaultPort
		if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
			host = host[1 : len(host)-1]
		}
	}
	if a, err := netip.ParseAddr(host); err == nil {
		if err := checkAddr(a); err != nil {
			return "", "", err
		}
		host = a.Unmap().String()
	} else if err := checkHost(host); err != nil {
		return "", "", err
	}
	if _, err := parsePort(port); err != nil {
		return "", "", err
	}
	return net.JoinHostPort(host, port), host, nil
}

func checkAddr(a netip.Addr) error {
	if a.Zone() != "" {
		return errors.New("an address with a zone")
	}
	return nil
}

// checkHost accepts a host name of letters, digits, hyphens, underscores and dots, or an
// address.
func checkHost(host string) error {
	if _, err := netip.ParseAddr(host); err == nil {
		return nil
	}
	name := strings.TrimSuffix(host, ".")
	if name == "" || len(name) > 253 {
		return fmt.Errorf("bad host name %q", host)
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 {
			return fmt.Errorf("bad host name %q", host)
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return fmt.Errorf("bad host name %q", host)
			}
		}
	}
	return nil
}

func parsePort(p string) (uint16, error) {
	n, err := strconv.ParseUint(p, 10, 16)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("bad port %q", p)
	}
	return uint16(n), nil
}
