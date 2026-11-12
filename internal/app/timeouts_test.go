package app

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"
	"syscall"
	"testing"

	"github.com/FreedomCats/dnstt-cli/core/dns"
	"github.com/FreedomCats/dnstt-cli/internal/config"
	"github.com/FreedomCats/dnstt-cli/internal/supervisor"
)

// The session config of connect carries the policy keepalive on every session; the rest of
// the fields still come from the profile and the options.
func TestSessionConfigCarriesPolicyKeepAlive(t *testing.T) {
	domain, err := dns.ParseName("t.example.com")
	if err != nil {
		t.Fatal(err)
	}
	pubkey, err := hex.DecodeString(strings.Repeat("01", 32))
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name    string
		utls    string
		control func(network, address string, c syscall.RawConn) error
	}{
		{name: "profile default"},
		{
			name: "uTLS spec and control hook",
			utls: "none",
			control: func(string, string, syscall.RawConn) error {
				return nil
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prof := testProfile(config.Exit{})
			prof.UTLS = tc.utls
			var logged []string
			logf := func(format string, args ...any) {
				logged = append(logged, fmt.Sprintf(format, args...))
			}
			opts := TunnelOptions{Profile: prof, Control: tc.control, Logf: logf}

			cfg := sessionConfig(prof, domain, pubkey, opts)

			if cfg.KeepAliveTimeout != supervisor.DefaultKeepAliveTimeout || cfg.KeepAliveTimeout == 0 {
				t.Errorf("keepalive %v, want the policy value %v",
					cfg.KeepAliveTimeout, supervisor.DefaultKeepAliveTimeout)
			}
			if cfg.Domain.String() != domain.String() || !bytes.Equal(cfg.PubKey, pubkey) {
				t.Error("the tunnel identity was not carried through")
			}
			if cfg.UTLS != tc.utls {
				t.Errorf("uTLS %q, want %q", cfg.UTLS, tc.utls)
			}
			if tc.control == nil {
				if cfg.Control != nil {
					t.Error("a control hook appeared without one in the options")
				}
			} else if cfg.Control == nil || cfg.Control("tcp", "192.0.2.1:443", nil) != nil {
				t.Error("the control hook did not come from the options")
			}
			cfg.Logf("session %d", 1)
			if len(logged) != 1 || logged[0] != "session 1" {
				t.Errorf("Logf wrote %v, want the options' logger", logged)
			}
		})
	}
}

// sessionConfig carries the profile's payload_size, so the first session and every redial
// get the cap.
func TestSessionConfigCarriesPayloadSize(t *testing.T) {
	domain, err := dns.ParseName("t.example.com")
	if err != nil {
		t.Fatal(err)
	}
	pubkey, err := hex.DecodeString(strings.Repeat("01", 32))
	if err != nil {
		t.Fatal(err)
	}

	for _, size := range []int{0, 120} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			prof := testProfile(config.Exit{})
			prof.PayloadSize = size
			cfg := sessionConfig(prof, domain, pubkey, TunnelOptions{Profile: prof})
			if cfg.PayloadSize != size {
				t.Errorf("PayloadSize = %d, want %d", cfg.PayloadSize, size)
			}
		})
	}
}
