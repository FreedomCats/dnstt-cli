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
	"github.com/FreedomCats/dnstt-cli/internal/exit"
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
	if err := parse(e, fs, args); err != nil {
		return err
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

	// Load profiles.
	profiles, err := config.LoadProfiles()
	if err != nil {
		return err
	}

	// Find the profile to use.
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

	// Build the tunnel.
	logf := func(format string, args ...any) {
		if *verbose {
			fmt.Fprintf(e.stderr, format+"\n", args...)
		}
	}

	transitions := make(chan supervisor.Transition, 100)
	tunnel, err := app.BuildTunnel(app.TunnelOptions{
		Profile:     prof,
		ProfileName: profileName,
		Logf:        logf,
		OnTransition: func(tr supervisor.Transition) {
			transitions <- tr
		},
	})
	if err != nil {
		return err
	}

	// Build the datapath.
	dp, err := app.BuildDatapath(tunnel, prof, logf)
	if err != nil {
		return err
	}
	defer dp.Close()

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
				fmt.Fprintf(e.stderr, "\n%s: stopping...\n", app.Name)
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

	// Start the status line if output is a terminal.
	var status *statusLine
	if isTerminal(e.stdout) {
		status = newStatusLine(e.stdout, profileName)
		defer status.stop()
		go status.run(transitions, tunnel.Supervisor)
	}

	// Run the tunnel. The datapath is already running (started in NewSocks).
	errCh := make(chan error, 1)
	go func() { errCh <- tunnel.Supervisor.Run(ctx) }()

	// Wait for an error or cancellation.
	select {
	case err := <-errCh:
		cancel()
		return finishConnect(err, prof.Exit.Type, dp, abortCh)
	case <-ctx.Done():
		// Clean shutdown requested.
		cancel()
		// Wait for the supervisor to finish, but abort on second Ctrl+C.
		select {
		case err := <-errCh:
			return finishConnect(err, prof.Exit.Type, dp, abortCh)
		case <-abortCh:
			// Second Ctrl+C: force immediate exit.
			dp.Close()
			return <-errCh
		}
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
