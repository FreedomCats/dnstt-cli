package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/FreedomCats/dnstt-cli/internal/config"
	"github.com/FreedomCats/dnstt-cli/internal/datapath"
	"github.com/FreedomCats/dnstt-cli/internal/sudo"
)

// --- the decision ---

// elevationFailInvoker is an invoker that fails the test if the decision asks for it.
func elevationFailInvoker(t *testing.T) func() (*sudo.User, error) {
	t.Helper()
	return func() (*sudo.User, error) {
		t.Fatalf("the elevation decision asked for the invoker")
		return nil, nil
	}
}

// elevationFailLookPath is a lookPath that fails the test if the decision looks for sudo.
func elevationFailLookPath(t *testing.T) func(string) (string, error) {
	t.Helper()
	return func(name string) (string, error) {
		t.Fatalf("the elevation decision looked for %q", name)
		return "", nil
	}
}

func TestPlanElevation(t *testing.T) {
	tunUnsupported := errors.New("tun is not implemented on this system yet; use -datapath socks")
	invokerErr := errors.New("synthetic invoker failure")
	user := &sudo.User{Name: "user"}

	tests := []struct {
		name    string
		kind    string
		env     func(t *testing.T) elevationEnv
		want    elevationPlan
		wantErr error
	}{
		{
			name: "socks has nothing to decide",
			kind: "socks",
			env: func(t *testing.T) elevationEnv {
				return elevationEnv{
					goos:     "linux",
					euid:     1000,
					invoker:  elevationFailInvoker(t),
					lookPath: elevationFailLookPath(t),
					requires: datapath.Requirements{Admin: true},
				}
			},
		},
		{
			name: "an unsupported datapath",
			kind: "tun",
			env: func(t *testing.T) elevationEnv {
				return elevationEnv{
					goos:     "linux",
					euid:     1000,
					invoker:  elevationFailInvoker(t),
					lookPath: elevationFailLookPath(t),
					requires: datapath.Requirements{Unsupported: tunUnsupported, Admin: true},
				}
			},
			want: elevationPlan{reason: "the tun datapath cannot run on this system: " + tunUnsupported.Error()},
		},
		{
			name: "a datapath that needs no admin",
			kind: "tun",
			env: func(t *testing.T) elevationEnv {
				return elevationEnv{
					goos:     "linux",
					euid:     1000,
					invoker:  elevationFailInvoker(t),
					lookPath: elevationFailLookPath(t),
					requires: datapath.Requirements{},
				}
			},
		},
		{
			name: "admin, non-root, sudo found",
			kind: "tun",
			env: func(t *testing.T) elevationEnv {
				return elevationEnv{
					goos:    "linux",
					euid:    1000,
					invoker: func() (*sudo.User, error) { return nil, nil },
					lookPath: func(name string) (string, error) {
						if name != "sudo" {
							t.Fatalf("lookPath(%q), want sudo", name)
						}
						return "/usr/bin/sudo", nil
					},
					requires: datapath.Requirements{Admin: true},
				}
			},
			want: elevationPlan{sudo: true},
		},
		{
			name: "sudo missing",
			kind: "tun",
			env: func(t *testing.T) elevationEnv {
				return elevationEnv{
					goos:     "linux",
					euid:     1000,
					invoker:  func() (*sudo.User, error) { return nil, nil },
					lookPath: func(string) (string, error) { return "", errors.New("executable file not found in $PATH") },
					requires: datapath.Requirements{Admin: true},
				}
			},
			want: elevationPlan{reason: "the tun datapath needs root, and the elevated run is not available: sudo was not found in PATH"},
		},
		{
			name: "root directly",
			kind: "tun",
			env: func(t *testing.T) elevationEnv {
				return elevationEnv{
					goos:     "linux",
					euid:     0,
					invoker:  func() (*sudo.User, error) { return nil, nil },
					lookPath: elevationFailLookPath(t),
					requires: datapath.Requirements{Admin: true},
				}
			},
		},
		{
			name: "under sudo",
			kind: "tun",
			env: func(t *testing.T) elevationEnv {
				return elevationEnv{
					goos:     "linux",
					euid:     0,
					invoker:  func() (*sudo.User, error) { return user, nil },
					lookPath: elevationFailLookPath(t),
					requires: datapath.Requirements{Admin: true},
				}
			},
		},
		{
			name: "an invoker without root",
			kind: "tun",
			env: func(t *testing.T) elevationEnv {
				return elevationEnv{
					goos:     "linux",
					euid:     1000,
					invoker:  func() (*sudo.User, error) { return user, nil },
					lookPath: elevationFailLookPath(t),
					requires: datapath.Requirements{Admin: true},
				}
			},
		},
		{
			name: "an invoker error",
			kind: "tun",
			env: func(t *testing.T) elevationEnv {
				return elevationEnv{
					goos:     "linux",
					euid:     1000,
					invoker:  func() (*sudo.User, error) { return nil, invokerErr },
					lookPath: elevationFailLookPath(t),
					requires: datapath.Requirements{Admin: true},
				}
			},
			wantErr: invokerErr,
		},
		{
			name: "windows",
			kind: "tun",
			env: func(t *testing.T) elevationEnv {
				return elevationEnv{
					goos:     "windows",
					euid:     1000,
					invoker:  elevationFailInvoker(t),
					lookPath: elevationFailLookPath(t),
					requires: datapath.Requirements{Admin: true},
				}
			},
			want: elevationPlan{reason: "the tun datapath needs root, and the elevated run is not available on this system yet"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := planElevation(tc.kind, tc.env(t))
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("planElevation = %v", err)
			}
			if got != tc.want {
				t.Fatalf("plan = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// --- the child's command ---

// The argv is a list with the flags before the profile name, the streams are the env's own
// and the working directory and the environment are inherited. Nothing is started.
func TestElevatedCommand(t *testing.T) {
	in := strings.NewReader("")
	var out, errOut bytes.Buffer
	e := &env{stdin: in, stdout: &out, stderr: &errOut}

	cmd, err := elevatedCommand(e, "home")
	if err != nil {
		t.Fatalf("elevatedCommand = %v", err)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"sudo", "--", exe, "connect", "-datapath", "tun", "home"}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("args = %q, want %q", cmd.Args, want)
	}
	if cmd.Stdin != e.stdin || cmd.Stdout != e.stdout || cmd.Stderr != e.stderr {
		t.Fatal("the child's streams are not the env's own")
	}
	if cmd.Dir != "" || cmd.Env != nil {
		t.Fatalf("Dir = %q, Env = %q: the working directory and the environment must be inherited", cmd.Dir, cmd.Env)
	}
	if cmd.Process != nil {
		t.Fatal("the command was started")
	}
}

// --- the item over the harness ---

// oneTunProfile is the set of one profile over tun, which is also its default.
func oneTunProfile(name string) *config.Profiles {
	return &config.Profiles{
		DefaultProfile: name,
		Profile:        map[string]*config.Profile{name: menuTunProfile()},
	}
}

// elevationEnvWithSudo is the environment of a non-root session where tun needs root and
// sudo is in PATH: the decision is the elevated run.
func elevationEnvWithSudo() elevationEnv {
	return elevationEnv{
		goos:     "linux",
		euid:     1000,
		invoker:  func() (*sudo.User, error) { return nil, nil },
		lookPath: func(string) (string, error) { return "/usr/bin/sudo", nil },
		requires: datapath.Requirements{Admin: true},
	}
}

// elevationEnvWithoutSudo is the same, with no sudo in PATH.
func elevationEnvWithoutSudo() elevationEnv {
	env := elevationEnvWithSudo()
	env.lookPath = func(string) (string, error) {
		return "", errors.New("executable file not found in $PATH")
	}
	return env
}

// newTunHarness is a menu harness over one tun profile and the given elevation environment.
func newTunHarness(t *testing.T, answers []scriptedAnswer, env elevationEnv) *menuHarness {
	t.Helper()
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneTunProfile("home"))
	h := newMenuHarness(t, answers)
	h.menu.elevation = func() elevationEnv { return env }
	return h
}

// A tun profile in a non-root session runs the elevated child, not the body.
func TestMenuElevatedConnectionRunsTheChild(t *testing.T) {
	h := newTunHarness(t, []scriptedAnswer{
		{value: "1"}, // connect
		{value: ""},  // connect with the saved resolvers
		{value: "0"}, // leave
	}, elevationEnvWithSudo())

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if !slices.Equal(h.elevated, []string{"home"}) {
		t.Fatalf("the elevated runner was called with %q, want [home]", h.elevated)
	}
	if len(h.connected) != 0 {
		t.Fatalf("the in-process body ran: %+v", h.connected)
	}
	if h.errOut.Len() != 0 {
		t.Fatalf("the elevated run wrote to stderr: %s", h.errOut.String())
	}
	if got := h.choices(); got != 2 {
		t.Fatalf("the loop asked %d times, want 2", got)
	}
}

// A non-zero status of the child is reported with its code and the menu stays; nothing is
// asked.
func TestMenuElevatedConnectionNonZeroStatus(t *testing.T) {
	h := newTunHarness(t, []scriptedAnswer{
		{value: "1"},
		{value: ""},
		{value: "0"},
	}, elevationEnvWithSudo())
	h.elevateCode = 3

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if !strings.Contains(h.errOut.String(), "dnstt-cli: the elevated connection exited with status 3") {
		t.Fatalf("the status was not reported: %s", h.errOut.String())
	}
	if slices.Contains(h.prompter.asked, socksOfferQuestion) {
		t.Fatalf("a status was answered with the socks question: %q", h.prompter.asked)
	}
	if len(h.connected) != 0 {
		t.Fatalf("the in-process body ran: %+v", h.connected)
	}
	if got := h.choices(); got != 2 {
		t.Fatalf("the loop asked %d times, want 2", got)
	}
}

// A child killed by a signal is reported with the signal line, not "status -1"; nothing is
// asked and the menu stays.
func TestMenuElevatedConnectionStoppedBySignal(t *testing.T) {
	h := newTunHarness(t, []scriptedAnswer{
		{value: "1"},
		{value: ""},
		{value: "0"},
	}, elevationEnvWithSudo())
	h.elevateCode = -1

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if !strings.Contains(h.errOut.String(), "dnstt-cli: the elevated connection was stopped by a signal") {
		t.Fatalf("the signal stop was not reported: %s", h.errOut.String())
	}
	if strings.Contains(h.errOut.String(), "status -1") {
		t.Fatalf("the signal stop was reported as a status: %s", h.errOut.String())
	}
	if slices.Contains(h.prompter.asked, socksOfferQuestion) {
		t.Fatalf("a signal stop was answered with the socks question: %q", h.prompter.asked)
	}
	if len(h.connected) != 0 {
		t.Fatalf("the in-process body ran: %+v", h.connected)
	}
	if got := h.choices(); got != 2 {
		t.Fatalf("the loop asked %d times, want 2", got)
	}
}

// A child that could not start is reported and the socks fallback is offered; a yes runs
// the body with the socks datapath and the usual mid-session offer.
func TestMenuElevatedConnectionStartFailureSocksYes(t *testing.T) {
	h := newTunHarness(t, []scriptedAnswer{
		{value: "1"},
		{value: ""},
		{value: "y"},
		{value: "0"},
	}, elevationEnvWithSudo())
	h.elevateErr = errors.New("the elevated run could not be started: synthetic failure")

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if !strings.Contains(h.errOut.String(), "dnstt-cli: the elevated run could not be started: synthetic failure") {
		t.Fatalf("the failed start was not reported: %s", h.errOut.String())
	}
	if !slices.Contains(h.prompter.asked, socksOfferQuestion) {
		t.Fatalf("the socks fallback was not offered: %q", h.prompter.asked)
	}
	if len(h.connected) != 1 {
		t.Fatalf("connections = %d, want the socks fallback", len(h.connected))
	}
	got := h.connected[0]
	if got.profileName != "home" || got.datapath != "socks" || got.onRoundFailed == nil {
		t.Fatalf("request %+v, want the profile with the socks datapath and the round offer", got)
	}
	if got := h.choices(); got != 2 {
		t.Fatalf("the loop asked %d times, want 2", got)
	}
}

// A no at the socks question connects nothing and the loop goes on.
func TestMenuElevatedConnectionStartFailureSocksNo(t *testing.T) {
	h := newTunHarness(t, []scriptedAnswer{
		{value: "1"},
		{value: ""},
		{value: "n"},
		{value: "0"},
	}, elevationEnvWithSudo())
	h.elevateErr = errors.New("the elevated run could not be started: synthetic failure")

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if len(h.connected) != 0 {
		t.Fatalf("a no connected: %+v", h.connected)
	}
	if !strings.Contains(h.out.String(), `Profile "home" (tun).`) {
		t.Fatalf("the menu did not continue after the refusal:\n%s", h.out.String())
	}
	if got := h.choices(); got != 2 {
		t.Fatalf("the loop asked %d times, want 2", got)
	}
}

// The end of input at the socks question closes the menu.
func TestMenuElevatedConnectionStartFailureEOF(t *testing.T) {
	h := newTunHarness(t, []scriptedAnswer{
		{value: "1"},
		{value: ""},
	}, elevationEnvWithSudo())
	h.elevateErr = errors.New("the elevated run could not be started: synthetic failure")

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the end of input = %v, want nil", err)
	}
	if len(h.connected) != 0 {
		t.Fatalf("the end of input connected: %+v", h.connected)
	}
	if got := h.choices(); got != 1 {
		t.Fatalf("the loop asked %d times, want 1", got)
	}
}

// A reason of the decision is printed and socks is offered without calling the runner.
func TestMenuElevationReasonOffersSocks(t *testing.T) {
	h := newTunHarness(t, []scriptedAnswer{
		{value: "1"},
		{value: ""},
		{value: "n"},
		{value: "0"},
	}, elevationEnvWithoutSudo())

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	const reason = "the tun datapath needs root, and the elevated run is not available: sudo was not found in PATH"
	if !strings.Contains(h.out.String(), reason) {
		t.Fatalf("the reason was not printed:\n%s", h.out.String())
	}
	if !slices.Contains(h.prompter.asked, socksOfferQuestion) {
		t.Fatalf("the socks fallback was not offered: %q", h.prompter.asked)
	}
	if len(h.elevated) != 0 {
		t.Fatalf("the runner ran although the decision had a reason: %q", h.elevated)
	}
	if len(h.connected) != 0 {
		t.Fatalf("the body ran: %+v", h.connected)
	}
}

// Root runs the body in the process: no second sudo and no runner.
func TestMenuElevationRootRunsTheBody(t *testing.T) {
	env := elevationEnvWithSudo()
	env.euid = 0
	h := newTunHarness(t, []scriptedAnswer{
		{value: "1"},
		{value: ""},
		{value: "0"},
	}, env)

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if len(h.connected) != 1 {
		t.Fatalf("connections = %d, want the body", len(h.connected))
	}
	got := h.connected[0]
	if got.profileName != "home" || got.datapath != "" || got.onRoundFailed == nil {
		t.Fatalf("request %+v, want the profile with its own datapath", got)
	}
	if len(h.elevated) != 0 {
		t.Fatalf("root ran the elevated runner: %q", h.elevated)
	}
}

// A stop that arrived before the child started: the item returns context.Canceled and asks
// nothing.
func TestMenuElevationCancelledItem(t *testing.T) {
	h := newTunHarness(t, nil, elevationEnvWithSudo())
	sess := newMenuSession(io.Discard, func() {})
	h.menu.elevate = func(ctx context.Context, e *env, name string) (int, error) {
		return runElevated(ctx, e, name, sess)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := h.menu.connectWithResolvers(ctx, h.env, "home", make(chan struct{}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("the item = %v, want context.Canceled", err)
	}
	if len(h.prompter.asked) != 0 {
		t.Fatalf("the cancelled item asked %q", h.prompter.asked)
	}
	if len(h.connected) != 0 {
		t.Fatalf("the cancelled item connected: %+v", h.connected)
	}
}

// menu -connect runs the body of a tun profile and never elevates.
func TestMenuConnectFlagDoesNotElevate(t *testing.T) {
	h := newTunHarness(t, nil, elevationEnvWithSudo())

	if err := h.runConnect(context.Background(), "home"); err != nil {
		t.Fatalf("the run = %v", err)
	}
	if len(h.connected) != 1 || h.connected[0].profileName != "home" {
		t.Fatalf("connections = %+v, want one for home", h.connected)
	}
	if len(h.elevated) != 0 {
		t.Fatalf("menu -connect elevated: %q", h.elevated)
	}
	if len(h.prompter.asked) != 0 {
		t.Fatalf("menu -connect asked %q", h.prompter.asked)
	}
}

// --- the session ---

// While suspended the session takes no signal of the terminal: the presses are the child's.
// After resume the item's rules are back.
func TestMenuSessionSuspendedLeavesSignalsToTheChild(t *testing.T) {
	var out bytes.Buffer
	menuCtx, menuCancel := context.WithCancel(context.Background())
	defer menuCancel()
	sess := newMenuSession(&out, menuCancel)

	itemCtx, itemCancel := context.WithCancel(context.Background())
	defer itemCancel()
	sess.running(itemCancel)

	sess.suspend()
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		sess.handle(sig)
	}
	if itemCtx.Err() != nil {
		t.Fatal("a press while suspended stopped the item")
	}
	if menuCtx.Err() != nil {
		t.Fatal("a press while suspended closed the menu")
	}
	if out.Len() != 0 {
		t.Fatalf("a press while suspended printed: %q", out.String())
	}

	sess.resume()
	sess.handle(os.Interrupt)
	if itemCtx.Err() == nil {
		t.Fatal("the first press after resume did not stop the item")
	}
	if !strings.Contains(out.String(), "\ndnstt-cli: stopping...\n") {
		t.Fatalf("the stop was not printed: %q", out.String())
	}
	if menuCtx.Err() != nil {
		t.Fatal("the first press after resume closed the menu")
	}
}
