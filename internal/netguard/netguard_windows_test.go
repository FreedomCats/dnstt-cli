package netguard

import (
	"errors"
	"math/bits"
	"net"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/FreedomCats/dnstt-cli/internal/winsys"
)

// Live checks; the ones shared with Linux are in live_test.go, here are the platform
// parts and the byte order.

func physicalIndex(t *testing.T) uint32 {
	t.Helper()
	iface, ok, err := winsys.DefaultInterface(false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Skip("no IPv4 default route")
	}
	t.Logf("physical interface: %d %q", iface.Index, iface.Alias)
	return iface.Index
}

// The refusal must come at connect/send, not at setsockopt: a setsockopt error means the
// byte order is mixed up and the index turned into a non-existent one. If the socket got
// through, the order is mixed up on a build that silently ignores it. This test catches
// both byte order mistakes.
func requireUnreachable(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("binding to loopback let the socket out")
	}
	if strings.Contains(err.Error(), "netguard:") {
		t.Fatalf("refused at setsockopt, not at routing: %v", err)
	}
	if !errors.Is(err, windows.WSAENETUNREACH) {
		t.Logf("refusal is not WSAENETUNREACH (also fine): %v", err)
	}
}

// The byte order trap directly: for IPv4 host order is rejected, for IPv6 network order.
// IPv6 may have no outward route, so for IPv6 only the order is checked.
func TestByteOrder(t *testing.T) {
	lo := loopbackIndex(t)
	swapped := bits.ReverseBytes32(lo)

	withSocket := func(t *testing.T, network, address string, f func(windows.Handle) error) error {
		c, err := net.ListenPacket(network, address)
		if err != nil {
			t.Skipf("%s %s: %v", network, address, err)
		}
		defer c.Close()
		rc, err := c.(*net.UDPConn).SyscallConn()
		if err != nil {
			t.Fatal(err)
		}
		var setErr error
		if err := rc.Control(func(fd uintptr) { setErr = f(windows.Handle(fd)) }); err != nil {
			t.Fatal(err)
		}
		return setErr
	}

	t.Run("IPv4", func(t *testing.T) {
		if err := withSocket(t, "udp4", "0.0.0.0:0", func(h windows.Handle) error { return bindSocketToInterface4(h, lo) }); err != nil {
			t.Fatalf("network order (ours): %v", err)
		}
		// A raw setsockopt without swapping the bytes: what a mistake would do.
		err := withSocket(t, "udp4", "0.0.0.0:0", func(h windows.Handle) error {
			return windows.SetsockoptInt(h, windows.IPPROTO_IP, 31, int(lo))
		})
		t.Logf("IPv4, host order: %v", err)
	})
	t.Run("IPv6", func(t *testing.T) {
		if err := withSocket(t, "udp6", "[::]:0", func(h windows.Handle) error { return bindSocketToInterface6(h, lo) }); err != nil {
			t.Fatalf("host order (ours): %v", err)
		}
		err := withSocket(t, "udp6", "[::]:0", func(h windows.Handle) error { return bindSocketToInterface6(h, swapped) })
		if err == nil {
			t.Fatalf("IPV6_UNICAST_IF=%#x (network order) accepted", swapped)
		}
		t.Logf("IPv6, network order: %v", err)
	})
}
