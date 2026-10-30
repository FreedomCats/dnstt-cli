package linuxsys

import (
	"errors"
	"maps"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/nftables"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// netnsEnv enables the tests that change the network: they create an interface and a
// default route. Only in a separate network namespace (a container with NET_ADMIN), not
// on the developer's machine:
//
//	CGO_ENABLED=0 go test -c -o build/out/linuxsys.test ./internal/linuxsys
//	docker run --rm --cap-add NET_ADMIN -e DNSTT_CLI_TEST_NETNS=1 \
//	  -v "$PWD/build/out":/dnstt-cli:ro ubuntu:latest /dnstt-cli/linuxsys.test -test.v
const netnsEnv = "DNSTT_CLI_TEST_NETNS"

// A new interface with a better default route than the previous one: the subscription
// brings events with its index, the choice moves to it, and after removal moves back.
func TestWatchNetworkEventsNetns(t *testing.T) {
	if os.Getenv(netnsEnv) != "1" || os.Geteuid() != 0 {
		t.Skipf("changes the network: only in a container, with %s=1 and root", netnsEnv)
	}
	before, ok, err := DefaultInterface(false, 0)
	if err != nil || !ok {
		t.Fatalf("initial choice: %+v %v %v", before, ok, err)
	}
	demoteDefault(t, before.Index)

	var mu sync.Mutex
	seen := map[uint64]int{}
	stop, err := WatchNetwork(func(index uint64) {
		mu.Lock()
		seen[index]++
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	// The callback keeps writing while the test prints the map: print a copy.
	snapshot := func() map[uint64]int {
		mu.Lock()
		defer mu.Unlock()
		return maps.Clone(seen)
	}

	link := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "ttwatch0"}}
	if err := netlink.LinkAdd(link); err != nil {
		t.Fatal(err)
	}
	defer netlink.LinkDel(link)
	if err := netlink.LinkSetUp(link); err != nil {
		t.Fatal(err)
	}
	addr, _ := netlink.ParseAddr("192.0.2.2/24")
	if err := netlink.AddrAdd(link, addr); err != nil {
		t.Fatal(err)
	}
	index := link.Attrs().Index
	route := &netlink.Route{LinkIndex: index, Gw: net.ParseIP("192.0.2.1"), Priority: 1}
	if err := netlink.RouteAdd(route); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		n := seen[uint64(index)]
		mu.Unlock()
		if n >= 3 { // link, address, route: at least one event each
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("events with index %d: %d, all: %v", index, n, snapshot())
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Logf("events: %v", snapshot())

	now, ok, err := DefaultInterface(false, 0)
	if err != nil || !ok || now.Index != uint32(index) {
		t.Fatalf("choice after a route with metric 1: %+v %v %v, want %d", now, ok, err, index)
	}
	if other, _, _ := DefaultInterface(false, uint64(index)); other.Index != before.Index {
		t.Errorf("with exclusion chose %+v, want the previous %+v", other, before)
	}

	if err := netlink.LinkSetDown(link); err != nil {
		t.Fatal(err)
	}
	if down, _, _ := DefaultInterface(false, 0); down.Index != before.Index {
		t.Errorf("down interface still chosen: %+v", down)
	}
}

// demoteDefault moves the default route via index to metric 100: docker has it with
// metric 0, and a new route could not beat it.
func demoteDefault(t *testing.T, index uint32) {
	t.Helper()
	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range routes {
		if !isDefault(r) || r.LinkIndex != int(index) || r.Priority != 0 {
			continue
		}
		demoted := r
		demoted.Priority = 100
		if err := netlink.RouteAdd(&demoted); err != nil {
			t.Fatal(err)
		}
		if err := netlink.RouteDel(&r); err != nil {
			t.Fatal(err)
		}
		return
	}
}

// DNS protection on real nftables: what it lets through and what it drops (a packet
// dropped on output is EPERM on send), opening again while the table is alive, closing
// IPv6 outward (local IPv6 stays open) and the table disappearing with the connection.
// The tunnel resolver is 192.0.2.53, a foreign one 198.51.100.1; no replies are needed.
// TCP is checked against the container's gateway: it refuses at once, while a dropped
// SYN is retransmitted by the kernel and the connection times out.
//
// The IPv6 part needs IPv6 in the container:
// --sysctl net.ipv6.conf.all.disable_ipv6=0.
func TestDNSGuardNetns(t *testing.T) {
	if os.Getenv(netnsEnv) != "1" || os.Geteuid() != 0 {
		t.Skipf("changes the network: only in a container, with %s=1 and root", netnsEnv)
	}
	const mark = 0x1076
	tunnelDNS := netip.MustParseAddr("192.0.2.53")

	g, err := OpenDNSGuard(mark)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if !dnsGuardTableExists(t) {
		t.Fatal("no table inet dnstt-cli after OpenDNSGuard")
	}
	if _, err := OpenDNSGuard(mark); err == nil || !strings.Contains(err.Error(), "another dnstt-cli seems to be running") {
		t.Errorf("second open while the table is alive: %v", err)
	}

	// Empty chains forbid nothing.
	expectSend(t, "udp", "198.51.100.1:53", 0, nil)

	if err := g.Enable([]netip.Addr{tunnelDNS}); err != nil {
		t.Fatal(err)
	}
	expectSend(t, "udp", "198.51.100.1:53", 0, unix.EPERM)
	expectSend(t, "udp", "192.0.2.53:53", 0, nil)
	expectSend(t, "udp", "127.0.0.1:53", 0, nil)
	expectSend(t, "udp", "198.51.100.1:443", 0, nil)
	expectSend(t, "udp", "198.51.100.1:53", mark, nil)
	expectSend(t, "udp", "198.51.100.1:53", mark+1, unix.EPERM)
	gw := defaultGateway(t)
	expectTCP(t, gw, 0, true)
	expectTCP(t, gw, mark, false)

	// A repeated Enable replaces the rules instead of adding to them.
	if err := g.Enable([]netip.Addr{netip.MustParseAddr("198.51.100.1")}); err != nil {
		t.Fatal(err)
	}
	expectSend(t, "udp", "198.51.100.1:53", 0, nil)
	expectSend(t, "udp", "192.0.2.53:53", 0, unix.EPERM)

	if err := g.Disable(); err != nil {
		t.Fatal(err)
	}
	expectSend(t, "udp", "192.0.2.53:53", 0, nil)
	expectTCP(t, gw, 0, false)
	if err := g.Disable(); err != nil {
		t.Errorf("repeated Disable: %v", err)
	}

	if ipv6Dummy(t, "ttdns0") {
		// IPv6 is closed outward; a tunnel resolver with an IPv6 address is not accepted,
		// even in ULA: there is no IPv6 route into the tunnel.
		g.BlockIPv6(true)
		if err := g.Enable([]netip.Addr{tunnelDNS, netip.MustParseAddr("fd00:db8:1::53")}); err != nil {
			t.Fatal(err)
		}
		expectSend(t, "udp", "[2001:db8:1::5]:443", 0, unix.EPERM)
		expectSend(t, "udp", "[2001:db8:1::5]:443", mark, nil)
		expectSend(t, "udp", "[::1]:443", 0, nil)
		expectSend(t, "udp", "192.0.2.53:53", 0, nil)
		expectSend(t, "udp", "[fd00:db8:1::53]:53", 0, unix.EPERM)
		// Local IPv6 as in "closed": link-local, ULA, multicast, ND and DHCPv6 pass, DNS
		// to the local network does not.
		expectSend(t, "udp", "[fe80::5%ttdns0]:443", 0, nil)
		expectSend(t, "udp", "[fd00:db8:1::5]:443", 0, nil)
		expectSend(t, "udp", "[ff02::fb%ttdns0]:5353", 0, nil)
		expectSend(t, "udp", "[fe80::5%ttdns0]:53", 0, unix.EPERM)
		expectSend(t, "udp", "[fd00:db8:1::5]:53", 0, unix.EPERM)
		expectICMPv6(t, "2001:db8:1::5", 128, unix.EPERM) // echo request
		expectICMPv6(t, "2001:db8:1::5", 135, nil)        // neighbor solicitation
		expectSendFrom(t, 546, "[2001:db8:1::5]:547", nil)
		expectSendFrom(t, 545, "[2001:db8:1::5]:547", unix.EPERM)
		g.BlockIPv6(false)
		if err := g.Enable([]netip.Addr{tunnelDNS}); err != nil {
			t.Fatal(err)
		}
		expectSend(t, "udp", "[2001:db8:1::5]:443", 0, nil)
	}

	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	if dnsGuardTableExists(t) {
		t.Error("table inet dnstt-cli left after Close")
	}
	expectSend(t, "udp", "198.51.100.1:53", 0, nil)
	if err := g.Enable([]netip.Addr{tunnelDNS}); err == nil {
		t.Error("Enable after Close succeeded")
	}
	if err := g.Close(); err != nil {
		t.Errorf("repeated Close: %v", err)
	}
}

// The kill switch on real nftables: in "closed" a public address gets EPERM, the local
// network, loopback, dnstt-cli sockets and DHCP pass, DNS does not pass even to the
// local network; transitions to DNS protection and back go without an empty table.
// The local network is 192.168.1.5 (it leaves by the default route) and the container's
// gateway (the docker network is 172.16/12).
func TestKillSwitchNetns(t *testing.T) {
	if os.Getenv(netnsEnv) != "1" || os.Geteuid() != 0 {
		t.Skipf("changes the network: only in a container, with %s=1 and root", netnsEnv)
	}
	const mark = 0x1076
	g, err := OpenDNSGuard(mark)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	if err := g.Lockdown(); err != nil {
		t.Fatal(err)
	}
	expectSend(t, "udp", "198.51.100.1:443", 0, unix.EPERM)
	expectSend(t, "udp", "198.51.100.1:443", mark, nil)
	expectSend(t, "udp", "192.168.1.5:443", 0, nil)
	expectSend(t, "udp", "192.168.1.5:53", 0, unix.EPERM)
	expectSend(t, "udp", "127.0.0.1:53", 0, nil)
	expectSend(t, "udp", "224.0.0.251:5353", 0, nil)
	expectSendFrom(t, 68, "198.51.100.1:67", nil)
	expectSendFrom(t, 69, "198.51.100.1:67", unix.EPERM)
	gw := defaultGateway(t)
	expectTCP(t, gw, 0, true)
	expectTCP(t, gw, mark, false)
	if err := g.Lockdown(); err != nil {
		t.Errorf("repeated Lockdown: %v", err)
	}

	if ipv6Dummy(t, "ttks6") {
		expectSend(t, "udp", "[2001:db8:1::5]:443", 0, unix.EPERM)
		expectSend(t, "udp", "[2001:db8:1::5]:443", mark, nil)
		expectSend(t, "udp", "[::1]:443", 0, nil)
		expectSend(t, "udp", "[ff02::fb%ttks6]:5353", 0, nil)
		expectSend(t, "udp", "[fd00:db8:1::5]:443", 0, nil)
		expectSend(t, "udp", "[fd00:db8:1::5]:53", 0, unix.EPERM)
		expectICMPv6(t, "2001:db8:1::5", 128, unix.EPERM) // echo request
		expectICMPv6(t, "2001:db8:1::5", 135, nil)        // neighbor solicitation
		expectSendFrom(t, 546, "[2001:db8:1::5]:547", nil)
	}

	// From "closed" to DNS protection: a public address is open, DNS only to the tunnel
	// resolver.
	if err := g.Enable([]netip.Addr{netip.MustParseAddr("192.0.2.53")}); err != nil {
		t.Fatal(err)
	}
	expectSend(t, "udp", "198.51.100.1:443", 0, nil)
	expectSend(t, "udp", "198.51.100.1:53", 0, unix.EPERM)
	expectSend(t, "udp", "192.0.2.53:53", 0, nil)

	// And back: the tunnel resolver is closed along with everything else.
	if err := g.Lockdown(); err != nil {
		t.Fatal(err)
	}
	expectSend(t, "udp", "192.0.2.53:53", 0, unix.EPERM)
	expectSend(t, "udp", "198.51.100.1:443", 0, unix.EPERM)

	if err := g.Disable(); err != nil {
		t.Fatal(err)
	}
	expectSend(t, "udp", "198.51.100.1:443", 0, nil)
	expectSend(t, "udp", "192.168.1.5:53", 0, nil)

	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	if err := g.Lockdown(); err == nil {
		t.Error("Lockdown after Close succeeded")
	}
}

// expectSendFrom sends a UDP packet to address (IPv4 or IPv6) from source port port.
func expectSendFrom(t *testing.T, port int, address string, want error) {
	t.Helper()
	d := &net.Dialer{LocalAddr: &net.UDPAddr{Port: port}}
	conn, err := d.Dial("udp", address)
	if err == nil {
		_, err = conn.Write([]byte("dnstt-cli"))
		conn.Close()
	}
	switch {
	case want == nil && err != nil:
		t.Errorf("udp :%d → %s: %v, want success", port, address, err)
	case want != nil && !errors.Is(err, want):
		t.Errorf("udp :%d → %s: %v, want %v", port, address, err, want)
	}
}

// expectICMPv6 sends an ICMPv6 message of type typ to address (the kernel computes the
// checksum); the body does not matter, the packet is checked on output.
func expectICMPv6(t *testing.T, address string, typ byte, want error) {
	t.Helper()
	conn, err := net.ListenPacket("ip6:ipv6-icmp", "::")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	msg := make([]byte, 24)
	msg[0] = typ
	_, err = conn.WriteTo(msg, &net.IPAddr{IP: net.ParseIP(address)})
	switch {
	case want == nil && err != nil:
		t.Errorf("icmpv6 type %d → %s: %v, want success", typ, address, err)
	case want != nil && !errors.Is(err, want):
		t.Errorf("icmpv6 type %d → %s: %v, want %v", typ, address, err, want)
	}
}

// expectSend sends a packet to address from a socket with mark mark (0 means none) and
// checks the send error against want: a UDP packet dropped on output is EPERM on send.
func expectSend(t *testing.T, network, address string, mark int, want error) {
	t.Helper()
	conn, err := markedDialer(mark).Dial(network, address)
	if err == nil {
		_, err = conn.Write([]byte("dnstt-cli"))
		conn.Close()
	}
	switch {
	case want == nil && err != nil:
		t.Errorf("%s %s, mark %#x: %v, want success", network, address, mark, err)
	case want != nil && !errors.Is(err, want):
		t.Errorf("%s %s, mark %#x: %v, want %v", network, address, mark, err, want)
	}
}

// expectTCP connects to port 53 of gateway gw: dropped means the SYN is dropped and the
// connection waits until the timeout; otherwise the gateway answers at once (success or
// refusal).
func expectTCP(t *testing.T, gw netip.Addr, mark int, dropped bool) {
	t.Helper()
	address := netip.AddrPortFrom(gw, 53).String()
	conn, err := markedDialer(mark).Dial("tcp", address)
	if err == nil {
		conn.Close()
	}
	var ne net.Error
	timeout := errors.As(err, &ne) && ne.Timeout()
	if timeout != dropped {
		t.Errorf("tcp %s, mark %#x: %v; want SYN dropped: %v", address, mark, err, dropped)
	}
}

func markedDialer(mark int) *net.Dialer {
	d := &net.Dialer{Timeout: 2 * time.Second}
	if mark != 0 {
		d.Control = func(_, _ string, c syscall.RawConn) error {
			var serr error
			if err := c.Control(func(fd uintptr) {
				serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, mark)
			}); err != nil {
				return err
			}
			return serr
		}
	}
	return d
}

// defaultGateway returns the gateway of the IPv4 default route (docker0 in a container).
func defaultGateway(t *testing.T) netip.Addr {
	t.Helper()
	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range routes {
		if isDefault(r) && r.Gw != nil {
			gw, _ := netip.AddrFromSlice(r.Gw.To4())
			return gw
		}
	}
	t.Fatal("no default route with a gateway")
	return netip.Addr{}
}

func dnsGuardTableExists(t *testing.T) bool {
	t.Helper()
	conn, err := nftables.New()
	if err != nil {
		t.Fatal(err)
	}
	tables, err := conn.ListTablesOfFamily(nftables.TableFamilyINet)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		if table.Name == dnsGuardTable {
			return true
		}
	}
	return false
}

// ipv6Dummy creates interface name with 2001:db8:1::2/64 and ULA fd00:db8:1::2/64, so
// that IPv6 packets have somewhere to go (link-local and multicast go through it too);
// false means IPv6 is off in the container and the check is skipped. Each test has its
// own name: net caches the zone index (%name) by name, and for a recreated interface
// with the same name, addresses with a zone would lead into the deleted one.
func ipv6Dummy(t *testing.T, name string) bool {
	t.Helper()
	link := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: name}}
	if err := netlink.LinkAdd(link); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { netlink.LinkDel(link) })
	if err := netlink.LinkSetUp(link); err != nil {
		t.Fatal(err)
	}
	for i, a := range []string{"2001:db8:1::2/64", "fd00:db8:1::2/64"} {
		addr, _ := netlink.ParseAddr(a)
		addr.Flags = unix.IFA_F_NODAD
		if err := netlink.AddrAdd(link, addr); err != nil {
			if i > 0 {
				t.Fatal(err)
			}
			t.Logf("IPv6 not checked: %v (needs --sysctl net.ipv6.conf.all.disable_ipv6=0)", err)
			return false
		}
	}
	return true
}
