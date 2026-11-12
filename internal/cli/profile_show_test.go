package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/FreedomCats/dnstt-cli/internal/config"
)

// An unknown flag is rejected wherever it stands; -full is accepted before or after the
// profile name as it always was.
func TestProfileShowFlags(t *testing.T) {
	path := isolatedConnectConfig(t)
	err := config.SaveProfilesTo(path, &config.Profiles{
		DefaultProfile: "home",
		Profile: map[string]*config.Profile{
			"home": {
				Domain:    "t.example.com",
				Pubkey:    wizardTestPubkey,
				Resolvers: []string{"udp:192.0.2.1:53"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		args []string
		flag string
	}{
		{name: "before the name", args: []string{"-ful", "home"}, flag: "-ful"},
		{name: "after the name", args: []string{"home", "-bogus"}, flag: "-bogus"},
	} {
		t.Run("unknown flag "+tc.name, func(t *testing.T) {
			code, stdout, stderr := runCLIInput(t, "", append([]string{"profile", "show"}, tc.args...)...)
			if code != exitUsage {
				t.Fatalf("exit code %d, want %d; stdout:\n%s\nstderr:\n%s", code, exitUsage, stdout, stderr)
			}
			if want := fmt.Sprintf("unknown flag %q", tc.flag); !strings.Contains(stderr, want) {
				t.Fatalf("stderr %q does not contain %q", stderr, want)
			}
		})
	}

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "before the name", args: []string{"-full", "home"}},
		{name: "after the name", args: []string{"home", "-full"}},
	} {
		t.Run("-full "+tc.name, func(t *testing.T) {
			code, stdout, stderr := runCLIInput(t, "", append([]string{"profile", "show"}, tc.args...)...)
			if code != exitOK {
				t.Fatalf("exit code %d, want %d; stdout:\n%s\nstderr:\n%s", code, exitOK, stdout, stderr)
			}
			if !strings.Contains(stdout, "Domain: t.example.com") {
				t.Fatalf("-full did not show the domain in full:\n%s", stdout)
			}
		})
	}
}
