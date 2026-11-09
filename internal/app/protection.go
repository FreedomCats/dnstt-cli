package app

import (
	"errors"
	"fmt"
	"net/netip"
	"sync"

	"github.com/FreedomCats/dnstt-cli/internal/datapath"
)

// The leak protection of architecture.md §05a for a datapath that takes all system
// traffic: the DNS guard session is opened before the first attempt and lives the whole
// connect. The datapath enables DNS protection with the routes (Tun.Start) and disables
// it in Suspend; the kill-switch is the same session's "closed" set, installed by this
// wrapper while the tunnel is not up.

// dnsGuardSession is what the platform hands to the protection: the DNS guard plus the
// kill-switch's "closed" set and the end of the session (linuxsys.DNSGuard,
// winsys.DNSGuard).
type dnsGuardSession interface {
	datapath.DNSGuard
	// Lockdown installs the "closed" set instead of the current rules, in one transition.
	Lockdown() error
	// Close ends the session; the rules and the table disappear with it.
	Close() error
}

// openDNSGuard opens the platform's protection session. It is a variable for the tests:
// the real one needs root (CAP_NET_ADMIN).
var openDNSGuard = openSystemDNSGuard

// errKillSwitchClosed is Enable after Close: there is no session left to raise the tunnel
// in.
var errKillSwitchClosed = errors.New("kill-switch: the protection session is closed")

// ProtectionOptions are the settings of EnableProtection.
type ProtectionOptions struct {
	// KillSwitch installs the "closed" set before the first attempt and after every
	// Suspend: while the tunnel is not up, system traffic is blocked.
	KillSwitch bool
	// AllowLeak connects even if the protection session cannot be opened or a rule
	// cannot be set: a warning instead of an error.
	AllowLeak bool
	// Notice receives the user-facing lines of the protection; nil — silent.
	Notice func(format string, args ...any)
}

// Protection is the open protection session: what the datapath is given and what the
// supervisor asks about the kill-switch. Close it after the datapath: the session
// outlives Tun.Close.
type Protection struct {
	// Guard is the DNS protection for datapath.Guarded.SetDNSGuard; nil under -allow-leak
	// when the session could not be opened.
	Guard datapath.DNSGuard
	// KillSwitch is the wrapper that owns the "closed" set; nil when the kill-switch is
	// off or could not be set.
	KillSwitch *killSwitchGuard
	// session is the platform session; it is closed by Close.
	session dnsGuardSession

	closeOnce sync.Once
	closeErr  error
}

// EnableProtection opens the platform's protection session and, if the kill-switch is on,
// installs "closed" at once — before Run and before the adapter exists: there is no
// session yet, and system traffic has nowhere to go but past the tunnel. On a failure
// without AllowLeak the error names the flags that accept a run without the protection.
func EnableProtection(opts ProtectionOptions) (*Protection, error) {
	notice := opts.Notice
	if notice == nil {
		notice = func(string, ...any) {}
	}
	session, err := openDNSGuard()
	if err != nil {
		if !opts.AllowLeak {
			return nil, fmt.Errorf("DNS protection: %w; connect with -allow-leak to run without "+
				"it: DNS queries and system traffic may go past the tunnel", err)
		}
		notice("warning: DNS protection could not be set: %v — with -allow-leak the tunnel "+
			"runs without it: DNS queries and system traffic may go past the tunnel", err)
		return &Protection{}, nil
	}
	prot := &Protection{Guard: session, session: session}
	if !opts.KillSwitch {
		if opts.AllowLeak {
			prot.Guard = tolerantDNSGuard{session: session, notice: notice}
		}
		return prot, nil
	}
	ks, err := newKillSwitchGuard(session, opts.AllowLeak, notice)
	if err != nil {
		if !opts.AllowLeak {
			prot.Close()
			return nil, fmt.Errorf("kill-switch: %w; connect with -no-kill-switch or "+
				"-allow-leak to run without it: system traffic goes past the tunnel while "+
				"the tunnel is down", err)
		}
		notice("warning: kill-switch could not be set: %v — with -allow-leak the tunnel "+
			"runs without it: system traffic goes past the tunnel while the tunnel is down", err)
		prot.Guard = tolerantDNSGuard{session: session, notice: notice}
		return prot, nil
	}
	notice("kill-switch: system traffic is blocked while the tunnel is down")
	prot.Guard, prot.KillSwitch = ks, ks
	return prot, nil
}

// Close ends the protection session: the rules and the table disappear with it. The
// kill-switch wrapper stops touching the rules first, so a late Disable is a no-op. It is
// safe when repeated and on a nil Protection.
func (p *Protection) Close() error {
	if p == nil {
		return nil
	}
	p.closeOnce.Do(func() {
		if p.KillSwitch != nil {
			p.KillSwitch.shutdown()
		}
		if p.session != nil {
			p.closeErr = p.session.Close()
		}
	})
	return p.closeErr
}

// Locked reports whether the kill-switch's "closed" set is in place now; false without
// the kill-switch.
func (p *Protection) Locked() bool {
	if p == nil || p.KillSwitch == nil {
		return false
	}
	return p.KillSwitch.Locked()
}

// killSwitchGuard turns the guard's Disable into Lockdown: Enable is the tunnel up (DNS
// protection), Disable is the tunnel down ("closed"). There is no captcha, so nothing
// lifts the kill-switch mid-session.
type killSwitchGuard struct {
	session   dnsGuardSession
	allowLeak bool
	notice    func(format string, args ...any)

	mu     sync.Mutex
	locked bool // the "closed" set is in place
	closed bool // the session is closed: the rules are gone with it
}

// newKillSwitchGuard installs "closed" at once — before the first attempt: there is no
// tunnel yet, and system traffic has nowhere to go but past it.
func newKillSwitchGuard(session dnsGuardSession, allowLeak bool, notice func(string, ...any)) (*killSwitchGuard, error) {
	if err := session.Lockdown(); err != nil {
		return nil, err
	}
	return &killSwitchGuard{session: session, allowLeak: allowLeak, notice: notice, locked: true}, nil
}

// Enable is the tunnel up: DNS protection instead of "closed". A refusal without
// -allow-leak is returned — the datapath refuses Start — and the system stays closed.
// With -allow-leak the tunnel works, so the system is open: all rules are dropped, as
// without the kill-switch.
func (g *killSwitchGuard) Enable(dns []netip.Addr) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return errKillSwitchClosed
	}
	err := g.session.Enable(dns)
	if err == nil {
		g.locked = false
		return nil
	}
	if !g.allowLeak {
		if derr := g.downLocked(); derr != nil {
			g.notice("warning: %v", derr)
		}
		return err
	}
	g.notice("warning: DNS protection could not be set: %v — with -allow-leak the tunnel "+
		"runs without it: DNS queries may go past the tunnel", err)
	g.locked = false
	if derr := g.session.Disable(); derr != nil {
		g.notice("warning: %v", derr)
	}
	return nil
}

// Disable is the tunnel down: "closed" instead of DNS protection. A failed Lockdown is
// not a datapath failure: a loud line, all rules are dropped, and system traffic, as
// without the kill-switch, goes past the tunnel. The error is returned only if the rules
// could not be dropped either.
func (g *killSwitchGuard) Disable() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil
	}
	return g.downLocked()
}

// downLocked installs the set for a tunnel that is down.
func (g *killSwitchGuard) downLocked() error {
	if err := g.session.Lockdown(); err != nil {
		g.locked = false
		g.notice("warning: kill-switch did not go up: %v — system traffic goes past the "+
			"tunnel while it is down", err)
		return g.session.Disable()
	}
	g.locked = true
	return nil
}

// BlockIPv6 forwards to the session if it implements datapath.IPv6Blocker.
func (g *killSwitchGuard) BlockIPv6(block bool) {
	if blocker, ok := g.session.(datapath.IPv6Blocker); ok {
		blocker.BlockIPv6(block)
	}
}

// shutdown marks the wrapper closed: later Enable is an error, later Disable is a silent
// no-op and touches no rules.
func (g *killSwitchGuard) shutdown() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closed, g.locked = true, false
}

// Locked reports whether "closed" is in place; for the supervisor's lines.
func (g *killSwitchGuard) Locked() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.locked
}

// tolerantDNSGuard is the DNS protection under -allow-leak: a refusal of the rules in the
// middle of a session is a line in the output, not a failed attempt.
type tolerantDNSGuard struct {
	session dnsGuardSession
	notice  func(format string, args ...any)
}

func (g tolerantDNSGuard) Enable(dns []netip.Addr) error {
	if err := g.session.Enable(dns); err != nil {
		g.notice("warning: DNS protection could not be set: %v — with -allow-leak the "+
			"tunnel runs without it: DNS queries may go past the tunnel", err)
	}
	return nil
}

func (g tolerantDNSGuard) Disable() error { return g.session.Disable() }

// BlockIPv6 forwards to the session if it implements datapath.IPv6Blocker.
func (g tolerantDNSGuard) BlockIPv6(block bool) {
	if blocker, ok := g.session.(datapath.IPv6Blocker); ok {
		blocker.BlockIPv6(block)
	}
}
