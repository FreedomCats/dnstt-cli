package datapath

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"github.com/FreedomCats/dnstt-cli/internal/identity"
	"github.com/FreedomCats/dnstt-cli/internal/linuxsys"
)

// netnsEnv enables the checks that create a real adapter and change the system network.
// Only in a separate network namespace (a container with NET_ADMIN and /dev/net/tun), not
// on the developer's machine:
//
//	CGO_ENABLED=0 go test -c -o build/out/datapath.test ./internal/datapath
//	docker run --rm --cap-add NET_ADMIN --device /dev/net/tun -e DNSTT_CLI_TEST_NETNS=1 \
//	  -v "$PWD/build/out":/dnstt-cli:ro ubuntu:latest /dnstt-cli/datapath.test -test.v
const netnsEnv = "DNSTT_CLI_TEST_NETNS"

// netnsDNS is the documentation DNS behind the fake exit.
var netnsDNS = netip.MustParseAddr("203.0.113.53")

func skipOutsideNetns(t *testing.T) {
	t.Helper()
	if os.Getenv(netnsEnv) != "1" || os.Geteuid() != 0 {
		t.Skipf("creates the adapter: only in a container, with %s=1 and root", netnsEnv)
	}
}

// netnsExit is the fake exit of the adapter test: a dial to netnsDNS goes to a DNS-over-TCP
// server that answers an A record, every other dial goes to echo (the same server for all
// destinations); the original destination is recorded.
type netnsExit struct {
	echo string

	mu   sync.Mutex
	seen []string
}

func (e *netnsExit) Dial(ctx context.Context, address string) (net.Conn, error) {
	e.mu.Lock()
	e.seen = append(e.seen, address)
	e.mu.Unlock()
	if address == netip.AddrPortFrom(netnsDNS, dnsPort).String() {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			serveARecord(server)
		}()
		return client, nil
	}
	var d net.Dialer
	return d.DialContext(ctx, "tcp", e.echo)
}

func (e *netnsExit) targets() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.seen)
}

// udpQuery sends query to the DNS address through the system network and returns one
// answer; an ICMP port unreachable or the deadline is the error. A connected socket is
// enough: the client and the resolver are one address each.
func udpQuery(t *testing.T, address string, query []byte, timeout time.Duration) ([]byte, error) {
	t.Helper()
	conn, err := net.Dial("udp4", address)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	if _, err := conn.Write(query); err != nil {
		return nil, err
	}
	buf := make([]byte, dnsMaxSize)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// sortedPrefixes returns prefixes sorted by address, for a comparison of route sets.
func sortedPrefixes(prefixes []netip.Prefix) []netip.Prefix {
	out := slices.Clone(prefixes)
	slices.SortFunc(out, func(a, b netip.Prefix) int { return a.Addr().Compare(b.Addr()) })
	return out
}

// adapterRoutes returns the routes of the main table through link that dnstt-cli
// installed: everything except the kernel's own routes for the link's addresses.
func adapterRoutes(t *testing.T, link netlink.Link) []netip.Prefix {
	t.Helper()
	list, err := netlink.RouteList(link, netlink.FAMILY_V4)
	if err != nil {
		t.Fatal(err)
	}
	var out []netip.Prefix
	for _, r := range list {
		if r.Dst == nil || r.Protocol == unix.RTPROT_KERNEL {
			continue
		}
		ones, _ := r.Dst.Mask.Size()
		ip, ok := netip.AddrFromSlice(r.Dst.IP)
		if !ok {
			continue
		}
		out = append(out, netip.PrefixFrom(ip.Unmap(), ones))
	}
	return sortedPrefixes(out)
}

// countString counts want in list.
func countString(list []string, want string) int {
	n := 0
	for _, s := range list {
		if s == want {
			n++
		}
	}
	return n
}

// A real adapter: a tuntap link, up, MTU 1500, host end 10.66.67.2/32 with peer
// 10.66.67.1/32, 10.66.67.1 routed into the adapter (not local), the two /1 routes into
// it. A TCP client to 203.0.113.9:80 reaches an echo server through the fake exit that
// sees the original destination; a UDP DNS query to 10.66.67.1:53 reaches the fake DNS
// behind the exit and the A record comes back. Suspend removes the routes and the
// netstack's address, Start restores them, Close removes the interface; the lock is held
// by the run's reservation and freed when it is released.
func TestTunAdapterNetns(t *testing.T) {
	skipOutsideNetns(t)
	const name = "dttun0"

	echo := echoServer(t)
	ex := &netnsExit{echo: echo}
	before := runtime.NumGoroutine()
	w, err := NewTun(TunOptions{Name: name, Dialer: ex, TunDNS: netnsDNS, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if r := w.Requires(); r.Unsupported != nil {
		t.Fatalf("Requires: %v", r.Unsupported)
	}
	release, err := w.Reserve()
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	defer release()

	if err := w.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := fromOtherProcess(t, "lock"); err == nil {
		t.Error("another process took the lock while the adapter is running")
	}

	link, err := netlink.LinkByName(name)
	if err != nil {
		t.Fatalf("interface %q: %v", name, err)
	}
	if kind := link.Type(); kind != "tuntap" {
		t.Errorf("link type %q, want tuntap", kind)
	}
	if mtu := link.Attrs().MTU; mtu != TunnelMTU {
		t.Errorf("MTU %d, want %d", mtu, TunnelMTU)
	}
	if link.Attrs().Flags&net.FlagUp == 0 {
		t.Error("the link is down")
	}
	index := link.Attrs().Index

	addrs, err := netlink.AddrList(link, netlink.FAMILY_V4)
	if err != nil {
		t.Fatal(err)
	}
	var host *netlink.Addr
	for i := range addrs {
		if addrs[i].IP.Equal(net.IP(tunHostAddress.AsSlice())) {
			host = &addrs[i]
		}
	}
	if host == nil {
		t.Fatalf("addresses %v, want the host end %v", addrs, tunHostAddress)
	}
	if ones, _ := host.Mask.Size(); ones != 32 {
		t.Errorf("the host end is /%d, want /32", ones)
	}
	if host.Peer == nil || !host.Peer.IP.Equal(net.IP(tunAddress.AsSlice())) {
		t.Fatalf("the host end's peer is %v, want %v", host.Peer, tunAddress)
	}
	if ones, _ := host.Peer.Mask.Size(); ones != 32 {
		t.Errorf("the peer is /%d, want /32", ones)
	}

	route, err := netlink.RouteGet(net.IP(tunAddress.AsSlice()))
	if err != nil {
		t.Fatalf("route get %v: %v", tunAddress, err)
	}
	if len(route) != 1 || route[0].LinkIndex != index || route[0].Type == unix.RTN_LOCAL {
		t.Errorf("route to %v: %+v, want into the adapter %d, not a local route", tunAddress, route, index)
	}
	if got, want := adapterRoutes(t, link), sortedPrefixes(tunRoutes4); !slices.Equal(got, want) {
		t.Errorf("routes after Start: %v, want %v", got, want)
	}

	// The system network into the tunnel: a TCP client reaches the echo server through
	// the exit, and the exit sees the original destination.
	conn, err := net.DialTimeout("tcp", "203.0.113.9:80", 10*time.Second)
	if err != nil {
		t.Fatalf("TCP through the adapter: %v", err)
	}
	echoThrough(t, conn, payload(20000))
	conn.Close()
	if got := ex.targets(); !slices.Contains(got, "203.0.113.9:80") {
		t.Errorf("the exit saw %v, want a dial to 203.0.113.9:80", got)
	}

	// The DNS listener on the adapter's address: a query to 10.66.67.1:53 goes as a
	// DNS-over-TCP exchange to tun_dns behind the exit and the A record comes back.
	query := dnsQuery(t, 0x1234, "t.example.com.")
	wantAnswer, err := dnsARecord(query, [4]byte{203, 0, 113, 80})
	if err != nil {
		t.Fatal(err)
	}
	dnsAddr := netip.AddrPortFrom(tunAddress, dnsPort).String()
	answer, err := udpQuery(t, dnsAddr, query, 10*time.Second)
	if err != nil {
		t.Fatalf("DNS query to %v: %v", dnsAddr, err)
	}
	if !bytes.Equal(answer, wantAnswer) {
		t.Fatalf("the DNS answer is %d bytes, want the %d-byte A record", len(answer), len(wantAnswer))
	}
	if got := ex.targets(); countString(got, netip.AddrPortFrom(netnsDNS, dnsPort).String()) != 1 {
		t.Errorf("the exit saw %v, want one dial to %v", got, netip.AddrPortFrom(netnsDNS, dnsPort))
	}

	// Between sessions: the routes and the netstack's address go away, the adapter stays.
	if err := w.Suspend(); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	if got := adapterRoutes(t, link); len(got) != 0 {
		t.Errorf("routes after Suspend: %v", got)
	}
	if _, err := netlink.LinkByName(name); err != nil {
		t.Errorf("the interface is gone after Suspend: %v", err)
	}
	if _, err := udpQuery(t, dnsAddr, query, time.Second); err == nil {
		t.Error("a DNS query was answered after Suspend")
	}

	// The next session brings the routes and the listener back.
	if err := w.Start(); err != nil {
		t.Fatalf("Start after Suspend: %v", err)
	}
	if got, want := adapterRoutes(t, link), sortedPrefixes(tunRoutes4); !slices.Equal(got, want) {
		t.Errorf("routes after the second Start: %v, want %v", got, want)
	}
	answer, err = udpQuery(t, dnsAddr, query, 10*time.Second)
	if err != nil {
		t.Fatalf("DNS query after the second Start: %v", err)
	}
	if !bytes.Equal(answer, wantAnswer) {
		t.Errorf("the DNS answer after the second Start is %d bytes, want %d", len(answer), len(wantAnswer))
	}
	if got := ex.targets(); countString(got, netip.AddrPortFrom(netnsDNS, dnsPort).String()) != 2 {
		t.Errorf("the exit saw %v, want two DNS dials", got)
	}

	// Close removes the interface and its routes; the lock lives while the reservation
	// does.
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := netlink.LinkByName(name); err == nil {
		t.Error("the interface remains after Close")
	}
	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range routes {
		if r.LinkIndex == index {
			t.Errorf("route %+v remains after Close", r)
		}
	}
	// The fd of the adapter is closed (the interface is gone), and its fdbased reader is
	// stopped: no datapath goroutine survives Close.
	waitGoroutines(t, before)
	if err := fromOtherProcess(t, "lock"); err == nil {
		t.Error("the lock is free while the reservation is held")
	}
	release()
	if err := fromOtherProcess(t, "lock"); err != nil {
		t.Errorf("the lock is not free after the release: %v", err)
	}
}

// resolvConfFile is the container's /etc/resolv.conf: docker mounts it as a separate file,
// nobody manages it, so the method is "overwrite".
const resolvConfFile = "/etc/resolv.conf"

func readResolvConf(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(resolvConfFile)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func logContains(lines []string, want string) bool {
	for _, line := range lines {
		if strings.Contains(line, want) {
			return true
		}
	}
	return false
}

// The system DNS through the real linuxsys: a crashed previous run left a marked
// resolv.conf and its copy; Start restores the copy and sets the tunnel DNS, Suspend and
// Close put the original back; the order is recover before set, and the log says what was
// restored.
func TestTunSystemDNSNetns(t *testing.T) {
	skipOutsideNetns(t)
	const name = "dttun1"

	orig, err := os.ReadFile(resolvConfFile)
	if err != nil {
		t.Fatal(err)
	}
	backup := resolvConfFile + "." + identity.Name + "-backup"
	t.Cleanup(func() {
		if err := os.WriteFile(resolvConfFile, orig, 0o644); err != nil {
			t.Errorf("restoring resolv.conf: %v", err)
		}
		os.Remove(backup)
	})

	// The test needs the overwrite method: with systemd-resolved or resolvconf the file
	// is not the place the DNS is set, and the whole sequence would check nothing.
	probe, err := linuxsys.DetectSystemDNS(name)
	if err != nil {
		t.Fatal(err)
	}
	if probe.Method() != linuxsys.DNSFile {
		t.Fatalf("DetectSystemDNS chose %v, want %v", probe.Method(), linuxsys.DNSFile)
	}

	// A run that crashed after it wrote its own resolv.conf: the marked file and the copy
	// of the original are still there.
	crashed := "# " + identity.Name + ": tunnel DNS; previous file is " + filepath.Base(backup) +
		", dnstt-cli will restore it on exit\nnameserver " + netnsDNS.String() + "\n"
	if err := os.WriteFile(resolvConfFile, []byte(crashed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup, orig, 0o644); err != nil {
		t.Fatal(err)
	}

	var logs []string
	w, err := NewTun(TunOptions{
		Name:   name,
		Dialer: dialerFunc(nil),
		TunDNS: netnsDNS,
		Logf: func(format string, args ...any) {
			logs = append(logs, fmt.Sprintf(format, args...))
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if err := w.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if conf := readResolvConf(t); !strings.Contains(conf, "\nnameserver "+tunAddress.String()+"\n") {
		t.Errorf("after Start resolv.conf is\n%s\nwant the tunnel DNS %v", conf, tunAddress)
	}
	if got, err := os.ReadFile(backup); err != nil || !bytes.Equal(got, orig) {
		t.Errorf("the backup after Start: %q, %v; want the original %q", got, err, orig)
	}
	if !logContains(logs, "previous resolv.conf restored") {
		t.Errorf("the log does not say the previous resolv.conf was restored: %v", logs)
	}

	if err := w.Suspend(); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	if conf := readResolvConf(t); conf != string(orig) {
		t.Errorf("after Suspend resolv.conf is\n%s\nwant the original\n%s", conf, orig)
	}
	if _, err := os.Stat(backup); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the backup remains after Suspend: %v", err)
	}

	if err := w.Start(); err != nil {
		t.Fatalf("Start after Suspend: %v", err)
	}
	if conf := readResolvConf(t); !strings.Contains(conf, "\nnameserver "+tunAddress.String()+"\n") {
		t.Errorf("after the second Start resolv.conf is\n%s\nwant the tunnel DNS %v", conf, tunAddress)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if conf := readResolvConf(t); conf != string(orig) {
		t.Errorf("after Close resolv.conf is\n%s\nwant the original\n%s", conf, orig)
	}
	if _, err := os.Stat(backup); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the backup remains after Close: %v", err)
	}
	if _, err := netlink.LinkByName(name); err == nil {
		t.Error("the interface remains after Close")
	}
}

// A foreign interface with the adapter's name is refused with its kind and the ip link del
// hint, and is never deleted; the lock is released.
func TestTunForeignLinkNetns(t *testing.T) {
	skipOutsideNetns(t)
	const name = "dttun2"

	dummy := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: name}}
	if err := netlink.LinkAdd(dummy); err != nil {
		t.Fatal(err)
	}
	defer netlink.LinkDel(dummy)

	w, err := NewTun(TunOptions{Name: name, Dialer: dialerFunc(nil), TunDNS: netnsDNS, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	err = w.Start()
	if err == nil || !strings.Contains(err.Error(), "dummy") || !strings.Contains(err.Error(), "ip link del "+name) {
		t.Fatalf("Start over a foreign interface: %v, want its kind and the ip link del hint", err)
	}
	if _, err := netlink.LinkByName(name); err != nil {
		t.Errorf("the foreign interface was deleted: %v", err)
	}
	if err := fromOtherProcess(t, "lock"); err != nil {
		t.Errorf("the lock was not released after the refusal: %v", err)
	}
}

// A persistent TUN with the adapter's name: createTUN refuses it with EBUSY (the window
// between the LinkKind check and TUNSETIFF), and Start refuses it as a foreign interface;
// the interface is never adopted or deleted.
func TestTunExclusiveNetns(t *testing.T) {
	skipOutsideNetns(t)
	const name = "dttun3"

	fd, err := unix.Open(tunDevice, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	ifr, err := unix.NewIfreq(name)
	if err != nil {
		unix.Close(fd)
		t.Fatal(err)
	}
	ifr.SetUint16(tunFlags)
	if err := unix.IoctlIfreq(fd, unix.TUNSETIFF, ifr); err != nil {
		unix.Close(fd)
		t.Fatal(err)
	}
	if err := unix.IoctlSetInt(fd, unix.TUNSETPERSIST, 1); err != nil {
		unix.Close(fd)
		t.Fatal(err)
	}
	unix.Close(fd)
	t.Cleanup(func() {
		if link, err := netlink.LinkByName(name); err == nil {
			netlink.LinkDel(link)
		}
	})

	fd, err = createTUN(name)
	if err == nil {
		unix.Close(fd)
		t.Fatal("createTUN adopted a foreign persistent TUN")
	}
	if !errors.Is(err, unix.EBUSY) {
		t.Errorf("createTUN: %v, want EBUSY", err)
	}

	w, err := NewTun(TunOptions{Name: name, Dialer: dialerFunc(nil), TunDNS: netnsDNS, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Start(); err == nil || !strings.Contains(err.Error(), "ip link del "+name) {
		t.Errorf("Start over a persistent foreign TUN: %v, want the foreign-interface refusal", err)
	}
	if _, err := netlink.LinkByName(name); err != nil {
		t.Errorf("the foreign persistent TUN was deleted: %v", err)
	}
}
