//go:build windows || linux

package netguard

import (
	"context"
	"errors"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Shared parts of the live binding checks on Windows and Linux. No privileges and no
// tunnel: binding to the loopback interface must keep the socket from getting out,
// binding to the physical one must let it out, and without the hook the socket goes as
// usual. No network means t.Skip, not a failure. The platform parts are physicalIndex and
// requireUnreachable.

const (
	tcpTarget = "1.1.1.1:443"
	udpTarget = "1.1.1.1:53"
)

// A DNS query for A example.com, 29 bytes.
var dnsQuery = []byte{
	0x12, 0x34, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0,
	0x00, 0x01, 0x00, 0x01,
}

func loopbackIndex(t *testing.T) uint32 {
	t.Helper()
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			return uint32(iface.Index)
		}
	}
	t.Fatal("no loopback interface")
	return 0
}

func dialTCP(control func(string, string, syscall.RawConn) error) error {
	d := net.Dialer{Timeout: 4 * time.Second, Control: control}
	c, err := d.DialContext(context.Background(), "tcp", tcpTarget)
	if err == nil {
		c.Close()
	}
	return err
}

func dialUDP(control func(string, string, syscall.RawConn) error) error {
	d := net.Dialer{Timeout: 4 * time.Second, Control: control}
	c, err := d.DialContext(context.Background(), "udp", udpTarget)
	if err != nil {
		return err
	}
	defer c.Close()
	return exchangeDNS(c.(net.PacketConn), nil)
}

// exchangeDNS sends the query and waits 3 s for a reply. to == nil means the socket is
// already connected.
func exchangeDNS(c net.PacketConn, to net.Addr) error {
	c.SetDeadline(time.Now().Add(3 * time.Second))
	var err error
	if to == nil {
		_, err = c.(net.Conn).Write(dnsQuery)
	} else {
		_, err = c.WriteTo(dnsQuery, to)
	}
	if err != nil {
		return err
	}
	buf := make([]byte, 512)
	n, _, err := c.ReadFrom(buf)
	if err != nil {
		return err
	}
	if n < 12 || buf[0] != 0x12 || buf[1] != 0x34 {
		return errors.New("wrong DNS reply")
	}
	return nil
}

// requireNet skips the test if 1.1.1.1 cannot be reached without the hook.
func requireNet(t *testing.T) {
	t.Helper()
	if err := dialTCP(nil); err != nil {
		t.Skipf("TCP does not get through without the hook, no network: %v", err)
	}
	if err := dialUDP(nil); err != nil {
		t.Skipf("UDP does not get through without the hook, no network: %v", err)
	}
}

// A dual-stack socket of the listening kind (ListenPacket "udp" ":0") sends to IPv4
// through the chosen interface; binding to loopback keeps it in.
func TestDualStackListener(t *testing.T) {
	requireNet(t)
	to, err := net.ResolveUDPAddr("udp", udpTarget)
	if err != nil {
		t.Fatal(err)
	}
	listen := func(index4 uint32) error {
		var g Guard
		g.SetInterfaces(index4, 0)
		lc := net.ListenConfig{Control: g.Control}
		c, err := lc.ListenPacket(context.Background(), "udp", ":0")
		if err != nil {
			return err
		}
		defer c.Close()
		return exchangeDNS(c, to)
	}
	t.Run("loopback", func(t *testing.T) { requireUnreachable(t, listen(loopbackIndex(t))) })
	t.Run("physical", func(t *testing.T) {
		if err := listen(physicalIndex(t)); err != nil {
			t.Fatal(err)
		}
	})
}

func TestLoopbackIndexBlocks(t *testing.T) {
	requireNet(t)
	var g Guard
	g.SetInterfaces(loopbackIndex(t), 0)
	t.Run("TCP", func(t *testing.T) { requireUnreachable(t, dialTCP(g.Control)) })
	t.Run("UDP", func(t *testing.T) { requireUnreachable(t, dialUDP(g.Control)) })
}

func TestPhysicalIndexPasses(t *testing.T) {
	requireNet(t)
	var g Guard
	g.SetInterfaces(physicalIndex(t), 0)
	if err := dialTCP(g.Control); err != nil {
		t.Errorf("TCP: %v", err)
	}
	if err := dialUDP(g.Control); err != nil {
		t.Errorf("UDP: %v", err)
	}
}

func TestUnknownIndexRefuses(t *testing.T) {
	var g Guard
	err := dialTCP(g.Control)
	if err == nil || !strings.Contains(err.Error(), "no physical interface") {
		t.Fatalf("error %v", err)
	}
}
