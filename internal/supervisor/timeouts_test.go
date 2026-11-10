package supervisor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FreedomCats/dnstt-cli/core/client"
	"github.com/FreedomCats/dnstt-cli/internal/resolvers"
)

// These are the user-approved timeout numbers. They are one set, and changing one of them
// is a new decision, not an implementation detail.
func TestApprovedTimeoutPolicy(t *testing.T) {
	if DefaultKeepAliveTimeout != 30*time.Second {
		t.Errorf("DefaultKeepAliveTimeout = %v, want 30s", DefaultKeepAliveTimeout)
	}
	if DefaultHandshakeTimeout != 10*time.Second {
		t.Errorf("DefaultHandshakeTimeout = %v, want 10s", DefaultHandshakeTimeout)
	}
	if DefaultDegradedAfter != 25*time.Second {
		t.Errorf("DefaultDegradedAfter = %v, want 25s", DefaultDegradedAfter)
	}
	if DefaultDegradedAfter >= DefaultKeepAliveTimeout {
		t.Errorf("DefaultDegradedAfter %v is not below DefaultKeepAliveTimeout %v",
			DefaultDegradedAfter, DefaultKeepAliveTimeout)
	}
}

// New fills the policy defaults into its options and does not wait for them. The keepalive
// has no supervisor default: the caller sets it in Options.Session.
func TestDefaultTimeoutsFromNew(t *testing.T) {
	list, err := resolvers.Expand([]string{r1})
	if err != nil {
		t.Fatal(err)
	}
	tun, err := New(Options{Resolvers: resolvers.NewPool(list)})
	if err != nil {
		t.Fatal(err)
	}
	if tun.opts.HandshakeTimeout != DefaultHandshakeTimeout {
		t.Errorf("handshake %v, want %v", tun.opts.HandshakeTimeout, DefaultHandshakeTimeout)
	}
	if tun.opts.DegradedAfter != DefaultDegradedAfter {
		t.Errorf("degraded %v, want %v", tun.opts.DegradedAfter, DefaultDegradedAfter)
	}
	if tun.opts.DrainTimeout != DefaultDrainTimeout {
		t.Errorf("drain %v, want %v", tun.opts.DrainTimeout, DefaultDrainTimeout)
	}
	if got := tun.opts.Session.KeepAliveTimeout; got != 0 {
		t.Errorf("keepalive %v: the supervisor must not set a session default", got)
	}
}

// The configured keepalive reaches the first dial and the redial: every attempt copies
// Options.Session into its own config.
func TestKeepAliveTimeoutReachesEveryDial(t *testing.T) {
	const injected = 250 * time.Millisecond
	h := newHarness(t, []string{r1}, func(o *Options) {
		o.Session.KeepAliveTimeout = injected
	})
	dial := h.tun.dial
	var got []time.Duration
	h.tun.dial = func(ctx context.Context, cfg client.Config) (Session, error) {
		h.mu.Lock()
		got = append(got, cfg.KeepAliveTimeout)
		h.mu.Unlock()
		return dial(ctx, cfg)
	}

	h.run(context.Background())
	h.waitState(StateConnected, 1)
	h.session(0).die(client.ErrSilent)
	h.waitState(StateConnected, 2)

	h.mu.Lock()
	dials := append([]time.Duration(nil), got...)
	h.mu.Unlock()
	if len(dials) != 2 {
		t.Fatalf("%d dials, want 2", len(dials))
	}
	for i, d := range dials {
		if d != injected {
			t.Errorf("dial %d keepalive %v, want %v", i+1, d, injected)
		}
	}
}

// A dial runs under the configured handshake deadline.
func TestHandshakeTimeoutBoundsDial(t *testing.T) {
	const injected = 300 * time.Millisecond
	h := newHarness(t, []string{r1}, func(o *Options) {
		o.HandshakeTimeout = injected
	})
	dial := h.tun.dial
	remaining := make(chan time.Duration, 1)
	h.tun.dial = func(ctx context.Context, cfg client.Config) (Session, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			remaining <- -1
			return nil, errors.New("dial without a deadline")
		}
		remaining <- time.Until(deadline)
		return dial(ctx, cfg)
	}

	h.run(context.Background())
	h.waitState(StateConnected, 1)

	select {
	case d := <-remaining:
		if d < 0 {
			t.Fatal("the dial ran without a deadline")
		}
		if d > injected {
			t.Errorf("remaining budget %v exceeds the injected handshake timeout %v", d, injected)
		}
		if d <= injected/2 {
			t.Errorf("remaining budget %v is not above half of the injected handshake timeout %v", d, injected)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the dial did not run")
	}
}
