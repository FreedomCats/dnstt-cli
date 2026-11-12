package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/FreedomCats/dnstt-cli/internal/app"
	"github.com/FreedomCats/dnstt-cli/internal/config"
	"github.com/FreedomCats/dnstt-cli/internal/datapath"
	"github.com/FreedomCats/dnstt-cli/internal/sudo"
)

// socksOfferQuestion is the question of the socks fallback: the profile's datapath cannot
// run as it asks, and the connection can go through the proxy for this time.
const socksOfferQuestion = "Connect with socks for this time? (y/N): "

// elevationPlan is what the menu does before one connection: run it as an elevated child,
// or offer socks with the reason.
type elevationPlan struct {
	// sudo: re-run the one connection through sudo.
	sudo bool
	// reason: why the connection cannot run as the profile asks — privileges that cannot
	// be got, or a datapath that does not work here. When it is set the menu offers socks
	// instead of connecting.
	reason string
}

// elevationEnv is what the elevation decision reads: the platform, the privileges, sudo
// and the requirements of the datapath. A test asks it about any system without root or a
// real sudo.
type elevationEnv struct {
	goos     string
	euid     int
	invoker  func() (*sudo.User, error)
	lookPath func(string) (string, error)
	requires datapath.Requirements
}

// systemElevation fills the decision's environment from the running system.
func systemElevation() elevationEnv {
	return elevationEnv{
		goos:     runtime.GOOS,
		euid:     os.Geteuid(),
		invoker:  sudo.Invoker,
		lookPath: exec.LookPath,
		requires: (&datapath.Tun{}).Requires(),
	}
}

// planElevation decides how the menu runs one connection of the given datapath. Each
// earlier answer makes the later ones pointless, so the order is the contract.
func planElevation(kind string, env elevationEnv) (elevationPlan, error) {
	if kind != "tun" {
		return elevationPlan{}, nil
	}
	if env.requires.Unsupported != nil {
		// Requirements says the other fields are not checked once Unsupported is set: no
		// privilege helps a datapath that does not work here.
		return elevationPlan{reason: fmt.Sprintf(
			"the %s datapath cannot run on this system: %v", kind, env.requires.Unsupported)}, nil
	}
	if env.goos == "windows" {
		// Before the sudo lookup: a Windows build must not report a missing sudo. The
		// elevated run in a new window is 04-tun-windows's.
		return elevationPlan{reason: fmt.Sprintf(
			"the %s datapath needs root, and the elevated run is not available on this system yet", kind)}, nil
	}
	if !env.requires.Admin {
		return elevationPlan{}, nil
	}
	invoker, err := env.invoker()
	if err != nil {
		return elevationPlan{}, err
	}
	if invoker != nil || env.euid == 0 {
		// Root itself, or sudo, whose Invoker names the user the settings and the keyring
		// belong to: a second sudo would be the same thing again, and a password prompt
		// for nothing.
		return elevationPlan{}, nil
	}
	if _, err := env.lookPath("sudo"); err != nil {
		return elevationPlan{reason: fmt.Sprintf(
			"the %s datapath needs root, and the elevated run is not available: sudo was not found in PATH", kind)}, nil
	}
	return elevationPlan{sudo: true}, nil
}

// elevationPlan decides what the menu does with the connection of the named profile. A
// missing profile returns the zero plan: the body still answers profile %q not found.
func (m *menu) elevationPlan(name string) (elevationPlan, error) {
	profiles, err := config.LoadProfiles()
	if err != nil {
		return elevationPlan{}, err
	}
	prof := profiles.Profile[name]
	if prof == nil {
		return elevationPlan{}, nil
	}
	return planElevation(datapathOf(prof), m.elevation())
}

// elevatedCommand builds the child's command: the same binary, one connection through the
// tun datapath, under sudo. The argv is a list, never a shell string: -- ends sudo's own
// options, and the flags of connect come before the profile name because flag.Parse stops
// at the first positional argument.
//
// The streams are the env's own, so sudo asks for its password on the terminal and the
// status line and Ctrl+C are the child's. Dir stays empty and Env stays nil, so the
// working directory and the environment are inherited. Nothing is added to them: the exit
// password never leaves the secret store, and sudo's own prompt is the only place a
// password is typed.
func elevatedCommand(e *env, name string) (*exec.Cmd, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("cannot find the dnstt-cli binary: %w", err)
	}
	cmd := exec.Command("sudo", "--", exe, "connect", "-datapath", "tun", name)
	cmd.Stdin = e.stdin
	cmd.Stdout = e.stdout
	cmd.Stderr = e.stderr
	return cmd, nil
}

// runElevated runs one connection as the same binary under sudo and returns its exit
// status. The terminal's signals are the child's while it runs: the session is suspended
// before Start and resumed after Wait, so the parent takes no press and prints nothing,
// and the kernel delivers them to the foreground process group, the child first.
//
// exec.Command, never exec.CommandContext: a cancelled context would SIGKILL the child and
// leave the routes, the system DNS and the kill-switch behind. The child's own ladder
// stops it — the same one the command line uses.
func runElevated(ctx context.Context, e *env, name string, sess *menuSession) (int, error) {
	if err := ctx.Err(); err != nil {
		// A stop that arrived before the child started must not start it.
		return 0, err
	}
	cmd, err := elevatedCommand(e, name)
	if err != nil {
		return 0, err
	}

	sess.suspend()
	defer sess.resume()

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("the elevated run could not be started: %w", err)
	}
	err = cmd.Wait()
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return 0, err
}

// socksInstead offers the socks datapath for this time. reason, when not empty, says why
// the connection cannot run as the profile asks. A yes runs the connection with socks and
// the usual mid-session offer; anything else returns to the menu.
func (m *menu) socksInstead(ctx context.Context, e *env, name, reason string, abort <-chan struct{}) error {
	if reason != "" {
		fmt.Fprintln(e.stdout, reason)
	}
	answer, err := m.ask.Ask(ctx, socksOfferQuestion)
	if err != nil {
		// The end of input at the question closes the menu.
		return readError(ctx, err)
	}
	if !isYes(answer) {
		return nil
	}
	return m.connectSessions(ctx, e, name, "socks", abort)
}

// elevatedConnection runs one connection as an elevated child and reports its outcome. A
// cancelled context is the person stopping the item: it is returned as it is, and
// reportItemError is silent about it, as in the round offer. Any other error means nothing
// ran: it is printed and the socks fallback is offered. A non-zero exit status is reported
// with its code, a child stopped by a signal with the signal line, and the menu stays in
// both cases. Zero prints nothing: the child said everything.
//
// The elevated run has no mid-session resolver offer: the child reports nothing back —
// there is no IPC — and the connection is the child's, as on the command line. That is
// recorded rather than built.
func (m *menu) elevatedConnection(ctx context.Context, e *env, name string, abort <-chan struct{}) error {
	code, err := m.elevate(ctx, e, name)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			return err
		}
		fmt.Fprintf(e.stderr, "%s: %v\n", app.Name, err)
		return m.socksInstead(ctx, e, name, "", abort)
	}
	switch {
	case code > 0:
		fmt.Fprintf(e.stderr, "%s: the elevated connection exited with status %d\n", app.Name, code)
	case code < 0:
		// A negative code is a child killed by a signal: Ctrl+C at sudo's password
		// prompt is the usual one, and the code would otherwise read as "status -1".
		fmt.Fprintf(e.stderr, "%s: the elevated connection was stopped by a signal\n", app.Name)
	}
	return nil
}
