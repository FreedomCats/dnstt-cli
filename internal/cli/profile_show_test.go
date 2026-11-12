package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/FreedomCats/dnstt-cli/internal/config"
)

// A payload_size of 0 is the domain's maximum and is not printed; a non-zero cap is.
func TestProfileShowPayloadSize(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int
		want string
	}{
		{"zero is the maximum", 0, ""},
		{"set", 120, "Payload Size: 120"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := isolatedConnectConfig(t)
			err := config.SaveProfilesTo(path, &config.Profiles{
				DefaultProfile: "home",
				Profile: map[string]*config.Profile{
					"home": {
						Domain:      "t.example.com",
						Pubkey:      wizardTestPubkey,
						Resolvers:   []string{"udp:192.0.2.1:53"},
						PayloadSize: tc.size,
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			code, stdout, stderr := runCLIInput(t, "", "profile", "show", "home")
			if code != exitOK {
				t.Fatalf("exit code %d, want %d; stdout:\n%s\nstderr:\n%s", code, exitOK, stdout, stderr)
			}
			if tc.want == "" {
				if strings.Contains(stdout, "Payload Size") {
					t.Fatalf("Payload Size printed at 0:\n%s", stdout)
				}
				return
			}
			if !strings.Contains(stdout, tc.want) {
				t.Fatalf("stdout does not contain %q:\n%s", tc.want, stdout)
			}
		})
	}
}

// An SSH profile prints the pinned fingerprint and the secret's reference, never the key
// itself, with and without -full.
func TestProfileShowSSHKey(t *testing.T) {
	path := isolatedConnectConfig(t)
	store := config.NewSecretStoreFor(path)
	key := generatedSSHKey(t)
	ref, err := store.Put("ssh-home.key", key)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.SaveProfilesTo(path, &config.Profiles{
		DefaultProfile: "ssh-home",
		Profile: map[string]*config.Profile{
			"ssh-home": {
				Domain:    "t.example.com",
				Pubkey:    wizardTestPubkey,
				Resolvers: []string{"udp:192.0.2.1:53"},
				Exit:      config.Exit{Type: "ssh", User: "synthetic-user", HostKey: cliTestHostKey, KeyRef: ref},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// A line of the key's body: the output must not contain any part of the key material.
	lines := strings.Split(strings.TrimSpace(key), "\n")
	body := lines[len(lines)/2]
	for _, args := range [][]string{
		{"profile", "show", "ssh-home"},
		{"profile", "show", "-full", "ssh-home"},
	} {
		t.Run(strings.Join(args[1:], " "), func(t *testing.T) {
			code, stdout, stderr := runCLIInput(t, "", args...)
			if code != exitOK {
				t.Fatalf("exit code %d, want %d; stdout:\n%s\nstderr:\n%s", code, exitOK, stdout, stderr)
			}
			for _, want := range []string{
				"Exit Host Key: " + cliTestHostKey,
				"Exit Key: (stored in " + ref + ")",
			} {
				if !strings.Contains(stdout, want) {
					t.Fatalf("stdout does not contain %q:\n%s", want, stdout)
				}
			}
			for _, private := range []string{"PRIVATE KEY", body} {
				if strings.Contains(stdout, private) {
					t.Fatalf("profile show printed the key:\n%s", stdout)
				}
			}
		})
	}
}

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
