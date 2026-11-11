package cli

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/FreedomCats/dnstt-cli/internal/app"
	"github.com/FreedomCats/dnstt-cli/internal/datapath"
)

// fakePrepareDatapath is an app.Datapath with both optional parts, Reserver and
// DNSRecoverer: it records the calls in order and returns the message or the error of the
// recovery.
type fakePrepareDatapath struct {
	calls []string
	done  string
	err   error
}

func (f *fakePrepareDatapath) Close() error                    { return nil }
func (f *fakePrepareDatapath) Requires() datapath.Requirements { return datapath.Requirements{} }

func (f *fakePrepareDatapath) Reserve() (func(), error) {
	f.calls = append(f.calls, "reserve")
	return func() { f.calls = append(f.calls, "release") }, nil
}

func (f *fakePrepareDatapath) RecoverSystemDNS() (string, error) {
	f.calls = append(f.calls, "recover")
	return f.done, f.err
}

var (
	_ app.Datapath          = (*fakePrepareDatapath)(nil)
	_ datapath.Reserver     = (*fakePrepareDatapath)(nil)
	_ datapath.DNSRecoverer = (*fakePrepareDatapath)(nil)
)

// plainDatapath is a datapath with neither optional part: socks.
type plainDatapath struct{}

func (plainDatapath) Close() error                    { return nil }
func (plainDatapath) Requires() datapath.Requirements { return datapath.Requirements{} }

// The reservation comes first, the recovery under it, and the release the helper returns
// lets the reservation go.
func TestPrepareDatapathOrder(t *testing.T) {
	dp := &fakePrepareDatapath{}
	release, err := prepareDatapath(dp, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"reserve", "recover"}; !slices.Equal(dp.calls, want) {
		t.Fatalf("calls %q, want %q", dp.calls, want)
	}
	release()
	if want := []string{"reserve", "recover", "release"}; !slices.Equal(dp.calls, want) {
		t.Errorf("after release: calls %q, want %q", dp.calls, want)
	}
}

// A recovery error stops before the first attempt: the error comes back as it is and the
// reservation is released.
func TestPrepareDatapathRecoverError(t *testing.T) {
	cause := errors.New("resolvconf -d tun.dnstt-cli: exit status 1")
	dp := &fakePrepareDatapath{err: cause}
	var lines []string
	logf := func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }

	release, err := prepareDatapath(dp, logf)
	if !errors.Is(err, cause) {
		t.Fatalf("err %v, want %v", err, cause)
	}
	if release != nil {
		t.Error("release is not nil after the recovery error")
	}
	if want := []string{"reserve", "recover", "release"}; !slices.Equal(dp.calls, want) {
		t.Errorf("calls %q, want %q", dp.calls, want)
	}
	if len(lines) != 0 {
		t.Errorf("log %q, want nothing", lines)
	}
}

// The message of the recovery is the line openTunAdapter prints, once; on a clean system
// there is no line.
func TestPrepareDatapathLogs(t *testing.T) {
	for _, tc := range []struct{ name, done string }{
		{"leftover", "record tun.dnstt-cli of the previous dnstt-cli run removed"},
		{"clean", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dp := &fakePrepareDatapath{done: tc.done}
			var lines []string
			logf := func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
			release, err := prepareDatapath(dp, logf)
			if err != nil {
				t.Fatal(err)
			}
			defer release()

			var want []string
			if tc.done != "" {
				want = []string{"[TUN] " + tc.done}
			}
			if !slices.Equal(lines, want) {
				t.Errorf("log %q, want %q", lines, want)
			}
		})
	}
}

// socks has neither interface: nothing is called and the release is a no-op.
func TestPrepareDatapathPlain(t *testing.T) {
	release, err := prepareDatapath(plainDatapath{}, func(string, ...any) {
		t.Error("logged for a datapath with neither Reserver nor DNSRecoverer")
	})
	if err != nil {
		t.Fatal(err)
	}
	if release == nil {
		t.Fatal("release is nil")
	}
	release()
}
