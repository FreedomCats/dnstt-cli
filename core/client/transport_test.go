// Added by dnstt-cli (scripts/vendorcore/_files): not part of the dnstt upstream.

package client

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// trackClosedWait bounds how long track may take on a closed session: it either closes
// the socket and returns or reports the session closed, and the old code instead blocked
// on s.mu forever.
const trackClosedWait = 2 * time.Second

// A session can close between a successful dial and track of what the dial made. track
// then closes the connection itself, and a *trackedConn closes through forget, which takes
// s.mu: holding the mutex while closing deadlocks the closer against itself.
func TestSocketsTrackClosedSession(t *testing.T) {
	s := newSockets(&Config{})
	c1, c2 := net.Pipe()
	defer c2.Close()
	// Set before track closes c1: net.Pipe refuses a deadline once either end is closed.
	if err := c2.SetReadDeadline(time.Now().Add(trackClosedWait)); err != nil {
		t.Fatal(err)
	}

	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()

	tc := &trackedConn{Conn: c1, s: s}
	errc := make(chan error, 1)
	go func() { errc <- s.track(tc) }()

	var err error
	select {
	case err = <-errc:
	case <-time.After(trackClosedWait):
		// On the old code track holds s.mu while trackedConn.Close waits for it, so
		// nothing below may touch s: the mutex stays locked.
		t.Fatal("s.track of a closed session blocked")
	}
	t.Cleanup(s.closeAll)
	if !errors.Is(err, net.ErrClosed) {
		t.Fatalf("track on a closed session: %v, want %v", err, net.ErrClosed)
	}

	// The connection track refused must be closed, not left open.
	if _, err := c2.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Errorf("read from the peer of the refused connection: %v, want EOF", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.open) != 0 {
		t.Errorf("s.open has %d entries after track of a closed session, want none", len(s.open))
	}
}
