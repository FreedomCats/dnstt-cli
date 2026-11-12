package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/FreedomCats/dnstt-cli/internal/config"
	"github.com/FreedomCats/dnstt-cli/internal/datapath"
	"github.com/FreedomCats/dnstt-cli/internal/sudo"
	"github.com/FreedomCats/dnstt-cli/internal/supervisor"
)

// menuProfile is a synthetic profile: the documentation domain, the shared key, a
// documentation resolver, the socks datapath and an exit with a login, so the password
// item is willing to run.
func menuProfile() *config.Profile {
	return &config.Profile{
		Domain:    "t.example.com",
		Pubkey:    wizardTestPubkey,
		Resolvers: []string{"udp:192.0.2.53:53"},
		Datapath:  "socks",
		Exit:      config.Exit{Type: "socks5", User: "user"},
	}
}

// menuTunProfile is the same profile over the tun datapath: the elevation decision has
// something to decide.
func menuTunProfile() *config.Profile {
	prof := menuProfile()
	prof.Datapath = "tun"
	return prof
}

// oneProfile is the set of one profile, which is also its default.
func oneProfile(name string) *config.Profiles {
	return &config.Profiles{
		DefaultProfile: name,
		Profile:        map[string]*config.Profile{name: menuProfile()},
	}
}

// twoProfiles is home and other, home as the default.
func twoProfiles() *config.Profiles {
	return &config.Profiles{
		DefaultProfile: "home",
		Profile: map[string]*config.Profile{
			"home":  menuProfile(),
			"other": menuProfile(),
		},
	}
}

// threeProfiles is home, other and second, home as the default.
func threeProfiles() *config.Profiles {
	return &config.Profiles{
		DefaultProfile: "home",
		Profile: map[string]*config.Profile{
			"home":   menuProfile(),
			"other":  menuProfile(),
			"second": menuProfile(),
		},
	}
}

// writeMenuProfiles saves a set into the isolated configuration directory.
func writeMenuProfiles(t *testing.T, profiles *config.Profiles) {
	t.Helper()
	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := config.SaveProfilesTo(path, profiles); err != nil {
		t.Fatal(err)
	}
}

// withTerminals fakes the two terminal tests of the start decision: a *os.File terminal
// cannot be faked, so the package variables are what the decision reads.
func withTerminals(t *testing.T, in, out bool) {
	t.Helper()
	previousIn, previousOut := stdinIsTerminal, stdoutIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return in }
	stdoutIsTerminal = func(io.Writer) bool { return out }
	t.Cleanup(func() {
		stdinIsTerminal, stdoutIsTerminal = previousIn, previousOut
	})
}

// menuHarness is a menu over fake command bodies: it records what each one was given and
// plays the person with a scripted prompter.
type menuHarness struct {
	t        *testing.T
	menu     *menu
	prompter *scriptedPrompter
	store    *fakeSecretStore
	out      *bytes.Buffer
	errOut   *bytes.Buffer
	env      *env

	connected []connectRequest
	checked   []string
	listed    int
	removed   [][]string
	saved     []*config.Profiles

	// elevated is the profile name of every call of the injected elevated runner;
	// elevateCode and elevateErr are what it returns.
	elevated    []string
	elevateCode int
	elevateErr  error

	// checkOutcomes is what the check body returns for a successful run.
	checkOutcomes []resolverOutcome

	// roundEvents is one entry per connection that reports a round: the fake calls the
	// request's callback with it and then waits for the connection to be stopped. With
	// blockConnections the connection only waits, so a test can stop it by signal.
	roundEvents      [][]supervisor.ResolverFailure
	blockConnections bool

	// mu guards finished, which a connection that reported a round sets before it
	// returns: a prompt can then check that the connection was stopped first.
	mu       sync.Mutex
	finished bool

	connectErr error
	checkErr   error
	saveErr    error
}

func newMenuHarness(t *testing.T, answers []scriptedAnswer) *menuHarness {
	t.Helper()
	var out, errOut bytes.Buffer
	h := &menuHarness{
		t:        t,
		prompter: &scriptedPrompter{t: t, answers: answers},
		store:    newFakeSecretStore(),
		out:      &out,
		errOut:   &errOut,
	}
	h.env = &env{stdout: &out, stderr: &errOut, stdin: strings.NewReader("")}

	h.menu = newMenu()
	// The elevation decision needs nothing here: a socks profile must never make an
	// existing test look for sudo on the machine.
	h.menu.elevation = func() elevationEnv {
		return elevationEnv{
			requires: datapath.Requirements{},
			invoker: func() (*sudo.User, error) {
				return nil, errors.New("the elevation decision must not look for sudo")
			},
			lookPath: func(string) (string, error) {
				return "", errors.New("the elevation decision must not look for sudo")
			},
		}
	}
	h.menu.elevate = func(_ context.Context, _ *env, name string) (int, error) {
		h.elevated = append(h.elevated, name)
		return h.elevateCode, h.elevateErr
	}
	h.menu.ask = h.prompter
	h.menu.store = func() (config.SecretStore, error) { return h.store, nil }
	h.menu.connect = func(ctx context.Context, _ *env, req connectRequest, _ <-chan struct{}) error {
		h.connected = append(h.connected, req)
		if h.blockConnections {
			<-ctx.Done()
			return nil
		}
		if len(h.roundEvents) > 0 {
			entry := h.roundEvents[0]
			h.roundEvents = h.roundEvents[1:]
			if req.onRoundFailed != nil {
				// A round can report twice: the second send is dropped by the
				// non-blocking callback, and the first event cancels the connection.
				req.onRoundFailed(entry)
				req.onRoundFailed(entry)
				<-ctx.Done()
				h.mu.Lock()
				h.finished = true
				h.mu.Unlock()
				return nil
			}
		}
		return h.connectErr
	}
	h.menu.check = func(_ context.Context, _ *env, name string) ([]resolverOutcome, error) {
		h.checked = append(h.checked, name)
		return h.checkOutcomes, h.checkErr
	}
	h.menu.list = func(_ context.Context, _ *env, _ []string) error {
		h.listed++
		return nil
	}
	h.menu.remove = func(_ context.Context, _ *env, args []string) error {
		h.removed = append(h.removed, args)
		return nil
	}
	h.menu.save = func(p *config.Profiles) error {
		h.saved = append(h.saved, p)
		return h.saveErr
	}
	return h
}

// choices is the number of times the loop asked for a choice. The question goes to the
// prompter, not to the output, because the scripted one does not write it.
func (h *menuHarness) choices() int {
	n := 0
	for _, question := range h.prompter.asked {
		if question == "Choice [1]: " {
			n++
		}
	}
	return n
}

// runLoop runs the loop on an injected signal channel and returns its result.
func (h *menuHarness) runLoop(ctx context.Context) error {
	return h.menu.runWith(ctx, h.env, "", make(chan os.Signal, 1))
}

// runConnect runs one connection of the named profile on an injected signal channel.
func (h *menuHarness) runConnect(ctx context.Context, name string) error {
	return h.menu.runWith(ctx, h.env, name, make(chan os.Signal, 1))
}

// menuRun is a menu session running in the background, with the signals the test sends.
type menuRun struct {
	t     *testing.T
	sig   chan os.Signal
	stop  context.CancelFunc
	once  sync.Once
	final chan error
}

func (h *menuHarness) start(ctx context.Context, name string) *menuRun {
	ctx, cancel := context.WithCancel(ctx)
	r := &menuRun{
		t:     h.t,
		sig:   make(chan os.Signal, 8),
		stop:  cancel,
		final: make(chan error, 1),
	}
	go func() { r.final <- h.menu.runWith(ctx, h.env, name, r.sig) }()
	return r
}

// close cancels the session, so that a failed test does not leave it running.
func (r *menuRun) close() {
	r.once.Do(r.stop)
}

// wait returns the session's result, or fails the test when it does not return.
func (r *menuRun) wait() error {
	r.t.Helper()
	select {
	case err := <-r.final:
		return err
	case <-time.After(10 * time.Second):
		r.close()
		return errors.New("the menu did not return")
	}
}

// waitingPrompter is a scripted prompter that, once its answers run out, waits for the
// context the way a real line reader waits for a line. asks receives every question.
type waitingPrompter struct {
	*scriptedPrompter
	asks chan string
}

func newWaitingPrompter(p *scriptedPrompter) *waitingPrompter {
	return &waitingPrompter{scriptedPrompter: p, asks: make(chan string, 16)}
}

func (p *waitingPrompter) Ask(ctx context.Context, question string) (string, error) {
	p.asks <- question
	if len(p.answers) == 0 {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return p.scriptedPrompter.Ask(ctx, question)
}

// --- the start decision and the command ---

func TestMenuStartDecision(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	t.Run("a terminal on both sides opens the menu", func(t *testing.T) {
		withTerminals(t, true, true)
		code, out, errOut := runCLI(t)
		if code != exitOK {
			t.Fatalf("exit code %d, want %d; stderr:\n%s", code, exitOK, errOut)
		}
		if !strings.Contains(out, `Profile "home" (socks).`) || !strings.Contains(out, "Choice [1]: ") {
			t.Fatalf("the menu did not open:\n%s", out)
		}
		if strings.Contains(out, "Commands:") {
			t.Fatalf("the command list was printed in a terminal:\n%s", out)
		}
	})

	for _, tc := range []struct {
		name string
		in   bool
		out  bool
	}{
		{"stdin redirected", false, true},
		{"stdout redirected", true, false},
	} {
		t.Run(tc.name+" prints the command list", func(t *testing.T) {
			withTerminals(t, tc.in, tc.out)
			code, out, _ := runCLI(t)
			if code != exitOK {
				t.Fatalf("exit code %d, want %d", code, exitOK)
			}
			if !strings.Contains(out, "Commands:") {
				t.Fatalf("the command list was not printed:\n%s", out)
			}
			if strings.Contains(out, "Choice") {
				t.Fatalf("the menu opened without a terminal:\n%s", out)
			}
		})
	}
}

func TestMenuConnectFlagRunsOneConnection(t *testing.T) {
	isolatedConnectConfig(t)
	h := newMenuHarness(t, nil)

	if err := h.runConnect(context.Background(), "home"); err != nil {
		t.Fatalf("the run = %v", err)
	}
	if len(h.connected) != 1 {
		t.Fatalf("connections = %d, want 1", len(h.connected))
	}
	// The request carries a callback, so it is not comparable: the fields are compared.
	// menu -connect leaves the round callback nil.
	got := h.connected[0]
	if got.profileName != "home" || got.datapath != "" || got.verbose || got.noKillSwitch ||
		got.allowLeak || got.onRoundFailed != nil {
		t.Fatalf("request %+v, want the profile only", got)
	}
	if h.listed != 0 || len(h.removed) != 0 || len(h.checked) != 0 || len(h.saved) != 0 {
		t.Fatal("the loop ran although -connect was given")
	}
}

func TestMenuConnectFlagUsage(t *testing.T) {
	isolatedConnectConfig(t)
	for _, args := range [][]string{
		{"menu", "-connect", ""},
		{"menu", "-connect", "home", "extra"},
		{"menu", "extra"},
	} {
		code, _, stderr := runCLI(t, args...)
		if code != exitUsage {
			t.Errorf("%q: exit code %d, want %d; stderr:\n%s", args, code, exitUsage, stderr)
		}
	}
}

// An unknown profile is the body's own error: the menu returns it as it is.
func TestMenuConnectUnknownProfile(t *testing.T) {
	isolatedConnectConfig(t)
	code, _, stderr := runCLI(t, "menu", "-connect", "nosuch")
	if code != exitError {
		t.Fatalf("exit code %d, want %d; stderr:\n%s", code, exitError, stderr)
	}
	if !strings.Contains(stderr, `profile "nosuch" not found`) {
		t.Fatalf("stderr does not name the missing profile:\n%s", stderr)
	}
}

// --- the loop ---

func TestMenuItemsCallTheCommandBodies(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, twoProfiles())

	items := newMenu().menuItems()
	tests := []struct {
		name    string
		answers []scriptedAnswer
		ran     func(h *menuHarness) bool
	}{
		{
			name:    "connect",
			answers: []scriptedAnswer{{value: "1"}, {value: ""}, {value: "0"}},
			ran:     func(h *menuHarness) bool { return len(h.connected) == 1 },
		},
		{
			name:    "check resolvers",
			answers: []scriptedAnswer{{value: "2"}, {value: "0"}},
			ran:     func(h *menuHarness) bool { return len(h.checked) == 1 },
		},
		{
			name:    "edit resolvers",
			answers: []scriptedAnswer{{value: "3"}, {value: "udp:192.0.2.53:53"}, {value: ""}, {value: "0"}},
			ran:     func(h *menuHarness) bool { return len(h.saved) == 1 },
		},
		{
			name:    "new exit password",
			answers: []scriptedAnswer{{value: "4"}, {secret: true, value: "new-password"}, {secret: true, value: "new-password"}, {value: "0"}},
			ran:     func(h *menuHarness) bool { return len(h.saved) == 1 && h.store.puts == 1 },
		},
		{
			name:    "change domain or key",
			answers: []scriptedAnswer{{value: "5"}, {value: "new.example.com"}, {value: ""}, {value: "0"}},
			ran:     func(h *menuHarness) bool { return len(h.saved) == 1 },
		},
		{
			name:    "another profile",
			answers: []scriptedAnswer{{value: "6"}, {value: "other"}, {value: "0"}},
			ran:     func(h *menuHarness) bool { return h.listed == 1 && len(h.saved) == 1 },
		},
		{
			name:    "delete profile",
			answers: []scriptedAnswer{{value: "7"}, {value: "y"}, {value: "0"}},
			ran:     func(h *menuHarness) bool { return len(h.removed) == 1 },
		},
	}
	if len(items) != len(tests) {
		t.Fatalf("items = %d, test cases = %d", len(items), len(tests))
	}
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if items[i].label != tc.name {
				t.Fatalf("item %d is %q, want %q", i+1, items[i].label, tc.name)
			}
			h := newMenuHarness(t, tc.answers)
			if err := h.runLoop(context.Background()); err != nil {
				t.Fatalf("the loop = %v", err)
			}
			if !tc.ran(h) {
				t.Fatalf("the body of %q did not run; stdout:\n%s", tc.name, h.out.String())
			}
			if h.choices() == 0 {
				t.Fatalf("the loop did not ask for a choice:\n%s", h.out.String())
			}
		})
	}
}

// newMenu must bind every item to the command's own function: the harness above replaces
// every seam, so two seams with the same signature could be swapped and all the other tests
// would still pass. Here newMenu's own list, remove, save and store run against the
// isolated configuration directory; only the bodies that would need a tunnel or a server,
// connect and check, are replaced. The store is never reached: no profile carries an exit
// password.
func TestMenuItemsCallTheRealCommandBodies(t *testing.T) {
	tests := []struct {
		name    string
		answers []scriptedAnswer
		want    []string
		check   func(t *testing.T, profiles *config.Profiles)
	}{
		{
			name:    "delete profile",
			answers: []scriptedAnswer{{value: "7"}, {value: "y"}, {value: "0"}},
			want:    []string{`Profile "home" removed.`},
			check: func(t *testing.T, profiles *config.Profiles) {
				if profiles.Profile["home"] != nil {
					t.Error("the deleted profile is still in the file")
				}
				if profiles.Profile["other"] == nil || len(profiles.Profile) != 1 {
					t.Errorf("remaining profiles = %v, want only other", profiles.Profile)
				}
			},
		},
		{
			name:    "another profile",
			answers: []scriptedAnswer{{value: "6"}, {value: "other"}, {value: "0"}},
			want:    []string{"Profiles:", "  home (default)"},
			check: func(t *testing.T, profiles *config.Profiles) {
				if profiles.DefaultProfile != "other" {
					t.Errorf("default_profile = %q, want other", profiles.DefaultProfile)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolatedConnectConfig(t)
			writeMenuProfiles(t, twoProfiles())

			var out, errOut bytes.Buffer
			ask := &scriptedPrompter{t: t, answers: tc.answers}
			m := newMenu()
			m.ask = ask
			m.connect = func(context.Context, *env, connectRequest, <-chan struct{}) error {
				return errors.New("the connect body must not run")
			}
			m.check = func(context.Context, *env, string) ([]resolverOutcome, error) {
				return nil, errors.New("the check body must not run")
			}
			e := &env{stdout: &out, stderr: &errOut, stdin: strings.NewReader("")}
			if err := m.runWith(context.Background(), e, "", make(chan os.Signal, 1)); err != nil {
				t.Fatalf("the loop = %v", err)
			}

			for _, want := range tc.want {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("the output does not contain %q:\n%s", want, out.String())
				}
			}
			if errOut.Len() != 0 {
				t.Fatalf("the loop wrote to stderr: %s", errOut.String())
			}
			profiles, err := config.LoadProfiles()
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, profiles)
		})
	}
}

// The header names the profile and its datapath, and an empty answer takes the first item.
func TestMenuEmptyAnswerConnects(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{{value: ""}, {value: ""}, {value: "0"}})
	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if !strings.Contains(h.out.String(), `Profile "home" (socks).`) {
		t.Fatalf("the header does not name the profile:\n%s", h.out.String())
	}
	if len(h.connected) != 1 || h.connected[0].profileName != "home" {
		t.Fatalf("the empty answer did not connect the current profile: %+v", h.connected)
	}
}

func TestMenuUnknownAnswerReasks(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{{value: "9"}, {value: "x"}, {value: "0"}})
	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	want := fmt.Sprintf("the choice must be a number from 0 to %d", len(h.menu.menuItems()))
	if got := strings.Count(h.out.String(), want); got != 2 {
		t.Fatalf("the range was printed %d times, want 2:\n%s", got, h.out.String())
	}
	if len(h.connected) != 0 {
		t.Fatal("an unknown answer ran an item")
	}
}

// The end of input closes the menu with nothing written and nothing reported.
func TestMenuEOFCloses(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, nil)
	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the end of input = %v, want nil", err)
	}
	if h.choices() != 1 {
		t.Fatalf("the menu asked %d times before the end of input, want 1", h.choices())
	}
	if h.errOut.Len() != 0 {
		t.Fatalf("the end of input wrote: %s", h.errOut.String())
	}
	if len(h.connected) != 0 || h.store.puts != 0 {
		t.Fatal("the end of input ran an item")
	}
}

// `menu` with redirected input reads the answer and closes cleanly.
func TestMenuCommandWithRedirectedInput(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	code, out, stderr := runCLIInput(t, "0\n", "menu")
	if code != exitOK {
		t.Fatalf("exit code %d, want %d; stderr:\n%s", code, exitOK, stderr)
	}
	if !strings.Contains(out, "Choice [1]: ") {
		t.Fatalf("the menu did not open:\n%s", out)
	}
}

// --- the items ---

func TestMenuAnotherProfile(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, twoProfiles())

	h := newMenuHarness(t, []scriptedAnswer{{value: "6"}, {value: "nosuch"}, {value: "other"}, {value: "0"}})
	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if h.listed != 1 {
		t.Fatalf("the list ran %d times, want 1", h.listed)
	}
	if !strings.Contains(h.out.String(), `profile "nosuch" not found`) {
		t.Fatalf("an unknown name was not reported:\n%s", h.out.String())
	}
	if len(h.saved) != 1 || h.saved[0].DefaultProfile != "other" {
		t.Fatalf("the default was not switched: %+v", h.saved)
	}
}

func TestMenuAnotherProfileFailedSave(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, twoProfiles())

	h := newMenuHarness(t, []scriptedAnswer{{value: "6"}, {value: "other"}, {value: "0"}})
	h.saveErr = errors.New("synthetic save failure")
	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if !strings.Contains(h.errOut.String(), "synthetic save failure") {
		t.Fatalf("the failed save was not reported: %s", h.errOut.String())
	}
	if got := h.choices(); got != 2 {
		t.Fatalf("the loop asked %d times, want 2", got)
	}

	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if profiles.DefaultProfile != "home" {
		t.Fatalf("default_profile = %q, want the old home", profiles.DefaultProfile)
	}
}

func TestMenuDeleteProfileAsksFirst(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	no := newMenuHarness(t, []scriptedAnswer{{value: "7"}, {value: "n"}, {value: "0"}})
	if err := no.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if len(no.removed) != 0 {
		t.Fatal("remove ran although the answer was n")
	}
	question := `Delete profile "home" and its exit password? (y/N): `
	if !strings.Contains(strings.Join(no.prompter.asked, "\n"), question) {
		t.Fatalf("the confirmation was not asked: %q", no.prompter.asked)
	}
	if !strings.Contains(no.out.String(), `Profile "home" (socks).`) {
		t.Fatal("the menu did not continue after the refusal")
	}

	yes := newMenuHarness(t, []scriptedAnswer{{value: "7"}, {value: "yes"}, {value: "0"}})
	if err := yes.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if len(yes.removed) != 1 || !slices.Equal(yes.removed[0], []string{"home"}) {
		t.Fatalf("remove was called with %q, want the profile name", yes.removed)
	}
}

// Deleting the default leaves the set without a default: the menu asks which profile to
// work with instead of treating the set as empty and asking for a link.
func TestMenuDeleteDefaultLeavesTheProfileChoice(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, threeProfiles())

	h := newMenuHarness(t, []scriptedAnswer{
		{value: "7"},     // delete profile
		{value: "y"},     // confirm
		{value: "other"}, // the profile to work with
		{value: "0"},     // leave
	})
	// The real command bodies: the delete must remove the default from the file, and the
	// choice must be listed and saved.
	h.menu.remove = runProfileRm
	h.menu.list = runProfileList
	h.menu.save = saveProfiles

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}

	for _, question := range h.prompter.asked {
		if strings.Contains(question, "slipnet://") {
			t.Fatalf("the link question was asked for a set with profiles: %q", h.prompter.asked)
		}
	}
	if !slices.Contains(h.prompter.asked, "Profile: ") {
		t.Fatalf("no profile question after the default was deleted; asked %q", h.prompter.asked)
	}
	if !strings.Contains(h.out.String(), "Profiles:") {
		t.Fatalf("the profiles were not listed:\n%s", h.out.String())
	}
	if !strings.Contains(h.out.String(), `Profile "home" removed.`) {
		t.Fatalf("the delete body did not run:\n%s", h.out.String())
	}
	if !strings.Contains(h.out.String(), `Profile "other" (socks).`) {
		t.Fatalf("the menu did not continue with the chosen profile:\n%s", h.out.String())
	}

	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if profiles.DefaultProfile != "other" {
		t.Fatalf("default_profile = %q, want other", profiles.DefaultProfile)
	}
	if len(profiles.Profile) != 2 {
		t.Fatalf("profiles = %d, want 2", len(profiles.Profile))
	}
}

// A set with profiles but no default is not an empty set: the menu asks which profile to
// work with, not for a link.
func TestMenuNoDefaultAsksForTheProfile(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, &config.Profiles{
		Profile: map[string]*config.Profile{
			"home":  menuProfile(),
			"other": menuProfile(),
		},
	})

	h := newMenuHarness(t, []scriptedAnswer{
		{value: "other"}, // the profile to work with
		{value: "0"},     // leave
	})
	h.menu.list = runProfileList
	h.menu.save = saveProfiles

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}

	for _, question := range h.prompter.asked {
		if strings.Contains(question, "slipnet://") {
			t.Fatalf("the link question was asked for a set with profiles: %q", h.prompter.asked)
		}
	}
	if !slices.Contains(h.prompter.asked, "Profile: ") {
		t.Fatalf("no profile question; asked %q", h.prompter.asked)
	}
	if !strings.Contains(h.out.String(), "Profiles:") {
		t.Fatalf("the profiles were not listed:\n%s", h.out.String())
	}
	if !strings.Contains(h.out.String(), `Profile "other" (socks).`) {
		t.Fatalf("the menu did not continue with the chosen profile:\n%s", h.out.String())
	}

	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if profiles.DefaultProfile != "other" {
		t.Fatalf("default_profile = %q, want other", profiles.DefaultProfile)
	}
}

// An empty answer at the profile question of the no-default state closes the menu: the
// person does not want to continue, and the menu has no way to go on without a default.
func TestMenuNoDefaultEmptyAnswerCloses(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, &config.Profiles{
		Profile: map[string]*config.Profile{
			"home":  menuProfile(),
			"other": menuProfile(),
		},
	})

	h := newMenuHarness(t, []scriptedAnswer{{value: ""}})
	h.menu.list = runProfileList
	h.menu.save = saveProfiles

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v, want nil", err)
	}
	if !slices.Contains(h.prompter.asked, "Profile: ") {
		t.Fatalf("the profile question was not asked; asked %q", h.prompter.asked)
	}
	if got := strings.Count(h.out.String(), "Profiles:"); got != 1 {
		t.Fatalf("the profiles were listed %d times, want 1:\n%s", got, h.out.String())
	}
	if h.choices() != 0 {
		t.Fatalf("the menu was shown after the empty answer: %d choices", h.choices())
	}
}

// The resolver check is the command's own body: it gets the current name, and its
// exitCodeError must not end the menu — the body has printed its line already.
func TestMenuCheckResolversExitsAndSurvives(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{{value: "2"}, {value: "0"}})
	h.checkErr = &exitCodeError{code: exitError}
	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop ended on exitCodeError: %v", err)
	}
	if len(h.checked) != 1 || h.checked[0] != "home" {
		t.Fatalf("check was called with %q, want the current profile", h.checked)
	}
	if h.errOut.Len() != 0 {
		t.Fatalf("the menu reprinted the body's line: %s", h.errOut.String())
	}
	if got := h.choices(); got != 2 {
		t.Fatalf("the loop asked %d times, want 2", got)
	}
}

func TestMenuFailedItemIsPrintedAndTheLoopContinues(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{{value: "1"}, {value: ""}, {value: "0"}})
	h.connectErr = errors.New("synthetic failure")
	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if !strings.Contains(h.errOut.String(), "dnstt-cli: synthetic failure") {
		t.Fatalf("the failed item was not printed: %s", h.errOut.String())
	}
	if got := h.choices(); got != 2 {
		t.Fatalf("the loop asked %d times, want 2", got)
	}
}

// --- the first profile ---

// An empty link answer runs the wizard; the profile appears and the loop continues with it.
func TestMenuNoProfileRunsTheWizard(t *testing.T) {
	isolatedConnectConfig(t)
	answers := append([]scriptedAnswer{{secret: true, value: ""}}, baseWizardAnswers("http")...)
	h := newMenuHarness(t, answers)

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if !strings.Contains(h.out.String(), `Profile "home" added (exit http, mode `) {
		t.Fatalf("the wizard's profile was not reported:\n%s", h.out.String())
	}
	if h.store.puts != 1 || h.store.values["home.password"] != wizardTestPassword {
		t.Fatalf("the exit password was not stored: puts %d", h.store.puts)
	}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if profiles.Profile["home"] == nil || profiles.DefaultProfile != "home" {
		t.Fatal("the wizard's profile is not in the file")
	}
	if !strings.Contains(h.out.String(), `Profile "home" (`) {
		t.Fatalf("the menu did not continue with the new profile:\n%s", h.out.String())
	}
}

// A dnstt link is imported through the same body as `profile import`: the first question of
// the menu is the hidden link.
func TestMenuNoProfileImportsTheLink(t *testing.T) {
	path := isolatedConnectConfig(t)
	h := newMenuHarness(t, []scriptedAnswer{{secret: true, value: importDnsttLink(nil)}})

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if !strings.Contains(h.out.String(), `Profile "home" imported (exit socks5).`) {
		t.Fatalf("the import was not reported:\n%s", h.out.String())
	}
	profiles, err := config.LoadProfilesFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if profiles.Profile["home"] == nil || profiles.DefaultProfile != "home" {
		t.Fatal("the imported profile is not in the file")
	}
	if h.prompter.asked[0] != "slipnet:// link (Enter to answer the questions): " {
		t.Fatalf("the first question = %q", h.prompter.asked[0])
	}
}

// A bad paste is reported and the same question is asked again.
func TestMenuNoProfileRetriesABadLink(t *testing.T) {
	isolatedConnectConfig(t)
	h := newMenuHarness(t, []scriptedAnswer{
		{secret: true, value: "not a link"},
		{secret: true, value: importDnsttLink(nil)},
	})

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if !strings.Contains(h.out.String(), "not a slipnet:// link") {
		t.Fatalf("the bad paste was not reported:\n%s", h.out.String())
	}
	if len(h.prompter.asked) < 2 || h.prompter.asked[0] != h.prompter.asked[1] {
		t.Fatalf("the question did not repeat: %q", h.prompter.asked)
	}
}

// The end of input at the link question closes the menu and writes nothing.
func TestMenuNoProfileEOF(t *testing.T) {
	isolatedConnectConfig(t)
	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}

	h := newMenuHarness(t, nil)
	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the end of input = %v, want nil", err)
	}
	if h.store.puts != 0 || len(h.saved) != 0 {
		t.Fatal("the end of input wrote a profile")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the end of input left a file: %v", err)
	}
	if h.errOut.Len() != 0 {
		t.Fatalf("the end of input wrote: %s", h.errOut.String())
	}
}

// --- the signals ---

// The session's press rules: stop the item, do not wait, and nothing on the third press.
func TestMenuSessionSignalRules(t *testing.T) {
	var out bytes.Buffer
	menuCtx, menuCancel := context.WithCancel(context.Background())
	defer menuCancel()
	sess := newMenuSession(&out, menuCancel)

	itemCtx, itemCancel := context.WithCancel(context.Background())
	defer itemCancel()
	abort := sess.running(itemCancel)

	sess.handle(os.Interrupt)
	if itemCtx.Err() == nil {
		t.Fatal("the first Ctrl+C did not stop the item")
	}
	if menuCtx.Err() != nil {
		t.Fatal("the first Ctrl+C closed the menu")
	}
	if !strings.Contains(out.String(), "\ndnstt-cli: stopping...\n") {
		t.Fatalf("the stop was not printed: %q", out.String())
	}

	sess.handle(os.Interrupt)
	select {
	case <-abort:
	default:
		t.Fatal("the second Ctrl+C did not close the abort channel")
	}
	if !strings.Contains(out.String(), "not waiting for open connections") {
		t.Fatalf("the second press was not printed: %q", out.String())
	}

	// The third press must not close the abort again and must not exit.
	sess.handle(os.Interrupt)
	if menuCtx.Err() != nil {
		t.Fatal("the third Ctrl+C closed the menu")
	}

	// SIGTERM and SIGHUP stop the item and close the menu.
	for _, sig := range []os.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(fmt.Sprint(sig), func(t *testing.T) {
			var subOut bytes.Buffer
			subMenuCtx, subMenuCancel := context.WithCancel(context.Background())
			defer subMenuCancel()
			sub := newMenuSession(&subOut, subMenuCancel)
			subItemCtx, subItemCancel := context.WithCancel(context.Background())
			defer subItemCancel()
			sub.running(subItemCancel)

			sub.handle(sig)
			if subItemCtx.Err() == nil {
				t.Fatalf("%v did not stop the item", sig)
			}
			if subMenuCtx.Err() == nil {
				t.Fatalf("%v did not close the menu", sig)
			}
		})
	}
}

// With no item, Ctrl+C closes the menu without a word.
func TestMenuSessionIdleInterrupt(t *testing.T) {
	var out bytes.Buffer
	menuCtx, menuCancel := context.WithCancel(context.Background())
	defer menuCancel()
	sess := newMenuSession(&out, menuCancel)

	sess.handle(os.Interrupt)
	if menuCtx.Err() == nil {
		t.Fatal("the idle Ctrl+C did not close the menu")
	}
	if out.Len() != 0 {
		t.Fatalf("the idle Ctrl+C printed: %q", out.String())
	}
}

// signalConnectBody is a connect body that waits for the item's stop: it reports when the
// context ended and whether the abort channel was closed. With waitAbort it waits for the
// abort channel; without it, it looks once and returns, so one press is enough.
type signalConnectBody struct {
	started   chan struct{}
	cancelled chan struct{}
	aborted   chan bool
	waitAbort bool
}

func newSignalConnectBody(waitAbort bool) *signalConnectBody {
	return &signalConnectBody{
		started:   make(chan struct{}),
		cancelled: make(chan struct{}),
		aborted:   make(chan bool, 1),
		waitAbort: waitAbort,
	}
}

func (b *signalConnectBody) run(ctx context.Context, abort <-chan struct{}) error {
	close(b.started)
	<-ctx.Done()
	close(b.cancelled)
	if b.waitAbort {
		select {
		case <-abort:
			b.aborted <- true
		case <-time.After(5 * time.Second):
			b.aborted <- false
		}
	} else {
		select {
		case <-abort:
			b.aborted <- true
		default:
			b.aborted <- false
		}
	}
	return ctx.Err()
}

// install makes the menu's connect item this body.
func (b *signalConnectBody) install(h *menuHarness) {
	h.menu.connect = func(ctx context.Context, _ *env, _ connectRequest, abort <-chan struct{}) error {
		return b.run(ctx, abort)
	}
}

// One Ctrl+C stops the running connection and returns to the menu.
func TestMenuInterruptStopsTheItem(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{{value: "1"}, {value: ""}, {value: "0"}})
	body := newSignalConnectBody(false)
	body.install(h)

	run := h.start(context.Background(), "")
	t.Cleanup(run.close)

	<-body.started
	run.sig <- os.Interrupt
	<-body.cancelled

	if aborted := <-body.aborted; aborted {
		t.Fatal("one Ctrl+C closed the abort channel")
	}
	if err := run.wait(); err != nil {
		t.Fatalf("the loop = %v, want nil", err)
	}
	if !strings.Contains(h.errOut.String(), "dnstt-cli: stopping...") {
		t.Fatalf("the stop was not printed: %s", h.errOut.String())
	}
	if got := h.choices(); got != 2 {
		t.Fatalf("the loop asked %d times, want 2", got)
	}
}

// The second Ctrl+C does not wait for open connections.
func TestMenuSecondInterruptClosesTheAbort(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{{value: "1"}, {value: ""}, {value: "0"}})
	body := newSignalConnectBody(true)
	body.install(h)

	run := h.start(context.Background(), "")
	t.Cleanup(run.close)

	<-body.started
	run.sig <- os.Interrupt
	<-body.cancelled
	run.sig <- os.Interrupt

	if aborted := <-body.aborted; !aborted {
		t.Fatal("the second Ctrl+C did not close the abort channel")
	}
	if err := run.wait(); err != nil {
		t.Fatalf("the loop = %v, want nil", err)
	}
	if !strings.Contains(h.errOut.String(), "not waiting for open connections") {
		t.Fatalf("the second press was not printed: %s", h.errOut.String())
	}
}

// A third Ctrl+C does not kill the process: it changes nothing, and the menu still closes
// on the next one.
func TestMenuThirdInterruptDoesNotExit(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{{value: "1"}, {value: ""}})
	ask := newWaitingPrompter(h.prompter)
	h.menu.ask = ask
	body := newSignalConnectBody(true)
	body.install(h)

	run := h.start(context.Background(), "")
	t.Cleanup(run.close)

	if got := <-ask.asks; got != "Choice [1]: " {
		t.Fatalf("the menu did not ask for a choice: %q", got)
	}
	// The offer asks before the body starts: its answer is scripted, so the connection
	// starts at once, and the question is read from the queue before the next choice.
	if got := <-ask.asks; got != offerResolversQuestion {
		t.Fatalf("the offer did not ask for the resolvers: %q", got)
	}
	<-body.started
	run.sig <- os.Interrupt
	<-body.cancelled
	run.sig <- os.Interrupt
	if aborted := <-body.aborted; !aborted {
		t.Fatal("the second Ctrl+C did not close the abort channel")
	}

	// The item has returned and the menu waits for a choice: a third press closes the menu
	// instead of exiting the process.
	if got := <-ask.asks; got != "Choice [1]: " {
		t.Fatalf("the menu did not ask for a choice: %q", got)
	}
	run.sig <- os.Interrupt
	if err := run.wait(); err != nil {
		t.Fatalf("the third Ctrl+C = %v, want the menu closed", err)
	}
}

// SIGTERM and SIGHUP stop the running connection and end the loop.
func TestMenuTermSignalEndsTheLoop(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	for _, sig := range []os.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(fmt.Sprint(sig), func(t *testing.T) {
			h := newMenuHarness(t, []scriptedAnswer{{value: "1"}, {value: ""}})
			body := newSignalConnectBody(false)
			body.install(h)

			run := h.start(context.Background(), "")
			t.Cleanup(run.close)

			<-body.started
			run.sig <- sig
			<-body.cancelled
			if aborted := <-body.aborted; aborted {
				t.Fatalf("%v closed the abort channel", sig)
			}
			if err := run.wait(); err != nil {
				t.Fatalf("the loop = %v, want nil", err)
			}
			if got := h.choices(); got != 1 {
				t.Fatalf("the loop asked %d times, want 1", got)
			}
		})
	}
}
