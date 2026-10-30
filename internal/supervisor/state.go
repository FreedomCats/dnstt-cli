// Package supervisor keeps the tunnel of one dnstt-cli connect up (architecture.md §08):
// it dials sessions of the core through the resolver list, checks the exit, notices a dead
// or silent session, reconnects with a backoff, restarts on a network change and stops on
// what another attempt cannot cure.
//
// The datapath lives the whole connect and opens its streams through Tunnel.OpenStream,
// which waits for the next session while there is none.
package supervisor

import (
	"errors"
	"time"
)

// State is the state of the tunnel, the diagram of architecture.md §08.
type State string

const (
	// StateIdle: nothing dialed yet.
	StateIdle State = "idle"
	// StateDialing: a session is dialed through a resolver, up to its handshake and the
	// check of the exit.
	StateDialing State = "dialing"
	// StateConnected: a session is up and the exit let us in.
	StateConnected State = "connected"
	// StateDegraded: the session is up, but the server has sent nothing for longer than
	// its keepalives allow. It ends by itself, either way: data comes back, or the core
	// gives up on the session after its keepalive timeout.
	StateDegraded State = "degraded"
	// StateReconnecting: an attempt or a session ended, and the next attempt waits for
	// its delay.
	StateReconnecting State = "reconnecting"
	// StateStopping: the tunnel is stopping for good. The final state.
	StateStopping State = "stopping"
)

// ErrNoData is the cause of StateDegraded.
var ErrNoData = errors.New("no data from the server")

// Transition is a change of state, for the output of connect.
type Transition struct {
	From, To State
	// Resolver is the resolver of the attempt or the session, by Name.
	Resolver string
	// Cause is why: the error of the attempt or the session for StateReconnecting and
	// StateStopping, ErrNoData for StateDegraded. nil — no cause (a stop on request).
	Cause error
	// Delay is the wait before the next attempt; StateReconnecting only.
	Delay time.Duration
}

// Delays between attempts: 1·2^n s, n failures in a row, up to maxBackoff (as in
// csqtt-cli).
const (
	baseBackoff = time.Second
	maxBackoff  = 64 * time.Second
)

// backoff counts the delay before the next attempt.
type backoff struct{ failures int }

// next is the delay after one more failure in a row.
func (b *backoff) next() time.Duration {
	d := baseBackoff << min(b.failures, 6) // 64 s is 1 << 6
	b.failures++
	return min(d, maxBackoff)
}

// reset: the tunnel works again, or the network is a new one.
func (b *backoff) reset() { b.failures = 0 }
