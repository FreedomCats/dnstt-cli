package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/FreedomCats/dnstt-cli/internal/app"
	"github.com/FreedomCats/dnstt-cli/internal/config"
)

func menuCommand() *command {
	return &command{
		name:    "menu",
		args:    "[-connect profile]",
		summary: "interactive menu",
		description: `Runs the menu: the commands chosen by number, over the same code as the commands.

Without arguments in a terminal, dnstt-cli opens this menu; without a terminal it prints
the command list instead. -connect runs one connection at once and returns, without the
menu.`,
		run: runMenu,
	}
}

// menuItem is one numbered line of the menu. run is the command body behind it: the menu
// has no logic of its own. Only the connect item uses the abort channel.
type menuItem struct {
	label string
	run   func(ctx context.Context, e *env, abort <-chan struct{}) error
}

// menu is the menu's seams: every item calls the function of the command behind it, the way
// the wizard holds its save. ask is the one prompter of the run, shared by the loop, the
// wizard and the link question.
type menu struct {
	connect func(ctx context.Context, e *env, req connectRequest, abort <-chan struct{}) error
	// check checks the resolvers of one profile: the menu only ever checks the current
	// profile, and it needs the outcome per entry to offer to drop the dead ones.
	check  func(ctx context.Context, e *env, name string) ([]resolverOutcome, error)
	remove func(ctx context.Context, e *env, args []string) error
	list   func(ctx context.Context, e *env, args []string) error
	save   func(*config.Profiles) error
	store  func() (config.SecretStore, error)

	// elevation is what the elevation decision reads: the platform, the privileges and
	// sudo. A test replaces it to ask about any system without root or a real sudo.
	elevation func() elevationEnv
	// elevate runs one connection as the same binary under sudo and returns its exit
	// status. runWith binds it to runElevated with the session it has just made; a test
	// supplies its own.
	elevate func(ctx context.Context, e *env, name string) (int, error)

	ask prompter
}

// newMenu builds the menu over the command bodies themselves.
func newMenu() *menu {
	return &menu{
		connect:   runConnection,
		check:     checkProfileResolvers,
		remove:    runProfileRm,
		list:      runProfileList,
		save:      saveProfiles,
		store:     newSecretStore,
		elevation: systemElevation,
	}
}

// runMenu parses the menu's flags and runs the session on the real signals. With -connect
// the name is required and the run is one connection; otherwise the loop runs. The
// prompter, the store and the session are created here and torn down on return.
func runMenu(ctx context.Context, e *env, args []string) error {
	fs := newFlagSet(menuCommand())
	connectName := fs.String("connect", "", "connect to the profile at once and return")
	if err := parse(e, fs, args); err != nil {
		return err
	}
	connectSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "connect" {
			connectSet = true
		}
	})
	if connectSet && *connectName == "" {
		return usagef("-connect requires a profile name")
	}
	if fs.NArg() > 0 {
		return usagef("extra argument: menu takes no arguments")
	}

	m := newMenu()
	// One prompter for the whole run: the loop, the wizard and the link question share it,
	// so internal/lines stays the one reader of the process.
	m.ask = newLinePrompter(e.stdin, e.stdout)

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigCh)

	name := ""
	if connectSet {
		name = *connectName
	}
	err := m.runWith(ctx, e, name, sigCh)
	if err != nil {
		// A window opened by a double click does not close on an error: Enter does.
		m.pauseBeforeExit(ctx)
	}
	return err
}

// runWith runs the menu session on the given signal channel: one connection when name is
// not empty, the loop otherwise. runMenu wires the process signals into it; tests inject
// their own channel.
func (m *menu) runWith(ctx context.Context, e *env, name string, sigCh <-chan os.Signal) error {
	store, err := m.store()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sess := newMenuSession(e.stderr, cancel)
	if m.elevate == nil {
		// The menu's own session is what leaves the terminal's signals to the child while
		// the elevated run is on.
		m.elevate = func(ctx context.Context, e *env, name string) (int, error) {
			return runElevated(ctx, e, name, sess)
		}
	}

	done := make(chan struct{})
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		sess.watch(sigCh, done)
	}()
	defer func() {
		close(done)
		<-watched
	}()

	if name != "" {
		return m.connectOnce(ctx, e, name, sess)
	}
	return m.loop(ctx, e, store, sess)
}

// connectOnce runs one connection of the named profile through the session, without the
// loop: this is `menu -connect`.
func (m *menu) connectOnce(ctx context.Context, e *env, name string, sess *menuSession) error {
	itemCtx, itemCancel := context.WithCancel(ctx)
	defer itemCancel()
	abort := sess.running(itemCancel)

	err := m.connect(itemCtx, e, connectRequest{profileName: name}, abort)
	itemCancel()
	sess.idle()
	return err
}

// loop is the menu itself. The profiles are loaded at the start of each iteration: profile
// rm, "another profile" and the wizard all change the file behind it.
func (m *menu) loop(ctx context.Context, e *env, store config.SecretStore, sess *menuSession) error {
	for {
		if ctx.Err() != nil {
			return nil
		}

		profiles, err := config.LoadProfiles()
		if err != nil {
			return err
		}

		if len(profiles.Profile) == 0 {
			// No profiles at all: the first questions, and the menu again after them.
			if err := m.firstProfile(ctx, e, profiles, store); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, errCancelled) {
					return nil
				}
				return err
			}
			continue
		}

		name := currentProfile(profiles)
		if name == "" {
			// Profiles exist but no default is set — the state profile rm leaves when the
			// default was removed and more than one profile remains. Ask for the profile to
			// work with, and the loop shows the menu with it.
			if err := m.anotherProfileItem(ctx, e, nil); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, errCancelled) {
					return nil
				}
				reportItemError(e, err)
				continue
			}
			// An empty answer leaves the default unset: the menu cannot go on without a
			// profile, so it closes.
			profiles, err = config.LoadProfiles()
			if err != nil {
				return err
			}
			if currentProfile(profiles) == "" {
				return nil
			}
			continue
		}

		items := m.menuItems()
		printMenu(e, profiles, name, items)

		choice, err := askChoice(ctx, m.ask, e, len(items))
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				// The menu closes without writing anything.
				return nil
			}
			return err
		}
		if choice == 0 {
			return nil
		}

		itemCtx, itemCancel := context.WithCancel(ctx)
		abort := sess.running(itemCancel)
		err = items[choice-1].run(itemCtx, e, abort)
		// The item stays registered until it has returned: a press in between belongs to
		// the connection, not to the menu.
		itemCancel()
		sess.idle()

		if errors.Is(err, errCancelled) {
			// The end of input at a question of the item closes the menu.
			return nil
		}
		reportItemError(e, err)
	}
}

// menuItems returns the items in the order of architecture.md §10a. The number is the
// position in this table, 1-based, and 0 is exit: nothing else in the dispatch changes when
// an item is added.
func (m *menu) menuItems() []menuItem {
	return []menuItem{
		{label: "connect", run: m.connectItem},
		{label: "check resolvers", run: m.checkResolversItem},
		{label: "edit resolvers", run: m.editResolversItem},
		{label: "new exit password or key", run: m.newExitSecretItem},
		{label: "change pinned fingerprint", run: m.changePinnedFingerprintItem},
		{label: "change domain or key", run: m.changeServerItem},
		{label: "another profile", run: m.anotherProfileItem},
		{label: "delete profile", run: m.deleteProfileItem},
	}
}

// printMenu prints the current profile and the numbered items.
func printMenu(e *env, profiles *config.Profiles, name string, items []menuItem) {
	// An absent datapath is socks, the product default.
	kind := "socks"
	if prof := profiles.Profile[name]; prof != nil && prof.Datapath != "" {
		kind = prof.Datapath
	}
	fmt.Fprintf(e.stdout, "Profile %q (%s).\n", name, kind)
	for i, item := range items {
		fmt.Fprintf(e.stdout, "  %d  %s\n", i+1, item.label)
	}
	fmt.Fprintln(e.stdout, "  0  exit")
}

// askChoice reads the number of an item. An empty answer is the first item, 0 is exit, and
// anything else outside the table prints the range and asks again.
func askChoice(ctx context.Context, ask prompter, e *env, count int) (int, error) {
	for {
		answer, err := ask.Ask(ctx, "Choice [1]: ")
		if err != nil {
			return 0, err
		}
		answer = strings.TrimSpace(answer)
		if answer == "" {
			return 1, nil
		}
		n, err := strconv.Atoi(answer)
		if err != nil || n < 0 || n > count {
			fmt.Fprintf(e.stdout, "the choice must be a number from 0 to %d\n", count)
			continue
		}
		return n, nil
	}
}

// currentProfile is the profile the menu works with: the default one, or the only one when
// no default is set — the state profile rm leaves after removing the default of two.
func currentProfile(profiles *config.Profiles) string {
	if profiles.DefaultProfile != "" {
		return profiles.DefaultProfile
	}
	if len(profiles.Profile) == 1 {
		for name := range profiles.Profile {
			return name
		}
	}
	return ""
}

// reportItemError prints what an item returned. A nil error says nothing; an item stopped by
// the person was already answered by the line of the stop; an exitCodeError has printed its
// own line before returning.
func reportItemError(e *env, err error) {
	switch {
	case err == nil:
		return
	case errors.Is(err, context.Canceled):
		return
	}
	var ec *exitCodeError
	if errors.As(err, &ec) {
		return
	}
	fmt.Fprintf(e.stderr, "%s: %v\n", app.Name, err)
}

// --- the items ---

// connectItem shows the current profile's resolvers and offers to change them first, then
// connects: the offer runs before the body because the body loads the profile from the
// file, so an edited list is used by this connect exactly because it was saved first. A
// full round of failed handshakes after the session is up offers the same check and edit
// again, and the connection is stopped before the question.
func (m *menu) connectItem(ctx context.Context, e *env, abort <-chan struct{}) error {
	profiles, err := config.LoadProfiles()
	if err != nil {
		return err
	}
	name := currentProfile(profiles)
	connect, err := m.offerResolvers(ctx, e, profiles, name)
	if err != nil || !connect {
		return err
	}
	return m.connectWithResolvers(ctx, e, name, abort)
}

// anotherProfileItem lists the profiles and makes the chosen one the default. An empty
// answer and 0 cancel — 0 leaves the menu everywhere else — unless a profile is literally
// named "0"; an unknown name asks again. A failed save leaves the old default, and the next
// iteration reloads the file.
func (m *menu) anotherProfileItem(ctx context.Context, e *env, _ <-chan struct{}) error {
	if err := m.list(ctx, e, nil); err != nil {
		return err
	}

	profiles, err := config.LoadProfiles()
	if err != nil {
		return err
	}
	for {
		answer, err := m.ask.Ask(ctx, "Profile: ")
		if err != nil {
			return readError(ctx, err)
		}
		name := strings.TrimSpace(answer)
		if name == "" || (name == "0" && profiles.Profile[name] == nil) {
			return nil
		}
		if profiles.Profile[name] == nil {
			fmt.Fprintf(e.stdout, "profile %q not found\n", name)
			continue
		}
		profiles.DefaultProfile = name
		return m.save(profiles)
	}
}

// deleteProfileItem asks before anything else, then removes the current profile and its
// exit password or key through the body of `profile rm`: the secret goes first, and the last
// profile removes profiles.toml.
func (m *menu) deleteProfileItem(ctx context.Context, e *env, _ <-chan struct{}) error {
	profiles, err := config.LoadProfiles()
	if err != nil {
		return err
	}
	name := currentProfile(profiles)
	if name == "" {
		return nil
	}

	word := "exit password"
	if prof := profiles.Profile[name]; prof != nil && prof.Exit.KeyRef != "" {
		word = "exit key"
	}
	answer, err := m.ask.Ask(ctx, fmt.Sprintf("Delete profile %q and its %s? (y/N): ", name, word))
	if err != nil {
		return readError(ctx, err)
	}
	if !isYes(answer) {
		return nil
	}
	return m.remove(ctx, e, []string{name})
}

// isYes reports whether a confirmation was answered yes.
func isYes(answer string) bool {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true
	}
	return false
}

// firstProfile asks for the first profile: the hidden link question comes first, an empty
// answer starts the wizard's questions instead. An imported profile or a built one returns
// with the profile written; the end of input cancels and writes nothing.
func (m *menu) firstProfile(ctx context.Context, e *env, profiles *config.Profiles, store config.SecretStore) error {
	imported, err := importOne(ctx, e, m.ask, profiles, store, "slipnet:// link (Enter to answer the questions): ")
	if err == nil && imported {
		return nil
	}
	if err == nil {
		// An empty answer: the questions.
		name, prof, addErr := newWizard(e.stdout, m.ask, store).add(ctx, profiles, "")
		if addErr == nil {
			fmt.Fprintf(e.stdout, "Profile %q added (exit %s, mode %s).\n", name, prof.Exit.Type, prof.Datapath)
			return nil
		}
		err = addErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, errCancelled) {
		// The person stopped answering: nothing more is written.
		return err
	}
	// A failed try: the menu says why and asks for the first profile again.
	reportItemError(e, err)
	return nil
}
