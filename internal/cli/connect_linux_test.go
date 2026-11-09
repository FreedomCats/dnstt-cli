//go:build linux

package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/FreedomCats/dnstt-cli/internal/config"
)

// The tun datapath of an ordinary user is refused before the reservation and before any
// dial, with both ways out; no SOCKS5 listener is opened and no dial is logged even with
// -v. The profile is socks: the flag is what makes the run tun.
func TestConnectTunNeedsRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the test checks the refusal of an ordinary user")
	}
	if _, err := os.Stat("/dev/net/tun"); err != nil {
		t.Skip("no /dev/net/tun: there is nothing to be root-tested against")
	}
	path := isolatedConnectConfig(t)
	p := &config.Profile{
		Domain: "t.example.com", Pubkey: strings.Repeat("01", 32),
		Resolvers: []string{"udp:192.0.2.53:53"},
		Datapath:  "socks",
		Exit:      config.Exit{Type: "socks5"},
		Socks:     config.Socks{Listen: "127.0.0.1:0"},
	}
	if err := config.SaveProfilesTo(path, &config.Profiles{DefaultProfile: "home", Profile: map[string]*config.Profile{"home": p}}); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLI(t, "connect", "-v", "-datapath", "tun")
	if code != exitError {
		t.Fatalf("exit code %d, want %d; stderr:\n%s", code, exitError, stderr)
	}
	for _, want := range []string{"sudo", "-datapath socks"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, stderr)
		}
	}
	// With -v on, a run that fell back to the profile's socks datapath would log its
	// listener, and a run that reached a dial would log the profile's resolver address.
	for _, early := range []string{"[SOCKS] listening", "192.0.2.53"} {
		if strings.Contains(stderr, early) || strings.Contains(stdout, early) {
			t.Errorf("the refused run still printed %q:\nstdout:\n%s\nstderr:\n%s", early, stdout, stderr)
		}
	}
}
