package datapath

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/tcpip/link/fdbased"

	"github.com/FreedomCats/dnstt-cli/internal/exit"
	"github.com/FreedomCats/dnstt-cli/internal/identity"
	"github.com/FreedomCats/dnstt-cli/internal/linuxsys"
)

// tunDevice is the device through which the Linux kernel creates TUN adapters.
const tunDevice = "/dev/net/tun"

// tunFlags are the adapter's flags: TUN (IP packets, no Ethernet) and no PI header.
// IFF_VNET_HDR is deliberately absent: with it every packet would carry a virtio_net
// offload header, and the netstack expects a bare IP packet. For the same reason the
// wireguard-go tun.CreateTUN is not used: it enables offload.
const tunFlags = unix.IFF_TUN | unix.IFF_NO_PI

// tunLockName is the abstract Unix socket of the one adapter on the machine: it leaves no
// file and dies with the process, so a crashed run does not block the next one. Abstract
// names are per network namespace, as interface names are: dnstt-cli in a container does
// not conflict with one on the host. A variable for the tests: they must not take a
// running dnstt-cli's lock.
var tunLockName = "@" + identity.Name + "-tun"

// detectSystemDNS finds how to set the system DNS; a variable for the tests.
var detectSystemDNS = linuxsys.DetectSystemDNS

// tunUnsupported reports why the adapter cannot work on this system: the TUN device is
// missing when the kernel has no tun module.
func tunUnsupported() error {
	if _, err := os.Stat(tunDevice); err != nil {
		return fmt.Errorf("no %s: the Linux kernel has no TUN support (module tun: sudo modprobe tun)", tunDevice)
	}
	return nil
}

// openTunAdapter creates the adapter name: without an address and down; configure and
// addRoutes set the address, MTU and routes, setDNS the system DNS. On failure it undoes
// everything it has done.
//
// The system DNS on Linux is not a property of the adapter but of the system: the way to
// set it (systemd-resolved, resolvconf, overwriting /etc/resolv.conf) is chosen by
// linuxsys.DetectSystemDNS from the file's owner — before the adapter exists, so an
// unknown generator is a refusal of the attempt, not of a session.
//
// The adapter is not persistent: the interface lives while its descriptor is open, and
// it disappears after the process dies. A foreign interface with the same name is
// therefore somebody else's, and it is named in the error, not deleted.
func openTunAdapter(name string, dialer exit.Dialer, tunDNS netip.Addr, logf func(string, ...any)) (*tunAdapter, error) {
	unlock, err := acquireTunLock()
	if err != nil {
		return nil, err
	}
	kind, exists, err := linuxsys.LinkKind(name)
	if err != nil {
		unlock()
		return nil, fmt.Errorf("adapter %q: %w", name, err)
	}
	if exists {
		unlock()
		return nil, foreignLinkError(name, kind)
	}
	dns, err := detectSystemDNS(name)
	if err != nil {
		unlock()
		return nil, fmt.Errorf("system DNS: %w", err)
	}
	logf("[TUN] system DNS is set by: %s", dns)
	if w := dns.Warning(); w != "" {
		logf("[TUN] Warning: %s", w)
	}
	// Recover after the lock and before Set: only the lock tells that no other
	// dnstt-cli is running, and the DNS of a running one must not be removed.
	done, err := dns.Recover()
	if err != nil {
		unlock()
		return nil, fmt.Errorf("system DNS: %w", err)
	}
	if done != "" {
		logf("[TUN] %s", done)
	}
	fd, err := createTUN(name)
	if errors.Is(err, unix.EBUSY) {
		// The foreign interface appeared between LinkKind and TUNSETIFF.
		unlock()
		return nil, foreignLinkError(name, "tun")
	}
	if err != nil {
		unlock()
		return nil, fmt.Errorf("adapter %q: %w", name, err)
	}
	index, err := linuxsys.LinkIndex(name)
	if err != nil {
		unix.Close(fd)
		unlock()
		return nil, fmt.Errorf("adapter %q: %w", name, err)
	}
	// The fdbased endpoint does not own the fd (its New says so), so the adapter closes
	// it in close. It makes the fd non-blocking and reads from it until the NIC is
	// detached.
	link, err := fdbased.New(&fdbased.Options{
		FDs:                  []int{fd},
		MTU:                  TunnelMTU,
		EthernetHeader:       false,
		ProcessorsPerChannel: 1,
	})
	if err != nil {
		unix.Close(fd)
		unlock()
		return nil, fmt.Errorf("adapter %q: %w", name, err)
	}
	ns, err := newNetstack(link, dialer, tunDNS)
	if err != nil {
		// The reader of a fdbased endpoint is stopped by detaching the NIC, not by
		// closing the fd: Attach(nil) stops the dispatcher and wakes its ppoll, and
		// Wait then returns. The stack of a failed newNetstack is not reachable, so
		// this is the only way to stop the reader.
		link.Attach(nil)
		link.Wait()
		unix.Close(fd)
		unlock()
		return nil, fmt.Errorf("adapter %q: %w", name, err)
	}

	return &tunAdapter{
		id: uint64(index),
		configure: func() error {
			return linuxsys.ConfigurePointToPoint(index, tunHostAddress, tunAddress, TunnelMTU, logf)
		},
		addAddress:    func() error { return ns.addAddress(tunAddress) },
		removeAddress: func() error { ns.removeAddress(tunAddress); return nil },
		addRoutes:     func() error { return linuxsys.SetTunnelRoutes(index, tunRoutes4) },
		removeRoutes:  func() error { return linuxsys.ClearTunnelRoutes(index, tunRoutes4) },
		setDNS: func() error {
			if err := dns.Set(index, []netip.Addr{tunAddress}); err != nil {
				return err
			}
			logf("[TUN] system DNS (%s): %v", dns.Method(), tunAddress)
			return nil
		},
		revertDNS: func() error {
			if !dns.Applied() {
				return nil
			}
			if err := dns.Revert(index); err != nil {
				return err
			}
			logf("[TUN] system DNS restored (%s)", dns.Method())
			return nil
		},
		// The interface's address and routes go away with the interface when the last
		// descriptor of it is closed.
		close: func() error {
			// A fdbased reader is not stopped by closing the fd, and closing it first
			// could leave the reader in readv on a descriptor number that another
			// goroutine has already reused. The stack removes the NIC, which detaches
			// the endpoint and wakes the reader; Wait then returns. Only after that the
			// fd is closed and the lock released.
			ns.close()
			link.Wait()
			err := unix.Close(fd)
			unlock()
			return err
		},
	}, nil
}

// foreignLinkError is the refusal when the interface name already exists and was not
// created by dnstt-cli.
func foreignLinkError(name, kind string) error {
	return fmt.Errorf("adapter %q: the system already has an interface with this name (type %s) that was not created by %s; "+
		"if it is not needed, delete it: sudo ip link del %s", name, kind, identity.Name, name)
}

// createTUN opens /dev/net/tun and creates the adapter name with tunFlags.
// IFF_TUN_EXCL makes it a new one only: without it an existing persistent TUN with the
// same name would be silently adopted, with it the kernel answers EBUSY.
func createTUN(name string) (int, error) {
	fd, err := unix.Open(tunDevice, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("%s: %w", tunDevice, err)
	}
	ifr, err := unix.NewIfreq(name)
	if err != nil {
		unix.Close(fd)
		return -1, err
	}
	ifr.SetUint16(tunFlags | unix.IFF_TUN_EXCL)
	if err := unix.IoctlIfreq(fd, unix.TUNSETIFF, ifr); err != nil {
		unix.Close(fd)
		return -1, fmt.Errorf("TUNSETIFF: %w", err)
	}
	return fd, nil
}

// acquireTunLock takes the abstract socket of the one adapter on the machine and returns
// a function that releases it. Busy by another process means a dnstt-cli with the tun
// datapath is already running on this machine.
//
// Inside one process the acquisitions are counted: the caller holds the socket for the
// whole run (Tun.Reserve), and opening the adapter takes it once more.
func acquireTunLock() (release func(), err error) {
	tunLock.mu.Lock()
	defer tunLock.mu.Unlock()

	if tunLock.refs == 0 {
		l, err := net.Listen("unix", tunLockName)
		if errors.Is(err, syscall.EADDRINUSE) {
			return nil, errTunBusy
		}
		if err != nil {
			return nil, fmt.Errorf("adapter lock: %w", err)
		}
		tunLock.listener = l
	}
	tunLock.refs++

	var once sync.Once
	return func() {
		once.Do(func() {
			tunLock.mu.Lock()
			defer tunLock.mu.Unlock()
			tunLock.refs--
			if tunLock.refs == 0 {
				tunLock.listener.Close()
				tunLock.listener = nil
			}
		})
	}, nil
}

// tunLock is the lock socket and the number of acquisitions in this process.
var tunLock struct {
	mu       sync.Mutex
	listener net.Listener
	refs     int
}
