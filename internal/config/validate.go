package config

import (
	"errors"
	"fmt"

	"github.com/FreedomCats/dnstt-cli/core/dns"
)

// ValidProfileName reports whether name can be a profile name: non-empty ASCII letters,
// digits and '-', not starting with '-'.
func ValidProfileName(name string) bool {
	if name == "" || name[0] == '-' {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// ValidDomainName checks a tunnel domain: the core's DNS limits plus the characters a
// tunnel name may use. The reasons never repeat the value.
func ValidDomainName(domain string) error {
	if domain == "" || domain == "." {
		return errors.New("a tunnel domain is required")
	}
	name, err := dns.ParseName(domain)
	if err != nil {
		return fmt.Errorf("not a valid domain: %w", err)
	}
	if len(name) == 0 {
		return errors.New("a tunnel domain is required")
	}
	for _, label := range name {
		if len(label) == 0 {
			return errors.New("not a valid domain: an empty label")
		}
		if len(label) > 63 {
			return errors.New("not a valid domain: a label is longer than 63 characters")
		}
		for _, c := range label {
			if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '-') {
				return errors.New("not a valid domain: only letters, digits and '-' are allowed in a label")
			}
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return errors.New("not a valid domain: a label must not start or end with '-'")
		}
	}
	return nil
}
