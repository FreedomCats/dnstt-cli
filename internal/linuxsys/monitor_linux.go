package linuxsys

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/vishvananda/netlink"
)

// receiveBuffer is how many netlink events can pile up in the socket before they are
// read. A network switch produces dozens of them; an overflow means ENOBUFS and a broken
// subscription (see follow). The kernel caps the value at net.core.rmem_max; that is not
// an error.
const receiveBuffer = 1 << 20

// resubscribeDelay is the pause before a new subscription when the previous one broke.
const resubscribeDelay = time.Second

// WatchNetwork subscribes via netlink to changes of routes, interfaces and addresses of
// both families and calls notify with the index of the interface the change touched; 0
// means the interface is unknown (a route with several gateways, a broken
// subscription). Counterpart of winsys.WatchNetwork.
//
// The events themselves decide nothing: the caller (netguard.Guard.Watch) collapses a
// burst and decides whether the network has changed.
//
// notify is called from several goroutines, possibly at the same time. stop removes the
// subscription and waits for notify calls already in progress; after stop there are no
// new ones.
func WatchNetwork(notify func(index uint64)) (stop func(), err error) {
	w := &watch{stop: make(chan struct{})}
	w.notify = func(index uint64) {
		if !w.stopping.Load() {
			notify(index)
		}
	}

	routes := func(done <-chan struct{}) (chan netlink.RouteUpdate, error) {
		ch := make(chan netlink.RouteUpdate, 64)
		return ch, netlink.RouteSubscribeWithOptions(ch, done,
			netlink.RouteSubscribeOptions{ReceiveBufferSize: receiveBuffer})
	}
	links := func(done <-chan struct{}) (chan netlink.LinkUpdate, error) {
		ch := make(chan netlink.LinkUpdate, 64)
		return ch, netlink.LinkSubscribeWithOptions(ch, done,
			netlink.LinkSubscribeOptions{ReceiveBufferSize: receiveBuffer})
	}
	addrs := func(done <-chan struct{}) (chan netlink.AddrUpdate, error) {
		ch := make(chan netlink.AddrUpdate, 64)
		return ch, netlink.AddrSubscribeWithOptions(ch, done,
			netlink.AddrSubscribeOptions{ReceiveBufferSize: receiveBuffer})
	}

	stop = w.close
	if err := follow(w, routes, func(u netlink.RouteUpdate) int { return u.LinkIndex }); err != nil {
		stop()
		return nil, err
	}
	if err := follow(w, links, func(u netlink.LinkUpdate) int { return int(u.Index) }); err != nil {
		stop()
		return nil, err
	}
	if err := follow(w, addrs, func(u netlink.AddrUpdate) int { return u.LinkIndex }); err != nil {
		stop()
		return nil, err
	}
	return stop, nil
}

// watch is what the three subscriptions share: stopping and waiting for the readers.
type watch struct {
	notify   func(index uint64)
	stopping atomic.Bool
	stop     chan struct{}
	once     sync.Once
	wg       sync.WaitGroup
}

func (w *watch) close() {
	w.once.Do(func() {
		w.stopping.Store(true)
		close(w.stop)
	})
	w.wg.Wait()
}

// follow subscribes and reads events until stop is called.
//
// netlink sends events into the channel with a blocking write and closes the channel
// when the socket is closed or receiving fails (ENOBUFS on buffer overflow). So the
// channel is read until it is closed, even after stop, and a close without stop is a
// break: the subscription is set up again until it succeeds or stop comes, and then
// notify(0) asks to recheck the choice, since events during the break are lost.
// Otherwise, after a single overflow, network changes would no longer be noticed.
func follow[T any](w *watch, subscribe func(done <-chan struct{}) (chan T, error), index func(T) int) error {
	ch, end, err := attempt(w, subscribe)
	if err != nil {
		return err
	}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		for {
			for u := range ch {
				w.notify(uint64(index(u)))
			}
			end()
			for {
				select {
				case <-w.stop:
					return
				case <-time.After(resubscribeDelay):
				}
				if ch, end, err = attempt(w, subscribe); err == nil {
					break
				}
			}
			w.notify(0)
		}
	}()
	return nil
}

// attempt is one subscription with its own done: end closes its socket (after a break
// it is no longer needed), and stop closes it too.
func attempt[T any](w *watch, subscribe func(done <-chan struct{}) (chan T, error)) (ch chan T, end func(), err error) {
	done := make(chan struct{})
	var once sync.Once
	end = func() { once.Do(func() { close(done) }) }
	ch, err = subscribe(done)
	if err != nil {
		end()
		return nil, nil, err
	}
	go func() {
		select {
		case <-w.stop:
			end()
		case <-done:
		}
	}()
	return ch, end, nil
}
