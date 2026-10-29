package netguard

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/FreedomCats/dnstt-cli/internal/winsys"
)

const supported = true

// bindPerFamily: a socket has separate bindings for IPv4 and IPv6.
const bindPerFamily = true

// The binding options, for error messages.
const (
	option4 = "IP_UNICAST_IF"
	option6 = "IPV6_UNICAST_IF"
)

// setter on Windows only binds: Mark is not used here.
func (g *Guard) setter() setter { return setUnicastIf }

// choose chooses the interface (winsys.DefaultInterface) without setting it: Watch
// first compares it with the previous one. exclude is the LUID of our own adapter.
func choose(exclude uint64) (Choice, error) {
	v4, ok4, err := winsys.DefaultInterface(false, exclude)
	if err != nil {
		return Choice{}, fmt.Errorf("netguard: choosing the IPv4 interface: %w", err)
	}
	v6, ok6, err := winsys.DefaultInterface(true, exclude)
	if err != nil {
		return Choice{}, fmt.Errorf("netguard: choosing the IPv6 interface: %w", err)
	}
	var c Choice
	if ok4 {
		c.Index4, c.Alias4 = v4.Index, v4.Alias
	}
	if ok6 {
		c.Index6, c.Alias6 = v6.Index, v6.Alias
	}
	return c, nil
}

// subscribe gives route and interface change events (winsys.WatchNetwork).
func subscribe(notify func(id uint64)) (func(), error) {
	return winsys.WatchNetwork(notify)
}

// setUnicastIf binds the socket to the interface. The byte order is as in wireguard-go,
// conn/bind_windows.go, bindSocketToInterface4/6: for IPv4 the index is in network order,
// for IPv6 in host order (a known trap). On Windows 11 a mixed-up order makes setsockopt
// fail (WSAEADDRNOTAVAIL / WSAEINVAL); on older builds, according to the documentation,
// it may be silently ignored — the test catches both outcomes.
func setUnicastIf(fd uintptr, ipv6 bool, index uint32) error {
	if ipv6 {
		return bindSocketToInterface6(windows.Handle(fd), index)
	}
	return bindSocketToInterface4(windows.Handle(fd), index)
}

// Carried over from golang.zx2c4.com/wireguard conn/bind_windows.go (MIT).
func bindSocketToInterface4(handle windows.Handle, interfaceIndex uint32) error {
	const IP_UNICAST_IF = 31
	/* MSDN says for IPv4 this needs to be in net byte order, so that it's like an IP address with leading zeros. */
	var bytes [4]byte
	binary.BigEndian.PutUint32(bytes[:], interfaceIndex)
	interfaceIndex = *(*uint32)(unsafe.Pointer(&bytes[0]))
	return windows.SetsockoptInt(handle, windows.IPPROTO_IP, IP_UNICAST_IF, int(interfaceIndex))
}

// Carried over from golang.zx2c4.com/wireguard conn/bind_windows.go (MIT).
func bindSocketToInterface6(handle windows.Handle, interfaceIndex uint32) error {
	const IPV6_UNICAST_IF = 31
	return windows.SetsockoptInt(handle, windows.IPPROTO_IPV6, IPV6_UNICAST_IF, int(interfaceIndex))
}

// v6Only reads IPV6_V6ONLY of the socket.
func v6Only(fd uintptr) (bool, error) {
	v, err := windows.GetsockoptInt(windows.Handle(fd), windows.IPPROTO_IPV6, windows.IPV6_V6ONLY)
	return v == 1, err
}
