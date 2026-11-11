//go:build linux

package datapath

import (
	"errors"
	"net/netip"
	"slices"
	"testing"

	"github.com/FreedomCats/dnstt-cli/internal/identity"
	"github.com/FreedomCats/dnstt-cli/internal/linuxsys"
)

// fakeSystemDNS is detectSystemDNS with no system behind it: Recover returns the message
// or the error of a crashed run, and Set and Revert record that the recovery did not call
// them.
type fakeSystemDNS struct {
	done       string
	recoverErr error
	calls      []string
}

func (f *fakeSystemDNS) String() string              { return "fake" }
func (f *fakeSystemDNS) Method() linuxsys.DNSMethod  { return linuxsys.DNSFile }
func (f *fakeSystemDNS) Warning() string             { return "" }
func (f *fakeSystemDNS) Applied() bool               { return false }
func (f *fakeSystemDNS) Set(int, []netip.Addr) error { f.calls = append(f.calls, "set"); return nil }
func (f *fakeSystemDNS) Revert(int) error            { f.calls = append(f.calls, "revert"); return nil }

func (f *fakeSystemDNS) Recover() (string, error) {
	f.calls = append(f.calls, "recover")
	return f.done, f.recoverErr
}

var _ systemDNS = (*fakeSystemDNS)(nil)

// stubDetectSystemDNS puts fake behind detectSystemDNS for the test and checks the adapter
// name the method passes; detectErr replaces the detection itself.
func stubDetectSystemDNS(t *testing.T, fake *fakeSystemDNS, detectErr error) {
	t.Helper()
	old := detectSystemDNS
	detectSystemDNS = func(name string) (systemDNS, error) {
		if detectErr != nil {
			return nil, detectErr
		}
		if name != identity.Name {
			t.Errorf("detectSystemDNS(%q), want the adapter name %q", name, identity.Name)
		}
		return fake, nil
	}
	t.Cleanup(func() { detectSystemDNS = old })
}

// newRecoverTun is a tun datapath with nothing opened: RecoverSystemDNS needs no adapter,
// no /dev/net/tun and no root.
func newRecoverTun(t *testing.T) *Tun {
	t.Helper()
	tun, err := NewTun(TunOptions{Dialer: dialerFunc(nil), TunDNS: netip.MustParseAddr("203.0.113.53")})
	if err != nil {
		t.Fatal(err)
	}
	return tun
}

func TestTunRecoverSystemDNS(t *testing.T) {
	t.Run("leftover", func(t *testing.T) {
		const msg = "record tun.dnstt-cli of the previous dnstt-cli run removed"
		fake := &fakeSystemDNS{done: msg}
		stubDetectSystemDNS(t, fake, nil)
		done, err := newRecoverTun(t).RecoverSystemDNS()
		if err != nil || done != msg {
			t.Fatalf("RecoverSystemDNS: %q, %v, want %q", done, err, msg)
		}
		if !slices.Equal(fake.calls, []string{"recover"}) {
			t.Errorf("calls %q, want only recover", fake.calls)
		}
	})

	t.Run("clean", func(t *testing.T) {
		fake := &fakeSystemDNS{}
		stubDetectSystemDNS(t, fake, nil)
		done, err := newRecoverTun(t).RecoverSystemDNS()
		if err != nil || done != "" {
			t.Errorf("RecoverSystemDNS: %q, %v, want nothing", done, err)
		}
	})

	t.Run("recover error", func(t *testing.T) {
		cause := errors.New("resolvconf -d tun.dnstt-cli: exit status 1")
		fake := &fakeSystemDNS{recoverErr: cause}
		stubDetectSystemDNS(t, fake, nil)
		done, err := newRecoverTun(t).RecoverSystemDNS()
		if err == nil || err.Error() != "system DNS: "+cause.Error() {
			t.Errorf("err %v, want system DNS: %v", err, cause)
		}
		if !errors.Is(err, cause) {
			t.Errorf("err %v does not wrap %v", err, cause)
		}
		if done != "" {
			t.Errorf("message %q with an error, want empty", done)
		}
	})

	t.Run("detection error", func(t *testing.T) {
		cause := errors.New("/etc/resolv.conf is a link to a file of an unknown generator")
		stubDetectSystemDNS(t, nil, cause)
		done, err := newRecoverTun(t).RecoverSystemDNS()
		if err == nil || err.Error() != "system DNS: "+cause.Error() {
			t.Errorf("err %v, want system DNS: %v", err, cause)
		}
		if !errors.Is(err, cause) {
			t.Errorf("err %v does not wrap %v", err, cause)
		}
		if done != "" {
			t.Errorf("message %q with an error, want empty", done)
		}
	})
}
