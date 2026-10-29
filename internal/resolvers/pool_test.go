package resolvers

import (
	"testing"
	"time"
)

func testPool(t *testing.T, n int) (*Pool, *time.Time) {
	t.Helper()
	list := make([]Resolver, n)
	for i := range list {
		list[i] = Resolver{Transport: "udp", Address: "192.0.2." + string(rune('1'+i)) + ":53"}
	}
	p := NewPool(list)
	now := time.Unix(1000000, 0)
	p.now = func() time.Time { return now }
	return p, &now
}

func TestPoolNext(t *testing.T) {
	p, _ := testPool(t, 3)
	for _, tc := range []struct{ prev, want int }{{-1, 0}, {0, 1}, {1, 2}, {2, 0}} {
		if got := p.Next(tc.prev); got != tc.want {
			t.Errorf("Next(%d) = %d, want %d", tc.prev, got, tc.want)
		}
	}
	if got := NewPool(nil).Next(-1); got != -1 {
		t.Errorf("empty pool: Next = %d", got)
	}
}

// Three failed handshakes in a row put a resolver aside; the list goes round it.
func TestPoolAside(t *testing.T) {
	p, now := testPool(t, 3)
	p.HandshakeFailed(1)
	p.HandshakeFailed(1)
	if _, aside := p.Aside(1); aside {
		t.Fatal("aside after two failures")
	}
	if got := p.Next(0); got != 1 {
		t.Fatalf("Next(0) = %d before the third failure", got)
	}
	p.HandshakeFailed(1)
	until, aside := p.Aside(1)
	if !aside || until != now.Add(asideFirst) {
		t.Fatalf("Aside(1) = %v, %v", until, aside)
	}
	if got := p.Next(0); got != 2 {
		t.Errorf("Next(0) = %d, want 2", got)
	}

	*now = now.Add(asideFirst)
	if _, aside := p.Aside(1); aside {
		t.Error("still aside after its time")
	}
	if got := p.Next(0); got != 1 {
		t.Errorf("Next(0) = %d after the time aside", got)
	}

	// The next time aside is twice as long, up to asideMax.
	want := 2 * asideFirst
	for range 6 {
		for range strikesToAside {
			p.HandshakeFailed(1)
		}
		until, _ := p.Aside(1)
		if got := until.Sub(*now); got != want {
			t.Errorf("aside for %v, want %v", got, want)
		}
		*now = until
		want = min(2*want, asideMax)
	}
}

// Sessions that die soon count against the resolver; one that lives clears its record.
func TestPoolShortSessions(t *testing.T) {
	p, _ := testPool(t, 2)
	p.SessionEnded(0, 10*time.Second)
	p.HandshakeFailed(0)
	p.SessionEnded(0, 2*time.Minute) // a good session: the two strikes are gone
	p.SessionEnded(0, 10*time.Second)
	p.SessionEnded(0, 10*time.Second)
	if _, aside := p.Aside(0); aside {
		t.Fatal("aside, although a long session came between")
	}
	p.SessionEnded(0, shortSession-time.Second)
	if _, aside := p.Aside(0); !aside {
		t.Fatal("not aside after three short sessions")
	}
}

// When every resolver is aside, the one that comes back first is dialed.
func TestPoolAllAside(t *testing.T) {
	p, now := testPool(t, 3)
	for _, i := range []int{0, 2, 1} {
		for range strikesToAside {
			p.HandshakeFailed(i)
		}
		*now = now.Add(time.Second)
	}
	for prev := -1; prev < 3; prev++ {
		if got := p.Next(prev); got != 0 {
			t.Errorf("Next(%d) = %d, want 0 (the first back)", prev, got)
		}
	}
	p.Reset()
	if got := p.Next(0); got != 1 {
		t.Errorf("after Reset: Next(0) = %d, want 1", got)
	}
	if _, aside := p.Aside(0); aside {
		t.Error("aside after Reset")
	}
}
