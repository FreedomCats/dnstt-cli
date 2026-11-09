package app

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/FreedomCats/dnstt-cli/internal/netguard"
)

// The order is the contract: the firewall decision, the interface choice with our own
// adapter excluded, the reverse path check, then the watcher; the choice is logged, and
// the returned stop is the watcher's.
func TestEnableNetguardOrder(t *testing.T) {
	var calls []string
	record := func(name string) { calls = append(calls, name) }
	var got netguard.WatchOptions
	stopped := false
	hooks := netguardHooks{
		checkFirewall: func() error {
			record("firewall")
			return nil
		},
		selectInterfaces: func(exclude uint64) (string, error) {
			record(fmt.Sprintf("select %d", exclude))
			return "IPv4 — eth0 (index 3), IPv6 — no route", nil
		},
		checkReversePath: func() error {
			record("reverse")
			return nil
		},
		watch: func(opts netguard.WatchOptions) (func(), error) {
			record("watch")
			got = opts
			return func() { stopped = true }, nil
		},
	}
	var lines []string
	stop, err := enableNetguard(NetguardOptions{
		Guard:   &netguard.Guard{},
		Exclude: func() uint64 { return 42 },
		Restart: func(error) {},
		Logf:    func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) },
	}, hooks)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"firewall", "select 42", "reverse", "watch"}; !slices.Equal(calls, want) {
		t.Fatalf("hooks ran as %v, want %v", calls, want)
	}
	if got.Exclude == nil {
		t.Fatal("the watcher does not exclude the adapter")
	}
	if exclude := got.Exclude(); exclude != 42 {
		t.Errorf("the watcher excludes %d, want the adapter's 42", exclude)
	}
	if got.Changed == nil {
		t.Fatal("the watcher has no Changed callback")
	}
	if !strings.Contains(strings.Join(lines, "\n"), "IPv4 — eth0 (index 3), IPv6 — no route") {
		t.Errorf("the choice is not logged: %q", lines)
	}
	stop()
	if !stopped {
		t.Error("the returned stop is not the watcher's")
	}
}

// An error at any step returns before the next one and leaves no watcher behind.
func TestEnableNetguardStopsAtErrors(t *testing.T) {
	want := errors.New("refused")
	for _, failing := range []string{"firewall", "select", "reverse", "watch"} {
		t.Run(failing, func(t *testing.T) {
			var calls []string
			step := func(name string) error {
				calls = append(calls, name)
				if name == failing {
					return want
				}
				return nil
			}
			stop, err := enableNetguard(NetguardOptions{
				Guard:   &netguard.Guard{},
				Restart: func(error) {},
			}, netguardHooks{
				checkFirewall:    func() error { return step("firewall") },
				selectInterfaces: func(uint64) (string, error) { return "choice", step("select") },
				checkReversePath: func() error { return step("reverse") },
				watch: func(netguard.WatchOptions) (func(), error) {
					if err := step("watch"); err != nil {
						return nil, err
					}
					return func() {}, nil
				},
			})
			if !errors.Is(err, want) {
				t.Fatalf("error %v, want %v", err, want)
			}
			if stop != nil {
				t.Error("a watcher was left behind")
			}
			last := ""
			for _, name := range calls {
				last = name
			}
			if last != failing {
				t.Errorf("steps ran as %v, want the sequence to stop at %s", calls, failing)
			}
		})
	}
}

// A folded network change with a network restarts once with a "network changed" reason; a
// change to no network does nothing and the next change still restarts.
func TestEnableNetguardChanged(t *testing.T) {
	var got netguard.WatchOptions
	var lines []string
	var reasons []error
	stop, err := enableNetguard(NetguardOptions{
		Guard:   &netguard.Guard{},
		Restart: func(err error) { reasons = append(reasons, err) },
		Logf:    func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) },
	}, netguardHooks{
		checkFirewall:    func() error { return nil },
		selectInterfaces: func(uint64) (string, error) { return "choice", nil },
		checkReversePath: func() error { return nil },
		watch: func(opts netguard.WatchOptions) (func(), error) {
			got = opts
			return func() {}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	eth0 := netguard.Choice{Index4: 3, Alias4: "eth0"}
	got.Changed(eth0, netguard.Choice{})
	if len(reasons) != 1 {
		t.Fatalf("restarts = %d, want one", len(reasons))
	}
	if msg := reasons[0].Error(); !strings.Contains(msg, "network changed") || !strings.Contains(msg, "eth0") {
		t.Errorf("restart reason %q, want a network change naming the interface", msg)
	}

	// No network: nothing to reconnect to, and the reason goes to the log.
	got.Changed(netguard.Choice{}, eth0)
	if len(reasons) != 1 {
		t.Fatalf("restarts after an empty choice = %d, want still one", len(reasons))
	}
	if !strings.Contains(strings.Join(lines, "\n"), "no network") {
		t.Errorf("the empty choice is not logged: %q", lines)
	}

	// The next network is followed.
	got.Changed(netguard.Choice{Index4: 4, Alias4: "eth1"}, netguard.Choice{})
	if len(reasons) != 2 {
		t.Fatalf("restarts after the next network = %d, want two", len(reasons))
	}
}

// A guard and a restart are required.
func TestEnableNetguardBadOptions(t *testing.T) {
	if _, err := enableNetguard(NetguardOptions{}, netguardHooks{}); err == nil {
		t.Error("no guard passed")
	}
	if _, err := enableNetguard(NetguardOptions{Guard: &netguard.Guard{}}, netguardHooks{}); err == nil {
		t.Error("no restart passed")
	}
}
