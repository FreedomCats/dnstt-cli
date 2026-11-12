package cli

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/FreedomCats/dnstt-cli/internal/supervisor"
)

// stoppedPrompt checks that the connection is already stopped when the round's
// question arrives. There is no terminal in the tests, so no status line would be
// lost; this check is the only way to notice the order.
type stoppedPrompt struct {
	*scriptedPrompter
	h *menuHarness
}

func (p *stoppedPrompt) Ask(ctx context.Context, question string) (string, error) {
	if question == roundOfferQuestion {
		p.h.mu.Lock()
		finished := p.h.finished
		p.h.mu.Unlock()
		if !finished {
			p.scriptedPrompter.t.Fatalf("the round question was asked before the connection stopped")
		}
	}
	return p.scriptedPrompter.Ask(ctx, question)
}

// The round's failures are named in the offer, and the answer is a yes.
func TestMenuRoundFailedOffersAndReconnects(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{
		{value: "1"},                 // connect
		{value: ""},                  // connect with the saved resolvers
		{value: "y"},                 // the round's question
		{value: "y"},                 // the check's drop offer
		{value: "udp:192.0.2.54:53"}, // the editor's entry
		{value: ""},                  // the editor's finish
		{value: "0"},                 // leave
	})
	h.roundEvents = [][]supervisor.ResolverFailure{{
		{Resolver: "network (192.0.2.53)", Err: errors.New("no answer from the server within 10s")},
		{Resolver: "udp:192.0.2.54:53", Err: errors.New("dial failed")},
	}}
	h.checkOutcomes = []resolverOutcome{
		{entry: "udp:192.0.2.53:53", ok: false},
		{entry: "doh-cloudflare", ok: true},
	}
	h.menu.save = saveProfiles
	h.menu.ask = &stoppedPrompt{scriptedPrompter: h.prompter, h: h}

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}

	out := h.out.String()
	for _, want := range []string{
		"No resolver completed a handshake in a full round:",
		"  network (192.0.2.53): no answer from the server within 10s\n",
		"  udp:192.0.2.54:53: dial failed\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the offer did not print %q:\n%s", want, out)
		}
	}
	if got := strings.Count(strings.Join(h.prompter.asked, "\n"), roundOfferQuestion); got != 1 {
		t.Fatalf("the round question was asked %d times, want 1; asked %q", got, h.prompter.asked)
	}
	if len(h.checked) != 1 || h.checked[0] != "home" {
		t.Fatalf("check was called with %q, want the current profile", h.checked)
	}
	if len(h.connected) != 2 {
		t.Fatalf("connections = %d, want 2", len(h.connected))
	}
	for i, req := range h.connected {
		if req.profileName != "home" {
			t.Fatalf("connection %d = %q, want home", i, req.profileName)
		}
		if req.onRoundFailed == nil {
			t.Fatalf("connection %d has no round callback", i)
		}
	}
	raw := readConfigFile(t)
	if !strings.Contains(raw, "udp:192.0.2.54:53") {
		t.Fatalf("the editor's entry is not in the file:\n%s", raw)
	}
	if strings.Contains(raw, "udp:192.0.2.53:53") {
		t.Fatalf("the old entry is still in the file:\n%s", raw)
	}
	if got := h.choices(); got != 2 {
		t.Fatalf("the loop asked %d times, want 2", got)
	}
}

// A no writes nothing and returns to the menu with the connection stopped.
func TestMenuRoundFailedDeclined(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))
	before := readConfigFile(t)

	h := newMenuHarness(t, []scriptedAnswer{{value: "1"}, {value: ""}, {value: "n"}, {value: "0"}})
	h.roundEvents = [][]supervisor.ResolverFailure{{
		{Resolver: "udp:192.0.2.53:53", Err: errors.New("dial failed")},
	}}

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if len(h.checked) != 0 || len(h.saved) != 0 {
		t.Fatalf("the refusal ran the check or the editor: %q, %v", h.checked, h.saved)
	}
	if len(h.connected) != 1 {
		t.Fatalf("connections = %d, want 1", len(h.connected))
	}
	if got := readConfigFile(t); got != before {
		t.Fatalf("the refusal changed the file:\n%s", got)
	}
	if got := h.choices(); got != 2 {
		t.Fatalf("the loop asked %d times, want 2", got)
	}
}

// A round that fires twice is still one question: the connection is cancelled by the
// first event, and the second send is dropped.
func TestMenuRoundFailedAsksOncePerConnection(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{{value: "1"}, {value: ""}, {value: "y"}, {value: ""}, {value: "0"}})
	h.roundEvents = [][]supervisor.ResolverFailure{{
		{Resolver: "udp:192.0.2.53:53", Err: errors.New("dial failed")},
	}}
	h.menu.save = saveProfiles

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if got := strings.Count(strings.Join(h.prompter.asked, "\n"), roundOfferQuestion); got != 1 {
		t.Fatalf("the round question was asked %d times, want 1; asked %q", got, h.prompter.asked)
	}
	if len(h.connected) != 2 {
		t.Fatalf("connections = %d, want 2", len(h.connected))
	}
	if len(h.checked) != 1 {
		t.Fatalf("the check ran %d times, want 1", len(h.checked))
	}
}

// A check where nothing passed is not the end of the item: its line is printed, and the
// editor opens exactly as the person asked.
func TestMenuRoundFailedNothingPassedStillEdits(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{
		{value: "1"},
		{value: ""},
		{value: "y"},
		{value: "udp:192.0.2.54:53"},
		{value: ""},
		{value: "0"},
	})
	h.roundEvents = [][]supervisor.ResolverFailure{{
		{Resolver: "udp:192.0.2.53:53", Err: errors.New("dial failed")},
	}}
	h.checkErr = &exitCodeError{code: exitError}
	h.menu.save = saveProfiles

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the loop = %v", err)
	}
	if h.errOut.Len() != 0 {
		t.Fatalf("the exitCodeError was reported again: %s", h.errOut.String())
	}
	if len(h.checked) != 1 {
		t.Fatalf("the check ran %d times, want 1", len(h.checked))
	}
	if len(h.connected) != 2 {
		t.Fatalf("connections = %d, want 2", len(h.connected))
	}
	if got := readConfigFile(t); !strings.Contains(got, "udp:192.0.2.54:53") {
		t.Fatalf("the editor's entry is not in the file:\n%s", got)
	}
}

// The end of input at the question closes the menu and writes nothing.
func TestMenuRoundFailedEOFClosesTheMenu(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))
	before := readConfigFile(t)

	h := newMenuHarness(t, []scriptedAnswer{{value: "1"}, {value: ""}})
	h.roundEvents = [][]supervisor.ResolverFailure{{
		{Resolver: "udp:192.0.2.53:53", Err: errors.New("dial failed")},
	}}

	if err := h.runLoop(context.Background()); err != nil {
		t.Fatalf("the end of input = %v, want nil", err)
	}
	if len(h.connected) != 1 {
		t.Fatalf("connections = %d, want 1", len(h.connected))
	}
	if len(h.saved) != 0 {
		t.Fatal("the end of input wrote a profile")
	}
	if got := readConfigFile(t); got != before {
		t.Fatalf("the end of input changed the file:\n%s", got)
	}
	if h.errOut.Len() != 0 {
		t.Fatalf("the end of input wrote: %s", h.errOut.String())
	}
}

// A Ctrl+C during the connection is not answered with a question: the item is stopped,
// and the menu goes back to the choice.
func TestMenuRoundFailedInterruptDoesNotOffer(t *testing.T) {
	isolatedConnectConfig(t)
	writeMenuProfiles(t, oneProfile("home"))

	h := newMenuHarness(t, []scriptedAnswer{{value: "1"}, {value: ""}})
	h.blockConnections = true
	ask := newWaitingPrompter(h.prompter)
	h.menu.ask = ask

	run := h.start(context.Background(), "")
	t.Cleanup(run.close)

	if got := <-ask.asks; got != "Choice [1]: " {
		t.Fatalf("the menu did not ask for a choice: %q", got)
	}
	if got := <-ask.asks; got != offerResolversQuestion {
		t.Fatalf("the offer did not ask for the resolvers: %q", got)
	}
	// The connection is the next thing to run; the press stops it, and the item returns
	// without asking the round's question.
	run.sig <- os.Interrupt
	if got := <-ask.asks; got != "Choice [1]: " {
		t.Fatalf("the loop did not ask for a choice again: %q", got)
	}
	run.sig <- os.Interrupt
	if err := run.wait(); err != nil {
		t.Fatalf("the loop = %v, want nil", err)
	}
	if slices.Contains(h.prompter.asked, roundOfferQuestion) {
		t.Fatalf("the round question was asked after a stop: %q", h.prompter.asked)
	}
	if len(h.connected) != 1 {
		t.Fatalf("connections = %d, want 1", len(h.connected))
	}
}

// menu -connect asks nothing: the request's callback is nil.
func TestMenuConnectFlagHasNoRoundOffer(t *testing.T) {
	isolatedConnectConfig(t)
	h := newMenuHarness(t, nil)
	h.roundEvents = [][]supervisor.ResolverFailure{{
		{Resolver: "udp:192.0.2.53:53", Err: errors.New("dial failed")},
	}}

	if err := h.runConnect(context.Background(), "home"); err != nil {
		t.Fatalf("the run = %v", err)
	}
	if len(h.connected) != 1 {
		t.Fatalf("connections = %d, want 1", len(h.connected))
	}
	if h.connected[0].onRoundFailed != nil {
		t.Fatal("menu -connect installed a round callback")
	}
	if len(h.prompter.asked) != 0 {
		t.Fatalf("menu -connect asked %q", h.prompter.asked)
	}
}
