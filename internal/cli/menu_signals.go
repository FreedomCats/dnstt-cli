package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"

	"github.com/FreedomCats/dnstt-cli/internal/app"
)

// menuSession holds what the signals of architecture.md §10a do to one menu run: the menu's
// own cancel, the running item's cancel and abort channel, and the press count of that
// item.
//
// The rules are the command's, minus the exit: the first Ctrl+C stops the item, the second
// does not wait for open connections, the third does nothing. os.Exit on the third press
// belongs to `connect` alone: here it would kill the menu. Ctrl+C while the menu waits for
// a choice closes it; SIGTERM and SIGHUP stop the item and close the menu.
type menuSession struct {
	// notice is where the lines of the stops go: the command's stderr.
	notice io.Writer

	mu sync.Mutex
	// cancel closes the menu.
	cancel context.CancelFunc
	// itemCancel stops the running item; nil when the menu waits for a choice.
	itemCancel context.CancelFunc
	// abort is the running item's force-abort channel, closed by the second Ctrl+C. A
	// fresh one is made per item: a channel reused across connections would abort the
	// next one at once.
	abort chan struct{}
	// presses is the count of Ctrl+C of the running item.
	presses int
}

func newMenuSession(notice io.Writer, cancel context.CancelFunc) *menuSession {
	return &menuSession{notice: notice, cancel: cancel}
}

// running registers the item that is about to start and returns its abort channel. The item
// is registered before its body starts and cleared after it returns, so a press between the
// status line's stop and the next prompt still belongs to the item.
func (s *menuSession) running(cancel context.CancelFunc) <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.itemCancel = cancel
	s.abort = make(chan struct{})
	s.presses = 0
	return s.abort
}

// idle clears the running item: the menu waits for a choice again.
func (s *menuSession) idle() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.itemCancel = nil
	s.abort = nil
	s.presses = 0
}

// watch consumes the signals until the caller closes done. The caller creates and stops the
// channel: runMenu wires signal.Notify, tests inject their own.
func (s *menuSession) watch(sig <-chan os.Signal, done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case received := <-sig:
			s.handle(received)
		}
	}
}

// handle applies one signal to the session.
func (s *menuSession) handle(sig os.Signal) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch sig {
	case os.Interrupt:
		if s.itemCancel == nil {
			// No item: the menu itself is what waits.
			s.cancel()
			return
		}
		s.presses++
		switch s.presses {
		case 1:
			writeStopNotice(s.notice)
			s.itemCancel()
		case 2:
			fmt.Fprintf(s.notice, "%s: not waiting for open connections\n", app.Name)
			close(s.abort)
		}
		// The third press does nothing: the menu must not call os.Exit.
	case syscall.SIGTERM, syscall.SIGHUP:
		if s.itemCancel != nil {
			s.itemCancel()
		}
		s.cancel()
	}
}
