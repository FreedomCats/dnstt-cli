package netguard

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/FreedomCats/dnstt-cli/internal/linuxsys"
)

// ReversePathFilter and FirewalldStrictIPv6 read /proc and /etc: the tests replace them,
// as the detection tests of other packages do.
func replaceReversePathFilter(t *testing.T, f func(string) (int, error)) {
	t.Helper()
	saved := reversePathFilter
	reversePathFilter = f
	t.Cleanup(func() { reversePathFilter = saved })
}

func replaceFirewalldStrictIPv6(t *testing.T, f func() (bool, error)) {
	t.Helper()
	saved := firewalldStrictIPv6
	firewalldStrictIPv6 = f
	t.Cleanup(func() { firewalldStrictIPv6 = saved })
}

// The strict IPv4 rp_filter of the chosen interface is a refusal with the interface and
// both ways out: loosen the sysctl or use the socks datapath. The loose and the off value
// pass.
func TestCheckReversePath(t *testing.T) {
	t.Run("strict", func(t *testing.T) {
		var g Guard
		g.commit(Choice{Index4: 12, Alias4: "eth0", Index6: 13, Alias6: "eth1"})
		var seen []string
		replaceReversePathFilter(t, func(name string) (int, error) {
			seen = append(seen, name)
			return linuxsys.RPFilterStrict, nil
		})
		err := g.CheckReversePath()
		if err == nil {
			t.Fatal("a strict rp_filter passed")
		}
		for _, want := range []string{
			"eth0",
			"sudo sysctl -w net.ipv4.conf.all.rp_filter=2 net.ipv4.conf.eth0.rp_filter=2",
			"-datapath socks",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not say %q", err, want)
			}
		}
		if len(seen) != 1 || seen[0] != "eth0" {
			t.Errorf("the filter of %q was read, want only eth0", seen)
		}
	})

	for _, tc := range []struct {
		name  string
		value int
	}{
		{"loose", linuxsys.RPFilterLoose},
		{"off", linuxsys.RPFilterOff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var g Guard
			g.commit(Choice{Index4: 12, Alias4: "eth0"})
			replaceReversePathFilter(t, func(string) (int, error) { return tc.value, nil })
			if err := g.CheckReversePath(); err != nil {
				t.Errorf("%s rp_filter: %v", tc.name, err)
			}
		})
	}

	t.Run("read error", func(t *testing.T) {
		var g Guard
		g.commit(Choice{Index4: 12, Alias4: "eth0"})
		want := errors.New("rp_filter eth0: no such file")
		replaceReversePathFilter(t, func(string) (int, error) { return 0, want })
		err := g.CheckReversePath()
		if !errors.Is(err, want) {
			t.Fatalf("%v, want the read error wrapped", err)
		}
		if !strings.Contains(err.Error(), "eth0") {
			t.Errorf("error %q does not name the interface", err)
		}
	})

	t.Run("no IPv4 interface", func(t *testing.T) {
		var g Guard
		g.commit(Choice{Index6: 13, Alias6: "eth1"})
		replaceReversePathFilter(t, func(name string) (int, error) {
			t.Fatalf("rp_filter of %q was read without an IPv4 interface", name)
			return 0, nil
		})
		if err := g.CheckReversePath(); err != nil {
			t.Errorf("no IPv4 interface: %v", err)
		}
	})
}

// firewalld with the strict IPv6 reverse path filter denies IPv6 to the core's sockets and
// says so once; without it nothing changes; a detection error is returned.
func TestCheckFirewall(t *testing.T) {
	t.Run("strict", func(t *testing.T) {
		var g Guard
		var lines []string
		g.Logf = func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
		replaceFirewalldStrictIPv6(t, func() (bool, error) { return true, nil })
		if err := g.CheckFirewall(); err != nil {
			t.Fatal(err)
		}
		if !g.NoIPv6 {
			t.Error("NoIPv6 is not set")
		}
		if len(lines) != 1 || !strings.Contains(lines[0], "IPv6") {
			t.Errorf("log lines %q, want one about IPv6", lines)
		}
		if c := g.allowed(Choice{Index4: 12, Alias4: "eth0", Index6: 13, Alias6: "eth1"}); c.Index6 != 0 || c.Alias6 != "" {
			t.Errorf("the IPv6 interface is not dropped: %+v", c)
		}
	})

	t.Run("not strict", func(t *testing.T) {
		var g Guard
		var lines []string
		g.Logf = func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
		replaceFirewalldStrictIPv6(t, func() (bool, error) { return false, nil })
		if err := g.CheckFirewall(); err != nil {
			t.Fatal(err)
		}
		if g.NoIPv6 {
			t.Error("NoIPv6 is set without a strict filter")
		}
		if len(lines) != 0 {
			t.Errorf("log lines %q, want none", lines)
		}
	})

	t.Run("error", func(t *testing.T) {
		var g Guard
		want := errors.New("firewalld.conf: permission denied")
		replaceFirewalldStrictIPv6(t, func() (bool, error) { return false, want })
		if err := g.CheckFirewall(); !errors.Is(err, want) {
			t.Fatalf("%v, want the detection error", err)
		}
		if g.NoIPv6 {
			t.Error("NoIPv6 is set after a failed detection")
		}
	})
}
