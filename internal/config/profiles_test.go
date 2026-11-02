package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreedomCats/dnstt-cli/internal/sudo"
)

// TestProfilesRoundTrip tests saving and loading profiles.
func TestProfilesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)

	profiles := &Profiles{
		DefaultProfile: "home",
		Profile: map[string]*Profile{
			"home": {
				Domain:     "t.example.com",
				Pubkey:     "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				Resolvers:  []string{"network"},
				Bootstrap:  []string{},
				UTLS:       "",
				Datapath:   "tun",
				KillSwitch: true,
				TunDNS:     "1.1.1.1",
				Exit: Exit{
					Type:        "socks5",
					User:        "user",
					PasswordRef: "secretservice:0123456789abcdef",
				},
				Socks: Socks{
					Listen: "127.0.0.1:1080",
				},
			},
		},
	}

	if err := SaveProfilesTo(path, profiles); err != nil {
		t.Fatalf("SaveProfilesTo: %v", err)
	}

	loaded, err := LoadProfilesFrom(path)
	if err != nil {
		t.Fatalf("LoadProfilesFrom: %v", err)
	}

	if loaded.DefaultProfile != profiles.DefaultProfile {
		t.Errorf("default_profile: got %q, want %q", loaded.DefaultProfile, profiles.DefaultProfile)
	}

	home := loaded.Profile["home"]
	if home == nil {
		t.Fatal("profile home not found")
	}

	if home.Domain != "t.example.com" {
		t.Errorf("domain: got %q, want %q", home.Domain, "t.example.com")
	}
	if home.Pubkey != "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" {
		t.Errorf("pubkey: got %q", home.Pubkey)
	}
	if home.Exit.Type != "socks5" {
		t.Errorf("exit.type: got %q, want %q", home.Exit.Type, "socks5")
	}
	if home.Exit.PasswordRef != "secretservice:0123456789abcdef" {
		t.Errorf("exit.password_ref: got %q", home.Exit.PasswordRef)
	}
}

// TestProfilesEmptyResolversList tests that empty resolvers list is validated correctly.
func TestProfilesEmptyResolversList(t *testing.T) {
	profiles := &Profiles{
		DefaultProfile: "home",
		Profile: map[string]*Profile{
			"home": {
				Domain:     "t.example.com",
				Pubkey:     "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				Resolvers:  []string{}, // Empty becomes ["network"] in validation
				Datapath:   "tun",
				KillSwitch: true,
				Exit: Exit{
					Type: "socks5",
				},
			},
		},
	}

	if err := validateProfiles(profiles); err != nil {
		t.Errorf("empty resolvers list should be valid: %v", err)
	}
}

// TestProfilesUnknownKey tests that unknown keys are rejected.
func TestProfilesUnknownKey(t *testing.T) {
	toml := `
default_profile = "home"

[profile.home]
domain = "t.example.com"
pubkey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
unknown_key = "value"
`
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadProfilesFrom(path)
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
	if !strings.Contains(err.Error(), "unknown_key") {
		t.Errorf("error should mention the offending key: %v", err)
	}
}

// TestProfilesMisspeltKey tests that a misspelt key is caught.
func TestProfilesMisspeltKey(t *testing.T) {
	toml := `
default_profile = "home"

[profile.home]
domain = "t.example.com"
pubkey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
kil_switch = true
`
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadProfilesFrom(path)
	if err == nil {
		t.Fatal("expected error for misspelt key")
	}
	if !strings.Contains(err.Error(), "kil_switch") {
		t.Errorf("error should mention the offending key 'kil_switch': %v", err)
	}
}

// TestProfilesPasswordValue tests that a password value in the TOML is rejected.
func TestProfilesPasswordValue(t *testing.T) {
	tests := []struct {
		name        string
		passwordRef string
		wantErr     bool
	}{
		{
			name:        "valid reference",
			passwordRef: "secretservice:0123456789abcdef",
			wantErr:     false,
		},
		{
			name:        "valid file reference",
			passwordRef: "file:0123456789abcdef",
			wantErr:     false,
		},
		{
			name:        "no colon",
			passwordRef: "mypassword123",
			wantErr:     true,
		},
		{
			name:        "empty scheme",
			passwordRef: ":0123456789abcdef",
			wantErr:     true,
		},
		{
			name:        "empty id",
			passwordRef: "secretservice:",
			wantErr:     true,
		},
		{
			name:        "too long id with spaces",
			passwordRef: "secretservice:this is a very long password that should not be in the toml file at all",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profiles := &Profiles{
				DefaultProfile: "home",
				Profile: map[string]*Profile{
					"home": {
						Domain:    "t.example.com",
						Pubkey:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
						Resolvers: []string{"network"},
						Exit: Exit{
							Type:        "socks5",
							User:        "user",
							PasswordRef: tt.passwordRef,
						},
					},
				},
			}

			err := validateProfiles(profiles)
			if tt.wantErr && err == nil {
				t.Errorf("expected error for password_ref %q", tt.passwordRef)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error for password_ref %q: %v", tt.passwordRef, err)
			}
			if tt.wantErr && err != nil && !strings.Contains(err.Error(), "password_ref") {
				t.Errorf("error should mention password_ref: %v", err)
			}
		})
	}
}

// TestProfilesInvalidResolvers tests that invalid resolvers are caught.
func TestProfilesInvalidResolvers(t *testing.T) {
	profiles := &Profiles{
		DefaultProfile: "home",
		Profile: map[string]*Profile{
			"home": {
				Domain:    "t.example.com",
				Pubkey:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				Resolvers: []string{"invalid-resolver-name"},
			},
		},
	}

	err := validateProfiles(profiles)
	if err == nil {
		t.Fatal("expected error for invalid resolver")
	}
	if !strings.Contains(err.Error(), "resolver") {
		t.Errorf("error should mention resolver: %v", err)
	}
}

// TestProfilesInvalidBootstrap tests that invalid bootstrap servers are caught.
func TestProfilesInvalidBootstrap(t *testing.T) {
	profiles := &Profiles{
		DefaultProfile: "home",
		Profile: map[string]*Profile{
			"home": {
				Domain:    "t.example.com",
				Pubkey:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				Resolvers: []string{"network"},
				Bootstrap: []string{"not-an-address"},
			},
		},
	}

	err := validateProfiles(profiles)
	if err == nil {
		t.Fatal("expected error for invalid bootstrap")
	}
	if !strings.Contains(err.Error(), "bootstrap") {
		t.Errorf("error should mention bootstrap: %v", err)
	}
}

// TestProfilesInvalidPubkey tests that invalid pubkeys are caught.
func TestProfilesInvalidPubkey(t *testing.T) {
	tests := []struct {
		name    string
		pubkey  string
		wantErr bool
	}{
		{
			name:    "valid",
			pubkey:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			wantErr: false,
		},
		{
			name:    "too short",
			pubkey:  "0123456789abcdef",
			wantErr: true,
		},
		{
			name:    "too long",
			pubkey:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef00",
			wantErr: true,
		},
		{
			name:    "invalid characters",
			pubkey:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdeg",
			wantErr: true,
		},
		{
			name:    "empty",
			pubkey:  "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profiles := &Profiles{
				DefaultProfile: "home",
				Profile: map[string]*Profile{
					"home": {
						Domain:    "t.example.com",
						Pubkey:    tt.pubkey,
						Resolvers: []string{"network"},
					},
				},
			}

			err := validateProfiles(profiles)
			if tt.wantErr && err == nil {
				t.Errorf("expected error for pubkey %q", tt.pubkey)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error for pubkey %q: %v", tt.pubkey, err)
			}
		})
	}
}

// TestProfilesRequiredFields tests that missing required fields are caught.
func TestProfilesRequiredFields(t *testing.T) {
	tests := []struct {
		name    string
		profile *Profile
		wantErr string
	}{
		{
			name: "missing domain",
			profile: &Profile{
				Pubkey:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				Resolvers: []string{"network"},
			},
			wantErr: "domain",
		},
		{
			name: "missing pubkey",
			profile: &Profile{
				Domain:    "t.example.com",
				Resolvers: []string{"network"},
			},
			wantErr: "pubkey",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profiles := &Profiles{
				DefaultProfile: "home",
				Profile: map[string]*Profile{
					"home": tt.profile,
				},
			}

			err := validateProfiles(profiles)
			if err == nil {
				t.Fatal("expected error for missing required field")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error should mention %q: %v", tt.wantErr, err)
			}
		})
	}
}

// TestProfilesOwnershipUnderSudo tests that files created under sudo belong to the
// invoking user, not root.
func TestProfilesOwnershipUnderSudo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping ownership test in short mode")
	}

	// This test uses the same approach as owner_test.go: replace the invoker and chown
	// functions.
	oldInvoker := invoker
	oldChown := chown
	defer func() {
		invoker = oldInvoker
		chown = oldChown
	}()

	var chownCalls []struct{ uid, gid int }
	invoker = func() (*sudo.User, error) {
		return &sudo.User{Name: "testuser", Uid: 1000, Gid: 1000, HomeDir: "/home/testuser"}, nil
	}
	chown = func(path string, uid, gid int) error {
		chownCalls = append(chownCalls, struct{ uid, gid int }{uid, gid})
		return nil
	}

	path := filepath.Join(t.TempDir(), FileName)
	profiles := &Profiles{
		DefaultProfile: "home",
		Profile: map[string]*Profile{
			"home": {
				Domain:    "t.example.com",
				Pubkey:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				Resolvers: []string{"network"},
			},
		},
	}

	if err := SaveProfilesTo(path, profiles); err != nil {
		t.Fatalf("SaveProfilesTo: %v", err)
	}

	if len(chownCalls) == 0 {
		t.Error("chown was not called")
	}
	for _, call := range chownCalls {
		if call.uid != 1000 || call.gid != 1000 {
			t.Errorf("chown called with uid=%d, gid=%d, want uid=1000, gid=1000", call.uid, call.gid)
		}
	}
}

// TestProfilesWithSecrets tests the integration of profiles and secrets: a profile with
// an exit password stored in the secret store.
func TestProfilesWithSecrets(t *testing.T) {
	dir := t.TempDir()
	profilePath := filepath.Join(dir, FileName)
	secretPath := filepath.Join(dir, SecretsFileName)

	store := newSecretStoreAt(secretPath)

	// Store the exit password in the secret store.
	ref, err := store.Put("home.password", "my-exit-password")
	if err != nil {
		t.Fatalf("store.Put: %v", err)
	}

	// Create a profile that references the secret.
	profiles := &Profiles{
		DefaultProfile: "home",
		Profile: map[string]*Profile{
			"home": {
				Domain:    "t.example.com",
				Pubkey:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				Resolvers: []string{"network"},
				Exit: Exit{
					Type:        "socks5",
					User:        "user",
					PasswordRef: ref,
				},
			},
		},
	}

	// Save the profile.
	if err := SaveProfilesTo(profilePath, profiles); err != nil {
		t.Fatalf("SaveProfilesTo: %v", err)
	}

	// Load the profile back.
	loaded, err := LoadProfilesFrom(profilePath)
	if err != nil {
		t.Fatalf("LoadProfilesFrom: %v", err)
	}

	// Verify the reference is preserved.
	home := loaded.Profile["home"]
	if home == nil {
		t.Fatal("profile home not found")
	}
	if home.Exit.PasswordRef != ref {
		t.Errorf("password_ref: got %q, want %q", home.Exit.PasswordRef, ref)
	}

	// Retrieve the password from the secret store.
	password, err := store.Get(home.Exit.PasswordRef)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if password != "my-exit-password" {
		t.Errorf("password: got %q, want %q", password, "my-exit-password")
	}
}

