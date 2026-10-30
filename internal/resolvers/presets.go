package resolvers

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Presets are the public resolvers built into the binary, by name. Each carries the
// addresses of its host name, so it works without the bootstrap resolver and under a
// closed kill-switch (architecture.md R5). DoH first: it is the default.
var Presets = []struct {
	Name  string
	Entry string
}{
	{"doh-cloudflare", "doh:https://cloudflare-dns.com/dns-query@1.1.1.1,1.0.0.1"},
	{"doh-google", "doh:https://dns.google/dns-query@8.8.8.8,8.8.4.4"},
	{"doh-quad9", "doh:https://dns.quad9.net/dns-query@9.9.9.9,149.112.112.112"},
	{"doh-adguard", "doh:https://dns.adguard-dns.com/dns-query@94.140.14.14,94.140.15.15"},
	{"dot-cloudflare", "dot:one.one.one.one:853@1.1.1.1,1.0.0.1"},
	{"dot-google", "dot:dns.google:853@8.8.8.8,8.8.4.4"},
	{"dot-quad9", "dot:dns.quad9.net:853@9.9.9.9,149.112.112.112"},
	{"dot-adguard", "dot:dns.adguard-dns.com:853@94.140.14.14,94.140.15.15"},
}

// Preset returns the preset called name.
func Preset(name string) (Resolver, bool) {
	for _, p := range Presets {
		if strings.EqualFold(p.Name, name) {
			r, err := Parse(p.Entry)
			if err != nil {
				panic(fmt.Sprintf("preset %s: %v", p.Name, err))
			}
			r.Preset = p.Name
			return r, true
		}
	}
	return Resolver{}, false
}

// Expand reads the resolvers of a profile: entries, preset names and NetworkEntry, in the
// order given.
// A resolver given twice stays at its first place.
func Expand(entries []string) ([]Resolver, error) {
	var list []Resolver
	var errs []error
	for _, e := range entries {
		r, ok := Preset(strings.TrimSpace(e))
		if !ok && strings.EqualFold(strings.TrimSpace(e), NetworkEntry) {
			r, ok = Resolver{Network: true}, true
		}
		if !ok && !strings.Contains(e, ":") {
			errs = append(errs, fmt.Errorf("resolver %q: not %s, no such preset (%s) and no transport (doh:, dot:, udp:)",
				e, NetworkEntry, strings.Join(presetNames(), ", ")))
			continue
		}
		if !ok {
			var err error
			if r, err = Parse(e); err != nil {
				errs = append(errs, err)
				continue
			}
		}
		if !slices.ContainsFunc(list, func(x Resolver) bool { return x.String() == r.String() }) {
			list = append(list, r)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, errors.New("no resolvers")
	}
	return list, nil
}

func presetNames() []string {
	names := make([]string, len(Presets))
	for i, p := range Presets {
		names[i] = p.Name
	}
	return names
}
