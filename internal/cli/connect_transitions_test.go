package cli

import (
	"bytes"
	"testing"
	"time"

	"github.com/FreedomCats/dnstt-cli/internal/supervisor"
)

// Redirected output has no status line: nothing reads the transitions, so no callback is
// installed and the supervisor cannot be held by a send nobody consumes.
func TestStatusTransitionsWithoutTerminal(t *testing.T) {
	transitions, notify := statusTransitions(&bytes.Buffer{})
	if transitions != nil || notify != nil {
		t.Fatalf("non-terminal output got transitions %v and callback %v, want neither", transitions, notify != nil)
	}
}

// A full channel must not hold the supervisor: the status line redraws on its ticker, so
// a dropped wake-up is harmless.
func TestNotifyTransitionsNeverBlocks(t *testing.T) {
	transitions := make(chan supervisor.Transition, 100)
	notify := notifyTransitions(transitions)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 10 * cap(transitions) {
			notify(supervisor.Transition{To: supervisor.StateConnected})
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the notifier blocked with no consumer")
	}
	if len(transitions) != cap(transitions) {
		t.Errorf("buffered %d transitions, want the buffer filled to %d", len(transitions), cap(transitions))
	}
}
