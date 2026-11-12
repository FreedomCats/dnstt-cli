package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/FreedomCats/dnstt-cli/internal/resolvers"
)

// Link is one parsed `slipnet://` link (architecture.md §10): the profile it describes
// and, when the exit password is in the link, the password itself.
//
// The profile's Exit.PasswordRef is empty: the caller stores the password and fills the
// reference. The password is never printed.
type Link struct {
	// Name is the profile name of field 2. Empty means the field was absent or unusable,
	// and the caller has to ask.
	Name string
	// Profile is the profile the link describes, with no password reference yet.
	Profile *Profile
	// Password is the exit password from the link: the value for the secret store.
	Password string
}

// ParseLink parses one `slipnet://` link. The input is trimmed and the scheme is matched
// case-insensitively; ASCII whitespace inside the base64 payload is removed, because a
// terminal may wrap the line. The payload is base64 of a UTF-8 line of `|`-separated
// fields, of which the parser reads the positions architecture.md §10 gives; other fields
// are ignored.
//
// Errors name the field (`field 3`) and the rule; they never repeat the link, the password
// or the key.
func ParseLink(text string) (*Link, error) {
	const scheme = "slipnet://"

	s := strings.TrimSpace(text)
	if s == "" {
		return nil, errors.New("the input is empty; paste a slipnet:// link")
	}
	lower := strings.ToLower(s)
	if !strings.HasPrefix(lower, scheme) {
		if strings.HasPrefix(lower, "slipnet-enc://") {
			return nil, errors.New("slipnet-enc:// links are not supported: the key is built into the SlipNet app")
		}
		return nil, errors.New("not a slipnet:// link")
	}

	payload := s[len(scheme):]
	// A link pasted twice arrives as two schemes in one token. The base64 alphabets have
	// no ':', so a scheme can only be a second link, not payload.
	repeated := strings.ToLower(payload)
	if strings.Contains(repeated, scheme) || strings.Contains(repeated, "slipnet-enc://") {
		return nil, errors.New("more than one link in the input; paste one link at a time")
	}

	data, ok := decodeLinkPayload(strings.Map(dropASCIISpace, payload))
	if !ok || !utf8.Valid(data) {
		return nil, errors.New("the link is damaged; copy it again in full")
	}
	fields := strings.Split(string(data), "|")

	version, err := strconv.Atoi(fieldAt(fields, 0))
	if err != nil || version < 0 {
		return nil, errors.New("field 0: the version must be a non-negative decimal number")
	}

	var exitType string
	switch typ := fieldAt(fields, 1); typ {
	case "dnstt":
		exitType = "socks5"
	case "dnstt_ssh":
		exitType = "ssh"
	case "":
		return nil, errors.New("field 1: the tunnel type is missing")
	case "sayedns", "sayedns_ssh":
		return nil, errors.New("field 1: NoizDNS profiles (sayedns, sayedns_ssh) are not supported: the core does not speak NoizDNS")
	default:
		return nil, fmt.Errorf("field 1: tunnel type %q is not supported", typ)
	}
	if exitType == "ssh" && strings.EqualFold(strings.TrimSpace(fieldAt(fields, 23)), "key") {
		return nil, errors.New("a key-based dnstt_ssh link is not imported until stage 06-ssh-exit: the profile is not stored and no key is kept")
	}

	name := fieldAt(fields, 2)
	if !ValidProfileName(name) {
		name = ""
	}

	domain := fieldAt(fields, 3)
	if err := ValidDomainName(domain); err != nil {
		return nil, fmt.Errorf("field 3: %w", err)
	}

	pubkey := strings.TrimSpace(fieldAt(fields, 11))
	if !IsValidPubkey(pubkey) {
		return nil, errors.New("field 11: the server public key must be exactly 64 hex digits")
	}

	loginField, passwordField := 12, 13
	if exitType == "ssh" {
		loginField, passwordField = 15, 16
	}
	user := fieldAt(fields, loginField)
	password := fieldAt(fields, passwordField)
	if strings.TrimSpace(user) == "" {
		user = ""
	}
	if user != "" && strings.ContainsFunc(user, unicode.IsControl) {
		return nil, fmt.Errorf("field %d: the login must not contain control characters", loginField)
	}
	if password != "" && strings.ContainsFunc(password, unicode.IsControl) {
		return nil, fmt.Errorf("field %d: the password must not contain control characters", passwordField)
	}
	if exitType == "ssh" {
		if user == "" {
			return nil, errors.New("field 15: a dnstt_ssh link needs a login")
		}
		if password == "" {
			return nil, errors.New("field 16: a dnstt_ssh link needs a password, because key-based links are not imported yet")
		}
	} else if password != "" && user == "" {
		return nil, errors.New("field 13: a password without a login is not accepted")
	}

	resolverList, err := linkResolvers(fields)
	if err != nil {
		return nil, err
	}

	payloadSize := 0
	if size := strings.TrimSpace(fieldAt(fields, 39)); size != "" {
		n, err := strconv.Atoi(size)
		if err != nil || n < 0 {
			return nil, errors.New("field 39: the payload size must be a non-negative decimal number")
		}
		payloadSize = n
	}

	// The kill-switch default is named explicitly, as the wizard does, so the profile keeps
	// it on even if the default changes later. Datapath and Socks stay empty: a link
	// carries no mode, and connect defaults to socks.
	killSwitch := true
	prof := &Profile{
		Domain:      domain,
		Pubkey:      pubkey,
		Resolvers:   resolverList,
		PayloadSize: payloadSize,
		KillSwitch:  &killSwitch,
		Exit:        Exit{Type: exitType, User: user},
	}
	return &Link{Name: name, Profile: prof, Password: password}, nil
}

// ParseLinks parses one link per non-empty line. A line that does not parse becomes a
// warning with its line number and the reason, never with the content; the good links are
// returned in their order, without de-duplication.
func ParseLinks(text string) ([]*Link, []error) {
	var links []*Link
	var warnings []error
	for i, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		link, err := ParseLink(line)
		if err != nil {
			warnings = append(warnings, fmt.Errorf("line %d: %w", i+1, err))
			continue
		}
		links = append(links, link)
	}
	return links, warnings
}

// fieldAt returns field i or "" when the line is too short: a field keeps its position in
// every version, and a missing one takes its default.
func fieldAt(fields []string, i int) string {
	if i < 0 || i >= len(fields) {
		return ""
	}
	return fields[i]
}

// dropASCIISpace removes the ASCII whitespace of a wrapped payload.
func dropASCIISpace(r rune) rune {
	switch r {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return -1
	}
	return r
}

// decodeLinkPayload tries the standard alphabet with padding, then without padding, then
// the URL-safe one. Any failure is the caller's damaged link.
func decodeLinkPayload(payload string) ([]byte, bool) {
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if data, err := enc.DecodeString(payload); err == nil {
			return data, true
		}
	}
	return nil, false
}

// linkResolvers builds the resolver list from field 22 (transport) and field 4, or field
// 37 when field 4 is empty; a DoH transport takes its URL from field 21. Every entry is
// stored in the normalized form of resolvers.Resolver.String, with the port always
// written.
func linkResolvers(fields []string) ([]string, error) {
	transport := strings.ToLower(strings.TrimSpace(fieldAt(fields, 22)))
	switch transport {
	case "udp", "tcp", "dot":
	case "doh":
		url := strings.TrimSpace(fieldAt(fields, 21))
		if !strings.HasPrefix(strings.ToLower(url), "https://") {
			return nil, errors.New("field 21: the DoH URL must be an https:// URL")
		}
		r, err := resolvers.Parse("doh:" + url)
		if err != nil {
			return nil, errors.New("field 21: the DoH URL is not a valid https:// URL without a user name or fragment")
		}
		return []string{r.String()}, nil
	default:
		// The absent and the unknown transport both mean udp (§10).
		transport = "udp"
	}

	listField := 4
	list := fieldAt(fields, 4)
	if strings.TrimSpace(list) == "" {
		listField = 37
		list = fieldAt(fields, 37)
	}
	if strings.TrimSpace(list) == "" {
		return nil, errors.New("field 4 or 37: at least one resolver is required unless the transport is doh")
	}

	var out []string
	for i, entry := range strings.Split(list, ",") {
		r, ok := parseLinkResolver(transport, entry)
		if !ok {
			return nil, fmt.Errorf("field %d: resolver %d is not valid; use host[:port] with an IP address or a host name", listField, i+1)
		}
		out = append(out, r.String())
	}
	return out, nil
}

// parseLinkResolver parses one `host[:port[:flag]]` entry of the link. The third part is
// Slipstream's flag and is ignored; the port defaults to 53, and for dot 53 means 853.
func parseLinkResolver(transport, entry string) (resolvers.Resolver, bool) {
	parts := strings.SplitN(strings.TrimSpace(entry), ":", 3)
	host := strings.TrimSpace(parts[0])
	if host == "" {
		return resolvers.Resolver{}, false
	}
	port := "53"
	if len(parts) > 1 {
		if p := strings.TrimSpace(parts[1]); p != "" {
			port = p
		}
	}
	if transport == "dot" && port == "53" {
		port = "853"
	}
	r, err := resolvers.Parse(transport + ":" + net.JoinHostPort(host, port))
	if err != nil {
		return resolvers.Resolver{}, false
	}
	return r, true
}
