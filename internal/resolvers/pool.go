package resolvers

import (
	"sync"
	"time"
)

// Health of the resolvers (architecture.md R5, §08): a resolver that fails the handshake
// or kills sessions repeatedly is put aside for a while, and the supervisor dials the next
// one.

const (
	// strikesToAside is how many failures in a row put a resolver aside: a failed
	// handshake, or a session that died by itself within shortSession.
	strikesToAside = 3
	// shortSession: a session that died sooner counts against its resolver; one that
	// lived longer proves the resolver and clears its record.
	shortSession = time.Minute
	// asideFirst is the first time aside; every next one doubles, up to asideMax.
	asideFirst = time.Minute
	asideMax   = 16 * time.Minute
)

// Pool is the resolver list of a connect with the health of each resolver. It is safe for
// concurrent use.
type Pool struct {
	list []Resolver
	now  func() time.Time

	mu sync.Mutex
	st []health
}

type health struct {
	strikes int
	// level is how many times in a row the resolver was put aside: the next time aside
	// is asideFirst·2^level.
	level int
	until time.Time
}

// NewPool takes the list as Expand returned it.
func NewPool(list []Resolver) *Pool {
	return &Pool{
		list: append([]Resolver(nil), list...),
		now:  time.Now,
		st:   make([]health, len(list)),
	}
}

// Len is the number of resolvers.
func (p *Pool) Len() int { return len(p.list) }

// Resolver returns resolver i.
func (p *Pool) Resolver(i int) Resolver { return p.list[i] }

// Next is the index of the resolver to dial after resolver prev (-1 to start from the
// top): the first one after it, in the order of the list and round to the top, that is not
// put aside. If all are aside, the one that comes back first: the tunnel does not stop
// trying because every resolver failed — the server may have been down.
func (p *Pool) Next(prev int) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	n := len(p.list)
	best := -1
	for k := 1; k <= n; k++ {
		i := ((prev+k)%n + n) % n
		if !now.Before(p.st[i].until) {
			return i
		}
		if best < 0 || p.st[i].until.Before(p.st[best].until) {
			best = i
		}
	}
	return best
}

// Aside reports until when resolver i is put aside; false if it is not.
func (p *Pool) Aside(i int) (time.Time, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	until := p.st[i].until
	return until, p.now().Before(until)
}

// HandshakeFailed records that a session through resolver i did not complete its
// handshake: a timeout or an error of the transport.
func (p *Pool) HandshakeFailed(i int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.strike(i)
}

// SessionEnded records that a session through resolver i died by itself (the server went
// silent, the transport broke) after it lived for lived. A session closed by dnstt-cli —
// Ctrl+C, a network change — says nothing about the resolver and is not recorded.
func (p *Pool) SessionEnded(i int, lived time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if lived < shortSession {
		p.strike(i)
		return
	}
	p.st[i] = health{}
}

func (p *Pool) strike(i int) {
	h := &p.st[i]
	h.strikes++
	if h.strikes < strikesToAside {
		return
	}
	aside := min(asideFirst<<min(h.level, 8), asideMax)
	h.until = p.now().Add(aside)
	h.strikes = 0
	h.level++
}

// Reset clears the health of every resolver: after a network change the record is of
// another network.
func (p *Pool) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	clear(p.st)
}
