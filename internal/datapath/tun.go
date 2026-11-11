package datapath

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"

	"github.com/FreedomCats/dnstt-cli/internal/exit"
	"github.com/FreedomCats/dnstt-cli/internal/identity"
)

// The point-to-point link of the tun datapath has two ends. The tunnel's only address is
// tunAddress: the netstack owns it, the DNS listener binds to it, and the system DNS
// points at it. tunHostAddress is the host end of the link, with tunAddress as its peer.
//
// The peer is deliberate: the kernel would deliver packets for a merely local address
// itself (through lo), and a DNS query to it would never reach the netstack. A peer is
// routed into the adapter, which is what the system resolver needs. The routes carry
// everything else; the kernel's own route for the peer covers the DNS address.
var (
	tunAddress     = netip.MustParseAddr("10.66.67.1")
	tunHostAddress = netip.MustParseAddr("10.66.67.2")
	tunRoutes4     = []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/1"),
		netip.MustParsePrefix("128.0.0.0/1"),
	}
)

// TunOptions are the settings of the tun datapath.
type TunOptions struct {
	// Name is the adapter name; "" — identity.Name.
	Name string

	// Dialer is the exit: the netstack turns every connection from the adapter into a
	// stream through it. Required.
	Dialer exit.Dialer

	// TunDNS is the DNS behind the exit: the netstack sends every query that arrives at
	// tunAddress there. A usable IPv4 address is required: the adapter serves no other.
	TunDNS netip.Addr

	// Logf receives the lines of the datapath: the adapter, the routes, the system DNS.
	// nil — silent.
	Logf func(format string, args ...any)
}

// Tun is the datapath with a system adapter: the traffic of the whole system goes into
// the tunnel (architecture.md §05, R6, R7). The adapter is a real /dev/net/tun device;
// its descriptor feeds the netstack in-process, with no second process and no descriptor
// handover.
//
// Tun is Persistent and Reserver: the adapter and the lock on it live the whole run. On
// Linux it is also DNSRecoverer: under the reservation, before the first attempt, it
// removes the system DNS a crashed run left behind.
// Start of every session configures the adapter once and puts the routes, the netstack's
// address and the system DNS in place; Suspend between sessions removes them; Close
// removes the adapter. The host side needs root and /dev/net/tun; the lifecycle itself
// does not, hence the adapter seam.
type Tun struct {
	opts TunOptions

	// openAdapter creates the platform adapter. It is a field for the tests: the real
	// one needs root and /dev/net/tun, while Start, Suspend and Close do not depend on
	// it.
	openAdapter func(name string, dialer exit.Dialer, tunDNS netip.Addr, logf func(string, ...any)) (*tunAdapter, error)

	// mu is held during the system calls too: they are short, and Close so waits for a
	// Start running in another goroutine.
	mu         sync.Mutex
	closed     bool
	adapter    *tunAdapter
	guard      DNSGuard // DNS protection, set by SetDNSGuard before Start
	configured bool     // configure has run for adapter; a repeated Start does not reconfigure
	up         bool     // Start went through and Suspend or Close has not

	// closeOnce and closeErr: the cleanup runs once, and every caller waits for it.
	closeOnce sync.Once
	closeErr  error
}

// tunAdapter is what the platform hands to Tun. The functions capture the adapter name,
// index, address and DNS; the remove and revert ones are harmless when repeated.
type tunAdapter struct {
	// id is the interface index of the adapter on Linux (the LUID on Windows), by which
	// netguard excludes it from the physical interface choice. 0 until the adapter
	// exists.
	id uint64
	// configure sets the adapter's address and MTU and brings it up. A repeated call
	// reconfigures.
	configure func() error
	// addAddress puts the netstack's address on the stack and binds the DNS listener to
	// it; removeAddress takes them away. Both are harmless when repeated.
	addAddress    func() error
	removeAddress func() error
	// addRoutes and removeRoutes put the two /1 routes into the adapter and take them
	// away; both are harmless when repeated.
	addRoutes    func() error
	removeRoutes func() error
	// setDNS makes tunAddress the system DNS; a repeated call replaces what was set, and
	// revertDNS puts the previous DNS back. revertDNS without setDNS does nothing.
	setDNS    func() error
	revertDNS func() error
	// close closes the fd, the link and the netstack and releases the adapter's lock
	// reference.
	close func() error
}

var (
	// errTunClosed refuses Start after Close.
	errTunClosed = errors.New("tun: the datapath is closed")

	// errTunBusy refuses a second dnstt-cli with the tun datapath on the same machine.
	errTunBusy = errors.New("the adapter is already busy: another " + identity.Name +
		" with the tun datapath is running on this machine; stop it or connect with -datapath socks")
)

// NewTun prepares the datapath. The system is not touched until Start: a caller builds
// the datapath just for Requires. A nil dialer or a TunDNS that cannot serve DNS is an
// error here, before anything is opened.
func NewTun(opts TunOptions) (*Tun, error) {
	if opts.Dialer == nil {
		return nil, errors.New("tun: no exit dialer")
	}
	if !opts.TunDNS.IsValid() || !opts.TunDNS.Is4() || opts.TunDNS.IsUnspecified() {
		return nil, fmt.Errorf("tun: tun_dns %v is not a usable IPv4 address", opts.TunDNS)
	}
	if strings.TrimSpace(opts.Name) == "" {
		opts.Name = identity.Name
	}
	return &Tun{opts: opts, openAdapter: openTunAdapter}, nil
}

// Requires: root, a netguard for the core's own sockets and the DNS protection session;
// the adapter takes all system traffic.
func (t *Tun) Requires() Requirements {
	return Requirements{Unsupported: tunUnsupported(), Admin: true, Netguard: true, DNSGuard: true}
}

// SetDNSGuard gives Start the DNS protection session (Guarded). It is called once before
// Run, after the protection is opened: the adapter does not exist yet.
func (t *Tun) SetDNSGuard(guard DNSGuard) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.guard = guard
}

// AdapterID is the interface index of the adapter (Adapter): 0 before the first Start,
// which opens it, and after Close, which removes it.
func (t *Tun) AdapterID() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.adapter == nil {
		return 0
	}
	return t.adapter.id
}

// Reserve takes the adapter lock for the whole run (Reserver): a second dnstt-cli with
// the tun datapath learns about the first at once, not after a failed session. Opening
// the adapter takes the same lock once more; inside one process that is not a conflict.
func (t *Tun) Reserve() (release func(), err error) {
	release, err = acquireTunLock()
	if err != nil {
		return nil, fmt.Errorf("tun: %w", err)
	}
	return release, nil
}

// Start brings the adapter up for a session. The order is the contract: configure
// (once per adapter) → the netstack's address and its DNS listener → the routes → the
// DNS protection (IPv6 closed first, then port 53) → the system DNS. The listener binds
// to the address, so the address goes first; a system DNS pointing into an adapter
// without a route would lose every query, so the routes go before it. The protection goes
// on after the routes and before the system DNS: its allow-list is the adapter's DNS
// address, not tun_dns, which the netstack reaches through the exit.
//
// A failure does not close the adapter: it is a failure of the attempt, and the next
// Start tries again. What was already set is undone as in Suspend, so no route or system
// DNS is left pointing into an adapter that is not serving.
func (t *Tun) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return errTunClosed
	}
	if t.up {
		return nil
	}
	adapter, err := t.openLocked()
	if err != nil {
		return err
	}

	if !t.configured {
		if err := adapter.configure(); err != nil {
			return errors.Join(fmt.Errorf("tun: adapter %q: %w", t.opts.Name, err), t.suspendLocked())
		}
		t.configured = true
	}

	if err := adapter.addAddress(); err != nil {
		return errors.Join(fmt.Errorf("tun: address %v: %w", tunAddress, err), t.suspendLocked())
	}
	if err := adapter.addRoutes(); err != nil {
		return errors.Join(fmt.Errorf("tun: routes: %w", err), t.suspendLocked())
	}
	if blocker, ok := t.guard.(IPv6Blocker); ok {
		blocker.BlockIPv6(true)
	}
	if t.guard != nil {
		if err := t.guard.Enable([]netip.Addr{tunAddress}); err != nil {
			return errors.Join(fmt.Errorf("tun: DNS protection: %w", err), t.suspendLocked())
		}
		t.logf("[TUN] DNS protection: port 53 only to %v, loopback and the %s sockets", tunAddress, identity.Name)
	}
	if err := adapter.setDNS(); err != nil {
		return errors.Join(fmt.Errorf("tun: system DNS: %w", err), t.suspendLocked())
	}

	t.up = true
	t.logf("[TUN] adapter %q: %v/32 peer %v/32, MTU %d, routes %v", t.opts.Name, tunHostAddress, tunAddress, TunnelMTU, tunRoutes4)
	return nil
}

// Suspend takes the system traffic out of the tunnel but leaves the adapter (Persistent):
// the next Start brings the address, the routes and the system DNS back. Before the first
// Start there is nothing to do and the adapter is not opened. The steps are idempotent,
// so a repeated Suspend is harmless and retries a cleanup that failed once.
func (t *Tun) Suspend() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.suspendLocked()
}

// suspendLocked is Suspend under t.mu, and the reverse of Start: the system DNS goes back
// first, while the routes still lead into the adapter, so no query leaves through a
// route that is about to be removed; then the DNS protection goes back to "closed" (or is
// removed); then the routes; then the netstack's address, which the DNS listener is bound
// to. Each step is attempted even if a previous one failed.
func (t *Tun) suspendLocked() error {
	if t.adapter == nil {
		return nil
	}
	t.up = false
	var errs []error
	if err := t.adapter.revertDNS(); err != nil {
		errs = append(errs, fmt.Errorf("tun: system DNS: %w", err))
	}
	if t.guard != nil {
		if err := t.guard.Disable(); err != nil {
			errs = append(errs, fmt.Errorf("tun: DNS protection: %w", err))
		}
	}
	if err := t.adapter.removeRoutes(); err != nil {
		errs = append(errs, fmt.Errorf("tun: routes: %w", err))
	}
	if err := t.adapter.removeAddress(); err != nil {
		errs = append(errs, fmt.Errorf("tun: address: %w", err))
	}
	return errors.Join(errs...)
}

// Close takes the adapter away, with its address, routes and system DNS. It is safe
// before Start and when repeated; concurrent calls all return after the same cleanup.
func (t *Tun) Close() error {
	t.closeOnce.Do(func() { t.closeErr = t.close() })
	return t.closeErr
}

func (t *Tun) close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	adapter := t.adapter
	if adapter == nil {
		return nil
	}
	// Even when up is false: a cleanup that failed in Suspend is retried before the
	// adapter, and with it the routes and the address, goes away.
	err := t.suspendLocked()
	t.adapter, t.configured = nil, false
	if cerr := adapter.close(); cerr != nil {
		err = errors.Join(err, fmt.Errorf("tun: adapter %q: %w", t.opts.Name, cerr))
	}
	t.logf("[TUN] adapter %q removed", t.opts.Name)
	return err
}

// openLocked is the adapter of the run, created on first need. Under t.mu.
func (t *Tun) openLocked() (*tunAdapter, error) {
	if t.closed {
		return nil, errTunClosed
	}
	if t.adapter != nil {
		return t.adapter, nil
	}
	adapter, err := t.openAdapter(t.opts.Name, t.opts.Dialer, t.opts.TunDNS, t.logf)
	if err != nil {
		return nil, fmt.Errorf("tun: %w", err)
	}
	t.adapter = adapter
	t.logf("[TUN] adapter %q created", t.opts.Name)
	return adapter, nil
}

func (t *Tun) logf(format string, args ...any) {
	if t.opts.Logf != nil {
		t.opts.Logf(format, args...)
	}
}

var (
	_ Adapter    = (*Tun)(nil)
	_ Guarded    = (*Tun)(nil)
	_ Persistent = (*Tun)(nil)
	_ Reserver   = (*Tun)(nil)
)
