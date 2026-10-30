package supervisor

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FreedomCats/dnstt-cli/core/client"
	"github.com/FreedomCats/dnstt-cli/internal/resolvers"
)

// fakeSession is a session of the core in memory: its streams are pipes to an echo, or to
// serve if set.
type fakeSession struct {
	resolver string
	serve    func(net.Conn)

	received atomic.Uint64
	closed   atomic.Bool
	done     chan struct{}
	once     sync.Once
	err      error
}

func newFakeSession(resolver string) *fakeSession {
	return &fakeSession{resolver: resolver, done: make(chan struct{})}
}

func (f *fakeSession) OpenStream() (net.Conn, error) {
	select {
	case <-f.done:
		return nil, errors.New("opening stream: session ended")
	default:
	}
	a, b := net.Pipe()
	serve := f.serve
	if serve == nil {
		serve = func(c net.Conn) { io.Copy(c, c) }
	}
	go func() {
		defer b.Close()
		serve(b)
	}()
	return a, nil
}

func (f *fakeSession) Done() <-chan struct{} { return f.done }

func (f *fakeSession) Err() error {
	select {
	case <-f.done:
		return f.err
	default:
		return nil
	}
}

func (f *fakeSession) Stats() client.Stats {
	return client.Stats{BytesReceived: f.received.Load()}
}

// die ends the session by itself, as the core does when the server goes silent.
func (f *fakeSession) die(err error) {
	f.once.Do(func() {
		f.err = err
		close(f.done)
	})
}

func (f *fakeSession) Close() error {
	f.closed.Store(true)
	f.die(client.ErrClosed)
	return nil
}

// harness runs a Tunnel with a fake dialer over a list of resolvers.
type harness struct {
	t   *testing.T
	tun *Tunnel

	// dial decides how the n-th dial (from 0) through a resolver ends; nil — a session.
	dial func(resolver string, n int) error

	mu       sync.Mutex
	dials    []string
	sessions []*fakeSession
	sleeps   []time.Duration
	trans    []Transition
	changed  chan struct{} // gets a value on every transition or dial

	// sleep, if set, replaces the instant sleep of the harness.
	sleep func(ctx context.Context, d time.Duration) bool

	cancel context.CancelFunc
	result chan error
}

const errDialText = "dial failed"

var errDial = errors.New(errDialText)

func newHarness(t *testing.T, entries []string, mutate func(*Options)) *harness {
	t.Helper()
	list, err := resolvers.Expand(entries)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, changed: make(chan struct{}, 1000)}
	opts := Options{
		Resolvers:        resolvers.NewPool(list),
		HandshakeTimeout: 5 * time.Second,
		OnTransition: func(tr Transition) {
			h.mu.Lock()
			h.trans = append(h.trans, tr)
			h.mu.Unlock()
			h.changed <- struct{}{}
		},
	}
	if mutate != nil {
		mutate(&opts)
	}
	h.tun, err = New(opts)
	if err != nil {
		t.Fatal(err)
	}
	h.tun.dial = func(ctx context.Context, cfg client.Config) (Session, error) {
		name := cfg.Resolver.String()
		h.mu.Lock()
		n := 0
		for _, d := range h.dials {
			if d == name {
				n++
			}
		}
		h.dials = append(h.dials, name)
		dial := h.dial
		h.mu.Unlock()
		h.changed <- struct{}{}
		if dial != nil {
			if err := dial(name, n); err != nil {
				if err == errBlock {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				return nil, err
			}
		}
		s := newFakeSession(name)
		h.mu.Lock()
		h.sessions = append(h.sessions, s)
		h.mu.Unlock()
		return s, nil
	}
	h.tun.sleep = func(ctx context.Context, d time.Duration) bool {
		h.mu.Lock()
		h.sleeps = append(h.sleeps, d)
		sleep := h.sleep
		h.mu.Unlock()
		if sleep != nil {
			return sleep(ctx, d)
		}
		return ctx.Err() == nil
	}
	return h
}

// errBlock makes a dial wait until its ctx ends: a server that does not answer.
var errBlock = errors.New("block")

func (h *harness) run(ctx context.Context) {
	ctx, h.cancel = context.WithCancel(ctx)
	h.result = make(chan error, 1)
	go func() { h.result <- h.tun.Run(ctx) }()
	h.t.Cleanup(func() {
		h.cancel()
		<-h.result
	})
}

// wait waits until cond holds, checked on every change.
func (h *harness) wait(what string, cond func() bool) {
	h.t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		h.mu.Lock()
		ok := cond()
		h.mu.Unlock()
		if ok {
			return
		}
		select {
		case <-h.changed:
		case <-time.After(10 * time.Millisecond):
		case <-timeout:
			h.mu.Lock()
			defer h.mu.Unlock()
			h.t.Fatalf("waiting for %s: dials %q, transitions %v", what, h.dials, h.trans)
		}
	}
}

// waitState waits for a transition into state, the n-th one (from 1).
func (h *harness) waitState(state State, n int) Transition {
	h.t.Helper()
	var got Transition
	h.wait(string(state), func() bool {
		k := 0
		for _, tr := range h.trans {
			if tr.To == state {
				if k++; k == n {
					got = tr
					return true
				}
			}
		}
		return false
	})
	return got
}

func (h *harness) stop() error {
	h.t.Helper()
	h.cancel()
	select {
	case err := <-h.result:
		h.result <- err // for Cleanup
		return err
	case <-time.After(10 * time.Second):
		h.t.Fatal("Run did not return")
		return nil
	}
}

func (h *harness) session(i int) *fakeSession {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessions[i]
}

func (h *harness) dialList() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.dials...)
}

func (h *harness) sleepList() []time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]time.Duration(nil), h.sleeps...)
}
