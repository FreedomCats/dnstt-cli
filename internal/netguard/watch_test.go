package netguard

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var (
	ethernet = Choice{Index4: 3, Alias4: "Ethernet 3"}
	wifi     = Choice{Index4: 7, Alias4: "Wi-Fi", Index6: 7, Alias6: "Wi-Fi"}
	offline  = Choice{}
)

func TestSettlerCollapsesSeries(t *testing.T) {
	t0 := time.Unix(1000, 0)
	s := settler{settle: 2 * time.Second, maxWait: 10 * time.Second}

	// Switching Wi-Fi → Ethernet: the first event changes the choice, the others no
	// longer do, but they extend the burst.
	s.event(t0, true)
	for _, d := range []time.Duration{300, 800, 1500} {
		s.event(t0.Add(d*time.Millisecond), false)
	}
	if s.due(t0.Add(3400 * time.Millisecond)) {
		t.Fatal("decision before 2 s of quiet after the last event")
	}
	if !s.due(t0.Add(3500 * time.Millisecond)) {
		t.Fatal("no decision after 2 s of quiet")
	}
	s.reset()
	if s.due(t0.Add(time.Hour)) {
		t.Fatal("the burst is still open after the decision")
	}
}

func TestSettlerMaxWait(t *testing.T) {
	t0 := time.Unix(1000, 0)
	s := settler{settle: 2 * time.Second, maxWait: 10 * time.Second}
	s.event(t0, true)
	// The network makes noise every second without end.
	for i := 1; i <= 15; i++ {
		s.event(t0.Add(time.Duration(i)*time.Second), false)
		if got, want := s.due(t0.Add(time.Duration(i)*time.Second)), i >= 10; got != want {
			t.Fatalf("at %d s due = %v, want %v", i, got, want)
		}
	}
}

func TestSettlerIgnoresUnchanged(t *testing.T) {
	t0 := time.Unix(1000, 0)
	s := settler{settle: 2 * time.Second, maxWait: 10 * time.Second}
	for i := 0; i < 5; i++ {
		s.event(t0.Add(time.Duration(i)*time.Second), false)
	}
	if s.pending || s.due(t0.Add(time.Hour)) {
		t.Fatal("events that did not change the choice opened a burst")
	}
}

// fakeNet stubs the subscription and the choice for watch.
type fakeNet struct {
	mu     sync.Mutex
	choice Choice
	err    error
	notify func(uint64)
	picks  atomic.Int32
	// excluded is what the choice last got in exclude.
	excluded atomic.Uint64
}

func (f *fakeNet) set(c Choice) {
	f.mu.Lock()
	f.choice = c
	f.mu.Unlock()
}

func (f *fakeNet) sub(notify func(uint64)) (func(), error) {
	f.notify = notify
	return func() {}, nil
}

func (f *fakeNet) pick(exclude uint64) (Choice, error) {
	f.picks.Add(1)
	f.excluded.Store(exclude)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.choice, f.err
}

type change struct{ now, before Choice }

// startWatch gives a Guard with choice start and a watcher with a short window.
func startWatch(t *testing.T, start Choice, exclude uint64) (*Guard, *fakeNet, <-chan change) {
	t.Helper()
	g := &Guard{}
	g.commit(start)
	f := &fakeNet{choice: start}
	changes := make(chan change, 16)
	stop, err := g.watch(WatchOptions{
		Exclude: func() uint64 { return exclude },
		Changed: func(now, before Choice) { changes <- change{now, before} },
		Settle:  40 * time.Millisecond,
		MaxWait: time.Second,
	}, f.sub, f.pick, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	return g, f, changes
}

func expectChange(t *testing.T, changes <-chan change) change {
	t.Helper()
	select {
	case c := <-changes:
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("no network change arrived")
		return change{}
	}
}

func expectNoChange(t *testing.T, changes <-chan change) {
	t.Helper()
	select {
	case c := <-changes:
		t.Fatalf("extra network change: %v (was %v)", c.now, c.before)
	case <-time.After(200 * time.Millisecond):
	}
}

// A burst of Wi-Fi → Ethernet events, with "no network" in between, gives exactly one
// network change, and the indexes are already set by then.
func TestWatchOneChangePerSeries(t *testing.T) {
	g, f, changes := startWatch(t, wifi, 0)

	f.set(offline)
	for i := 0; i < 5; i++ {
		f.notify(7)
	}
	f.set(ethernet)
	for i := 0; i < 5; i++ {
		f.notify(3)
	}

	c := expectChange(t, changes)
	if !c.now.Same(ethernet) || !c.before.Same(wifi) {
		t.Fatalf("change %v (was %v), want Ethernet after Wi-Fi", c.now, c.before)
	}
	if i4, i6 := g.Interfaces(); i4 != 3 || i6 != 0 {
		t.Fatalf("indexes %d/%d, want 3/0", i4, i6)
	}
	expectNoChange(t, changes)

	// The next change is again a single one.
	f.set(wifi)
	f.notify(7)
	f.notify(7)
	if c := expectChange(t, changes); !c.now.Same(wifi) || !c.before.Same(ethernet) {
		t.Fatalf("second change %v (was %v)", c.now, c.before)
	}
	expectNoChange(t, changes)
}

// Our own adapter: its events are skipped without a choice, and the exclusion is passed
// to the choice. Events of other interfaces that did not change the choice give nothing
// either.
func TestWatchIgnoresOwnAdapter(t *testing.T) {
	_, f, changes := startWatch(t, ethernet, 42)

	for i := 0; i < 10; i++ {
		f.notify(42)
	}
	if n := f.picks.Load(); n != 0 {
		t.Fatalf("events of our own adapter reached the choice %d times", n)
	}
	for i := 0; i < 10; i++ {
		f.notify(9) // another program's non-default route
	}
	expectNoChange(t, changes)
	if f.picks.Load() == 0 || f.excluded.Load() != 42 {
		t.Fatalf("choice called %d times with exclude %d, want with 42", f.picks.Load(), f.excluded.Load())
	}
}

// The interface disappeared and came back within the window (sleep, Wi-Fi
// reconnection): the choice is the same, but the sockets through it are dead — there is
// still one change.
func TestWatchBlipStillChanges(t *testing.T) {
	_, f, changes := startWatch(t, wifi, 0)

	f.set(offline)
	f.notify(7)
	f.set(wifi)
	f.notify(7)

	c := expectChange(t, changes)
	if !c.now.Same(wifi) || !c.before.Same(wifi) {
		t.Fatalf("change %v (was %v), want Wi-Fi after Wi-Fi", c.now, c.before)
	}
	expectNoChange(t, changes)
}

// The system refusing to give out the table at decision time postpones the decision
// instead of setting an empty choice.
func TestWatchChooseErrorRetries(t *testing.T) {
	g, f, changes := startWatch(t, wifi, 0)

	f.mu.Lock()
	f.choice, f.err = ethernet, errors.New("routing table unavailable")
	f.mu.Unlock()
	f.notify(3)
	expectNoChange(t, changes)
	if i4, _ := g.Interfaces(); i4 != 7 {
		t.Fatalf("IPv4 index %d after the choice failed, want the previous 7", i4)
	}

	f.mu.Lock()
	f.err = nil
	f.mu.Unlock()
	if c := expectChange(t, changes); !c.now.Same(ethernet) {
		t.Fatalf("change %v, want Ethernet", c.now)
	}
}

func TestWatchStop(t *testing.T) {
	g := &Guard{}
	g.commit(wifi)
	f := &fakeNet{choice: wifi}
	var calls atomic.Int32
	stop, err := g.watch(WatchOptions{
		Changed: func(Choice, Choice) { calls.Add(1) },
		Settle:  20 * time.Millisecond,
	}, f.sub, f.pick, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	f.set(ethernet)
	f.notify(3) // burst open, timer armed
	stop()
	f.notify(3)
	time.Sleep(100 * time.Millisecond)
	if n := calls.Load(); n != 0 {
		t.Fatalf("Changed after stop: %d times", n)
	}
}

// NoIPv6: the IPv6 choice is dropped both at the first choice and at a network change,
// and a change of the IPv6 interface alone does not count as a network change.
func TestNoIPv6DropsIPv6Choice(t *testing.T) {
	g := &Guard{NoIPv6: true}
	g.commit(wifi)
	if i4, i6 := g.Interfaces(); i4 != 7 || i6 != 0 {
		t.Fatalf("indexes %d, %d; want 7 and 0", i4, i6)
	}
	if c := g.Choice(); c.Index6 != 0 || c.Alias6 != "" {
		t.Fatalf("IPv6 left in the choice: %+v", c)
	}

	f := &fakeNet{choice: wifi}
	changes := make(chan change, 16)
	stop, err := g.watch(WatchOptions{
		Changed: func(now, before Choice) { changes <- change{now, before} },
		Settle:  40 * time.Millisecond,
		MaxWait: time.Second,
	}, f.sub, f.pick, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	other6 := wifi
	other6.Index6 = 9
	f.set(other6)
	f.notify(9)
	expectNoChange(t, changes)

	f.set(ethernet)
	f.notify(3)
	if c := expectChange(t, changes); c.now.Index4 != 3 || c.now.Index6 != 0 {
		t.Errorf("network change: %+v", c.now)
	}
}
