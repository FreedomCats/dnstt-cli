package linuxsys

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The reverse path filter drops a packet if the reply to it would leave through a
// different interface than the one it arrived on. Under the tunnel the /1 routes lead
// into dnstt-cli, while the tunnel core's sockets are bound to the physical interface:
// replies from the resolvers arrive on the physical one, the return route to the source
// goes through dnstt-cli, and the strict filter drops them.
// The loose filter only checks that a route to the source exists, so they pass.

// RPFilter* are the values of IPv4 rp_filter.
const (
	RPFilterOff    = 0
	RPFilterStrict = 1
	RPFilterLoose  = 2
)

// procRoot, etcRoot are the roots of /proc and /etc; tests replace them.
var (
	procRoot = "/proc"
	etcRoot  = "/etc"
)

// ReversePathFilter returns the effective IPv4 rp_filter of interface name: the kernel
// takes the larger of net.ipv4.conf.all and the interface's own value.
func ReversePathFilter(name string) (int, error) {
	all, err := readSysctlInt("all")
	if err != nil {
		return 0, err
	}
	own, err := readSysctlInt(name)
	if err != nil {
		return 0, err
	}
	return max(all, own), nil
}

func readSysctlInt(iface string) (int, error) {
	path := filepath.Join(procRoot, "sys/net/ipv4/conf", iface, "rp_filter")
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("rp_filter %s: %w", iface, err)
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, fmt.Errorf("rp_filter %s: %q is not a number", iface, b)
	}
	return v, nil
}

// FirewalldStrictIPv6 reports whether firewalld runs with the strict IPv6 reverse path
// filter (IPv6_rpfilter in firewalld.conf: yes, the default, or strict). Its rule (fib
// saddr . mark . iif oif missing drop) drops IPv6 replies to the tunnel core's sockets
// just as strict rp_filter does for IPv4 (verified by experiment). The loose and
// *-forward modes leave incoming replies alone.
//
// firewalld is recognised by its process (comm "firewalld"): its rules are in nftables,
// but the service may be installed and not running.
func FirewalldStrictIPv6() (bool, error) {
	running, err := processRunning("firewalld")
	if err != nil || !running {
		return false, err
	}
	conf, err := os.ReadFile(filepath.Join(etcRoot, "firewalld/firewalld.conf"))
	if os.IsNotExist(err) {
		return true, nil // no file means the default, yes
	}
	if err != nil {
		return false, fmt.Errorf("firewalld.conf: %w", err)
	}
	switch strings.ToLower(confValue(conf, "IPv6_rpfilter", "yes")) {
	case "yes", "true", "strict":
		return true, nil
	default:
		return false, nil
	}
}

// confValue returns the value of key in a KEY=value file with "#" comments; def if the
// key is absent.
func confValue(conf []byte, key, def string) string {
	value := def
	sc := bufio.NewScanner(bytes.NewReader(conf))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(k) == key {
			value = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return value
}

// processRunning reports whether a process named comm exists.
func processRunning(comm string) (bool, error) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "comm"))
		if err == nil && strings.TrimSpace(string(b)) == comm {
			return true, nil
		}
	}
	return false, nil
}
