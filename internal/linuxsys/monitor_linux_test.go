package linuxsys

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeSub is a subscription driven by the test: each attempt returns a new channel, a
// break is closing the channel, as netlink does on ENOBUFS.
type fakeSub struct {
	mu       sync.Mutex
	attempts int
	fail     int // this many next attempts will fail
	ch       chan int
	ended    int // how many attempts were closed through done
}

func (f *fakeSub) subscribe(done <-chan struct{}) (chan int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	if f.fail > 0 {
		f.fail--
		return nil, errors.New("subscription failed")
	}
	ch := make(chan int, 8)
	f.ch = ch
	go func() {
		<-done
		f.mu.Lock()
		f.ended++
		// Socket closed: netlink closes the channel. After a break it is already closed.
		if f.ch == ch {
			close(ch)
			f.ch = nil
		}
		f.mu.Unlock()
	}()
	return ch, nil
}

// send sends an event into the current subscription.
func (f *fakeSub) send(t *testing.T, index int) {
	t.Helper()
	f.mu.Lock()
	ch := f.ch
	f.mu.Unlock()
	if ch == nil {
		t.Fatal("no subscription")
	}
	ch <- index
}

// breakOff breaks the current subscription, as netlink does on a receive error.
func (f *fakeSub) breakOff() {
	f.mu.Lock()
	defer f.mu.Unlock()
	close(f.ch)
	f.ch = nil
}

func (f *fakeSub) state() (attempts, ended int, live bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts, f.ended, f.ch != nil
}

type notes struct {
	mu  sync.Mutex
	got []uint64
}

func (n *notes) add(index uint64) {
	n.mu.Lock()
	n.got = append(n.got, index)
	n.mu.Unlock()
}

func (n *notes) wait(t *testing.T, want ...uint64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		n.mu.Lock()
		got := append([]uint64(nil), n.got...)
		n.mu.Unlock()
		if len(got) >= len(want) {
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("events %v, want %v", got, want)
				}
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("events %v, want %v", got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func newWatch(n *notes) *watch {
	w := &watch{stop: make(chan struct{})}
	w.notify = func(index uint64) {
		if !w.stopping.Load() {
			n.add(index)
		}
	}
	return w
}

// A broken subscription means a new subscription and notify(0): events during the
// break are lost, the choice has to be rechecked.
func TestFollowResubscribes(t *testing.T) {
	var n notes
	w := newWatch(&n)
	f := &fakeSub{}
	if err := follow(w, f.subscribe, func(i int) int { return i }); err != nil {
		t.Fatal(err)
	}
	f.send(t, 3)
	n.wait(t, 3)

	f.mu.Lock()
	f.fail = 1 // the first resubscription fails, the second succeeds
	f.mu.Unlock()
	f.breakOff()
	n.wait(t, 3, 0)
	attempts, ended, live := f.state()
	if attempts != 3 || !live {
		t.Fatalf("%d attempts, subscription live: %v; want 3 and true", attempts, live)
	}
	if ended != 1 {
		t.Errorf("%d attempts closed, want 1, the broken one", ended)
	}

	f.send(t, 5)
	n.wait(t, 3, 0, 5)

	w.close()
	if _, ended, live := f.state(); live || ended != 2 {
		t.Errorf("after stop: %d closed, live %v", ended, live)
	}
}

// The first subscription failed: an error to the caller, no reader.
func TestFollowInitialError(t *testing.T) {
	var n notes
	w := newWatch(&n)
	f := &fakeSub{fail: 1}
	if err := follow(w, f.subscribe, func(i int) int { return i }); err == nil {
		t.Fatal("no error")
	}
	w.close() // does not hang
}

// stop during the pause before resubscribing does not wait for the pause to end.
func TestFollowStopDuringRetry(t *testing.T) {
	var n notes
	w := newWatch(&n)
	f := &fakeSub{}
	if err := follow(w, f.subscribe, func(i int) int { return i }); err != nil {
		t.Fatal(err)
	}
	f.breakOff()
	start := time.Now()
	w.close()
	if d := time.Since(start); d > resubscribeDelay/2 {
		t.Errorf("stop waited %v", d)
	}
	if attempts, _, _ := f.state(); attempts != 1 {
		t.Errorf("resubscribed after stop anyway: %d attempts", attempts)
	}
}

// A live netlink subscription: set up without privileges and removed without hanging.
func TestWatchNetworkLive(t *testing.T) {
	stop, err := WatchNetwork(func(uint64) {})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stop hung")
	}
	stop() // a second call is harmless
}
