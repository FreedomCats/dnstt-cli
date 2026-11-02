package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/FreedomCats/dnstt-cli/internal/resolvers"
)

// Profiles is the content of profiles.toml: a set of profiles and the default one.
type Profiles struct {
	DefaultProfile string               `toml:"default_profile"`
	Profile        map[string]*Profile  `toml:"profile"`
}

// Profile is one profile: the tunnel settings and the exit.
type Profile struct {
	Domain      string   `toml:"domain"`
	Pubkey      string   `toml:"pubkey"`
	Resolvers   []string `toml:"resolvers"`
	Bootstrap   []string `toml:"bootstrap"`
	UTLS        string   `toml:"utls"`
	Datapath    string   `toml:"datapath"`
	KillSwitch  bool     `toml:"kill_switch"`
	TunDNS      string   `toml:"tun_dns"`
	Exit        Exit     `toml:"exit"`
	Socks       Socks    `toml:"socks"`
}

// Exit is the exit configuration: SOCKS5 or SSH.
type Exit struct {
	Type        string `toml:"type"`
	User        string `toml:"user"`
	PasswordRef string `toml:"password_ref"`
}

// Socks is the local SOCKS5 proxy configuration.
type Socks struct {
	Listen string `toml:"listen"`
}

// LoadProfiles reads profiles.toml from the default path.
func LoadProfiles() (*Profiles, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	return LoadProfilesFrom(path)
}

// LoadProfilesFrom reads profiles.toml from the given path.
func LoadProfilesFrom(path string) (*Profiles, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("profiles %s: %w", path, err)
	}

	// First, decode into a map to check for unknown keys.
	var rawData map[string]any
	if err := toml.Unmarshal(raw, &rawData); err != nil {
		return nil, fmt.Errorf("profiles %s: %w", path, err)
	}
	if err := checkUnknownKeys(rawData); err != nil {
		return nil, fmt.Errorf("profiles %s: %w", path, err)
	}

	// Now decode into the struct.
	var p Profiles
	if err := toml.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("profiles %s: %w", path, err)
	}

	if err := validateProfiles(&p); err != nil {
		return nil, fmt.Errorf("profiles %s: %w", path, err)
	}

	return &p, nil
}

// SaveProfiles writes profiles.toml to the default path.
func SaveProfiles(p *Profiles) error {
	path, err := Path()
	if err != nil {
		return err
	}
	return SaveProfilesTo(path, p)
}

// SaveProfilesTo writes profiles.toml to the given path.
func SaveProfilesTo(path string, p *Profiles) error {
	if err := validateProfiles(p); err != nil {
		return fmt.Errorf("profiles: %w", err)
	}

	if err := makeDir(filepath.Dir(path)); err != nil {
		return err
	}

	raw, err := toml.Marshal(p)
	if err != nil {
		return fmt.Errorf("profiles: cannot marshal: %w", err)
	}

	// Write to a temporary file and rename it: an interrupted write must not leave the
	// file half written.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return fmt.Errorf("profiles %s: %w", tmp, err)
	}
	if err := giveToInvoker(tmp); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("profiles %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("profiles %s: %w", path, err)
	}
	return nil
}

// validateProfiles checks the profiles for errors: missing required fields, unknown keys,
// secrets written into the TOML, invalid resolvers and bootstrap servers.
func validateProfiles(p *Profiles) error {
	if p.Profile == nil || len(p.Profile) == 0 {
		return errors.New("no profiles defined")
	}

	var errs []error
	for name, prof := range p.Profile {
		if err := validateProfile(name, prof); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// validateProfile checks one profile.
func validateProfile(name string, p *Profile) error {
	var errs []error
	prefix := fmt.Sprintf("profile %q", name)

	// Required fields.
	if strings.TrimSpace(p.Domain) == "" {
		errs = append(errs, fmt.Errorf("%s: domain is required", prefix))
	}
	if strings.TrimSpace(p.Pubkey) == "" {
		errs = append(errs, fmt.Errorf("%s: pubkey is required", prefix))
	} else if !isValidPubkey(p.Pubkey) {
		errs = append(errs, fmt.Errorf("%s: pubkey must be exactly 64 hex digits", prefix))
	}

	// Check for secrets written directly into the TOML.
	if err := checkForSecretValues(prefix, p); err != nil {
		errs = append(errs, err)
	}

	// Validate resolvers: empty becomes ["network"].
	resolverList := p.Resolvers
	if len(resolverList) == 0 {
		resolverList = []string{"network"}
	}
	if _, err := resolvers.Expand(resolverList); err != nil {
		errs = append(errs, fmt.Errorf("%s: resolvers: %w", prefix, err))
	}

	// Validate bootstrap servers.
	if len(p.Bootstrap) > 0 {
		if _, err := resolvers.ParseBootstrap(p.Bootstrap); err != nil {
			errs = append(errs, fmt.Errorf("%s: bootstrap: %w", prefix, err))
		}
	}

	// Validate exit type.
	if p.Exit.Type != "" && p.Exit.Type != "socks5" && p.Exit.Type != "ssh" {
		errs = append(errs, fmt.Errorf("%s: exit.type must be \"socks5\" or \"ssh\"", prefix))
	}

	// Validate datapath.
	if p.Datapath != "" && p.Datapath != "tun" && p.Datapath != "socks" {
		errs = append(errs, fmt.Errorf("%s: datapath must be \"tun\" or \"socks\"", prefix))
	}

	return errors.Join(errs...)
}

// isValidPubkey checks that the pubkey is exactly 64 hex digits.
func isValidPubkey(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// checkForSecretValues detects if a password or key value was written directly into the
// TOML instead of a reference. This is an error: secrets must go to the secret store.
func checkForSecretValues(prefix string, p *Profile) error {
	// The only field that should contain a reference is password_ref.
	// If password_ref looks like a plain password (not a reference), it's an error.
	if p.Exit.PasswordRef != "" {
		ref := strings.TrimSpace(p.Exit.PasswordRef)
		// A reference has the form <scheme>:<id>. If it doesn't contain ":", or if it
		// looks too long or too short, it might be a password.
		if !strings.Contains(ref, ":") {
			return fmt.Errorf("%s: exit.password_ref must be a reference (<scheme>:<id>), not a password value", prefix)
		}
		parts := strings.SplitN(ref, ":", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return fmt.Errorf("%s: exit.password_ref must be a reference (<scheme>:<id>), not a password value", prefix)
		}
		// If the "id" part is longer than 100 characters or contains spaces, it's likely
		// a password.
		id := parts[1]
		if len(id) > 100 || strings.Contains(id, " ") {
			return fmt.Errorf("%s: exit.password_ref looks like a password value, not a reference", prefix)
		}
	}

	return nil
}

// checkUnknownKeys checks for unknown keys in the raw TOML data.
func checkUnknownKeys(data map[string]any) error {
	knownTopLevel := map[string]bool{
		"default_profile": true,
		"profile":         true,
	}

	for key := range data {
		if !knownTopLevel[key] {
			return fmt.Errorf("unknown key %q at top level", key)
		}
	}

	profiles, ok := data["profile"].(map[string]any)
	if !ok {
		return nil // No profiles to check
	}

	for profileName, profileData := range profiles {
		profileMap, ok := profileData.(map[string]any)
		if !ok {
			continue
		}

		if err := checkProfileKeys(profileName, profileMap); err != nil {
			return err
		}
	}

	return nil
}

func checkProfileKeys(profileName string, data map[string]any) error {
	knownProfileKeys := map[string]bool{
		"domain":       true,
		"pubkey":       true,
		"resolvers":    true,
		"bootstrap":    true,
		"utls":         true,
		"datapath":     true,
		"kill_switch":  true,
		"tun_dns":      true,
		"exit":         true,
		"socks":        true,
	}

	knownExitKeys := map[string]bool{
		"type":         true,
		"user":         true,
		"password_ref": true,
	}

	knownSocksKeys := map[string]bool{
		"listen": true,
	}

	for key := range data {
		if !knownProfileKeys[key] {
			return fmt.Errorf("profile %q: unknown key %q", profileName, key)
		}

		if key == "exit" {
			exitMap, ok := data["exit"].(map[string]any)
			if ok {
				for exitKey := range exitMap {
					if !knownExitKeys[exitKey] {
						return fmt.Errorf("profile %q: unknown key %q in exit section", profileName, exitKey)
					}
				}
			}
		}

		if key == "socks" {
			socksMap, ok := data["socks"].(map[string]any)
			if ok {
				for socksKey := range socksMap {
					if !knownSocksKeys[socksKey] {
						return fmt.Errorf("profile %q: unknown key %q in socks section", profileName, socksKey)
					}
				}
			}
		}
	}

	return nil
}
