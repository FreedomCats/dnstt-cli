package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/FreedomCats/dnstt-cli/internal/app"
	"github.com/FreedomCats/dnstt-cli/internal/config"
	"github.com/FreedomCats/dnstt-cli/internal/datapath"
	"github.com/FreedomCats/dnstt-cli/internal/exit"
	"github.com/FreedomCats/dnstt-cli/internal/netguard"
	"github.com/FreedomCats/dnstt-cli/internal/supervisor"
)

func connectCommand() *command {
	return &command{
		name:    "connect",
		args:    "[profile]",
		summary: "connect to the dnstt server",
		description: `Reads the profile, builds the tunnel and the datapath, and runs until stopped.
The first Ctrl+C stops cleanly, the second does not wait for open connections.

If no profile name is given, the default profile is used.`,
		run: runConnect,
	}
}

func runConnect(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(connectCommand())
	verbose := fs.Bool("v", false, "verbose: show tunnel logs")
	datapathFlag := fs.String("datapath", "", "datapath: socks or tun (default: the profile's, then socks)")
	noKillSwitch := fs.Bool("no-kill-switch", false, "tun: let system traffic pass between sessions")
	allowLeak := fs.Bool("allow-leak", false, "tun: connect even if DNS protection or the kill-switch cannot be set")
	if err := parse(e, fs, args); err != nil {
		return err
	}
	if *datapathFlag != "" && *datapathFlag != "socks" && *datapathFlag != "tun" {
		return usagef("invalid -datapath %q: the datapath is socks or tun", *datapathFlag)
	}

	// Determine the profile name.
	var profileName string
	switch fs.NArg() {
	case 0:
		// Use the default profile.
	case 1:
		profileName = fs.Arg(0)
	default:
		return usagef("extra argument: connect takes at most one profile name")
	}

	// Set up signal handling: first Ctrl+C stops cleanly, second does not wait.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	abortCh := make(chan struct{})
	go func() {
		count := 0
		for {
			sig := <-sigCh
			count++
			if count == 1 {
				writeStopNotice(e.stderr)
				cancel()
			} else if count == 2 {
				fmt.Fprintf(e.stderr, "%s: not waiting for open connections\n", app.Name)
				close(abortCh)
			} else {
				fmt.Fprintf(e.stderr, "%s: received %v, exiting\n", app.Name, sig)
				os.Exit(1)
			}
		}
	}()

	return runConnection(ctx, e, connectRequest{
		profileName:  profileName,
		datapath:     *datapathFlag,
		verbose:      *verbose,
		noKillSwitch: *noKillSwitch,
		allowLeak:    *allowLeak,
	}, abortCh)
}

// writeStopNotice is the one wording of the line the first stop prints: connect's ladder
// and the menu's session share it.
func writeStopNotice(w io.Writer) {
	fmt.Fprintf(w, "\n%s: stopping...\n", app.Name)
}

// connectRequest is what one connection needs: the profile and the flags of `connect`. The
// menu supplies the profile alone.
type connectRequest struct {
	profileName  string
	datapath     string
	verbose      bool
	noKillSwitch bool
	allowLeak    bool

	// onRoundFailed receives a full round of failed handshakes after the tunnel has been
	// up; nil — silent. The menu installs it to offer the resolvers; connect leaves it nil.
	onRoundFailed func([]supervisor.ResolverFailure)
}

// runConnection connects once: it loads the profile, builds the tunnel and the datapath,
// and runs until stopped. abort is the caller's force-abort channel: closing it stops the
// wait for open connections instead of draining them.
//
// The caller owns ctx and is what stops the run: this function installs no signal handling,
// because the menu has a ladder of its own and two of them would race.
func runConnection(ctx context.Context, e *env, req connectRequest, abort <-chan struct{}) error {
	// Load profiles.
	profiles, err := config.LoadProfiles()
	if err != nil {
		return err
	}

	// Find the profile to use.
	profileName := req.profileName
	if profileName == "" {
		profileName = profiles.DefaultProfile
		if profileName == "" {
			return errors.New("no default profile set")
		}
	}
	prof, ok := profiles.Profile[profileName]
	if !ok {
		return fmt.Errorf("profile %q not found", profileName)
	}

	// The datapath of the run: the flag, then the profile, then socks.
	kind := prof.Datapath
	if req.datapath != "" {
		kind = req.datapath
	}
	if kind == "" {
		kind = "socks"
	}
	p := *prof
	p.Datapath = kind

	logf := func(format string, args ...any) {
		if req.verbose {
			fmt.Fprintf(e.stderr, format+"\n", args...)
		}
	}
	// notice is the user-facing output: the leak protection lines go to stderr even
	// without -v.
	notice := func(format string, args ...any) {
		fmt.Fprintf(e.stderr, format+"\n", args...)
	}

	// The guard exists before the tunnel, because the session config takes its Control at
	// construction. It does not touch the system yet: the adapter index is the
	// datapath's, and only the datapath knows it.
	var guard *netguard.Guard
	var control func(network, address string, c syscall.RawConn) error
	if kind == "tun" {
		guard = &netguard.Guard{Logf: logf}
		control = guard.Control
	}

	// Build the tunnel. The status line exists only on a terminal, and only it reads the
	// transitions: with redirected output no callback is installed, so nobody can stall
	// the supervisor by not consuming them.
	transitions, onTransition := statusTransitions(e.stdout)
	tunnel, err := app.BuildTunnel(app.TunnelOptions{
		Profile:       &p,
		ProfileName:   profileName,
		Logf:          logf,
		Notice:        notice,
		Control:       control,
		OnTransition:  onTransition,
		OnRoundFailed: req.onRoundFailed,
	})
	if err != nil {
		return err
	}

	// Build the datapath. For socks the listener is open already; for tun the adapter
	// opens on the first session.
	dp, err := app.BuildDatapath(tunnel, &p, logf)
	if err != nil {
		return err
	}
	// The datapath goes first, then the protection: the guard session outlives the
	// adapter and takes the rules away with it. The protection is opened later.
	var prot *app.Protection
	defer func() {
		dp.Close()
		if prot != nil {
			prot.Close()
		}
	}()

	// Refuse a datapath this system cannot run before the reservation and before any
	// dial: a missing TUN device or a missing root is an error of the command, not a
	// failed attempt to retry.
	requires := dp.Requires()
	if requires.Unsupported != nil {
		return fmt.Errorf("%s datapath: %w", kind, requires.Unsupported)
	}
	if requires.Admin && os.Geteuid() != 0 {
		return fmt.Errorf("the %s datapath needs root: run sudo %s connect %q, or connect with -datapath socks",
			kind, app.Name, profileName)
	}

	// The datapath that lives across sessions (the tun adapter) belongs to the
	// supervisor: it starts and suspends it with the sessions.
	if d, ok := dp.(supervisor.Datapath); ok {
		tunnel.Supervisor.SetDatapath(d)
	}

	// Hold what has to be taken once, before the first attempt, and remove the system
	// DNS a crashed run left behind. Supervisor.Run comes later in this function, and
	// its first session resolves a resolver's host name through the system DNS, while
	// Tun.Start and its own recovery come only after that session: without this call the
	// leftover of kill -9 outlives the attempt.
	release, err := prepareDatapath(dp, logf)
	if err != nil {
		return err
	}
	defer release()

	// Keep the core's own sockets out of a datapath that takes all system traffic, and
	// follow the network changes.
	if requires.Netguard {
		adapter, ok := dp.(datapath.Adapter)
		if !ok {
			return fmt.Errorf("internal error: the %s datapath needs netguard but has no adapter index", kind)
		}
		if guard == nil {
			return fmt.Errorf("internal error: the %s datapath needs netguard but the guard was not created", kind)
		}
		stop, err := app.EnableNetguard(app.NetguardOptions{
			Guard:   guard,
			Exclude: adapter.AdapterID,
			Restart: tunnel.Supervisor.Restart,
			Logf:    logf,
		})
		if err != nil {
			return err
		}
		defer stop()
	}

	// DNS leak protection and the kill-switch: a datapath that takes all system traffic
	// needs the session before Run, and a refusal is a clear error, not a loop of
	// reconnections. The mark is set only here: SO_MARK needs the table's CAP_NET_ADMIN.
	if requires.DNSGuard {
		on, off := killSwitchPolicy(&p, req.noKillSwitch)
		prot, err = app.EnableProtection(app.ProtectionOptions{
			KillSwitch: on,
			AllowLeak:  req.allowLeak,
			Notice:     notice,
		})
		if err != nil {
			return err
		}
		if off != "" {
			notice("kill-switch is off (%s): system traffic goes past the tunnel while it is down", off)
		}
		if prot.Guard != nil {
			guarded, ok := dp.(datapath.Guarded)
			if !ok {
				return fmt.Errorf("internal error: the %s datapath needs DNS protection but has no guard seam", kind)
			}
			if guard == nil {
				return fmt.Errorf("internal error: the %s datapath needs DNS protection but the guard was not created", kind)
			}
			guarded.SetDNSGuard(prot.Guard)
			guard.Mark = netguard.SocketMark
		}
		tunnel.Supervisor.SetKillSwitch(prot.Locked)
	}

	// Start the status line if output is a terminal.
	var status *statusLine
	if transitions != nil {
		status = newStatusLine(e.stdout, profileName)
		defer status.stop()
		go status.run(transitions, tunnel.Supervisor)
	}

	// Run the tunnel. The socks listener is already up; the tun adapter opens on the
	// first session.
	errCh := make(chan error, 1)
	go func() { errCh <- tunnel.Supervisor.Run(ctx) }()

	// Wait for an error or cancellation.
	select {
	case err := <-errCh:
		return finishConnect(err, prof.Exit.Type, dp, abort)
	case <-ctx.Done():
		// Clean shutdown requested.
		// Wait for the supervisor to finish, but abort on the second stop.
		select {
		case err := <-errCh:
			return finishConnect(err, prof.Exit.Type, dp, abort)
		case <-abort:
			// The second stop does not wait for open connections.
			dp.Close()
			return <-errCh
		}
	}
}

// prepareDatapath takes what has to be taken once, before the first attempt, and returns
// a function that releases it: the datapath's reservation (Reserver) and, under it, the
// system DNS a crashed run left behind (DNSRecoverer).
//
// The recovery runs here rather than in the first Start because Supervisor.Run comes
// later and its first session may resolve a resolver's host name through the system DNS,
// while the adapter's own recovery comes only after that session. Under the reservation
// the leftover can only be the dead run's; without it, the call could remove the DNS of
// a running dnstt-cli. A datapath with neither interface (socks) is left alone, and its
// release does nothing.
func prepareDatapath(dp app.Datapath, logf func(string, ...any)) (release func(), err error) {
	if r, ok := dp.(datapath.Reserver); ok {
		release, err = r.Reserve()
		if err != nil {
			return nil, err
		}
	}
	if r, ok := dp.(datapath.DNSRecoverer); ok {
		done, err := r.RecoverSystemDNS()
		if err != nil {
			if release != nil {
				release()
			}
			return nil, err
		}
		if done != "" {
			logf("[TUN] %s", done)
		}
	}
	if release == nil {
		release = func() {}
	}
	return release, nil
}

// killSwitchPolicy decides whether the kill-switch is on for this run and, when it is
// off, what turned it off: the flag first, then the profile's kill_switch.
func killSwitchPolicy(prof *config.Profile, noKillSwitch bool) (on bool, off string) {
	switch {
	case noKillSwitch:
		return false, "-no-kill-switch"
	case !prof.KillSwitchOn():
		return false, "kill_switch = false in the profile"
	default:
		return true, ""
	}
}

// A fatal HTTP refusal may stop Run before the application goroutine sends its SOCKS5
// failure. Give accepted handshakes the usual shutdown limit without delaying FatalExit.
func finishConnect(err error, exitType string, dp interface{ Close() error }, aborted <-chan struct{}) error {
	if exitType == "http" && exit.IsLogin(err) {
		if s, ok := dp.(interface{ Shutdown(context.Context) error }); ok {
			ctx, cancel := context.WithTimeout(context.Background(), supervisor.DefaultDrainTimeout)
			defer cancel()
			go func() {
				select {
				case <-aborted:
					cancel()
				case <-ctx.Done():
				}
			}()
			_ = s.Shutdown(ctx)
		}
	}
	return err
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// isTerminalReader is isTerminal for the input side: it decides whether a command can ask
// a person a question on stdin.
func isTerminalReader(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// statusTransitions decides whether the run has a status line and returns the channel it
// reads and the callback for the supervisor. Only a terminal has a status line; without
// one nothing reads the transitions, so no callback is installed and a run under systemd
// or with its output redirected cannot hold the supervisor.
func statusTransitions(w io.Writer) (chan supervisor.Transition, func(supervisor.Transition)) {
	if !isTerminal(w) {
		return nil, nil
	}
	transitions := make(chan supervisor.Transition, 100)
	return transitions, notifyTransitions(transitions)
}

// notifyTransitions wakes the status line up. It never blocks the supervisor: the status
// line redraws on its ticker as well, so a transition dropped on a full buffer costs a
// redraw, not the state.
func notifyTransitions(transitions chan<- supervisor.Transition) func(supervisor.Transition) {
	return func(tr supervisor.Transition) {
		select {
		case transitions <- tr:
		default:
		}
	}
}

// statusLine prints the tunnel status at the bottom of the terminal.
type statusLine struct {
	w           io.Writer
	profileName string
	stopCh      chan struct{}
	stopped     chan struct{}
}

func newStatusLine(w io.Writer, profileName string) *statusLine {
	return &statusLine{
		w:           w,
		profileName: profileName,
		stopCh:      make(chan struct{}),
		stopped:     make(chan struct{}),
	}
}

func (s *statusLine) run(transitions <-chan supervisor.Transition, tun *supervisor.Tunnel) {
	defer close(s.stopped)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			s.clear()
			return
		case <-transitions:
			s.update(tun)
		case <-ticker.C:
			s.update(tun)
		}
	}
}

func (s *statusLine) update(tun *supervisor.Tunnel) {
	status := tun.Status()
	var line string

	switch status.State {
	case supervisor.StateIdle:
		line = "idle"
	case supervisor.StateDialing:
		line = fmt.Sprintf("dialing %s...", status.Resolver)
	case supervisor.StateConnected:
		uptime := time.Since(status.Since).Truncate(time.Second)
		stats := status.Stats
		line = fmt.Sprintf("connected | %s | up %v | streams %d | ↓%s ↑%s | latency %v",
			status.Resolver, uptime, stats.Streams,
			formatBytes(int64(stats.BytesReceived)), formatBytes(int64(stats.BytesSent)),
			status.Handshake.Truncate(time.Millisecond))
	case supervisor.StateDegraded:
		uptime := time.Since(status.Since).Truncate(time.Second)
		stats := status.Stats
		line = fmt.Sprintf("degraded | %s | up %v | streams %d | ↓%s ↑%s",
			status.Resolver, uptime, stats.Streams,
			formatBytes(int64(stats.BytesReceived)), formatBytes(int64(stats.BytesSent)))
	case supervisor.StateReconnecting:
		line = "reconnecting..."
	case supervisor.StateStopping:
		line = "stopping..."
	}

	// Clear the line and print the status.
	s.clear()
	fmt.Fprintf(s.w, "%s", line)
}

func (s *statusLine) clear() {
	// Clear the current line.
	fmt.Fprintf(s.w, "\r\033[K")
}

func (s *statusLine) stop() {
	close(s.stopCh)
	<-s.stopped
}

func formatBytes(n int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case n < KB:
		return fmt.Sprintf("%dB", n)
	case n < MB:
		return fmt.Sprintf("%.1fKB", float64(n)/KB)
	case n < GB:
		return fmt.Sprintf("%.1fMB", float64(n)/MB)
	default:
		return fmt.Sprintf("%.2fGB", float64(n)/GB)
	}
}
