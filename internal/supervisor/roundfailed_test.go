package supervisor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/FreedomCats/dnstt-cli/core/client"
)

// A full round of failed handshakes after a session reports once per round, with the
// failures in dial order, and the next round reports again. The event comes before the
// pause after the round: the menu's question would otherwise wait for the backoff.
func TestRoundFailedFiresOncePerRound(t *testing.T) {
	// seen is what the callback may not overtake: the reconnecting transitions and the
	// backoff sleeps recorded so far.
	type seen struct{ reconnecting, sleeps int }
	var (
		h       *harness
		rounds  [][]ResolverFailure
		started []seen // the counts at each round's first failed dial
		events  []seen // the counts each callback saw
	)
	// The session's death is followed by a pause of its own, so the event is pinned to
	// the counts at the round's first failed dial, not to zero. snapshot is called with
	// h.mu held.
	snapshot := func() seen {
		s := seen{sleeps: len(h.sleeps)}
		for _, tr := range h.trans {
			if tr.To == StateReconnecting {
				s.reconnecting++
			}
		}
		return s
	}
	h = newHarness(t, []string{r1, r2}, func(o *Options) {
		o.OnRoundFailed = func(failures []ResolverFailure) {
			h.mu.Lock()
			rounds = append(rounds, failures)
			events = append(events, snapshot())
			h.mu.Unlock()
			h.changed <- struct{}{}
		}
	})
	h.dial = func(r string, n int) error {
		if r == r1 && n == 0 {
			return nil
		}
		// The pause after the round leaves prev at r2, so every round begins with a
		// failed dial of r1: take the counts before the rest of the round's dials.
		if r == r1 {
			h.mu.Lock()
			started = append(started, snapshot())
			h.mu.Unlock()
		}
		return errDial
	}
	// checkOrder compares the i-th event with the start of its round: a pause between
	// them would add a reconnecting transition and a sleep to the event's counts.
	checkOrder := func(what string, i int) {
		t.Helper()
		h.mu.Lock()
		if len(events) <= i || len(started) <= i {
			nEvents, nStarted := len(events), len(started)
			h.mu.Unlock()
			t.Fatalf("%s: events %d, rounds started %d", what, nEvents, nStarted)
		}
		got, want := events[i], started[i]
		h.mu.Unlock()
		if got != want {
			t.Errorf("%s event saw %d reconnecting transition(s) and %d sleep(s), the round began at %d and %d: the pause came before the event",
				what, got.reconnecting, got.sleeps, want.reconnecting, want.sleeps)
		}
	}
	h.run(context.Background())
	h.waitState(StateConnected, 1)

	h.mu.Lock()
	before := len(rounds)
	h.mu.Unlock()
	if before != 0 {
		t.Fatalf("the event fired before the session died: %d", before)
	}

	h.session(0).die(client.ErrSilent)
	// The harness's sleep is instant, so later rounds may already have run by the time the
	// test looks: the count only has to reach the round this step waits for. The rounds
	// are in order, so rounds[0] and rounds[1] stay the first and the second.
	h.wait("the first round", func() bool { return len(rounds) >= 1 })
	checkOrder("the first", 0)
	h.mu.Lock()
	first := rounds[0]
	h.mu.Unlock()
	if len(first) != 2 {
		t.Fatalf("the first round = %d failures, want 2", len(first))
	}
	for i, want := range []string{r1, r2} {
		if first[i].Resolver != want {
			t.Errorf("failure %d is %q, want %q", i, first[i].Resolver, want)
		}
		if !errors.Is(first[i].Err, errDial) {
			t.Errorf("failure %d = %v, want %v", i, first[i].Err, errDial)
		}
	}

	tr := h.waitState(StateReconnecting, 2)
	if !strings.Contains(tr.Cause.Error(), "no resolver of 2 completed a handshake") {
		t.Errorf("the reconnecting cause = %v", tr.Cause)
	}

	h.wait("the second round", func() bool { return len(rounds) >= 2 })
	checkOrder("the second", 1)
	h.mu.Lock()
	second := rounds[1]
	h.mu.Unlock()
	if len(second) != 2 {
		t.Fatalf("the second round = %d failures, want 2", len(second))
	}
}

// The first connect never reports: it ends with NeverConnectedError.
func TestRoundFailedNotOnFirstConnect(t *testing.T) {
	var (
		h      *harness
		rounds [][]ResolverFailure
	)
	h = newHarness(t, []string{r1, r2}, func(o *Options) {
		o.OnRoundFailed = func(failures []ResolverFailure) {
			h.mu.Lock()
			rounds = append(rounds, failures)
			h.mu.Unlock()
			h.changed <- struct{}{}
		}
	})
	h.dial = func(string, int) error { return errDial }
	h.run(context.Background())

	var err error
	select {
	case err = <-h.result:
		h.result <- err // for Cleanup
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop")
	}
	var ne *NeverConnectedError
	if !errors.As(err, &ne) {
		t.Fatalf("Run = %v, want NeverConnectedError", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(rounds) != 0 {
		t.Fatalf("the first connect fired %d events", len(rounds))
	}
}

// The menu's stop of the connection ends the supervisor cleanly: the callback cancels the
// run, and Run returns without a pause or a NeverConnectedError.
func TestRoundFailedStopIsClean(t *testing.T) {
	var (
		h     *harness
		fired bool
	)
	h = newHarness(t, []string{r1, r2}, func(o *Options) {
		o.OnRoundFailed = func([]ResolverFailure) {
			h.mu.Lock()
			fired = true
			h.mu.Unlock()
			h.cancel()
		}
	})
	h.dial = func(r string, n int) error {
		if r == r1 && n == 0 {
			return nil
		}
		return errDial
	}
	h.run(context.Background())
	h.waitState(StateConnected, 1)
	h.session(0).die(client.ErrSilent)
	h.wait("the round", func() bool { return fired })

	if err := h.stop(); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if h.tun.State() != StateStopping {
		t.Errorf("state %s after Run", h.tun.State())
	}
	if !h.session(0).closed.Load() {
		t.Error("the session is left open")
	}
}
