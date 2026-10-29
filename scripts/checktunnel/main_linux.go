//go:build linux

// Command checktunnel prints a snapshot of the system state for a live check of
// dnstt-cli: what dnstt-cli puts into the system and what must be gone after it.
//
//	go run ./scripts/checktunnel [-label "before start"] [-name dnstt-cli] [-timeout 10s] [-offline]
//
// The output before dnstt-cli starts, while it runs and after it is the report of the
// scenario. Sections on Linux:
//
//   - dnstt-cli processes and the adapter lock (an abstract socket);
//   - interfaces: the up ones with addresses (except lo) and the tunnel adapter in any
//     state, with type, MTU, addresses;
//   - /0 and /1 routes of both families in all tables except local;
//   - routing rules (ip rule): dnstt-cli sets none of its own, non-standard ones belong
//     to someone else;
//   - the interface for the core's sockets: the one netguard would pick;
//   - external address and resolver, as on Windows (common.go);
//   - nftables: all tables, in detail inet dnstt-cli (DNS protection) and inet
//     dnstt-cli-outage (an outage table a live-run script may add to cut the network;
//     the repository has no such script);
//   - system DNS: resolv.conf, the method dnstt-cli would use to set the tunnel DNS,
//     resolvconf records, the copy of the overwritten file;
//   - rp_filter and the strict IPv6 reverse-path filter of firewalld.
//
// The command changes nothing in the system. Without root rights (not under sudo) the
// nftables section prints an error instead of data; the rest works.
package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/nftables"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"github.com/FreedomCats/dnstt-cli/internal/app"
	"github.com/FreedomCats/dnstt-cli/internal/linuxsys"
)

// tunLock is the abstract socket of the adapter lock
// (internal/datapath/tun_linux.go, tunLockName).
const tunLock = "@" + app.Name + "-tun"

// fileMarker is the start of the first line of a resolv.conf written by dnstt-cli
// (internal/linuxsys/dns_linux.go, fileMarker); the two must stay equal.
const fileMarker = "# " + app.Name + ": tunnel DNS"

// outageTable is the outage table of a live-run script that cuts the network (the
// repository has no such script).
const outageTable = app.Name + "-outage"

// Roots of /proc, /etc and /run; replaced in tests.
var (
	procRoot = "/proc"
	etcRoot  = "/etc"
	runRoot  = "/run"
)

type options struct {
	label   string
	name    string        // name of the tunnel adapter
	timeout time.Duration // for each network request
	offline bool          // without the external address and the resolver
}

func main() {
	var o options
	flag.StringVar(&o.label, "label", "", "snapshot label (for example, \"before start\")")
	flag.StringVar(&o.name, "name", app.Name, "name of the tunnel adapter")
	flag.DurationVar(&o.timeout, "timeout", 10*time.Second, "timeout of each network request")
	flag.BoolVar(&o.offline, "offline", false, "do not check the external address and the resolver")
	flag.Parse()
	if flag.NArg() > 0 {
		flag.Usage()
		os.Exit(2)
	}
	run(os.Stdout, o)
}

// run prints the snapshot. An error of one section is printed in that section and does
// not stop the others.
func run(w io.Writer, o options) {
	header(w, o.label)

	section(w, "dnstt-cli processes", func(w io.Writer) error { return printProcesses(w) })
	links, linksErr := netlink.LinkList()
	section(w, "Interfaces", func(w io.Writer) error {
		if linksErr != nil {
			return linksErr
		}
		return printInterfaces(w, links, o.name)
	})
	section(w, "Routes /0 and /1", func(w io.Writer) error { return printRoutes(w, links) })
	section(w, "Routing rules", func(w io.Writer) error { return printRules(w) })
	section(w, "Interface for the core's sockets", func(w io.Writer) error {
		printBypass(w, links, o.name)
		return nil
	})
	if !o.offline {
		printNetwork(w, o.timeout)
	}
	section(w, "nftables", func(w io.Writer) error { return printNftables(w) })
	section(w, "System DNS", func(w io.Writer) error {
		printDNS(w, o.name)
		return nil
	})
	section(w, "Reverse-path filter", func(w io.Writer) error {
		printReversePath(w, links, o.name)
		return nil
	})
}

// printProcesses prints the processes named dnstt-cli (comm) and the adapter lock.
func printProcesses(w io.Writer) error {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return err
	}
	n := 0
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		dir := filepath.Join(procRoot, e.Name())
		comm, err := os.ReadFile(filepath.Join(dir, "comm"))
		if err != nil || strings.TrimSpace(string(comm)) != app.Name {
			continue
		}
		n++
		status, _ := os.ReadFile(filepath.Join(dir, "status"))
		cmdline, _ := os.ReadFile(filepath.Join(dir, "cmdline"))
		fmt.Fprintf(w, "   PID %s, uid %s: %s\n", e.Name(), statusUID(status), cmdlineString(cmdline))
	}
	if n == 0 {
		fmt.Fprintln(w, "   none")
	}
	unixSockets, err := os.ReadFile(filepath.Join(procRoot, "net/unix"))
	if err != nil {
		fmt.Fprintf(w, "   lock %s: error: %v\n", tunLock, err)
		return nil
	}
	state := "free"
	if hasUnixSocket(unixSockets, tunLock) {
		state = "held"
	}
	fmt.Fprintf(w, "   lock %s: %s\n", tunLock, state)
	return nil
}

// statusUID is the effective uid from /proc/<pid>/status (the line "Uid: real
// effective saved fs").
func statusUID(status []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(status))
	for sc.Scan() {
		if rest, ok := strings.CutPrefix(sc.Text(), "Uid:"); ok {
			if f := strings.Fields(rest); len(f) >= 2 {
				return f[1]
			}
		}
	}
	return "?"
}

// cmdlineString is the command line from /proc/<pid>/cmdline: arguments separated by
// a zero byte.
func cmdlineString(b []byte) string {
	args := strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
	return strings.Join(args, " ")
}

// hasUnixSocket reports whether /proc/net/unix has a socket with path path (abstract
// ones are printed with "@" instead of the zero byte).
func hasUnixSocket(table []byte, path string) bool {
	sc := bufio.NewScanner(bytes.NewReader(table))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 8 && f[len(f)-1] == path {
			return true
		}
	}
	return false
}

// printInterfaces prints the up interfaces with addresses (except lo) and the tunnel
// adapter in any state.
func printInterfaces(w io.Writer, links []netlink.Link, name string) error {
	tunnelSeen := false
	for _, l := range links {
		a := l.Attrs()
		isTunnel := a.Name == name
		if a.Flags&net.FlagLoopback != 0 && !isTunnel {
			continue
		}
		addrs, err := netlink.AddrList(l, netlink.FAMILY_ALL)
		if err != nil {
			return fmt.Errorf("addresses of %s: %w", a.Name, err)
		}
		var ips []string
		for _, ad := range addrs {
			p, ok := ipNetPrefix(ad.IPNet)
			if !ok || (p.Addr().IsLinkLocalUnicast() && !isTunnel) {
				continue
			}
			ips = append(ips, p.String())
		}
		if !isTunnel && (a.Flags&net.FlagUp == 0 || len(ips) == 0) {
			continue
		}
		mark := ""
		if isTunnel {
			tunnelSeen = true
			mark = "  [tunnel " + name + "]"
		}
		fmt.Fprintf(w, "   %s (index %d, %s, link %s, MTU %d, %s)%s\n",
			a.Name, a.Index, adminState(a.Flags), a.OperState, a.MTU, l.Type(), mark)
		fmt.Fprintf(w, "      addresses: %s\n", listOrNone(ips))
	}
	if !tunnelSeen {
		fmt.Fprintf(w, "   no tunnel adapter %q\n", name)
	}
	return nil
}

func adminState(f net.Flags) string {
	if f&net.FlagUp != 0 {
		return "UP"
	}
	return "DOWN"
}

func ipNetPrefix(n *net.IPNet) (netip.Prefix, bool) {
	if n == nil {
		return netip.Prefix{}, false
	}
	addr, ok := netip.AddrFromSlice(n.IP)
	if !ok {
		return netip.Prefix{}, false
	}
	ones, _ := n.Mask.Size()
	return netip.PrefixFrom(addr.Unmap(), ones), true
}

// printRoutes prints the /0 and /1 routes of all tables except local. A route with
// several gateways is printed as a line for each.
func printRoutes(w io.Writer, links []netlink.Link) error {
	names := map[int]string{}
	for _, l := range links {
		names[l.Attrs().Index] = l.Attrs().Name
	}
	all, err := netlink.RouteListFiltered(netlink.FAMILY_ALL,
		&netlink.Route{Table: unix.RT_TABLE_UNSPEC}, netlink.RT_FILTER_TABLE)
	if err != nil {
		return err
	}
	var routes []route
	for _, r := range all {
		if r.Table == unix.RT_TABLE_LOCAL {
			continue
		}
		p, ok := routePrefix(r)
		if !ok || !interestingRoute(p) {
			continue
		}
		for _, hop := range routeHops(r) {
			routes = append(routes, route{
				prefix:  p,
				nextHop: hop.gw,
				iface:   names[hop.index],
				index:   uint32(hop.index),
				metric:  uint32(r.Priority),
				table:   r.Table,
				proto:   r.Protocol.String(),
			})
		}
	}
	sortRoutes(routes)
	if len(routes) == 0 {
		fmt.Fprintln(w, "   none")
	}
	for _, r := range routes {
		fmt.Fprintf(w, "   %-14s %-22s %s (index %d), metric %d, %s%s\n",
			r.prefix, hopString(r.nextHop), r.iface, r.index, r.metric, r.proto, tableSuffix(r.table))
	}
	return nil
}

// routePrefix is the destination of a route. netlink returns a default route with
// Dst = nil or with a zero prefix length, depending on the version and the family.
func routePrefix(r netlink.Route) (netip.Prefix, bool) {
	if r.Dst != nil {
		return ipNetPrefix(r.Dst)
	}
	switch r.Family {
	case unix.AF_INET:
		return netip.PrefixFrom(netip.IPv4Unspecified(), 0), true
	case unix.AF_INET6:
		return netip.PrefixFrom(netip.IPv6Unspecified(), 0), true
	}
	return netip.Prefix{}, false
}

type hop struct {
	index int
	gw    netip.Addr
}

func routeHops(r netlink.Route) []hop {
	if len(r.MultiPath) == 0 {
		return []hop{{index: r.LinkIndex, gw: ipAddr(r.Gw)}}
	}
	hops := make([]hop, 0, len(r.MultiPath))
	for _, nh := range r.MultiPath {
		hops = append(hops, hop{index: nh.LinkIndex, gw: ipAddr(nh.Gw)})
	}
	return hops
}

func ipAddr(ip net.IP) netip.Addr {
	a, _ := netip.AddrFromSlice(ip)
	return a.Unmap()
}

// tableSuffix is the table of a route, if it is not main.
func tableSuffix(table int) string {
	if table == unix.RT_TABLE_MAIN {
		return ""
	}
	return fmt.Sprintf(", table %d", table)
}

// printRules prints the routing rules of both families. dnstt-cli sets none of its own
// (it binds sockets rather than using fwmark).
func printRules(w io.Writer) error {
	for _, family := range []int{netlink.FAMILY_V4, netlink.FAMILY_V6} {
		rules, err := netlink.RuleList(family)
		if err != nil {
			return err
		}
		label := "IPv4"
		if family == netlink.FAMILY_V6 {
			label = "IPv6"
		}
		var lines []string
		extra := 0
		for _, r := range rules {
			s := ruleString(r)
			if standardRule(r) {
				s += "  [standard]"
			} else {
				extra++
			}
			lines = append(lines, s)
		}
		fmt.Fprintf(w, "   %s: non-standard: %d\n", label, extra)
		for _, l := range lines {
			fmt.Fprintf(w, "      %s\n", l)
		}
	}
	return nil
}

// ruleString is a rule in the style of ip rule: "32766: from all lookup main".
func ruleString(r netlink.Rule) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d:", rulePriority(r))
	if r.Invert {
		b.WriteString(" not")
	}
	b.WriteString(" from ")
	if r.Src != nil {
		b.WriteString(r.Src.String())
	} else {
		b.WriteString("all")
	}
	if r.Dst != nil {
		b.WriteString(" to " + r.Dst.String())
	}
	if r.Mark != 0 || r.Mask != nil {
		fmt.Fprintf(&b, " fwmark %#x", r.Mark)
		if r.Mask != nil {
			fmt.Fprintf(&b, "/%#x", *r.Mask)
		}
	}
	if r.IifName != "" {
		b.WriteString(" iif " + r.IifName)
	}
	if r.OifName != "" {
		b.WriteString(" oif " + r.OifName)
	}
	if r.UIDRange != nil {
		fmt.Fprintf(&b, " uidrange %d-%d", r.UIDRange.Start, r.UIDRange.End)
	}
	if r.SuppressPrefixlen >= 0 {
		fmt.Fprintf(&b, " suppress_prefixlength %d", r.SuppressPrefixlen)
	}
	if r.Goto >= 0 {
		fmt.Fprintf(&b, " goto %d", r.Goto)
	} else {
		b.WriteString(" lookup " + tableName(r.Table))
	}
	return b.String()
}

// rulePriority is the priority of a rule: for rule 0 the kernel does not send the
// priority attribute, and netlink leaves -1.
func rulePriority(r netlink.Rule) int {
	return max(r.Priority, 0)
}

func tableName(t int) string {
	switch t {
	case unix.RT_TABLE_LOCAL:
		return "local"
	case unix.RT_TABLE_MAIN:
		return "main"
	case unix.RT_TABLE_DEFAULT:
		return "default"
	}
	return strconv.Itoa(t)
}

// standardRule reports a rule the kernel sets itself: 0 local, 32766 main,
// 32767 default, without conditions.
func standardRule(r netlink.Rule) bool {
	plain := r.Src == nil && r.Dst == nil && r.Mark == 0 && r.Mask == nil && r.IifName == "" &&
		r.OifName == "" && r.UIDRange == nil && !r.Invert && r.Goto < 0 && r.SuppressPrefixlen < 0
	switch {
	case !plain:
		return false
	case rulePriority(r) == 0 && r.Table == unix.RT_TABLE_LOCAL,
		r.Priority == 32766 && r.Table == unix.RT_TABLE_MAIN,
		r.Priority == 32767 && r.Table == unix.RT_TABLE_DEFAULT:
		return true
	}
	return false
}

// printBypass prints the physical interface netguard would bind the core's sockets to
// (excluding the tunnel's own adapter).
func printBypass(w io.Writer, links []netlink.Link, name string) {
	var exclude uint64
	for _, l := range links {
		if l.Attrs().Name == name {
			exclude = uint64(l.Attrs().Index)
		}
	}
	for _, ipv6 := range []bool{false, true} {
		label := "IPv4"
		if ipv6 {
			label = "IPv6"
		}
		iface, ok, err := linuxsys.DefaultInterface(ipv6, exclude)
		switch {
		case err != nil:
			fmt.Fprintf(w, "   %s: error: %v\n", label, err)
		case !ok:
			fmt.Fprintf(w, "   %s: none (no default route)\n", label)
		default:
			fmt.Fprintf(w, "   %s: %s (index %d)\n", label, iface.Name, iface.Index)
		}
	}
}

// printNftables prints all nftables tables; the dnstt-cli and outage tables with their
// chains and the number of rules.
func printNftables(w io.Writer) error {
	conn, err := nftables.New()
	if err != nil {
		return err
	}
	tables, err := conn.ListTables()
	if err != nil {
		if errors.Is(err, unix.EPERM) {
			return fmt.Errorf("root rights needed (sudo): %w", err)
		}
		return err
	}
	if len(tables) == 0 {
		fmt.Fprintln(w, "   no tables")
		return nil
	}
	slices.SortFunc(tables, func(a, b *nftables.Table) int {
		return strings.Compare(tableTitle(a), tableTitle(b))
	})
	var ours []*nftables.Table
	var names []string
	for _, t := range tables {
		names = append(names, tableTitle(t))
		if t.Family == nftables.TableFamilyINet && (t.Name == app.Name || t.Name == outageTable) {
			ours = append(ours, t)
		}
	}
	fmt.Fprintf(w, "   tables: %s\n", strings.Join(names, ", "))
	if len(ours) == 0 {
		fmt.Fprintln(w, "   no inet dnstt-cli or inet "+outageTable+" tables")
		return nil
	}
	chains, err := conn.ListChains()
	if err != nil {
		return fmt.Errorf("chains: %w", err)
	}
	for _, t := range ours {
		owner := "no owner"
		if t.Flags&nftables.TableFlagOwner != 0 {
			owner = fmt.Sprintf("owner: netlink port %d", t.Owner)
		}
		fmt.Fprintf(w, "   %s: %s\n", tableTitle(t), owner)
		for _, c := range chains {
			if c.Table.Name != t.Name || c.Table.Family != t.Family {
				continue
			}
			rules, err := conn.GetRules(t, c)
			count := strconv.Itoa(len(rules))
			if err != nil {
				count = "error: " + err.Error()
			}
			fmt.Fprintf(w, "      %s: %s, rules %s\n", c.Name, chainHook(c), count)
		}
	}
	return nil
}

func tableTitle(t *nftables.Table) string {
	return familyName(t.Family) + " " + t.Name
}

func familyName(f nftables.TableFamily) string {
	switch f {
	case nftables.TableFamilyINet:
		return "inet"
	case nftables.TableFamilyIPv4:
		return "ip"
	case nftables.TableFamilyIPv6:
		return "ip6"
	case nftables.TableFamilyARP:
		return "arp"
	case nftables.TableFamilyNetdev:
		return "netdev"
	case nftables.TableFamilyBridge:
		return "bridge"
	}
	return fmt.Sprintf("family %d", f)
}

// chainHook is the hook, priority and policy of a base chain.
func chainHook(c *nftables.Chain) string {
	if c.Hooknum == nil {
		return "regular"
	}
	hooks := map[nftables.ChainHook]string{
		*nftables.ChainHookPrerouting:  "prerouting",
		*nftables.ChainHookInput:       "input",
		*nftables.ChainHookForward:     "forward",
		*nftables.ChainHookOutput:      "output",
		*nftables.ChainHookPostrouting: "postrouting",
	}
	hook, ok := hooks[*c.Hooknum]
	if !ok {
		hook = fmt.Sprintf("hook %d", *c.Hooknum)
	}
	s := "hook " + hook
	if c.Priority != nil {
		s += fmt.Sprintf(", priority %d", *c.Priority)
	}
	if c.Policy != nil {
		policy := "accept"
		if *c.Policy == nftables.ChainPolicyDrop {
			policy = "drop"
		}
		s += ", policy " + policy
	}
	return s
}

// printDNS prints resolv.conf, the dnstt-cli method and traces of dnstt-cli: the
// resolvconf record and the copy of the overwritten file.
func printDNS(w io.Writer, name string) {
	path := filepath.Join(etcRoot, "resolv.conf")
	if fi, err := os.Lstat(path); err != nil {
		fmt.Fprintf(w, "   %s: %v\n", path, err)
	} else if fi.Mode()&fs.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(path)
		if err != nil {
			target = "leads nowhere: " + err.Error()
		}
		fmt.Fprintf(w, "   %s → %s\n", path, target)
	} else {
		fmt.Fprintf(w, "   %s: regular file\n", path)
	}
	if b, err := os.ReadFile(path); err == nil {
		first, servers, search := resolvConfSummary(b)
		if first != "" {
			fmt.Fprintf(w, "      first line: %s\n", first)
		}
		fmt.Fprintf(w, "      nameserver: %s\n", listOrNone(servers))
		fmt.Fprintf(w, "      search domains: %d\n", search)
		if strings.HasPrefix(first, fileMarker) {
			fmt.Fprintln(w, "      file written by dnstt-cli")
		}
	}

	backup := path + "." + app.Name + "-backup"
	if _, err := os.Stat(backup); err == nil {
		fmt.Fprintf(w, "   copy %s: present\n", backup)
	} else {
		fmt.Fprintf(w, "   copy %s: absent\n", backup)
	}

	if s, err := linuxsys.DetectSystemDNS(name); err != nil {
		fmt.Fprintf(w, "   dnstt-cli method: error: %v\n", err)
	} else {
		fmt.Fprintf(w, "   dnstt-cli method: %s\n", s)
		if s.Warning() != "" {
			fmt.Fprintf(w, "      warning: %s\n", s.Warning())
		}
	}

	printResolvconfRecords(w, "tun."+name)
}

// resolvConfSummary returns the first comment line, the servers and the number of
// search domains from resolv.conf.
func resolvConfSummary(conf []byte) (first string, servers []string, search int) {
	sc := bufio.NewScanner(bytes.NewReader(conf))
	for i := 0; sc.Scan(); i++ {
		line := strings.TrimSpace(sc.Text())
		if i == 0 && strings.HasPrefix(line, "#") {
			first = line
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "nameserver":
			servers = append(servers, f[1])
		case "search":
			search = len(f) - 1
		}
	}
	return first, servers, search
}

// printResolvconfRecords prints the resolvconf records (Debian keeps them in interface,
// openresolv in interfaces); for the dnstt-cli record, also its servers.
func printResolvconfRecords(w io.Writer, record string) {
	for _, dir := range []string{"interface", "interfaces"} {
		full := filepath.Join(runRoot, "resolvconf", dir)
		entries, err := os.ReadDir(full)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			fmt.Fprintf(w, "   records %s: error: %v\n", full, err)
			continue
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		fmt.Fprintf(w, "   records %s: %s\n", full, listOrNone(names))
		if slices.Contains(names, record) {
			b, err := os.ReadFile(filepath.Join(full, record))
			if err != nil {
				fmt.Fprintf(w, "      %s: error: %v\n", record, err)
				continue
			}
			_, servers, _ := resolvConfSummary(b)
			fmt.Fprintf(w, "      %s: nameserver %s\n", record, listOrNone(servers))
		}
	}
}

// printReversePath prints rp_filter: all, default, the up interfaces with an IPv4
// address and the tunnel adapter; the larger of all and the interface value is in
// effect. rp_filter is about IPv4 only.
func printReversePath(w io.Writer, links []netlink.Link, name string) {
	all, errAll := readRPFilter("all")
	def, errDef := readRPFilter("default")
	fmt.Fprintf(w, "   all %s, default %s\n", rpValue(all, errAll), rpValue(def, errDef))
	for _, l := range links {
		a := l.Attrs()
		if a.Name != name {
			if a.Flags&net.FlagLoopback != 0 || a.Flags&net.FlagUp == 0 {
				continue
			}
			if v4, err := netlink.AddrList(l, netlink.FAMILY_V4); err == nil && len(v4) == 0 {
				continue
			}
		}
		own, err := readRPFilter(a.Name)
		if err != nil {
			fmt.Fprintf(w, "   %s: error: %v\n", a.Name, err)
			continue
		}
		eff := own
		if errAll == nil {
			eff = max(all, own)
		}
		fmt.Fprintf(w, "   %s: %d → effective %s\n", a.Name, own, rpValue(eff, nil))
	}
	strict, err := linuxsys.FirewalldStrictIPv6()
	switch {
	case err != nil:
		fmt.Fprintf(w, "   firewalld: error: %v\n", err)
	case strict:
		fmt.Fprintln(w, "   firewalld: strict IPv6 reverse-path filter, dnstt-cli core sockets are IPv4 only")
	default:
		fmt.Fprintln(w, "   firewalld: no strict IPv6 reverse-path filter")
	}
}

func readRPFilter(iface string) (int, error) {
	b, err := os.ReadFile(filepath.Join(procRoot, "sys/net/ipv4/conf", iface, "rp_filter"))
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}

func rpValue(v int, err error) string {
	if err != nil {
		return "error: " + err.Error()
	}
	switch v {
	case linuxsys.RPFilterOff:
		return "0 (off)"
	case linuxsys.RPFilterStrict:
		return "1 (strict)"
	case linuxsys.RPFilterLoose:
		return "2 (loose)"
	}
	return strconv.Itoa(v)
}
