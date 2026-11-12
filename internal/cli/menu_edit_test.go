package cli

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/FreedomCats/dnstt-cli/internal/config"
	"github.com/FreedomCats/dnstt-cli/internal/exit"
	"github.com/FreedomCats/dnstt-cli/internal/resolvers"
)

// seedFakeSecret puts a value into the fake store without counting a Put: the test seeds
// the state a previous run left, not a call of the code under test.
func seedFakeSecret(store *fakeSecretStore, name, value string) string {
	ref := "file:" + name
	store.values[name] = value
	store.refs[ref] = name
	return ref
}

// readConfigFile reads profiles.toml of the isolated configuration directory.
func readConfigFile(t *testing.T) string {
	t.Helper()
	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// --- the connect-time offer ---

func TestMenuConnectOfferPrintsResolvers(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{{value: "1"}, {value: ""}, {value: "0"}})
	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	out := h.out.String()
	if !strings.Contains(out, `Resolvers of profile "home":`) {
		t.Fatalf("the resolvers were not printed:\n%s", out)
	}
	if !strings.Contains(out, "  udp:192.0.2.53:53\n") {
		t.Fatalf("the resolver entry was not printed:\n%s", out)
	}
	if !slices.Contains(h.prompter.asked, offerResolversQuestion) {
		t.Fatalf("the offer was not asked; asked %q", h.prompter.asked)
	}
	if len(h.connected) != 1 || h.connected[0].profileName != "home" {
		t.Fatalf("the offer did not connect the current profile: %+v", h.connected)
	}
	if len(h.saved) != 0 {
		t.Fatal("the offer wrote a profile")
	}
}

// An empty resolver list is shown and used as "network".
func TestMenuConnectOfferShowsNetworkForAnEmptyList(t *testing.T) {
	isolatedConnectConfig(t)
	profiles := oneProfile("home")
	profiles.Profile["home"].Resolvers = nil
	writeMenuProfiles(t, profiles)

	h := newMenuHarness(t, []scriptedAnswer{{value: "1"}, {value: ""}, {value: "0"}})
	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if !strings.Contains(h.out.String(), "  network\n") {
		t.Fatalf("the empty list was not shown as network:\n%s", h.out.String())
	}
}

func TestMenuConnectOfferEditsAndConnects(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{
		{value: "1"},              // connect
		{value: "e"},              // edit the resolvers
		{value: "doh-cloudflare"}, // one entry
		{value: ""},               // finish
		{value: "0"},              // leave
	})
	h.menu.save = saveProfiles
	// The body reads the profile from the file: record what this connect sees, so the
	// edited list must already be saved by the time the body runs.
	var seen []string
	h.menu.connect = func(_ context.Context, _ *env, req connectRequest, _ <-chan struct{}) error {
		h.connected = append(h.connected, req)
		profiles, err := config.LoadProfiles()
		if err != nil {
			t.Fatal(err)
		}
		seen = profiles.Profile[req.profileName].Resolvers
		return nil
	}

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if len(h.connected) != 1 {
		t.Fatalf("the connection did not run after the edit: %+v", h.connected)
	}
	if !slices.Equal(seen, []string{"doh-cloudflare"}) {
		t.Fatalf("the connection saw the resolvers %v, want the edited list", seen)
	}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if got := profiles.Profile["home"].Resolvers; !slices.Equal(got, []string{"doh-cloudflare"}) {
		t.Fatalf("the saved resolvers = %v, want the edited list", got)
	}
}

func TestMenuConnectOfferReturnsToTheMenu(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{{value: "1"}, {value: "n"}, {value: "0"}})
	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if len(h.connected) != 0 {
		t.Fatal("n started a connection")
	}
	if len(h.saved) != 0 {
		t.Fatal("n wrote a profile")
	}
	if got := h.choices(); got != 2 {
		t.Fatalf("the menu asked %d times, want 2", got)
	}
}

func TestMenuConnectOfferUnknownAnswerReasks(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{{value: "1"}, {value: "x"}, {value: "n"}, {value: "0"}})
	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if !strings.Contains(h.out.String(), offerResolversHint) {
		t.Fatalf("the keys were not printed for an unknown answer:\n%s", h.out.String())
	}
	if len(h.connected) != 0 {
		t.Fatal("an unknown answer connected")
	}
}

// The end of input at the offer closes the menu, as anywhere else, and writes nothing.
func TestMenuConnectOfferEOFCloses(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{{value: "1"}})
	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the end of input = %v, want nil", err)
	}
	if len(h.connected) != 0 {
		t.Fatal("the end of input connected")
	}
	if len(h.saved) != 0 {
		t.Fatal("the end of input wrote a profile")
	}
	if h.errOut.Len() != 0 {
		t.Fatalf("the end of input wrote: %s", h.errOut.String())
	}
}

// --- the resolver editor ---

func TestMenuEditResolversShowsSavesAndPrintsBack(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{
		{value: "3"},                // edit resolvers
		{value: "udp:192.0.2.1:53"}, // one entry
		{value: ""},                 // finish
		{value: "0"},                // leave
	})
	h.menu.save = saveProfiles

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	out := h.out.String()
	if !strings.Contains(out, "  udp:192.0.2.53:53\n") {
		t.Fatalf("the current list was not shown:\n%s", out)
	}
	if !strings.Contains(out, `Resolvers of profile "home" saved.`) {
		t.Fatalf("the save was not reported:\n%s", out)
	}
	if !strings.Contains(out, "  udp:192.0.2.1:53\n") {
		t.Fatalf("the saved list was not printed back:\n%s", out)
	}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if got := profiles.Profile["home"].Resolvers; !slices.Equal(got, []string{"udp:192.0.2.1:53"}) {
		t.Fatalf("the saved resolvers = %v, want the typed list", got)
	}
}

func TestMenuEditResolversInvalidEntryReasksAndSavesTheValidEntry(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{
		{value: "3"},              // edit resolvers
		{value: "not-a-resolver"}, // rejected
		{value: "doh-cloudflare"}, // accepted after the re-ask
		{value: ""},               // finish
		{value: "0"},              // leave
	})
	h.menu.save = saveProfiles

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	out := h.out.String()
	if !strings.Contains(out, "no such preset") {
		t.Fatalf("the reason was not printed:\n%s", out)
	}
	// The rejected entry was asked again: the first question repeats before the valid
	// entry moves the editor on to the "another resolver" question.
	if got := strings.Count(strings.Join(h.prompter.asked, "\n"), `Resolver (Enter for "network"): `); got != 2 {
		t.Fatalf("the invalid entry was not asked again: %q", h.prompter.asked)
	}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if got := profiles.Profile["home"].Resolvers; !slices.Equal(got, []string{"doh-cloudflare"}) {
		t.Fatalf("the saved resolvers = %v, want only the valid entry", got)
	}
	if !strings.Contains(out, "  doh-cloudflare\n") {
		t.Fatalf("the saved list was not printed back:\n%s", out)
	}
}

func TestMenuEditResolversEmptyFirstAnswerIsNetwork(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{{value: "3"}, {value: ""}, {value: "0"}})
	h.menu.save = saveProfiles

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if got := profiles.Profile["home"].Resolvers; !slices.Equal(got, []string{"network"}) {
		t.Fatalf("the saved resolvers = %v, want network", got)
	}
	if !strings.Contains(h.out.String(), "  network\n") {
		t.Fatalf("the saved list was not printed back:\n%s", h.out.String())
	}
}

func TestMenuEditResolversEOFWritesNothing(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{{value: "3"}})
	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the end of input = %v, want nil", err)
	}
	if len(h.saved) != 0 {
		t.Fatal("the end of input saved a list")
	}
}

func TestMenuEditResolversFailedSaveIsReported(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{
		{value: "3"},
		{value: "udp:192.0.2.1:53"},
		{value: ""},
		{value: "0"},
	})
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
	if got := profiles.Profile["home"].Resolvers; !slices.Equal(got, []string{"udp:192.0.2.53:53"}) {
		t.Fatalf("a failed save changed the file: %v", got)
	}
}

// --- the check item's drop offer ---

// mixedCheckOutcomes is a run where the network entry has one passing server and one
// failing, one hand-written entry fails and a preset passes.
func mixedCheckOutcomes() []resolverOutcome {
	return []resolverOutcome{
		{entry: "network", ok: false},
		{entry: "network", ok: true},
		{entry: "udp:192.0.2.53:53", ok: false},
		{entry: "doh-cloudflare", ok: true},
	}
}

func TestMenuCheckDropsFailedEntries(t *testing.T) {
	isolatedConnectConfig(t)
	profiles := oneProfile("home")
	profiles.Profile["home"].Resolvers = []string{"network", "udp:192.0.2.53:53", "doh-cloudflare"}
	writeMenuProfiles(t, profiles)

	h := newMenuHarness(t, []scriptedAnswer{{value: "2"}, {value: "y"}, {value: "0"}})
	h.checkOutcomes = mixedCheckOutcomes()
	h.menu.save = saveProfiles

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	out := h.out.String()
	if !strings.Contains(out, "1 of 3 resolvers failed: udp:192.0.2.53:53") {
		t.Fatalf("the failed entries were not named:\n%s", out)
	}
	if !slices.Contains(h.prompter.asked, "Remove them and save the profile? (y/N): ") {
		t.Fatalf("the drop offer was not asked; asked %q", h.prompter.asked)
	}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if got := profiles.Profile["home"].Resolvers; !slices.Equal(got, []string{"network", "doh-cloudflare"}) {
		t.Fatalf("the saved resolvers = %v, want the list without the failed entry", got)
	}
	if got := strings.Count(out, "  network\n"); got != 1 {
		// The saved list is printed back: the network entry stays exactly once even though
		// it expanded to two checks, one of which passed.
		t.Fatalf("network appears %d times in the output, want 1:\n%s", got, out)
	}
}

func TestMenuCheckDeclinedWritesNothing(t *testing.T) {
	isolatedConnectConfig(t)
	profiles := oneProfile("home")
	profiles.Profile["home"].Resolvers = []string{"network", "udp:192.0.2.53:53", "doh-cloudflare"}
	writeMenuProfiles(t, profiles)

	h := newMenuHarness(t, []scriptedAnswer{{value: "2"}, {value: "n"}, {value: "0"}})
	h.checkOutcomes = mixedCheckOutcomes()

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if len(h.saved) != 0 {
		t.Fatal("a refusal saved a profile")
	}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if got := profiles.Profile["home"].Resolvers; !slices.Equal(got, []string{"network", "udp:192.0.2.53:53", "doh-cloudflare"}) {
		t.Fatalf("a refusal changed the file: %v", got)
	}
}

func TestMenuCheckNothingFailedAsksNothing(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{{value: "2"}, {value: "0"}})
	h.checkOutcomes = []resolverOutcome{
		{entry: "udp:192.0.2.53:53", ok: true},
	}

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if slices.Contains(h.prompter.asked, "Remove them and save the profile? (y/N): ") {
		t.Fatal("the drop offer was asked with nothing failed")
	}
	if len(h.saved) != 0 {
		t.Fatal("a passing run saved a profile")
	}
}

// A run where nothing passed is the exitCodeError of the body: no offer, no second report.
func TestMenuCheckNothingPassedOffersNothing(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{{value: "2"}, {value: "0"}})
	h.checkErr = &exitCodeError{code: exitError}

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop ended on exitCodeError: %v", err)
	}
	if slices.Contains(h.prompter.asked, "Remove them and save the profile? (y/N): ") {
		t.Fatal("the drop offer was asked when nothing passed")
	}
	if len(h.saved) != 0 {
		t.Fatal("an all-failed run saved a profile")
	}
	if h.errOut.Len() != 0 {
		t.Fatalf("the body's line was printed again: %s", h.errOut.String())
	}
	if got := h.choices(); got != 2 {
		t.Fatalf("the loop asked %d times, want 2", got)
	}
}

func TestMenuCheckFailedSaveIsReported(t *testing.T) {
	isolatedConnectConfig(t)
	profiles := oneProfile("home")
	profiles.Profile["home"].Resolvers = []string{"network", "udp:192.0.2.53:53", "doh-cloudflare"}
	writeMenuProfiles(t, profiles)

	h := newMenuHarness(t, []scriptedAnswer{{value: "2"}, {value: "y"}, {value: "0"}})
	h.checkOutcomes = mixedCheckOutcomes()
	h.saveErr = errors.New("synthetic save failure")

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if !strings.Contains(h.errOut.String(), "synthetic save failure") {
		t.Fatalf("the failed save was not reported: %s", h.errOut.String())
	}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if got := profiles.Profile["home"].Resolvers; !slices.Equal(got, []string{"network", "udp:192.0.2.53:53", "doh-cloudflare"}) {
		t.Fatalf("a failed save changed the file: %v", got)
	}
}

// The entry of a check is the profile entry, not the printed name: the network expansion
// is several checks of one entry, and the bootstrap error is no entry at all.
func TestResolverChecksCarryTheProfileEntry(t *testing.T) {
	list, err := resolvers.Expand([]string{"network", "doh-cloudflare", "udp:192.0.2.53"})
	if err != nil {
		t.Fatal(err)
	}
	checks := resolverChecks(list, nil, func() ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("198.51.100.53")}, nil
	})
	if len(checks) != 3 {
		t.Fatalf("checks = %d, want 3", len(checks))
	}
	for i, want := range []string{resolvers.NetworkEntry, "doh-cloudflare", "udp:192.0.2.53:53"} {
		if checks[i].entry != want {
			t.Errorf("check %d entry = %q, want %q", i, checks[i].entry, want)
		}
	}
	if checks[0].name != "network (198.51.100.53)" {
		t.Errorf("the network check's printed name = %q", checks[0].name)
	}

	// A bootstrap parse error is not a resolver of the profile.
	bad := resolverChecks(list, []string{"not-an-address"}, func() ([]netip.Addr, error) {
		t.Fatal("network DNS was read after a bootstrap parse error")
		return nil, nil
	})
	if len(bad) != 1 || bad[0].entry != "" || bad[0].name != "bootstrap" {
		t.Fatalf("bootstrap check = %+v, want one check without an entry", bad)
	}
}

// --- a new exit password ---

func TestMenuNewExitPasswordStoresAndUpdates(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{
		{value: "4"},
		{secret: true, value: wizardTestPassword},
		{secret: true, value: wizardTestPassword},
		{value: "0"},
	})
	h.menu.save = saveProfiles

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if got := profiles.Profile["home"].Exit.PasswordRef; got != "file:home.password" {
		t.Fatalf("password_ref = %q, want the new reference", got)
	}
	if got := h.store.values["home.password"]; got != wizardTestPassword {
		t.Fatalf("stored password = %q", got)
	}
	if !strings.Contains(h.out.String(), `The exit password of profile "home" was updated.`) {
		t.Fatalf("the update was not reported:\n%s", h.out.String())
	}
	raw := readConfigFile(t)
	if strings.Contains(raw, wizardTestPassword) {
		t.Fatal("the password reached profiles.toml")
	}
	for _, out := range []string{h.out.String(), h.errOut.String()} {
		if strings.Contains(out, wizardTestPassword) {
			t.Fatalf("the password reached the output: %s", out)
		}
	}
	for _, question := range h.prompter.asked {
		if strings.Contains(question, wizardTestPassword) {
			t.Fatal("the password reached a question")
		}
	}
}

func TestMenuNewExitPasswordReplacesTheOldEntryAfterTheSave(t *testing.T) {
	isolatedConnectConfig(t)
	profiles := oneProfile("home")
	profiles.Profile["home"].Exit.PasswordRef = "file:home.password"
	writeMenuProfiles(t, profiles)

	h := newMenuHarness(t, []scriptedAnswer{
		{value: "4"},
		{secret: true, value: wizardTestPassword},
		{secret: true, value: wizardTestPassword},
		{value: "0"},
	})
	oldRef := seedFakeSecret(h.store, "home.password", "old-password")
	h.menu.save = func(p *config.Profiles) error {
		if len(h.store.deleted) != 0 {
			t.Error("the old entry was deleted before the save")
		}
		return saveProfiles(p)
	}

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	newRef := profiles.Profile["home"].Exit.PasswordRef
	if newRef == oldRef {
		t.Fatal("the profile still points at the old reference")
	}
	if !strings.HasPrefix(newRef, "file:home.password-") {
		t.Fatalf("the new reference = %q, want a fresh name", newRef)
	}
	if len(h.store.deleted) != 1 || h.store.deleted[0] != oldRef {
		t.Fatalf("deleted = %v, want the old reference once", h.store.deleted)
	}
	if _, ok := h.store.values["home.password"]; ok {
		t.Fatal("the old value stayed in the store")
	}
}

func TestMenuNewExitPasswordFailedSaveRollsBack(t *testing.T) {
	isolatedConnectConfig(t)
	profiles := oneProfile("home")
	profiles.Profile["home"].Exit.PasswordRef = "file:home.password"
	writeMenuProfiles(t, profiles)

	h := newMenuHarness(t, []scriptedAnswer{
		{value: "4"},
		{secret: true, value: wizardTestPassword},
		{secret: true, value: wizardTestPassword},
		{value: "0"},
	})
	seedFakeSecret(h.store, "home.password", "old-password")
	h.saveErr = errors.New("synthetic save failure")

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if got := profiles.Profile["home"].Exit.PasswordRef; got != "file:home.password" {
		t.Fatalf("password_ref = %q, want the old reference", got)
	}
	if got := h.store.values["home.password"]; got != "old-password" {
		t.Fatalf("the old value = %q, want it kept", got)
	}
	if len(h.store.deleted) != 1 || !strings.HasPrefix(h.store.deleted[0], "file:home.password-") {
		t.Fatalf("deleted = %v, want the new entry", h.store.deleted)
	}
	if len(h.store.values) != 1 {
		t.Fatalf("the store has %d values, want only the old one", len(h.store.values))
	}
	errOut := h.errOut.String()
	if !strings.Contains(errOut, "synthetic save failure") ||
		!strings.Contains(errOut, "the exit password was removed from the secret store") {
		t.Fatalf("the rollback was not reported: %s", errOut)
	}
	if strings.Contains(errOut, wizardTestPassword) {
		t.Fatal("the password reached the error")
	}
}

func TestMenuNewExitPasswordFailedDeleteSucceeds(t *testing.T) {
	isolatedConnectConfig(t)
	profiles := oneProfile("home")
	profiles.Profile["home"].Exit.PasswordRef = "file:home.password"
	writeMenuProfiles(t, profiles)

	h := newMenuHarness(t, []scriptedAnswer{
		{value: "4"},
		{secret: true, value: wizardTestPassword},
		{secret: true, value: wizardTestPassword},
		{value: "0"},
	})
	seedFakeSecret(h.store, "home.password", "old-password")
	h.menu.save = saveProfiles
	h.store.deleteErr = errors.New("synthetic delete failure")

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if got := profiles.Profile["home"].Exit.PasswordRef; !strings.HasPrefix(got, "file:home.password-") {
		t.Fatalf("password_ref = %q, want the new reference", got)
	}
	if !strings.Contains(h.errOut.String(), "the previous exit password could not be removed from the secret store: synthetic delete failure") {
		t.Fatalf("the failed delete was not reported: %s", h.errOut.String())
	}
	if got := h.choices(); got != 2 {
		t.Fatalf("the loop asked %d times, want 2", got)
	}
}

func TestMenuNewExitPasswordReasks(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{
		{value: "4"},
		{secret: true, value: ""},                 // empty
		{secret: true, value: "bad\x01password"},  // control character
		{secret: true, value: "first-password"},   // mismatch
		{secret: true, value: "second-password"},  // mismatch (again)
		{secret: true, value: wizardTestPassword}, // accepted
		{secret: true, value: wizardTestPassword}, // accepted again
		{value: "0"},
	})
	h.menu.save = saveProfiles

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	out := h.out.String()
	for _, want := range []string{
		"the exit password must not be empty",
		"the exit password must not contain control characters",
		"the passwords do not match",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q was not printed:\n%s", want, out)
		}
	}
	if got := h.store.values["home.password"]; got != wizardTestPassword {
		t.Fatalf("stored password = %q", got)
	}
}

// An absent exit type is the connect's socks5 default: the 255-byte cap applies, the
// question is asked again, and the refused value reaches neither the store nor the file.
func TestMenuNewExitPasswordEmptyTypeIsSocks5(t *testing.T) {
	isolatedConnectConfig(t)
	profiles := oneProfile("home")
	profiles.Profile["home"].Exit.Type = ""
	writeMenuProfiles(t, profiles)

	h := newMenuHarness(t, []scriptedAnswer{
		{value: "4"},
		{secret: true, value: strings.Repeat("p", 256)},
	})
	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the end of input = %v, want nil", err)
	}
	if !strings.Contains(h.out.String(), "a SOCKS5 password must be at most 255 bytes") {
		t.Fatalf("the SOCKS5 cap was not applied to an absent exit type:\n%s", h.out.String())
	}
	asked := 0
	for _, question := range h.prompter.asked {
		if question == "Exit password: " {
			asked++
		}
	}
	if asked != 2 {
		t.Fatalf("the password question was asked %d times, want 2: %q", asked, h.prompter.asked)
	}
	if slices.Contains(h.prompter.asked, "Exit password again: ") {
		t.Fatalf("a too-long password reached the confirmation: %q", h.prompter.asked)
	}
	if h.store.puts != 0 || len(h.store.values) != 0 || len(h.store.refs) != 0 {
		t.Fatalf("the refused password reached the store: puts %d, values %d", h.store.puts, len(h.store.values))
	}
	if len(h.saved) != 0 {
		t.Fatal("the refused password saved a profile")
	}
	if raw := readConfigFile(t); strings.Contains(raw, strings.Repeat("p", 16)) {
		t.Fatal("the refused password reached profiles.toml")
	}
}

// A profile without an exit login is refused before any question: the credential would be
// unused. An ssh profile cannot be in that state — internal/config refuses one without a
// login — so a SOCKS5 profile is what reaches the check on disk.
func TestMenuNewExitPasswordNoLogin(t *testing.T) {
	isolatedConnectConfig(t)
	profiles := oneProfile("home")
	profiles.Profile["home"].Exit.User = ""
	writeMenuProfiles(t, profiles)

	h := newMenuHarness(t, []scriptedAnswer{{value: "4"}, {value: "0"}})
	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if !strings.Contains(h.out.String(), `profile "home" has no exit login, so the exit secret is not used`) {
		t.Fatalf("the refusal was not printed:\n%s", h.out.String())
	}
	if h.store.puts != 0 || len(h.saved) != 0 {
		t.Fatal("the refusal wrote something")
	}
	for _, question := range h.prompter.asked {
		if strings.HasPrefix(question, "Exit password") {
			t.Fatalf("the refusal asked for a password: %q", h.prompter.asked)
		}
	}
}

func TestMenuNewExitPasswordPutFailure(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{
		{value: "4"},
		{secret: true, value: wizardTestPassword},
		{secret: true, value: wizardTestPassword},
		{value: "0"},
	})
	h.store.putErr = errors.New("synthetic store failure")

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if !strings.Contains(h.errOut.String(), "cannot store the exit password: synthetic store failure") {
		t.Fatalf("the store failure was not reported: %s", h.errOut.String())
	}
	if len(h.saved) != 0 {
		t.Fatal("the profile was saved after the store failed")
	}
}

func TestMenuNewExitPasswordEOF(t *testing.T) {
	for _, tc := range []struct {
		name    string
		answers []scriptedAnswer
	}{
		{"at the first question", []scriptedAnswer{{value: "4"}}},
		{"at the second question", []scriptedAnswer{
			{value: "4"},
			{secret: true, value: wizardTestPassword},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolatedConnectConfig(t)
			writeMenuProfiles(t, oneProfile("home"))
			h := newMenuHarness(t, tc.answers)
			if err := h.runLoop(context.Background()); err != nil {
				t.Fatalf("the end of input = %v, want nil", err)
			}
			if h.store.puts != 0 || len(h.saved) != 0 {
				t.Fatal("the end of input wrote something")
			}
		})
	}
}

// --- the exit secret of a key profile ---

func TestMenuNewExitSecretKeyStoresAndUpdates(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneSSHProfile("home", "file:home.key"))
	keys := writeTestKeys(t)

	h := newMenuHarness(t, []scriptedAnswer{
		{value: "4"},
		{value: keys.encryptedPath},
		{secret: true, value: keys.passphrase},
		{value: "0"},
	})
	oldRef := seedFakeSecret(h.store, "home.key", "old-key-material")
	h.menu.save = func(p *config.Profiles) error {
		if len(h.store.deleted) != 0 {
			t.Error("the old key was deleted before the save")
		}
		return saveProfiles(p)
	}

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatalf("the saved profile does not load: %v", err)
	}
	prof := profiles.Profile["home"]
	newRef := prof.Exit.KeyRef
	if newRef == oldRef || !strings.HasPrefix(newRef, "file:home.key-") {
		t.Fatalf("key_ref = %q, want a fresh home.key name", newRef)
	}
	if prof.Exit.PasswordRef != "" {
		t.Fatalf("a key profile gained password_ref %q", prof.Exit.PasswordRef)
	}
	if len(h.store.deleted) != 1 || h.store.deleted[0] != oldRef {
		t.Fatalf("deleted = %v, want the old reference once", h.store.deleted)
	}
	if _, ok := h.store.values["home.key"]; ok {
		t.Fatal("the old key stayed in the store")
	}

	var stored string
	for name, value := range h.store.values {
		if strings.HasPrefix(name, "home.key-") {
			stored = value
		}
	}
	if stored == "" {
		t.Fatal("the new key is not in the store under home.key-<random>")
	}
	if _, err := exit.ParseKey([]byte(stored), ""); err != nil {
		t.Fatalf("the stored key does not parse without a passphrase: %v", err)
	}
	if !strings.Contains(h.out.String(), `The exit key of profile "home" was updated.`) {
		t.Fatalf("the update was not reported:\n%s", h.out.String())
	}

	marker := keyMarker(stored)
	checked := []string{h.out.String(), h.errOut.String(), strings.Join(h.prompter.asked, "\n"), readConfigFile(t)}
	for _, text := range checked {
		if strings.Contains(text, keys.passphrase) {
			t.Fatal("the passphrase reached the output, a question or the file")
		}
		if strings.Contains(text, marker) {
			t.Fatal("the key reached the output, a question or the file")
		}
	}
}

func TestMenuNewExitSecretKeyFailedSaveRollsBack(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneSSHProfile("home", "file:home.key"))
	keys := writeTestKeys(t)

	h := newMenuHarness(t, []scriptedAnswer{
		{value: "4"},
		{value: keys.encryptedPath},
		{secret: true, value: keys.passphrase},
		{value: "0"},
	})
	oldRef := seedFakeSecret(h.store, "home.key", "old-key-material")
	h.saveErr = errors.New("synthetic save failure")

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if got := profiles.Profile["home"].Exit.KeyRef; got != oldRef {
		t.Fatalf("key_ref = %q, want the old reference", got)
	}
	if got := h.store.values["home.key"]; got != "old-key-material" {
		t.Fatalf("the old value = %q, want it kept", got)
	}
	if len(h.store.deleted) != 1 || !strings.HasPrefix(h.store.deleted[0], "file:home.key-") {
		t.Fatalf("deleted = %v, want the new key", h.store.deleted)
	}
	if len(h.store.values) != 1 {
		t.Fatalf("the store has %d values, want only the old one", len(h.store.values))
	}
	errOut := h.errOut.String()
	if !strings.Contains(errOut, "synthetic save failure") ||
		!strings.Contains(errOut, "the exit key was removed from the secret store") {
		t.Fatalf("the rollback was not reported: %s", errOut)
	}
	if strings.Contains(errOut, string(keys.plain)) {
		t.Fatal("the key reached the error")
	}
}

func TestMenuNewExitSecretKeyFailedDeleteSucceeds(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneSSHProfile("home", "file:home.key"))
	keys := writeTestKeys(t)

	h := newMenuHarness(t, []scriptedAnswer{
		{value: "4"},
		{value: keys.plainPath},
		{value: "0"},
	})
	seedFakeSecret(h.store, "home.key", "old-key-material")
	h.menu.save = saveProfiles
	h.store.deleteErr = errors.New("synthetic delete failure")

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if got := profiles.Profile["home"].Exit.KeyRef; !strings.HasPrefix(got, "file:home.key-") {
		t.Fatalf("key_ref = %q, want the new reference", got)
	}
	if !strings.Contains(h.errOut.String(), "the previous exit key could not be removed from the secret store: synthetic delete failure") {
		t.Fatalf("the failed delete was not reported: %s", h.errOut.String())
	}
	if got := h.choices(); got != 2 {
		t.Fatalf("the loop asked %d times, want 2", got)
	}
}

// --- the pinned fingerprint ---

// countSaves replaces the menu's save with the real one and counts its calls: the item must
// write the file exactly once, and a wrapped save proves it without hiding the file.
func countSaves(h *menuHarness) *int {
	saves := 0
	h.menu.save = func(p *config.Profiles) error {
		saves++
		return saveProfiles(p)
	}
	return &saves
}

func TestMenuChangePinnedFingerprint(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneSSHProfile("home", "file:home.key"))
	keys := writeTestKeys(t)

	h := newMenuHarness(t, []scriptedAnswer{
		{value: "5"},
		{value: strings.TrimPrefix(keys.fingerprint, "SHA256:")}, // a bare body is normalized
		{value: "0"},
	})
	saves := countSaves(h)

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if *saves != 1 {
		t.Fatalf("saves = %d, want 1", *saves)
	}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatalf("the saved profile does not load: %v", err)
	}
	if got := profiles.Profile["home"].Exit.HostKey; got != keys.fingerprint {
		t.Fatalf("host_key = %q, want %q", got, keys.fingerprint)
	}
	if got := strings.Join(h.prompter.asked, "\n"); !strings.Contains(got, " ["+cliTestHostKey+"]: ") {
		t.Fatalf("the current fingerprint is not the default answer:\n%s", got)
	}
	if !strings.Contains(h.out.String(), `Pinned host key fingerprint of profile "home" updated.`) {
		t.Fatalf("the update was not reported:\n%s", h.out.String())
	}
}

func TestMenuChangePinnedFingerprintUnchanged(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneSSHProfile("home", "file:home.key"))

	h := newMenuHarness(t, []scriptedAnswer{{value: "5"}, {value: ""}, {value: "0"}})
	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if len(h.saved) != 0 {
		t.Fatal("an unchanged fingerprint saved a profile")
	}
	if !strings.Contains(h.out.String(), "nothing changed") {
		t.Fatalf("the empty answer did not say nothing changed:\n%s", h.out.String())
	}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if got := profiles.Profile["home"].Exit.HostKey; got != cliTestHostKey {
		t.Fatalf("host_key = %q, want it kept", got)
	}
}

func TestMenuChangePinnedFingerprintReasks(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneSSHProfile("home", "file:home.key"))
	keys := writeTestKeys(t)

	h := newMenuHarness(t, []scriptedAnswer{
		{value: "5"},
		{value: "MD5:aa:bb:cc:dd"}, // refused
		{value: keys.fingerprint},
		{value: "0"},
	})
	saves := countSaves(h)

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if *saves != 1 {
		t.Fatalf("saves = %d, want 1", *saves)
	}
	if !strings.Contains(h.out.String(), "must be SHA256") {
		t.Fatalf("the invalid value was not explained:\n%s", h.out.String())
	}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if got := profiles.Profile["home"].Exit.HostKey; got != keys.fingerprint {
		t.Fatalf("host_key = %q, want %q", got, keys.fingerprint)
	}
}

func TestMenuChangePinnedFingerprintNotSSH(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{{value: "5"}, {value: "0"}})
	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if !strings.Contains(h.out.String(), `profile "home" has no pinned host key: exit.type is not ssh`) {
		t.Fatalf("the refusal was not printed:\n%s", h.out.String())
	}
	if len(h.saved) != 0 {
		t.Fatal("a profile that is not ssh saved something")
	}
	for _, question := range h.prompter.asked {
		if strings.Contains(question, "Pinned host key fingerprint") {
			t.Fatalf("the refusal asked for a fingerprint: %q", h.prompter.asked)
		}
	}
}

func TestMenuChangePinnedFingerprintEOF(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneSSHProfile("home", "file:home.key"))

	h := newMenuHarness(t, []scriptedAnswer{{value: "5"}})
	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the end of input = %v, want nil", err)
	}
	if len(h.saved) != 0 {
		t.Fatal("the end of input saved a profile")
	}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if got := profiles.Profile["home"].Exit.HostKey; got != cliTestHostKey {
		t.Fatalf("host_key = %q, want it kept", got)
	}
}

func TestMenuChangePinnedFingerprintFailedSave(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneSSHProfile("home", "file:home.key"))
	keys := writeTestKeys(t)

	h := newMenuHarness(t, []scriptedAnswer{{value: "5"}, {value: keys.fingerprint}, {value: "0"}})
	h.saveErr = errors.New("synthetic save failure")

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if !strings.Contains(h.errOut.String(), "synthetic save failure") {
		t.Fatalf("the failed save was not reported: %s", h.errOut.String())
	}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if got := profiles.Profile["home"].Exit.HostKey; got != cliTestHostKey {
		t.Fatalf("host_key = %q, want the old one", got)
	}
}

// --- a new domain or key ---

func maskedDomainQuestion(domain string) string {
	return "Domain [" + maskSensitive(domain) + "]: "
}

func maskedPubkeyQuestion(key string) string {
	return "Server public key [" + maskSensitive(key) + "]: "
}

func TestMenuChangeServerSavesBoth(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))
	newKey := strings.Repeat("ab", 32)

	h := newMenuHarness(t, []scriptedAnswer{
		{value: "6"},
		{value: "new.example.com"},
		{value: newKey},
		{value: "0"},
	})
	h.menu.save = saveProfiles

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if got := profiles.Profile["home"].Domain; got != "new.example.com" {
		t.Fatalf("domain = %q", got)
	}
	if got := profiles.Profile["home"].Pubkey; got != newKey {
		t.Fatalf("pubkey = %q", got)
	}
	if !slices.Contains(h.prompter.asked, maskedDomainQuestion("t.example.com")) {
		t.Fatalf("the domain question does not carry the masked default; asked %q", h.prompter.asked)
	}
	if !slices.Contains(h.prompter.asked, maskedPubkeyQuestion(wizardTestPubkey)) {
		t.Fatalf("the key question does not carry the masked default; asked %q", h.prompter.asked)
	}
	wantUpdated := `Profile "home" updated: domain ` + maskSensitive("new.example.com") +
		`, key ` + maskSensitive(newKey) + ".\n"
	if !strings.Contains(h.out.String(), wantUpdated) {
		t.Fatalf("the update was not reported masked; want %q, got:\n%s", wantUpdated, h.out.String())
	}
}

func TestMenuChangeServerEmptyPairKeepsBoth(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{{value: "6"}, {value: ""}, {value: ""}, {value: "0"}})
	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if len(h.saved) != 0 {
		t.Fatal("an unchanged pair saved a profile")
	}
	if !strings.Contains(h.out.String(), "nothing changed") {
		t.Fatalf("the empty answer did not say nothing changed:\n%s", h.out.String())
	}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if got := profiles.Profile["home"].Domain; got != "t.example.com" {
		t.Fatalf("domain = %q, want it kept", got)
	}
	if got := profiles.Profile["home"].Pubkey; got != wizardTestPubkey {
		t.Fatalf("pubkey = %q, want it kept", got)
	}
}

func TestMenuChangeServerInvalidAnswersReask(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))
	newKey := strings.Repeat("cd", 32)

	h := newMenuHarness(t, []scriptedAnswer{
		{value: "6"},
		{value: "bad_domain"}, // invalid
		{value: "new.example.com"},
		{value: "0123"}, // short key
		{value: newKey},
		{value: "0"},
	})
	h.menu.save = saveProfiles

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	out := h.out.String()
	if !strings.Contains(out, "not a valid domain") {
		t.Fatalf("the invalid domain was not explained:\n%s", out)
	}
	if !strings.Contains(out, "the server public key must be exactly 64 hex digits") {
		t.Fatalf("the invalid key was not explained:\n%s", out)
	}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if got := profiles.Profile["home"].Domain; got != "new.example.com" {
		t.Fatalf("domain = %q", got)
	}
	if got := profiles.Profile["home"].Pubkey; got != newKey {
		t.Fatalf("pubkey = %q", got)
	}
}

func TestMenuChangeServerFailedSaveIsReported(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{
		{value: "6"},
		{value: "new.example.com"},
		{value: ""},
		{value: "0"},
	})
	h.saveErr = errors.New("synthetic save failure")

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if !strings.Contains(h.errOut.String(), "synthetic save failure") {
		t.Fatalf("the failed save was not reported: %s", h.errOut.String())
	}
	profiles, err := config.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if got := profiles.Profile["home"].Domain; got != "t.example.com" {
		t.Fatalf("domain = %q, want the old one", got)
	}
}

func TestMenuChangeServerEOF(t *testing.T) {
	for _, tc := range []struct {
		name    string
		answers []scriptedAnswer
	}{
		{"at the domain question", []scriptedAnswer{{value: "6"}}},
		{"at the key question", []scriptedAnswer{{value: "6"}, {value: "new.example.com"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolatedConnectConfig(t)
			writeMenuProfiles(t, oneProfile("home"))
			h := newMenuHarness(t, tc.answers)
			if err := h.runLoop(context.Background()); err != nil {
				t.Fatalf("the end of input = %v, want nil", err)
			}
			if len(h.saved) != 0 {
				t.Fatal("the end of input saved a profile")
			}
			profiles, err := config.LoadProfiles()
			if err != nil {
				t.Fatal(err)
			}
			if got := profiles.Profile["home"].Domain; got != "t.example.com" {
				t.Fatalf("domain = %q, want the old one", got)
			}
		})
	}
}
