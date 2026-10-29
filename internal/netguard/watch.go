// watch.go — tracking network changes.
//
// The physical interface is chosen at start (SelectInterfaces), but the network changes
// on the fly: Wi-Fi → Ethernet, an unplugged cable, sleep and wake-up. Watch catches
// changes of routes and interfaces, folds a burst of them into one decision, sets the new
// indexes and tells the caller that the network has changed — restarting the tunnel is
// the caller's business.
package netguard

import (
	"sync"
	"time"
)

// DefaultSettle is how long to wait for quiet after the last event before deciding.
// Switching Wi-Fi → Ethernet gives a burst of route and interface events within a second
// or two; deciding on the first of them would give several restarts in a row.
const DefaultSettle = 2 * time.Second

// DefaultMaxWait caps the wait for quiet: a network that keeps making noise still gets a
// decision.
const DefaultMaxWait = 10 * time.Second

// WatchOptions are the settings of Watch.
type WatchOptions struct {
	// Exclude is our own adapter at the time of the event (the datapath's AdapterID: the
	// LUID on Windows, the interface index on Linux); 0 or nil means there is nothing to
	// exclude. Events of that interface are skipped, and it never gets into the choice:
	// otherwise the tunnel, setting up its own routes, would restart itself.
	Exclude func() uint64

	// Changed gets the new and the previous choice — once per folded burst, when the new
	// indexes are already set. The choices may be equal: the interface disappeared and
	// came back (sleep, Wi-Fi reconnection), and the sockets opened through it are
	// already dead. Calls do not overlap. Required.
	Changed func(now, before Choice)

	// Settle and MaxWait are the folding window; 0 means DefaultSettle and
	// DefaultMaxWait.
	Settle, MaxWait time.Duration
}

// Watch tracks network changes until stop is called. It starts from the choice set by
// SelectInterfaces. stop waits for a Changed already in progress to finish.
func (g *Guard) Watch(opts WatchOptions) (stop func(), err error) {
	return g.watch(opts, subscribe, choose, time.Now)
}

// watch is Watch with a replaceable subscription, choice and clock (tests).
func (g *Guard) watch(opts WatchOptions, sub func(func(uint64)) (func(), error),
	pick func(uint64) (Choice, error), now func() time.Time) (func(), error) {
	if opts.Settle <= 0 {
		opts.Settle = DefaultSettle
	}
	if opts.MaxWait <= 0 {
		opts.MaxWait = DefaultMaxWait
	}
	w := &watcher{
		guard: g,
		opts:  opts,
		// The choice is compared with the one set already without IPv6, if IPv6 is
		// denied: otherwise a change of the IPv6 interface alone would pass for a
		// network change.
		choose: func(exclude uint64) (Choice, error) {
			c, err := pick(exclude)
			return g.allowed(c), err
		},
		now:     now,
		settler: settler{settle: opts.Settle, maxWait: opts.MaxWait},
	}
	unsubscribe, err := sub(w.event)
	if err != nil {
		return nil, err
	}
	return func() {
		w.mu.Lock()
		w.stopped = true
		if w.timer != nil {
			w.timer.Stop()
		}
		w.mu.Unlock()
		unsubscribe()
		// Wait for a Changed already in progress.
		w.fireMu.Lock()
		w.fireMu.Unlock() //nolint:staticcheck // empty critical section: a wait
	}, nil
}

type watcher struct {
	guard  *Guard
	opts   WatchOptions
	choose func(uint64) (Choice, error)
	now    func() time.Time

	mu      sync.Mutex
	settler settler
	timer   *time.Timer // armed while a burst is undecided
	stopped bool

	// fireMu keeps decisions from overlapping: the next timer may fire while the
	// previous Changed is still running.
	fireMu sync.Mutex
}

func (w *watcher) exclude() uint64 {
	if w.opts.Exclude == nil {
		return 0
	}
	return w.opts.Exclude()
}

// event is a change of routes or of interface id (0 means the interface is unknown).
//
// The choice is computed and compared under w.mu, as in fire: otherwise an event that
// computed its choice before fire set the new one would compare the old with the new and
// open an extra burst — a second restart for one network change.
func (w *watcher) event(id uint64) {
	exclude := w.exclude()
	if exclude != 0 && id == exclude {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return
	}
	c, err := w.choose(exclude)
	// The system refusing to give out the table is also a reason to recheck later.
	differs := err != nil || !c.Same(w.guard.Choice())
	w.settler.event(w.now(), differs)
	w.arm()
}

// arm sets a timer for the end of the window if a burst is open and there is no timer.
// Called under w.mu.
func (w *watcher) arm() {
	if !w.settler.pending || w.timer != nil {
		return
	}
	w.timer = time.AfterFunc(w.settler.deadline().Sub(w.now()), w.fire)
}

// fire: the window is over (or not yet, if events kept coming).
func (w *watcher) fire() {
	w.fireMu.Lock()
	defer w.fireMu.Unlock()

	w.mu.Lock()
	w.timer = nil
	if w.stopped {
		w.mu.Unlock()
		return
	}
	now := w.now()
	if !w.settler.due(now) {
		w.arm()
		w.mu.Unlock()
		return
	}
	c, err := w.choose(w.exclude())
	if err != nil {
		// Nothing to decide on: one more window.
		w.settler.reset()
		w.settler.event(now, true)
		w.arm()
		w.mu.Unlock()
		return
	}
	w.settler.reset()
	before := w.guard.commit(c)
	w.mu.Unlock()

	w.opts.Changed(c, before)
}

// settler folds a burst of events, without a clock: the time comes as an argument.
//
// A burst is opened by an event after which the interface choice differs from the one
// set; events that do not change the choice (the tunnel's own routes, other programs'
// non-default routes) do not open a burst. Any event extends an open burst; the decision
// comes after settle of quiet, but no later than maxWait from the start. A burst is
// always decided, even if by the end the choice has returned to the previous one: the
// interface disappeared, and the sockets through it are dead.
type settler struct {
	settle, maxWait time.Duration

	pending     bool
	first, last time.Time
}

func (s *settler) event(now time.Time, differs bool) {
	if !s.pending {
		if !differs {
			return
		}
		s.pending, s.first = true, now
	}
	s.last = now
}

// deadline is when to decide an open burst.
func (s *settler) deadline() time.Time {
	quiet := s.last.Add(s.settle)
	if limit := s.first.Add(s.maxWait); limit.Before(quiet) {
		return limit
	}
	return quiet
}

// due: a burst is open, and it is time to decide.
func (s *settler) due(now time.Time) bool {
	return s.pending && !now.Before(s.deadline())
}

func (s *settler) reset() {
	s.pending = false
	s.first, s.last = time.Time{}, time.Time{}
}
