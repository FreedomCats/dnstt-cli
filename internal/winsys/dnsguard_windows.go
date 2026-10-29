package winsys

import (
	"cmp"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"slices"
	"sync"

	"github.com/tailscale/wf"
	"golang.org/x/sys/windows"

	"github.com/FreedomCats/dnstt-cli/internal/app"
)

// Constant GUIDs of the WFP provider and sublayer: by them our filters are recognised in
// `netsh wfp show filters`, and scripts/checktunnel looks up the provider. Rules get new
// GUIDs on every Enable.
//
// The GUIDs are our own, not inherited from the project the shell was carried over from:
// both programs may be installed on one machine, and neither must take the other's
// filters for its own.
var (
	DNSGuardProvider = wf.ProviderID{
		Data1: 0x61375d0e, Data2: 0x67e6, Data3: 0x43fd,
		Data4: [8]byte{0x84, 0x47, 0x73, 0x31, 0xa4, 0xcc, 0xd9, 0x2e},
	}
	dnsGuardSublayer = wf.SublayerID{
		Data1: 0xfb36600d, Data2: 0x3fc7, Data3: 0x4704,
		Data4: [8]byte{0xa4, 0x15, 0xe9, 0x5e, 0x84, 0x96, 0x0c, 0x76},
	}
)

// Rule weights inside the sublayer: a permit is heavier than a block, as in blockDNS of
// wireguard-windows (tunnel/firewall/rules.go, MIT, v1.1.1).
const (
	dnsGuardPermitWeight = 15
	dnsGuardBlockWeight  = 14
)

// DNSGuard is the DNS leak protection: WFP filters forbid connections to port 53, UDP and
// TCP, to all addresses except the tunnel's resolvers, loopback and queries of dnstt-cli
// itself.
//
// The WFP session is dynamic: the system removes everything created in it when it is
// closed or when the process dies, so a crashed dnstt-cli leaves no filters behind and no
// cleanup is needed on start.
//
// Our own process, dnstt-cli.exe, is allowed everything: the tunnel core runs inside it
// and reaches the dnstt resolver with sockets that bypass the tunnel (bound to the
// interface by the netguard Control hook, §06); in UDP mode those are queries to port 53
// themselves, and without the permit the tunnel could not connect. System queries come
// from the DNS client service, not from programs; the rule by the address of the tunnel's
// resolver lets them through.
//
// The session is opened once per dnstt-cli connect (OpenDNSGuard), and the rules are set
// and removed along the way (Enable, Lockdown, Disable): this way a WFP failure is found
// out before connecting, not in the middle of it. The kill-switch, the "closed" set, lives
// in the same session too, see Lockdown.
type DNSGuard struct {
	mu      sync.Mutex
	session *wf.Session
	appIDs  []string   // dnstt-cli and extra programs: they are allowed everything
	rules   []*wf.Rule // set in the session
	state   guardState
	// blockIPv6 tells Enable to close the system's IPv6 outward (BlockIPv6).
	blockIPv6 bool

	// step is for the transition test: it is called after every rule that was set
	// (added) or removed, while g.mu is held.
	step func(r *wf.Rule, added bool)
}

// guardState tells which set of rules is in the session.
type guardState int

const (
	guardOpen     guardState = iota // no rules: nothing is forbidden
	guardTunnel                     // the tunnel is up: DNS protection (Enable)
	guardLockdown                   // closed: kill-switch (Lockdown)
)

// OpenDNSGuard opens a WFP session and creates the provider and sublayer in it; there are
// no rules yet. It needs administrator rights and a running BFE service (Base Filtering
// Engine).
//
// Everything is allowed to the process itself and to the programs, full paths to exe
// files. dnstt-cli passes none: the tunnel core runs inside dnstt-cli.exe. WFP recognises
// a program by the path to its file, not by the process, so a path must be the one it is
// started from. Paths are converted to long names: WFP knows the process by them, while a
// path with short names (8.3, "PROGRA~1") gives a different identifier, and the
// kill-switch would close dnstt-cli itself.
func OpenDNSGuard(programs ...string) (*DNSGuard, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("path to dnstt-cli.exe: %w", err)
	}
	var appIDs []string
	for _, path := range append([]string{exe}, programs...) {
		appID, err := wf.AppID(longPath(path))
		if err != nil {
			return nil, fmt.Errorf("WFP application ID for %s: %w", path, err)
		}
		appIDs = append(appIDs, appID)
	}

	session, err := wf.New(&wf.Options{
		Name:        app.Name,
		Description: "dnstt-cli: DNS leak protection",
		Dynamic:     true,
	})
	if err != nil {
		return nil, fmt.Errorf("WFP session: %w", err)
	}
	if err := session.AddProvider(&wf.Provider{
		ID:          DNSGuardProvider,
		Name:        app.Name,
		Description: "dnstt-cli: DNS leak protection",
	}); err != nil {
		session.Close()
		return nil, fmt.Errorf("WFP provider: %w", err)
	}
	if err := session.AddSublayer(&wf.Sublayer{
		ID:          dnsGuardSublayer,
		Name:        app.Name + " DNS",
		Description: "dnstt-cli: DNS only through the tunnel",
		Provider:    DNSGuardProvider,
		Weight:      ^uint16(0),
	}); err != nil {
		session.Close()
		return nil, fmt.Errorf("WFP sublayer: %w", err)
	}
	return &DNSGuard{session: session, appIDs: appIDs}, nil
}

// longPath returns path with long names instead of short 8.3 ones. If that fails (no
// file, no rights on the directory), it returns path as is: then WFP gets the same one.
func longPath(path string) string {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return path
	}
	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetLongPathName(p, &buf[0], uint32(len(buf)))
		switch {
		case err != nil || n == 0:
			return path
		case int(n) < len(buf):
			// It fit: n is the length without the trailing zero.
			return windows.UTF16ToString(buf[:n])
		}
		// The buffer is too small: n is the required size with the zero.
		buf = make([]uint16, n)
	}
}

// BlockIPv6 sets whether the next Enable closes the system's IPv6 outward: the tunnel does
// not carry IPv6, and if there are no IPv6 routes into the adapter (IPv6 is off on it),
// IPv6 would bypass the tunnel. What stays open is loopback, our own processes and, as in
// "closed", DHCPv6, ND, RA and the IPv6 local network; DNS to the local network is closed
// by the port 53 block.
func (g *DNSGuard) BlockIPv6(block bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.blockIPv6 = block
}

// Enable sets the rules: DNS only to the resolvers dns. The previous rules are replaced
// (replaceLocked), so a repeated call is harmless. On failure the rules are removed so
// that no old ones remain either, except the "closed" set: it is stricter than DNS
// protection, and there is no reason to open the system because of a failure.
func (g *DNSGuard) Enable(dns []netip.Addr) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	rules, err := dnsGuardRules(dns, g.appIDs, g.blockIPv6)
	if err == nil {
		err = g.replaceLocked(guardTunnel, "DNS protection", rules)
	}
	if err == nil || g.state == guardLockdown {
		return err
	}
	return errors.Join(err, g.disableLocked())
}

// replaceLocked sets rules in place of the previous rules and on success records the
// state; what names whose rules they are, for errors.
//
// tailscale/wf has no WFP transactions, so the new rules are set first and the old ones
// removed after: there is no moment without rules during the transition. New rules are
// set from heavy to light, old ones removed from light to heavy, so the permit for
// dnstt-cli itself stays all the time, and blocks appear after their permits. While both
// the "closed" and the DNS protection sets are in place, "closed" with the tunnel's
// resolvers added is in effect.
//
// If a new rule fails to be set, the new rules already set are removed, and the previous
// set stays. If a rule fails to be removed, it stays in g.rules (the next Disable or Close
// removes it), and the error is returned.
func (g *DNSGuard) replaceLocked(state guardState, what string, rules []*wf.Rule) error {
	if g.session == nil {
		return fmt.Errorf("%s: WFP session closed", what)
	}
	rules = slices.Clone(rules)
	slices.SortStableFunc(rules, func(a, b *wf.Rule) int { return cmp.Compare(b.Weight, a.Weight) })
	for i, r := range rules {
		if err := g.session.AddRule(r); err != nil {
			err = fmt.Errorf("%s: rule %q: %w", what, r.Name, err)
			left, derr := g.deleteLocked(rules[:i])
			g.rules = append(g.rules, left...)
			return errors.Join(err, derr)
		}
		if g.step != nil {
			g.step(r, true)
		}
	}
	old := g.rules
	g.rules, g.state = rules, state
	left, err := g.deleteLocked(old)
	g.rules = append(g.rules, left...)
	return err
}

// Disable removes the rules; the session stays for the next Enable. Without rules it does
// nothing.
func (g *DNSGuard) Disable() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.disableLocked()
}

func (g *DNSGuard) disableLocked() error {
	left, err := g.deleteLocked(g.rules)
	g.rules = left
	if len(left) == 0 {
		g.state = guardOpen
	}
	return err
}

// deleteLocked removes rules from light to heavy: the block-all goes first, the permit for
// dnstt-cli itself last. It returns the rules that could not be removed.
func (g *DNSGuard) deleteLocked(rules []*wf.Rule) ([]*wf.Rule, error) {
	rules = slices.Clone(rules)
	slices.SortStableFunc(rules, func(a, b *wf.Rule) int { return cmp.Compare(a.Weight, b.Weight) })
	var left []*wf.Rule
	var errs []error
	for _, r := range rules {
		if err := g.session.DeleteRule(r.ID); err != nil && !errors.Is(err, windows.Errno(fwpEFilterNotFound)) {
			left = append(left, r)
			errs = append(errs, err)
			continue
		}
		if g.step != nil {
			g.step(r, false)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return left, fmt.Errorf("DNS protection: removing rules: %w", err)
	}
	return nil, nil
}

// Close closes the session: the system removes everything created in it. A repeated call
// is harmless.
func (g *DNSGuard) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.session == nil {
		return nil
	}
	err := g.session.Close()
	g.session, g.rules, g.state = nil, nil, guardOpen
	return err
}

// fwpEFilterNotFound is FWP_E_FILTER_NOT_FOUND: the rule is already gone.
const fwpEFilterNotFound = 0x80320003

// dnsGuardLayers are the layers where the rules act: outgoing connections and the first
// incoming packet of an unconnected socket, IPv4 and IPv6, as in blockDNS of
// wireguard-windows.
var dnsGuardLayers = []guardLayer{
	{wf.LayerALEAuthConnectV4, false, "outgoing IPv4"},
	{wf.LayerALEAuthRecvAcceptV4, false, "incoming IPv4"},
	{wf.LayerALEAuthConnectV6, true, "outgoing IPv6"},
	{wf.LayerALEAuthRecvAcceptV6, true, "incoming IPv6"},
}

// guardLayer is a WFP layer the protection rules are set on.
type guardLayer struct {
	id   wf.LayerID
	ipv6 bool
	name string
}

// dnsGuardRules builds the rules for Enable; it is separate for a test without rights. On
// every layer: a block of port 53 over UDP and TCP, a permit for our own processes, a
// permit for the tunnel's resolvers of the layer's family and loopback. WFP reads repeated
// conditions on the same field as "or".
//
// With blockIPv6 the IPv6 layers also get the "closed" set without our own processes and
// the DNS block (they are already there): loopback, service protocols and the local
// network, block-all (lockdownLocalRules).
func dnsGuardRules(dns []netip.Addr, appIDs []string, blockIPv6 bool) ([]*wf.Rule, error) {
	var rules []*wf.Rule
	add := func(layer wf.LayerID, name string, weight uint64, action wf.Action, conds []*wf.Match) error {
		r, err := newGuardRule(layer, name, weight, action, conds)
		if err != nil {
			return err
		}
		rules = append(rules, r)
		return nil
	}

	for _, layer := range dnsGuardLayers {
		if err := add(layer.id, "dnstt-cli: block DNS bypassing the tunnel, "+layer.name,
			dnsGuardBlockWeight, wf.ActionBlock, dnsPortMatches()); err != nil {
			return nil, err
		}

		if err := add(layer.id, "dnstt-cli: DNS of dnstt-cli itself, "+layer.name,
			dnsGuardPermitWeight, wf.ActionPermit, selfMatches(appIDs)); err != nil {
			return nil, err
		}

		var allowed []*wf.Match
		if layer.ipv6 {
			allowed = append(allowed, &wf.Match{Field: wf.FieldIPRemoteAddress, Op: wf.MatchTypeEqual, Value: netip.IPv6Loopback()})
		} else {
			allowed = append(allowed, &wf.Match{Field: wf.FieldIPRemoteAddress, Op: wf.MatchTypeEqual, Value: netip.MustParsePrefix("127.0.0.0/8")})
		}
		for _, a := range dns {
			a = a.Unmap()
			if a.Is6() == layer.ipv6 {
				allowed = append(allowed, &wf.Match{Field: wf.FieldIPRemoteAddress, Op: wf.MatchTypeEqual, Value: a})
			}
		}
		if err := add(layer.id, "dnstt-cli: tunnel DNS and loopback, "+layer.name,
			dnsGuardPermitWeight, wf.ActionPermit, allowed); err != nil {
			return nil, err
		}

		if blockIPv6 && layer.ipv6 {
			local, err := lockdownLocalRules(layer, "dnstt-cli: IPv6 outward closed")
			if err != nil {
				return nil, err
			}
			rules = append(rules, local...)
		}
	}
	return rules, nil
}

// selfMatches is the "own process" condition: any of appIDs.
func selfMatches(appIDs []string) []*wf.Match {
	var m []*wf.Match
	for _, id := range appIDs {
		m = append(m, &wf.Match{Field: wf.FieldALEAppID, Op: wf.MatchTypeEqual, Value: id})
	}
	return m
}

// dnsPortMatches are the conditions of the DNS block: port 53, UDP or TCP.
func dnsPortMatches() []*wf.Match {
	return []*wf.Match{
		{Field: wf.FieldIPRemotePort, Op: wf.MatchTypeEqual, Value: uint16(53)},
		{Field: wf.FieldIPProtocol, Op: wf.MatchTypeEqual, Value: wf.IPProtoUDP},
		{Field: wf.FieldIPProtocol, Op: wf.MatchTypeEqual, Value: wf.IPProtoTCP},
	}
}

// newGuardRule returns a rule in our sublayer with a new GUID.
func newGuardRule(layer wf.LayerID, name string, weight uint64, action wf.Action, conds []*wf.Match) (*wf.Rule, error) {
	guid, err := windows.GenerateGUID()
	if err != nil {
		return nil, fmt.Errorf("DNS protection: rule GUID: %w", err)
	}
	return &wf.Rule{
		ID:         wf.RuleID(guid),
		Name:       name,
		Layer:      layer,
		Sublayer:   dnsGuardSublayer,
		Provider:   DNSGuardProvider,
		Weight:     weight,
		Conditions: conds,
		Action:     action,
	}, nil
}
