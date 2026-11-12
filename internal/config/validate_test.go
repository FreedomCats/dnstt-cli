package config

import (
	"strings"
	"testing"
)

func TestValidProfileName(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"home", true},
		{"work-2", true},
		{"2home", true},
		{"a", true},
		{"", false},
		{"-home", false},
		{"home/1", false},
		{"home name", false},
		{"home.", false},
		{"høme", false},
	} {
		if got := ValidProfileName(tc.name); got != tc.want {
			t.Errorf("ValidProfileName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestValidDomainName(t *testing.T) {
	for _, tc := range []struct {
		domain string
		ok     bool
	}{
		{"t.example.com", true},
		{"t.example.com.", true},
		{"T.Example2.COM", true},
		{"localhost", true},
		{"", false},
		{".", false},
		{"t..example.com", false},
		{"-t.example.com", false},
		{"t-.example.com", false},
		{"bad_domain.example.com", false},
		{strings.Repeat("a", 64) + ".example.com", false},
		{strings.Join([]string{"a", "b", "c", "d"}, ".") + "." + strings.Repeat("x", 250), false},
	} {
		err := ValidDomainName(tc.domain)
		if (err == nil) != tc.ok {
			t.Errorf("ValidDomainName(%q) = %v, want ok %v", tc.domain, err, tc.ok)
		}
		if err != nil && strings.Contains(err.Error(), tc.domain) && tc.domain != "" {
			t.Errorf("ValidDomainName(%q) repeats the value: %v", tc.domain, err)
		}
	}
}
