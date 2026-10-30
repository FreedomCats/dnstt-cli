package supervisor

// Ctrl+C, from csqtt-cli (MIT): the first one stops the tunnel and cleans up, the second
// one does not wait for open connections, the third one kills the process
// (architecture.md §08).

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// Interrupt returns a context that ends on the first Ctrl+C, SIGTERM or SIGHUP (if it is
// not ignored). The second signal calls abort of the context (WithAbort): a Run that is
// stopping stops waiting for open connections. If there is nothing to abort, the process
// exits at once with code 130. After the second signal the handler is removed, and the
// third goes to the system, which kills the process.
//
// The returned function must be called on exit: it removes the handler and cancels the
// context.
func Interrupt(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	ctx, abort := WithAbort(ctx)
	hook := ctx.Value(abortKey{}).(*abortHook)

	signals := make(chan os.Signal, 1)
	stopOn := []os.Signal{os.Interrupt, syscall.SIGTERM}
	if !signal.Ignored(syscall.SIGHUP) {
		stopOn = append(stopOn, syscall.SIGHUP)
	}
	signal.Notify(signals, stopOn...)

	released := make(chan struct{})
	go func() {
		defer signal.Stop(signals)
		select {
		case <-signals:
			cancel()
		case <-ctx.Done():
			return
		}
		// Nothing to abort: the second signal goes to the system.
		if !hook.armed() {
			return
		}
		select {
		case <-signals:
			if !abort() {
				// Run ended between the signals: nothing to abort, and the signal
				// means what it means without a handler — exit at once. 128 + SIGINT,
				// as shells do.
				os.Exit(130)
			}
		case <-released:
		}
	}()

	var once sync.Once
	return ctx, func() {
		once.Do(func() { close(released) })
		signal.Stop(signals)
		cancel()
	}
}

// abortKey is the context key of *abortHook.
type abortKey struct{}

// abortHook is what aborts a stop in progress (OnAbort); nil f — nothing.
type abortHook struct {
	mu sync.Mutex
	f  func()
}

func (h *abortHook) armed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.f != nil
}

// WithAbort returns a context through which a long stop leaves a way to abort it
// (OnAbort), and abort for the signal handler: the second Ctrl+C calls it instead of
// killing the process. abort reports whether there was anything to abort; false — the
// handler does what it would without it. Interrupt and the menu are built on it.
func WithAbort(parent context.Context) (ctx context.Context, abort func() bool) {
	hook := &abortHook{}
	return context.WithValue(parent, abortKey{}, hook), func() bool {
		hook.mu.Lock()
		f := hook.f
		hook.mu.Unlock()
		if f == nil {
			return false
		}
		f()
		return true
	}
}

// OnAbort: until released, abort of ctx (WithAbort) calls f. Without WithAbort up the
// chain of ctx — nothing: there is nobody to abort.
func OnAbort(ctx context.Context, f func()) (release func()) {
	hook, ok := ctx.Value(abortKey{}).(*abortHook)
	if !ok {
		return func() {}
	}
	hook.mu.Lock()
	hook.f = f
	hook.mu.Unlock()
	return func() {
		hook.mu.Lock()
		hook.f = nil
		hook.mu.Unlock()
	}
}
