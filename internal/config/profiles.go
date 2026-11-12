package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/pelletier/go-toml/v2"

	"github.com/FreedomCats/dnstt-cli/internal/exit"
	"github.com/FreedomCats/dnstt-cli/internal/resolvers"
)

// Profiles is the content of profiles.toml: a set of profiles and the default one.
type Profiles struct {
	DefaultProfile string              `toml:"default_profile"`
	Profile        map[string]*Profile `toml:"profile"`
}

// Profile is one profile: the tunnel settings and the exit.
type Profile struct {
	Domain    string   `toml:"domain"`
	Pubkey    string   `toml:"pubkey"`
	Resolvers []string `toml:"resolvers"`
	Bootstrap []string `toml:"bootstrap"`
	UTLS      string   `toml:"utls"`
	Datapath  string   `toml:"datapath"`
	// KillSwitch is a pointer: a missing key means on; only an explicit false turns it
	// off. Read it with KillSwitchOn.
	KillSwitch *bool  `toml:"kill_switch,omitempty"`
	TunDNS     string `toml:"tun_dns"`
	// PayloadSize is the cap on the DNS payload one query carries, in bytes: 0 or
	// absent means the domain's maximum. No omitempty: a zero is written like the
	// other defaults, and an absent key reads as zero either way.
	PayloadSize int   `toml:"payload_size"`
	Exit        Exit  `toml:"exit"`
	Socks       Socks `toml:"socks"`
}

// KillSwitchOn reports whether the kill-switch is on: a missing key means on, only an
// explicit false turns it off.
func (p *Profile) KillSwitchOn() bool {
	return p.KillSwitch == nil || *p.KillSwitch
}

// Exit is the exit configuration: SOCKS5, HTTP CONNECT or SSH.
type Exit struct {
	Type        string `toml:"type"`
	User        string `toml:"user"`
	PasswordRef string `toml:"password_ref"`
	// KeyRef is the reference of the SSH exit's private key in the secret store; exactly
	// one of KeyRef and PasswordRef is set for exit.type "ssh".
	KeyRef string `toml:"key_ref,omitempty"`
	// HostKey is the pinned fingerprint of the SSH exit's host key, SHA256:…; required for
	// exit.type "ssh", refused with any other type. Normalized by exit.ParseFingerprint.
	HostKey string `toml:"host_key,omitempty"`
	// OneWrite is the one-write SOCKS5 exchange: the greeting, the login and the CONNECT in
	// one Write. It applies to exit.type "socks5" (an empty type counts) and to the
	// datapath only, never to the startup check, which has no target. Off by default.
	OneWrite bool `toml:"one_write"`
}

// Socks is the local SOCKS5 proxy configuration.
type Socks struct {
	Listen string `toml:"listen"`
}

// LoadProfiles reads profiles.toml from the default path.
//
// A missing file is the empty set: no profiles configured. An existing empty or invalid
// file is an error.
func LoadProfiles() (*Profiles, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	return LoadProfilesFrom(path)
}

// LoadProfilesFrom reads profiles.toml from the given path. A missing file is the empty
// set; an existing empty or invalid file is an error.
func LoadProfilesFrom(path string) (*Profiles, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Profiles{Profile: map[string]*Profile{}}, nil
	}
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

// SaveProfiles writes profiles.toml to the default path. The file is never written empty,
// because an existing empty file is invalid: the empty set is the absence of the file.
func SaveProfiles(p *Profiles) error {
	path, err := Path()
	if err != nil {
		return err
	}
	return SaveProfilesTo(path, p)
}

// SaveProfilesTo writes profiles.toml to the given path. An empty set removes the file
// instead of writing it: an existing empty file is invalid, and a missing file is the
// empty set on load.
func SaveProfilesTo(path string, p *Profiles) error {
	if p == nil || len(p.Profile) == 0 {
		return removeProfilesFile(path)
	}

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

// removeProfilesFile removes profiles.toml when the set is empty. A file that is already
// gone is not an error.
func removeProfilesFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
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
	} else if !IsValidPubkey(p.Pubkey) {
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

	// Validate exit type. The effective type is taken first: an empty type is socks5, and
	// the messages must not print an empty type.
	exitType := p.Exit.Type
	if exitType == "" {
		exitType = "socks5"
	}
	if exitType != "socks5" && exitType != "http" && exitType != "ssh" {
		errs = append(errs, fmt.Errorf("%s: exit.type must be \"socks5\", \"http\" or \"ssh\"", prefix))
	}
	// exit.one_write applies to SOCKS5 only. The false value stays valid with every type:
	// saved profiles carry one_write = false in every exit section.
	if p.Exit.OneWrite && exitType != "socks5" {
		errs = append(errs, fmt.Errorf("%s: exit.one_write applies to exit.type \"socks5\" only, not %q", prefix, exitType))
	}
	switch exitType {
	case "http":
		if strings.Contains(p.Exit.User, ":") || strings.ContainsFunc(p.Exit.User, unicode.IsControl) {
			errs = append(errs, fmt.Errorf("%s: HTTP exit: invalid user", prefix))
		}
		if p.Exit.PasswordRef != "" && p.Exit.User == "" {
			errs = append(errs, fmt.Errorf("%s: HTTP exit: password_ref requires a user", prefix))
		}
	case "ssh":
		if strings.TrimSpace(p.Exit.User) == "" {
			errs = append(errs, fmt.Errorf("%s: exit.user is required for exit.type \"ssh\"", prefix))
		} else if strings.ContainsFunc(p.Exit.User, unicode.IsControl) {
			errs = append(errs, fmt.Errorf("%s: exit.user must not contain control characters", prefix))
		}
		if p.Exit.HostKey == "" {
			errs = append(errs, fmt.Errorf("%s: exit.host_key is required for exit.type \"ssh\"", prefix))
		} else if _, err := exit.ParseFingerprint(p.Exit.HostKey); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", prefix, err))
		}
		switch {
		case p.Exit.PasswordRef != "" && p.Exit.KeyRef != "":
			errs = append(errs, fmt.Errorf("%s: exit.type \"ssh\" takes exit.password_ref or exit.key_ref, not both", prefix))
		case p.Exit.PasswordRef == "" && p.Exit.KeyRef == "":
			errs = append(errs, fmt.Errorf("%s: exit.type \"ssh\" needs exactly one of exit.password_ref and exit.key_ref", prefix))
		}
	}
	// host_key and key_ref belong to the SSH exit alone, like every unknown or misplaced
	// key in this project.
	if exitType != "ssh" {
		if p.Exit.HostKey != "" {
			errs = append(errs, fmt.Errorf("%s: exit.host_key applies to exit.type \"ssh\" only, not %q", prefix, exitType))
		}
		if p.Exit.KeyRef != "" {
			errs = append(errs, fmt.Errorf("%s: exit.key_ref applies to exit.type \"ssh\" only, not %q", prefix, exitType))
		}
	}

	// Validate datapath.
	if p.Datapath != "" && p.Datapath != "tun" && p.Datapath != "socks" {
		errs = append(errs, fmt.Errorf("%s: datapath must be \"tun\" or \"socks\"", prefix))
	}

	// A negative payload_size is refused. The upper bound is not checked here: the
	// 80-byte floor depends on the domain and is enforced by the session.
	if p.PayloadSize < 0 {
		errs = append(errs, fmt.Errorf("%s: payload_size must not be negative", prefix))
	}

	return errors.Join(errs...)
}

// IsValidPubkey reports whether s is a server public key: exactly 64 hex digits. The
// wizard uses it too, so the rule lives in one place.
func IsValidPubkey(s string) bool {
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
	var errs []error
	if p.Exit.PasswordRef != "" {
		if err := checkRefValue(prefix, "exit.password_ref", p.Exit.PasswordRef); err != nil {
			errs = append(errs, err)
		}
	}
	if p.Exit.KeyRef != "" {
		if err := checkRefValue(prefix, "exit.key_ref", p.Exit.KeyRef); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// checkRefValue checks that value is a reference of the form <scheme>:<id> and not the
// secret itself. The messages name the field and never repeat the value.
func checkRefValue(prefix, field, value string) error {
	ref := strings.TrimSpace(value)
	// A reference has the form <scheme>:<id>. If it doesn't contain ":", or if it
	// looks too long or too short, it might be the secret.
	if !strings.Contains(ref, ":") {
		return fmt.Errorf("%s: %s must be a reference (<scheme>:<id>), not a secret value", prefix, field)
	}
	parts := strings.SplitN(ref, ":", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return fmt.Errorf("%s: %s must be a reference (<scheme>:<id>), not a secret value", prefix, field)
	}
	// If the "id" part is longer than 100 characters or contains spaces, it's likely
	// a secret.
	id := parts[1]
	if len(id) > 100 || strings.Contains(id, " ") {
		return fmt.Errorf("%s: %s looks like a secret value, not a reference", prefix, field)
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
		"payload_size": true,
		"exit":         true,
		"socks":        true,
	}

	knownExitKeys := map[string]bool{
		"type":         true,
		"user":         true,
		"password_ref": true,
		"key_ref":      true,
		"host_key":     true,
		"one_write":    true,
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
