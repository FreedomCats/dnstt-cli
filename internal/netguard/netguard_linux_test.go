package netguard

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/FreedomCats/dnstt-cli/internal/linuxsys"
)

// Live checks for Linux; the ones shared with Windows are in live_test.go. No privileges
// needed: the binding does not require them, the mark does.

func physicalIndex(t *testing.T) uint32 {
	t.Helper()
	iface, ok, err := linuxsys.DefaultInterface(false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Skip("no IPv4 default route")
	}
	t.Logf("physical interface: %d %q", iface.Index, iface.Name)
	return iface.Index
}

// A socket bound to loopback does not get out: TCP times out, UDP gets no reply. A
// setsockopt refusal is a binding error, not a routing one.
func requireUnreachable(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("binding to loopback let the socket out")
	}
	if strings.Contains(err.Error(), "netguard:") {
		t.Fatalf("refused at setsockopt, not at routing: %v", err)
	}
}

// The kernel returns the index set by setter through the same getsockopt.
func TestSetterBindsToIndex(t *testing.T) {
	index := physicalIndex(t)
	c, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	rc, err := c.(*net.UDPConn).SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var g Guard
	var setErr, getErr error
	var got int
	err = rc.Control(func(fd uintptr) {
		if setErr = g.setter()(fd, false, index); setErr != nil {
			return
		}
		got, getErr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_BINDTOIFINDEX)
	})
	if err != nil || setErr != nil || getErr != nil {
		t.Fatalf("Control: %v, setter: %v, getsockopt: %v", err, setErr, getErr)
	}
	if uint32(got) != index {
		t.Fatalf("SO_BINDTOIFINDEX = %d, set %d", got, index)
	}
}

// IPv6 bound to an interface that has no outward IPv6 route fails at once instead of
// going out through another interface.
func TestFamilyWithoutRouteRefuses(t *testing.T) {
	if _, ok, err := linuxsys.DefaultInterface(true, 0); err != nil || ok {
		t.Skipf("the machine has an IPv6 default route (or error %v)", err)
	}
	var g Guard
	g.SetInterfaces(physicalIndex(t), physicalIndex(t))
	d := net.Dialer{Timeout: 4 * time.Second, Control: g.Control}
	start := time.Now()
	c, err := d.DialContext(context.Background(), "tcp6", "[2606:4700:4700::1111]:443")
	if err == nil {
		c.Close()
		t.Fatal("IPv6 without a route connected")
	}
	if !errors.Is(err, unix.ENETUNREACH) {
		t.Errorf("want ENETUNREACH: %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("refused after %v, want at once", d)
	}
}

// The mark without CAP_NET_ADMIN is EPERM, and the socket is not created: that is why the
// CLI sets Mark only when DNS protection is on (under sudo). Under root the mark is set
// and read back.
func TestMark(t *testing.T) {
	index := physicalIndex(t)
	g := Guard{Mark: SocketMark}
	g.SetInterfaces(index, 0)
	lc := net.ListenConfig{Control: g.Control}
	c, err := lc.ListenPacket(context.Background(), "udp4", "0.0.0.0:0")
	if os.Geteuid() != 0 {
		if err == nil {
			c.Close()
			t.Fatal("mark set without privileges")
		}
		if !errors.Is(err, unix.EPERM) || !strings.Contains(err.Error(), "SO_MARK") {
			t.Fatalf("want EPERM on SO_MARK: %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	rc, _ := c.(*net.UDPConn).SyscallConn()
	var mark int
	var getErr error
	_ = rc.Control(func(fd uintptr) { mark, getErr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK) })
	if getErr != nil || uint32(mark) != SocketMark {
		t.Fatalf("SO_MARK = %#x (%v), set %#x", mark, getErr, SocketMark)
	}
}
