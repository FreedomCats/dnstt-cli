package app

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/FreedomCats/dnstt-cli/internal/datapath"
)

// fakeGuardSession is the platform protection in memory: it records the calls in order
// and can fail any of them.
type fakeGuardSession struct {
	mu          sync.Mutex
	calls       []string
	enableErr   error
	disableErr  error
	lockdownErr error
	closeErr    error
}

func (f *fakeGuardSession) record(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
	switch name {
	case "Enable":
		return f.enableErr
	case "Disable":
		return f.disableErr
	case "Lockdown":
		return f.lockdownErr
	case "Close":
		return f.closeErr
	}
	return nil
}

func (f *fakeGuardSession) Enable([]netip.Addr) error { return f.record("Enable") }
func (f *fakeGuardSession) Disable() error            { return f.record("Disable") }
func (f *fakeGuardSession) Lockdown() error           { return f.record("Lockdown") }
func (f *fakeGuardSession) Close() error              { return f.record("Close") }
func (f *fakeGuardSession) BlockIPv6(bool)            { _ = f.record("BlockIPv6") }

func (f *fakeGuardSession) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakeGuardSession) setLockdownErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lockdownErr = err
}

// noIPv6Guard hides the optional BlockIPv6 of the session.
type noIPv6Guard struct{ dnsGuardSession }

// useGuard replaces the platform opener for the test.
func useGuard(t *testing.T, session dnsGuardSession) {
	t.Helper()
	old := openDNSGuard
	openDNSGuard = func() (dnsGuardSession, error) { return session, nil }
	t.Cleanup(func() { openDNSGuard = old })
}

// useOpenError makes the platform opener fail for the test.
func useOpenError(t *testing.T, err error) {
	t.Helper()
	old := openDNSGuard
	openDNSGuard = func() (dnsGuardSession, error) { return nil, err }
	t.Cleanup(func() { openDNSGuard = old })
}

// captureNotice collects the user-facing lines of the protection.
func captureNotice() (func(string, ...any), func() []string) {
	var mu sync.Mutex
	var lines []string
	notice := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, fmt.Sprintf(format, args...))
	}
	snapshot := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(lines)
	}
	return notice, snapshot
}

func joinLines(lines []string) string { return strings.Join(lines, "\n") }

// The kill-switch is installed before the first attempt; Enable replaces it with DNS
// protection and Disable brings "closed" back.
func TestProtectionKillSwitchLifecycle(t *testing.T) {
	session := &fakeGuardSession{}
	useGuard(t, session)
	notice, lines := captureNotice()

	prot, err := EnableProtection(ProtectionOptions{KillSwitch: true, Notice: notice})
	if err != nil {
		t.Fatal(err)
	}
	defer prot.Close()
	if got := session.snapshot(); !slices.Equal(got, []string{"Lockdown"}) {
		t.Fatalf("calls after EnableProtection: %v, want the initial Lockdown", got)
	}
	if !prot.Locked() {
		t.Fatal("the kill-switch is not locked before the first session")
	}
	if !strings.Contains(joinLines(lines()), "kill-switch: system traffic is blocked while the tunnel is down") {
		t.Errorf("no startup line: %q", lines())
	}
	if _, ok := prot.Guard.(*killSwitchGuard); !ok {
		t.Errorf("Guard is %T, want the kill-switch wrapper", prot.Guard)
	}

	if err := prot.Guard.Enable([]netip.Addr{netip.MustParseAddr("10.66.67.1")}); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if prot.Locked() {
		t.Error("the kill-switch stayed locked with the tunnel up")
	}
	if err := prot.Guard.Disable(); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if !prot.Locked() {
		t.Error("Disable did not install the closed set")
	}
	if want := []string{"Lockdown", "Enable", "Lockdown"}; !slices.Equal(session.snapshot(), want) {
		t.Errorf("calls %v, want %v", session.snapshot(), want)
	}
}

// With the kill-switch off nothing is locked; -allow-leak makes the guard tolerant.
func TestProtectionKillSwitchOff(t *testing.T) {
	session := &fakeGuardSession{}
	useGuard(t, session)
	prot, err := EnableProtection(ProtectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer prot.Close()
	if prot.Guard != session {
		t.Errorf("Guard is %T, want the session itself", prot.Guard)
	}
	if prot.Locked() {
		t.Error("the kill-switch is locked although it is off")
	}
	if got := session.snapshot(); len(got) != 0 {
		t.Errorf("calls %v, want none without the kill-switch", got)
	}

	session2 := &fakeGuardSession{}
	useGuard(t, session2)
	prot2, err := EnableProtection(ProtectionOptions{AllowLeak: true})
	if err != nil {
		t.Fatal(err)
	}
	defer prot2.Close()
	if _, ok := prot2.Guard.(tolerantDNSGuard); !ok {
		t.Errorf("Guard is %T, want the tolerant wrapper", prot2.Guard)
	}
}

// A refused session is an error naming -allow-leak; with it the app warns and runs
// without a guard.
func TestProtectionOpenFailure(t *testing.T) {
	cause := errors.New("nftables: operation not permitted")
	t.Run("refused", func(t *testing.T) {
		useOpenError(t, cause)
		prot, err := EnableProtection(ProtectionOptions{KillSwitch: true})
		if err == nil || prot != nil {
			t.Fatalf("EnableProtection = %v, %v; want an error", prot, err)
		}
		for _, want := range []string{cause.Error(), "-allow-leak"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not name %q", err, want)
			}
		}
	})
	t.Run("allow leak", func(t *testing.T) {
		useOpenError(t, cause)
		notice, lines := captureNotice()
		prot, err := EnableProtection(ProtectionOptions{KillSwitch: true, AllowLeak: true, Notice: notice})
		if err != nil {
			t.Fatal(err)
		}
		if prot == nil || prot.Guard != nil || prot.Locked() {
			t.Fatalf("Protection = %+v, want no guard and no lock", prot)
		}
		if !strings.Contains(joinLines(lines()), cause.Error()) {
			t.Errorf("no warning with the cause: %q", lines())
		}
		if err := prot.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
}

// A refused Lockdown is an error naming both ways out; with -allow-leak the session stays
// open and the tolerant guard takes over.
func TestProtectionLockdownFailure(t *testing.T) {
	cause := errors.New("nftables: no space left")
	t.Run("refused", func(t *testing.T) {
		session := &fakeGuardSession{lockdownErr: cause}
		useGuard(t, session)
		prot, err := EnableProtection(ProtectionOptions{KillSwitch: true})
		if err == nil || prot != nil {
			t.Fatalf("EnableProtection = %v, %v; want an error", prot, err)
		}
		for _, want := range []string{cause.Error(), "-no-kill-switch", "-allow-leak"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not name %q", err, want)
			}
		}
		if got := session.snapshot(); !slices.Equal(got, []string{"Lockdown", "Close"}) {
			t.Errorf("calls %v, want the session closed after the refusal", got)
		}
	})
	t.Run("allow leak", func(t *testing.T) {
		session := &fakeGuardSession{lockdownErr: cause}
		useGuard(t, session)
		notice, lines := captureNotice()
		prot, err := EnableProtection(ProtectionOptions{KillSwitch: true, AllowLeak: true, Notice: notice})
		if err != nil {
			t.Fatal(err)
		}
		defer prot.Close()
		if prot.Locked() {
			t.Error("the kill-switch is reported locked although Lockdown failed")
		}
		if _, ok := prot.Guard.(tolerantDNSGuard); !ok {
			t.Errorf("Guard is %T, want the tolerant wrapper", prot.Guard)
		}
		if !strings.Contains(joinLines(lines()), cause.Error()) {
			t.Errorf("no warning with the cause: %q", lines())
		}
	})
}

// A refused Enable keeps the system closed without -allow-leak; with it the rules are
// dropped and the tunnel runs open.
func TestProtectionEnableFailure(t *testing.T) {
	cause := errors.New("rules refused")
	t.Run("refused", func(t *testing.T) {
		session := &fakeGuardSession{enableErr: cause}
		useGuard(t, session)
		prot, err := EnableProtection(ProtectionOptions{KillSwitch: true})
		if err != nil {
			t.Fatal(err)
		}
		defer prot.Close()
		if err := prot.Guard.Enable([]netip.Addr{netip.MustParseAddr("10.66.67.1")}); !errors.Is(err, cause) {
			t.Fatalf("Enable = %v, want %v", err, cause)
		}
		if !prot.Locked() {
			t.Error("the system was opened although the tunnel's rules failed")
		}
		if want := []string{"Lockdown", "Enable", "Lockdown"}; !slices.Equal(session.snapshot(), want) {
			t.Errorf("calls %v, want %v", session.snapshot(), want)
		}
	})
	t.Run("allow leak", func(t *testing.T) {
		session := &fakeGuardSession{enableErr: cause}
		useGuard(t, session)
		notice, lines := captureNotice()
		prot, err := EnableProtection(ProtectionOptions{KillSwitch: true, AllowLeak: true, Notice: notice})
		if err != nil {
			t.Fatal(err)
		}
		defer prot.Close()
		if err := prot.Guard.Enable([]netip.Addr{netip.MustParseAddr("10.66.67.1")}); err != nil {
			t.Fatalf("Enable = %v, want nil with -allow-leak", err)
		}
		if prot.Locked() {
			t.Error("the system stayed closed although the tunnel runs")
		}
		if want := []string{"Lockdown", "Enable", "Disable"}; !slices.Equal(session.snapshot(), want) {
			t.Errorf("calls %v, want %v", session.snapshot(), want)
		}
		if !strings.Contains(joinLines(lines()), cause.Error()) {
			t.Errorf("no warning with the cause: %q", lines())
		}
	})
}

// The tolerant guard turns a refused Enable into a warning and lets the tunnel run.
func TestProtectionTolerantEnable(t *testing.T) {
	cause := errors.New("rules refused")
	session := &fakeGuardSession{enableErr: cause}
	useGuard(t, session)
	notice, lines := captureNotice()
	prot, err := EnableProtection(ProtectionOptions{AllowLeak: true, Notice: notice})
	if err != nil {
		t.Fatal(err)
	}
	defer prot.Close()
	if err := prot.Guard.Enable([]netip.Addr{netip.MustParseAddr("10.66.67.1")}); err != nil {
		t.Fatalf("Enable = %v, want nil with -allow-leak", err)
	}
	if !strings.Contains(joinLines(lines()), cause.Error()) {
		t.Errorf("no warning with the cause: %q", lines())
	}
}

// A Lockdown failure in Disable is a loud line and open rules, not a datapath error.
func TestProtectionDisableLockdownFailure(t *testing.T) {
	session := &fakeGuardSession{}
	useGuard(t, session)
	notice, lines := captureNotice()
	prot, err := EnableProtection(ProtectionOptions{KillSwitch: true, Notice: notice})
	if err != nil {
		t.Fatal(err)
	}
	defer prot.Close()
	if err := prot.Guard.Enable([]netip.Addr{netip.MustParseAddr("10.66.67.1")}); err != nil {
		t.Fatal(err)
	}

	cause := errors.New("closed set refused")
	session.setLockdownErr(cause)
	if err := prot.Guard.Disable(); err != nil {
		t.Fatalf("Disable = %v, want nil: the rules were dropped", err)
	}
	if prot.Locked() {
		t.Error("the kill-switch reports closed although Lockdown failed")
	}
	if want := []string{"Lockdown", "Enable", "Lockdown", "Disable"}; !slices.Equal(session.snapshot(), want) {
		t.Errorf("calls %v, want %v", session.snapshot(), want)
	}
	if !strings.Contains(joinLines(lines()), cause.Error()) {
		t.Errorf("no loud line with the cause: %q", lines())
	}
}

// BlockIPv6 reaches the session through both wrappers, and a session without the option
// is not a problem.
func TestProtectionBlockIPv6(t *testing.T) {
	t.Run("kill switch", func(t *testing.T) {
		session := &fakeGuardSession{}
		useGuard(t, session)
		prot, err := EnableProtection(ProtectionOptions{KillSwitch: true})
		if err != nil {
			t.Fatal(err)
		}
		defer prot.Close()
		blocker, ok := prot.Guard.(datapath.IPv6Blocker)
		if !ok {
			t.Fatalf("Guard %T does not implement IPv6Blocker", prot.Guard)
		}
		blocker.BlockIPv6(true)
		if !slices.Contains(session.snapshot(), "BlockIPv6") {
			t.Errorf("calls %v, want BlockIPv6", session.snapshot())
		}
	})
	t.Run("tolerant", func(t *testing.T) {
		session := &fakeGuardSession{}
		useGuard(t, session)
		prot, err := EnableProtection(ProtectionOptions{AllowLeak: true})
		if err != nil {
			t.Fatal(err)
		}
		defer prot.Close()
		blocker, ok := prot.Guard.(datapath.IPv6Blocker)
		if !ok {
			t.Fatalf("Guard %T does not implement IPv6Blocker", prot.Guard)
		}
		blocker.BlockIPv6(true)
		if !slices.Contains(session.snapshot(), "BlockIPv6") {
			t.Errorf("calls %v, want BlockIPv6", session.snapshot())
		}
	})
	t.Run("no option", func(t *testing.T) {
		session := &fakeGuardSession{}
		useGuard(t, noIPv6Guard{session})
		prot, err := EnableProtection(ProtectionOptions{KillSwitch: true})
		if err != nil {
			t.Fatal(err)
		}
		defer prot.Close()
		blocker, ok := prot.Guard.(datapath.IPv6Blocker)
		if !ok {
			t.Fatalf("Guard %T does not implement IPv6Blocker", prot.Guard)
		}
		blocker.BlockIPv6(true) // must not panic
		if slices.Contains(session.snapshot(), "BlockIPv6") {
			t.Errorf("calls %v, want no BlockIPv6", session.snapshot())
		}
	})
}

// Close ends the session once; a later Disable touches no rules and is silent.
func TestProtectionClose(t *testing.T) {
	session := &fakeGuardSession{}
	useGuard(t, session)
	prot, err := EnableProtection(ProtectionOptions{KillSwitch: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := prot.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := session.snapshot(); !slices.Equal(got, []string{"Lockdown", "Close"}) {
		t.Errorf("calls %v, want the session closed", got)
	}
	if prot.Locked() {
		t.Error("the kill-switch is locked after Close")
	}
	if err := prot.Guard.Disable(); err != nil {
		t.Errorf("Disable after Close = %v, want nil", err)
	}
	if got := session.snapshot(); len(got) != 2 {
		t.Errorf("Disable after Close touched the rules: %v", got)
	}
	if err := prot.Close(); err != nil {
		t.Errorf("repeated Close: %v", err)
	}
	if got := session.snapshot(); len(got) != 2 {
		t.Errorf("repeated Close touched the session: %v", got)
	}

	var nilProt *Protection
	if nilProt.Locked() {
		t.Error("nil Protection reports locked")
	}
	if err := nilProt.Close(); err != nil {
		t.Errorf("nil Protection Close: %v", err)
	}
}
