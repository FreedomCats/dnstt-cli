package linuxsys

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// NetworkDNS returns the DNS servers the network gave the system: the ISP's resolver, set
// by DHCP or the network manager. The bootstrap resolver asks them for the names of
// resolvers (architecture.md R5): in a network that lets through only some resolvers, the
// ISP's is the one that answers.
//
// The list comes from /etc/resolv.conf. When it names the systemd-resolved stub, the
// servers behind it come from /run/systemd/resolve/resolv.conf; when it is the file
// dnstt-cli wrote itself (tun), from its copy. Loopback addresses — local stubs and
// caches — are left out: a query to them would go through the system after all.
func NetworkDNS() ([]netip.Addr, error) {
	conf, err := os.ReadFile(resolvConfPath())
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(string(conf), fileMarker) {
		if conf, err = os.ReadFile(backupPath()); err != nil {
			return nil, fmt.Errorf("copy of resolv.conf: %w", err)
		}
	}
	servers := nameservers(conf)
	if slices.Contains(servers, netip.MustParseAddr(resolvedStub)) {
		upstream, err := os.ReadFile(filepath.Join(runRoot, "systemd/resolve/resolv.conf"))
		if err != nil {
			return nil, fmt.Errorf("servers behind systemd-resolved: %w", err)
		}
		servers = nameservers(upstream)
	}
	var out []netip.Addr
	for _, a := range servers {
		if !a.IsLoopback() && !a.IsUnspecified() && !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	return out, nil
}

// nameservers are the addresses of the nameserver lines of resolv.conf, in order.
func nameservers(conf []byte) []netip.Addr {
	var out []netip.Addr
	for _, line := range strings.Split(string(conf), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "nameserver" {
			continue
		}
		if a, err := netip.ParseAddr(f[1]); err == nil {
			out = append(out, a.Unmap())
		}
	}
	return out
}
