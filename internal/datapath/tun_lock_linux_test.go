package datapath

import (
	"errors"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/FreedomCats/dnstt-cli/internal/identity"
)

// tunHelperEnv turns on the helper mode: the test binary, started by the test itself,
// behaves as a second dnstt-cli and reports the result by its exit code. The only mode is
// "lock": take the adapter lock.
const tunHelperEnv = "DNSTT_CLI_TUN_HELPER"

// tunLockEnv is the adapter lock name the test passes to the helper.
const tunLockEnv = "DNSTT_CLI_TUN_LOCK"

// realTunLockName is the lock of a running dnstt-cli: tunLockName before init replaces it
// with the test's one.
var realTunLockName string

// helperRefused is the exit code of a helper that was refused.
const helperRefused = 3

// init moves the tests to their own adapter lock: the real one (realTunLockName) is held
// by a running dnstt-cli, so the tests would fail falsely, and having taken it they would
// get in its way. The helper takes the name from the test.
//
// A TestMain cannot do this here: the package already has one (socks_test.go), and a
// second is a compile error.
func init() {
	realTunLockName = tunLockName
	tunLockName = "@" + identity.Name + "-test-tun-" + strconv.Itoa(os.Getpid())
	if name, ok := os.LookupEnv(tunLockEnv); ok {
		tunLockName = name
	}
	if mode, ok := os.LookupEnv(tunHelperEnv); ok {
		os.Exit(runTunHelper(mode))
	}
}

func runTunHelper(mode string) int {
	switch mode {
	case "lock":
		release, err := acquireTunLock()
		if err != nil {
			os.Stderr.WriteString(err.Error())
			return helperRefused
		}
		release()
		return 0
	default:
		os.Stderr.WriteString("unknown helper mode: " + mode)
		return 2
	}
}

// fromOtherProcess runs the helper in mode: nil when it was not refused, otherwise the
// text of the refusal.
func fromOtherProcess(t *testing.T, mode string) error {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), tunHelperEnv+"="+mode, tunLockEnv+"="+tunLockName)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == helperRefused {
		return errors.New(string(out))
	}
	if err != nil {
		t.Fatalf("helper: %v\n%s", err, out)
	}
	return nil
}

// Requires on Linux: the adapter is unsupported exactly when /dev/net/tun is missing, and
// the refusal carries the modprobe hint. The expectation comes from the device, not from a
// hard "nil": an ordinary machine may run without the tun module.
func TestTunRequiresLinux(t *testing.T) {
	w, err := NewTun(TunOptions{Dialer: dialerFunc(nil), TunDNS: netip.MustParseAddr("203.0.113.53")})
	if err != nil {
		t.Fatal(err)
	}
	r := w.Requires()
	if !r.Admin || !r.Netguard || r.DNSGuard {
		t.Errorf("Requires: %+v, want Admin and Netguard", r)
	}
	if _, err := os.Stat(tunDevice); err == nil {
		if r.Unsupported != nil {
			t.Errorf("Unsupported: %v, want nil with %s present", r.Unsupported, tunDevice)
		}
	} else {
		if r.Unsupported == nil {
			t.Fatalf("no %s, but Requires says the adapter is supported", tunDevice)
		}
		if !strings.Contains(r.Unsupported.Error(), "modprobe tun") {
			t.Errorf("Unsupported: %v, want the modprobe tun hint", r.Unsupported)
		}
	}
}

// A second dnstt-cli with the tun datapath is refused while the first is alive and gets
// through once the first has released the lock. Inside one process the acquisitions are
// counted: the run's reservation and the adapter's own acquisition do not conflict, and
// releasing the adapter does not release the reservation. The lock needs no root: an
// abstract socket takes no privilege.
func TestTunLockExcludesOtherProcess(t *testing.T) {
	if tunLockName == realTunLockName {
		t.Fatal("the test would take a running dnstt-cli's lock")
	}
	w, err := NewTun(TunOptions{Dialer: dialerFunc(nil), TunDNS: netip.MustParseAddr("203.0.113.53")})
	if err != nil {
		t.Fatal(err)
	}
	session, err := w.Reserve()
	if err != nil {
		t.Fatalf("the run's reservation: %v", err)
	}
	adapter, err := acquireTunLock()
	if err != nil {
		session()
		t.Fatalf("the adapter's own acquisition in the same process: %v", err)
	}

	if err := fromOtherProcess(t, "lock"); err == nil {
		t.Error("another process took the lock while the first one is alive")
	} else if !strings.Contains(err.Error(), "-datapath socks") {
		t.Errorf("the refusal does not mention -datapath socks: %v", err)
	}

	// The adapter is released, the run still holds the lock: another process is still
	// refused.
	adapter()
	adapter() // a repeated release does not decrement twice
	if err := fromOtherProcess(t, "lock"); err == nil {
		t.Error("the lock was released together with the adapter while the run still holds it")
	}

	session()
	if err := fromOtherProcess(t, "lock"); err != nil {
		t.Errorf("after the release another process could not take the lock: %v", err)
	}
}
