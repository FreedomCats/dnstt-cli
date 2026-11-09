package datapath

import (
	"errors"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FreedomCats/dnstt-cli/internal/exit"
	"github.com/FreedomCats/dnstt-cli/internal/identity"
)

// fakeAdapter is an adapter with no system behind it: it records the calls in order and
// can fail any step.
type fakeAdapter struct {
	mu    sync.Mutex
	id    uint64
	calls []string

	configureErr     error
	addAddressErr    error
	removeAddressErr error
	addRoutesErr     error
	removeRoutesErr  error
	setDNSErr        error
	revertDNSErr     error
	closeErr         error

	// closeGate, when set, holds close until it is closed.
	closeGate chan struct{}
}

func (f *fakeAdapter) adapter() *tunAdapter {
	return &tunAdapter{
		id:            f.id,
		configure:     func() error { return f.step("configure") },
		addAddress:    func() error { return f.step("addAddress") },
		removeAddress: func() error { return f.step("removeAddress") },
		addRoutes:     func() error { return f.step("addRoutes") },
		removeRoutes:  func() error { return f.step("removeRoutes") },
		setDNS:        func() error { return f.step("setDNS") },
		revertDNS:     func() error { return f.step("revertDNS") },
		close:         f.close,
	}
}

func (f *fakeAdapter) step(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
	switch name {
	case "configure":
		return f.configureErr
	case "addAddress":
		return f.addAddressErr
	case "removeAddress":
		return f.removeAddressErr
	case "addRoutes":
		return f.addRoutesErr
	case "removeRoutes":
		return f.removeRoutesErr
	case "setDNS":
		return f.setDNSErr
	case "revertDNS":
		return f.revertDNSErr
	case "close":
		return f.closeErr
	}
	return nil
}

func (f *fakeAdapter) close() error {
	f.mu.Lock()
	gate := f.closeGate
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	return f.step("close")
}

func (f *fakeAdapter) setErr(step string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch step {
	case "configure":
		f.configureErr = err
	case "addAddress":
		f.addAddressErr = err
	case "removeAddress":
		f.removeAddressErr = err
	case "addRoutes":
		f.addRoutesErr = err
	case "removeRoutes":
		f.removeRoutesErr = err
	case "setDNS":
		f.setDNSErr = err
	case "revertDNS":
		f.revertDNSErr = err
	case "close":
		f.closeErr = err
	default:
		panic("unknown fake step " + step)
	}
}

func (f *fakeAdapter) setCloseGate(gate chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeGate = gate
}

func (f *fakeAdapter) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakeAdapter) clearCalls() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

func (f *fakeAdapter) countCalls(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == name {
			n++
		}
	}
	return n
}

// fakeOpener replaces openTunAdapter: it counts the opens, records the arguments and
// returns the fake adapter unless it is told to fail.
type fakeOpener struct {
	mu      sync.Mutex
	opens   int
	err     error
	adapter *fakeAdapter
	names   []string
	tunDNS  []netip.Addr
}

func (o *fakeOpener) open(name string, _ exit.Dialer, tunDNS netip.Addr, _ func(string, ...any)) (*tunAdapter, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.opens++
	o.names = append(o.names, name)
	o.tunDNS = append(o.tunDNS, tunDNS)
	if o.err != nil {
		return nil, o.err
	}
	return o.adapter.adapter(), nil
}

func (o *fakeOpener) setErr(err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.err = err
}

func (o *fakeOpener) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.opens
}

func (o *fakeOpener) seenTunDNS() []netip.Addr {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.tunDNS)
}

// newFakeTun is a Tun over a fake adapter; the real opener needs root and /dev/net/tun.
func newFakeTun(t *testing.T) (*Tun, *fakeAdapter, *fakeOpener) {
	t.Helper()
	f := &fakeAdapter{}
	o := &fakeOpener{adapter: f}
	w, err := NewTun(TunOptions{
		Dialer: dialerFunc(nil),
		TunDNS: netip.MustParseAddr("203.0.113.53"),
	})
	if err != nil {
		t.Fatal(err)
	}
	w.openAdapter = o.open
	return w, f, o
}

func wantCalls(t *testing.T, f *fakeAdapter, want ...string) {
	t.Helper()
	if got := f.snapshot(); !slices.Equal(got, want) {
		t.Errorf("calls %v, want %v", got, want)
	}
}

// The order of Start is configure (once) → address → routes → system DNS; Suspend
// reverses it; a repeated Start adds nothing, a repeated Suspend re-runs the idempotent
// cleanup; Start after Suspend repeats the forward order without configure; Close
// cleans up and closes the adapter once.
func TestTunLifecycle(t *testing.T) {
	w, f, o := newFakeTun(t)

	if err := w.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	wantCalls(t, f, "configure", "addAddress", "addRoutes", "setDNS")
	f.clearCalls()

	if err := w.Start(); err != nil {
		t.Fatalf("repeated Start: %v", err)
	}
	wantCalls(t, f)

	if err := w.Suspend(); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	wantCalls(t, f, "revertDNS", "removeRoutes", "removeAddress")
	f.clearCalls()

	if err := w.Suspend(); err != nil {
		t.Fatalf("repeated Suspend: %v", err)
	}
	wantCalls(t, f, "revertDNS", "removeRoutes", "removeAddress")
	f.clearCalls()

	if err := w.Start(); err != nil {
		t.Fatalf("Start after Suspend: %v", err)
	}
	wantCalls(t, f, "addAddress", "addRoutes", "setDNS")
	f.clearCalls()

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	wantCalls(t, f, "revertDNS", "removeRoutes", "removeAddress", "close")
	f.clearCalls()

	if err := w.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
	wantCalls(t, f)

	if err := w.Start(); !errors.Is(err, errTunClosed) {
		t.Errorf("Start after Close: %v, want %v", err, errTunClosed)
	}
	wantCalls(t, f)
	if got := o.count(); got != 1 {
		t.Errorf("the adapter was opened %d times, want 1", got)
	}
}

// Before the first Start neither Suspend nor Close opens the adapter, and a Start after
// Close is refused without opening it.
func TestTunCloseBeforeStart(t *testing.T) {
	w, f, o := newFakeTun(t)
	if err := w.Suspend(); err != nil {
		t.Errorf("Suspend before Start: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("Close before Start: %v", err)
	}
	if got := o.count(); got != 0 {
		t.Errorf("the adapter was opened %d times, want 0", got)
	}
	wantCalls(t, f)
	if err := w.Start(); !errors.Is(err, errTunClosed) {
		t.Errorf("Start after Close: %v, want %v", err, errTunClosed)
	}
	if got := o.count(); got != 0 {
		t.Errorf("Start after Close opened the adapter")
	}
}

// A failure of any forward step returns the error, undoes the steps done, and the next
// Start with the fault cleared succeeds: configure is not repeated once it went through.
func TestTunStartFailures(t *testing.T) {
	forward := []string{"configure", "addAddress", "addRoutes", "setDNS"}
	for i, failing := range forward {
		t.Run(failing, func(t *testing.T) {
			w, f, _ := newFakeTun(t)
			f.setErr(failing, errors.New("boom"))
			err := w.Start()
			if err == nil || !strings.Contains(err.Error(), "boom") {
				t.Fatalf("Start: %v, want the step's error", err)
			}
			want := append(slices.Clone(forward[:i+1]), "revertDNS", "removeRoutes", "removeAddress")
			wantCalls(t, f, want...)

			f.setErr(failing, nil)
			f.clearCalls()
			if err := w.Start(); err != nil {
				t.Fatalf("Start after the failure: %v", err)
			}
			// The cleanup removed the address and the routes, so the next Start builds
			// them again; only configure stays done once it went through.
			next := []string{"addAddress", "addRoutes", "setDNS"}
			if failing == "configure" {
				next = forward
			}
			wantCalls(t, f, next...)
		})
	}
}

// A failing cleanup step does not stop the other two: Suspend joins their errors and
// leaves the datapath suspended, so a repeated Suspend retries and Start restores the
// forward order.
func TestTunSuspendFailures(t *testing.T) {
	w, f, _ := newFakeTun(t)
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	f.clearCalls()
	revertErr := errors.New("revert")
	routesErr := errors.New("routes")
	addressErr := errors.New("address")
	f.setErr("revertDNS", revertErr)
	f.setErr("removeRoutes", routesErr)
	f.setErr("removeAddress", addressErr)

	err := w.Suspend()
	for _, want := range []error{revertErr, routesErr, addressErr} {
		if !errors.Is(err, want) {
			t.Errorf("Suspend: %v, want %v", err, want)
		}
	}
	wantCalls(t, f, "revertDNS", "removeRoutes", "removeAddress")

	for _, step := range []string{"revertDNS", "removeRoutes", "removeAddress"} {
		f.setErr(step, nil)
	}
	f.clearCalls()
	if err := w.Suspend(); err != nil {
		t.Fatalf("repeated Suspend: %v", err)
	}
	wantCalls(t, f, "revertDNS", "removeRoutes", "removeAddress")

	f.clearCalls()
	if err := w.Start(); err != nil {
		t.Fatalf("Start after Suspend: %v", err)
	}
	wantCalls(t, f, "addAddress", "addRoutes", "setDNS")
}

// The opener receives exactly TunDNS, and TunDNS is not the adapter's address. The address
// 10.66.67.1 that the real seam passes to addAddress and setDNS is a constant of
// tun_linux.go, not a value the fake could observe; TestTunAdapterNetns and
// TestTunSystemDNSNetns prove it on a real adapter. Here the wiring is checked: the
// netstack's DNS goes to the opener, and a repeated Start changes nothing.
func TestTunDNSTarget(t *testing.T) {
	w, f, o := newFakeTun(t)
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	if got := o.seenTunDNS(); !slices.Equal(got, []netip.Addr{w.opts.TunDNS}) {
		t.Errorf("the opener was given tun_dns %v, want %v", got, w.opts.TunDNS)
	}
	if w.opts.TunDNS == tunAddress {
		t.Fatalf("tun_dns of the test is the adapter's address; the check proves nothing")
	}
	f.clearCalls()
	if err := w.Start(); err != nil {
		t.Fatalf("repeated Start: %v", err)
	}
	wantCalls(t, f)
}

// NewTun refuses a missing dialer and a TunDNS that cannot serve DNS; the default name is
// the program's name.
func TestNewTun(t *testing.T) {
	d := dialerFunc(nil)
	cases := []struct {
		name string
		opts TunOptions
	}{
		{"no dialer", TunOptions{TunDNS: netip.MustParseAddr("203.0.113.53")}},
		{"no tun_dns", TunOptions{Dialer: d}},
		{"IPv6 tun_dns", TunOptions{Dialer: d, TunDNS: netip.MustParseAddr("2001:db8::53")}},
		{"unspecified tun_dns", TunOptions{Dialer: d, TunDNS: netip.MustParseAddr("0.0.0.0")}},
	}
	for _, c := range cases {
		if _, err := NewTun(c.opts); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}
	w, err := NewTun(TunOptions{Dialer: d, TunDNS: netip.MustParseAddr("203.0.113.53")})
	if err != nil {
		t.Fatal(err)
	}
	if w.opts.Name != identity.Name {
		t.Errorf("default name %q, want %q", w.opts.Name, identity.Name)
	}
}

// Requires: root and a netguard; DNSGuard does not come with this child.
func TestTunRequires(t *testing.T) {
	w, err := NewTun(TunOptions{Dialer: dialerFunc(nil), TunDNS: netip.MustParseAddr("203.0.113.53")})
	if err != nil {
		t.Fatal(err)
	}
	if r := w.Requires(); !r.Admin || !r.Netguard || r.DNSGuard {
		t.Errorf("Requires: %+v, want Admin and Netguard", r)
	}
}

// The adapter id is the netguard exclusion: 0 until the first Start opens the adapter,
// the id while it exists, 0 after Close.
func TestTunAdapterID(t *testing.T) {
	w, f, _ := newFakeTun(t)
	f.id = 7
	if got := w.AdapterID(); got != 0 {
		t.Errorf("AdapterID before Start = %d, want 0", got)
	}
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	if got := w.AdapterID(); got != 7 {
		t.Errorf("AdapterID = %d, want the adapter's id 7", got)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if got := w.AdapterID(); got != 0 {
		t.Errorf("AdapterID after Close = %d, want 0", got)
	}
}

// A failed open is returned by Start and retried on the next call.
func TestTunOpenFailure(t *testing.T) {
	w, _, o := newFakeTun(t)
	o.setErr(errors.New("no privileges"))
	for range 2 {
		if err := w.Start(); err == nil || !strings.Contains(err.Error(), "no privileges") {
			t.Fatalf("Start: %v, want the open error", err)
		}
	}
	if got := o.count(); got != 2 {
		t.Errorf("the adapter was opened %d times, want 2 attempts", got)
	}
	o.setErr(nil)
	if err := w.Start(); err != nil {
		t.Fatalf("Start after the open failure: %v", err)
	}
	if got := o.count(); got != 3 {
		t.Errorf("the adapter was opened %d times, want 3", got)
	}
}

// Concurrent Close calls all return after the same cleanup, and the adapter is closed
// once.
func TestTunConcurrentClose(t *testing.T) {
	w, f, _ := newFakeTun(t)
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	gate := make(chan struct{})
	f.setCloseGate(gate)
	done := make(chan error, 3)
	for range 3 {
		go func() { done <- w.Close() }()
	}
	select {
	case err := <-done:
		t.Fatalf("Close returned before the adapter was closed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(gate)
	for range 3 {
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Close: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Close did not return")
		}
	}
	if got := f.countCalls("close"); got != 1 {
		t.Errorf("the adapter was closed %d times, want 1", got)
	}
}
