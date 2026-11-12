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
// failures in dial order, and the next round reports again.
func TestRoundFailedFiresOncePerRound(t *testing.T) {
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
	h.dial = func(r string, n int) error {
		if r == r1 && n == 0 {
			return nil
		}
		return errDial
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
	h.wait("the first round", func() bool { return len(rounds) == 1 })
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

	h.wait("the second round", func() bool { return len(rounds) == 2 })
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
