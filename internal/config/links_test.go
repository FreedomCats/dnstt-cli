package config

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/FreedomCats/dnstt-cli/internal/exit"
)

// Synthetic values only: a documentation domain, documentation addresses and a key made
// of a repeated pattern. No value here comes from a real link.
const (
	linkTestKey      = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	linkTestPassword = "synthetic-exit-password"
	linkTestSSHPass  = "synthetic-ssh-password"
	linkTestSSHUser  = "synthetic-ssh-user"
	// linkTestPassphrase is the passphrase of the generated encrypted key; the wrong one is
	// what a link with a mistake carries.
	linkTestPassphrase      = "synthetic-link-passphrase"
	linkTestWrongPassphrase = "the wrong link passphrase"
)

// sshLinkKey is a generated ed25519 key of one test: the PEM forms, unencrypted and
// encrypted with linkTestPassphrase, and the signer that proves a re-marshalled key is the
// same key. No fixture and no key of the machine.
type sshLinkKey struct {
	plain     []byte
	encrypted []byte
	signer    ssh.Signer
}

func newSSHLinkKey(t *testing.T) sshLinkKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	plainBlock, err := ssh.MarshalPrivateKey(priv, "synthetic-link-key")
	if err != nil {
		t.Fatal(err)
	}
	encryptedBlock, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "synthetic-link-key", []byte(linkTestPassphrase))
	if err != nil {
		t.Fatal(err)
	}
	return sshLinkKey{
		plain:     pem.EncodeToMemory(plainBlock),
		encrypted: pem.EncodeToMemory(encryptedBlock),
		signer:    signer,
	}
}

// makeLine joins the fields of a synthetic link: field 0 is the version, the map holds the
// others, and empty fields fill the gaps up to the highest index used.
func makeLine(version int, fields map[int]string) string {
	values := map[int]string{0: strconv.Itoa(version)}
	highest := 0
	for i, v := range fields {
		values[i] = v
		if i > highest {
			highest = i
		}
	}
	parts := make([]string, highest+1)
	for i := range parts {
		parts[i] = values[i]
	}
	return strings.Join(parts, "|")
}

// makeLink builds a link the way the generators write it: the standard alphabet with
// padding on one line.
func makeLink(version int, fields map[int]string) string {
	return "slipnet://" + base64.StdEncoding.EncodeToString([]byte(makeLine(version, fields)))
}

// linkFields is a valid dnstt link with the given fields on top.
func linkFields(over map[int]string) map[int]string {
	fields := map[int]string{
		1:  "dnstt",
		2:  "home",
		3:  "t.example.com",
		4:  "192.0.2.53:53:0",
		11: linkTestKey,
	}
	for i, v := range over {
		fields[i] = v
	}
	return fields
}

func TestParseLinkDecoding(t *testing.T) {
	line := makeLine(22, linkFields(map[int]string{60: "¾ "}))
	standard := base64.StdEncoding.EncodeToString([]byte(line))
	urlSafe := base64.URLEncoding.EncodeToString([]byte(line))
	if !strings.ContainsAny(urlSafe, "-_") {
		t.Fatalf("the URL-safe case is not exercised: %q", urlSafe)
	}
	if !strings.HasSuffix(standard, "=") {
		t.Fatalf("the padding case is not exercised: %q", standard)
	}

	for _, tc := range []struct {
		name string
		text string
	}{
		{"standard alphabet with padding", "slipnet://" + standard},
		{"standard alphabet without padding", "slipnet://" + strings.TrimRight(standard, "=")},
		{"URL-safe alphabet with padding", "slipnet://" + urlSafe},
		{"URL-safe alphabet without padding", "slipnet://" + strings.TrimRight(urlSafe, "=")},
		{"whitespace inside a wrapped line", "slipnet://" + standard[:10] + "\n \t" + standard[10:]},
		{"CRLF around the link", "slipnet://" + standard + "\r\n"},
		{"uppercase scheme", "SLIPNET://" + standard},
	} {
		t.Run(tc.name, func(t *testing.T) {
			link, err := ParseLink(tc.text)
			if err != nil {
				t.Fatalf("ParseLink = %v", err)
			}
			if link.Name != "home" || link.Profile.Domain != "t.example.com" {
				t.Fatalf("link = %+v", link)
			}
		})
	}
}

func TestParseLinkDecodingFailures(t *testing.T) {
	link := makeLink(22, linkFields(nil))
	for _, tc := range []struct {
		name string
		text string
		want string
	}{
		{"empty input", "  \r\n ", "empty"},
		{"no scheme", "not a link", "slipnet://"},
		{"a second scheme in the token", link + " " + link, "more than one"},
		{"a second scheme after a newline", link + "\n" + link, "more than one"},
		{"damaged base64", "slipnet://%%%", "damaged"},
		{"non-UTF-8 payload", "slipnet://" + base64.StdEncoding.EncodeToString([]byte{0xff, 0xfe, 0x00}), "damaged"},
		{"slipnet-enc is refused", "slipnet-enc://" + base64.StdEncoding.EncodeToString([]byte("22|dnstt")), "SlipNet"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseLink(tc.text)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ParseLink = %v, want %q", err, tc.want)
			}
			if body := linkBody(tc.text); body != "" && strings.Contains(err.Error(), body) {
				t.Fatalf("the error repeats the payload: %v", err)
			}
		})
	}
}

// linkBody is the text after the scheme, for the assertion that an error does not echo it.
func linkBody(text string) string {
	if i := strings.Index(text, "://"); i >= 0 {
		return text[i+3:]
	}
	return ""
}

func TestParseLinkDnstt(t *testing.T) {
	link, err := ParseLink(makeLink(22, linkFields(map[int]string{
		2:  "home-2",
		4:  "192.0.2.53:53:0,192.0.2.54:5353",
		12: "synthetic-user",
		13: linkTestPassword,
		22: "udp",
		39: "120",
	})))
	if err != nil {
		t.Fatal(err)
	}

	if link.Name != "home-2" {
		t.Fatalf("name = %q", link.Name)
	}
	prof := link.Profile
	if prof.Domain != "t.example.com" || prof.Pubkey != linkTestKey {
		t.Fatalf("domain or key = %+v", prof)
	}
	wantResolvers := []string{"udp:192.0.2.53:53", "udp:192.0.2.54:5353"}
	if strings.Join(prof.Resolvers, ",") != strings.Join(wantResolvers, ",") {
		t.Fatalf("resolvers = %v, want %v", prof.Resolvers, wantResolvers)
	}
	if prof.PayloadSize != 120 {
		t.Fatalf("payload_size = %d, want 120", prof.PayloadSize)
	}
	if prof.KillSwitch == nil || !*prof.KillSwitch {
		t.Fatal("kill_switch is not explicitly on")
	}
	if prof.Datapath != "" || prof.Socks != (Socks{}) {
		t.Fatalf("datapath %q, socks %+v; want both empty", prof.Datapath, prof.Socks)
	}
	if prof.Exit.Type != "socks5" || prof.Exit.User != "synthetic-user" {
		t.Fatalf("exit = %+v", prof.Exit)
	}
	if prof.Exit.PasswordRef != "" {
		t.Fatalf("password_ref = %q before the password is stored", prof.Exit.PasswordRef)
	}
	if link.Password != linkTestPassword {
		t.Fatal("the password is not returned separately")
	}
}

func TestParseLinkNoAuthentication(t *testing.T) {
	link, err := ParseLink(makeLink(22, linkFields(nil)))
	if err != nil {
		t.Fatal(err)
	}
	if link.Profile.Exit.Type != "socks5" || link.Profile.Exit.User != "" || link.Password != "" {
		t.Fatalf("exit = %+v, password = %q", link.Profile.Exit, link.Password)
	}
}

func TestParseLinkResolvers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields map[int]string
		want   []string
	}{
		{"udp by default", map[int]string{4: "192.0.2.53"}, []string{"udp:192.0.2.53:53"}},
		{"an unknown transport is udp", map[int]string{4: "192.0.2.53", 22: "quic"}, []string{"udp:192.0.2.53:53"}},
		{"tcp", map[int]string{4: "192.0.2.53:5353", 22: "tcp"}, []string{"tcp:192.0.2.53:5353"}},
		{"dot takes 853 by default", map[int]string{4: "dns.example", 22: "dot"}, []string{"dot:dns.example:853"}},
		{"dot maps 53 to 853", map[int]string{4: "dns.example:53", 22: "dot"}, []string{"dot:dns.example:853"}},
		{"dot keeps another port", map[int]string{4: "dns.example:8853", 22: "dot"}, []string{"dot:dns.example:8853"}},
		{"field 37 when field 4 is empty", map[int]string{4: "", 37: "192.0.2.53:53,192.0.2.54:53"}, []string{"udp:192.0.2.53:53", "udp:192.0.2.54:53"}},
		{"doh takes field 21, not field 4", map[int]string{4: "192.0.2.53", 21: "https://doh.example/dns-query", 22: "doh"}, []string{"doh:https://doh.example/dns-query"}},
		{"the Slipstream flag is ignored", map[int]string{4: "192.0.2.53:53:1"}, []string{"udp:192.0.2.53:53"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			link, err := ParseLink(makeLink(22, linkFields(tc.fields)))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(link.Profile.Resolvers, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("resolvers = %v, want %v", link.Profile.Resolvers, tc.want)
			}
		})
	}
}

func TestParseLinkPayloadSize(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  int
		ok    bool
	}{
		{"absent", "", 0, true},
		{"zero", "0", 0, true},
		{"a value", "120", 120, true},
		{"negative", "-1", 0, false},
		{"not a number", "big", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields := linkFields(nil)
			if tc.name != "absent" {
				fields[39] = tc.value
			}
			link, err := ParseLink(makeLink(22, fields))
			if (err == nil) != tc.ok {
				t.Fatalf("ParseLink = %v, want ok %v", err, tc.ok)
			}
			if err != nil {
				if !strings.Contains(err.Error(), "field 39") {
					t.Fatalf("error does not name the field: %v", err)
				}
				return
			}
			if link.Profile.PayloadSize != tc.want {
				t.Fatalf("payload_size = %d, want %d", link.Profile.PayloadSize, tc.want)
			}
		})
	}
}

func TestParseLinkName(t *testing.T) {
	for _, tc := range []struct {
		name string
		ok   bool
	}{
		{"home", true},
		{"work-2", true},
		{"", false},
		{"home name", false},
		{"home/1", false},
		{"-home", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			link, err := ParseLink(makeLink(22, linkFields(map[int]string{2: tc.name})))
			if err != nil {
				t.Fatal(err)
			}
			if tc.ok && link.Name != tc.name {
				t.Fatalf("name = %q, want %q", link.Name, tc.name)
			}
			if !tc.ok && link.Name != "" {
				t.Fatalf("name = %q, want empty", link.Name)
			}
		})
	}
}

// Every refusal names the field (or the rule) and never repeats the link, the password, the
// key or the passphrase: the link is the secret.
func TestParseLinkRefusals(t *testing.T) {
	keys := newSSHLinkKey(t)
	dnstt := linkFields(map[int]string{12: "synthetic-user", 13: linkTestPassword})
	ssh := linkFields(map[int]string{1: "dnstt_ssh", 15: linkTestSSHUser, 16: linkTestSSHPass})
	sshKey := func(over map[int]string) map[int]string {
		fields := linkFields(map[int]string{1: "dnstt_ssh", 15: linkTestSSHUser, 23: "key"})
		for i, v := range over {
			fields[i] = v
		}
		return fields
	}

	for _, tc := range []struct {
		name   string
		fields map[int]string
		want   []string
	}{
		{"sayedns names NoizDNS", linkFields(map[int]string{1: "sayedns", 12: "synthetic-user", 13: linkTestPassword}), []string{"field 1", "NoizDNS"}},
		{"sayedns_ssh names NoizDNS", linkFields(map[int]string{1: "sayedns_ssh", 15: linkTestSSHUser, 16: linkTestSSHPass}), []string{"field 1", "NoizDNS"}},
		{"an unknown type is named", linkFields(map[int]string{1: "vless", 12: "synthetic-user", 13: linkTestPassword}), []string{"field 1", "vless"}},
		{"a missing type", linkFields(map[int]string{1: ""}), []string{"field 1", "type"}},
		{"a dnstt_ssh link without a login", linkFields(map[int]string{1: "dnstt_ssh", 15: "", 16: linkTestSSHPass}), []string{"field 15", "login"}},
		{"a key link without a private key", sshKey(nil), []string{"field 24", "no private key"}},
		{"an unknown SSH authentication type", linkFields(map[int]string{1: "dnstt_ssh", 15: linkTestSSHUser, 16: linkTestSSHPass, 23: "agent"}), []string{"field 23", "password", "key"}},
		{"a private key that is not base64", sshKey(map[int]string{24: "%%%"}), []string{"field 24", "base64"}},
		{"an encrypted key without a passphrase", sshKey(map[int]string{24: base64.StdEncoding.EncodeToString(keys.encrypted)}), []string{"field 25", "passphrase"}},
		{"an encrypted key with a wrong passphrase", sshKey(map[int]string{
			24: base64.StdEncoding.EncodeToString(keys.encrypted),
			25: base64.StdEncoding.EncodeToString([]byte(linkTestWrongPassphrase)),
		}), []string{"field 24", "passphrase"}},
		{"a password without a login", linkFields(map[int]string{12: "", 13: linkTestPassword}), []string{"field 13", "login"}},
		{"a bad domain", linkFields(map[int]string{3: "bad_domain.example.com", 12: "synthetic-user", 13: linkTestPassword}), []string{"field 3", "domain"}},
		{"a bad key", linkFields(map[int]string{11: "not-a-key", 12: "synthetic-user", 13: linkTestPassword}), []string{"field 11", "64 hex"}},
		{"no resolver", linkFields(map[int]string{4: "", 37: "", 12: "synthetic-user", 13: linkTestPassword}), []string{"field 4", "resolver"}},
		{"an IPv6 literal without brackets", linkFields(map[int]string{4: "2001:db8::53", 12: "synthetic-user", 13: linkTestPassword}), []string{"field 4"}},
		{"a bracketed IPv6 literal", linkFields(map[int]string{4: "[2001:db8::53]:53", 12: "synthetic-user", 13: linkTestPassword}), []string{"field 4"}},
		{"a non-https DoH URL", linkFields(map[int]string{4: "", 21: "http://doh.example/dns-query", 22: "doh", 12: "synthetic-user", 13: linkTestPassword}), []string{"field 21", "https"}},
		{"a bad version", linkFields(map[int]string{0: "x", 12: "synthetic-user", 13: linkTestPassword}), []string{"field 0", "version"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			link, err := ParseLink(makeLink(22, tc.fields))
			if err == nil {
				t.Fatalf("ParseLink = %+v, want an error", link)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q does not contain %q", err, want)
				}
			}
			if strings.Contains(err.Error(), linkTestPassword) ||
				strings.Contains(err.Error(), linkTestSSHPass) ||
				strings.Contains(err.Error(), linkTestKey) ||
				strings.Contains(err.Error(), dnstt[12]) ||
				strings.Contains(err.Error(), ssh[15]) ||
				strings.Contains(err.Error(), linkTestPassphrase) ||
				strings.Contains(err.Error(), linkTestWrongPassphrase) ||
				strings.Contains(err.Error(), "PRIVATE KEY") {
				t.Fatalf("the error repeats a value from the link: %v", err)
			}
		})
	}
}

// A dnstt_ssh link becomes an ssh exit: the login comes from field 15 and the credential
// from field 16 or fields 23–25, and the fingerprint is left to the import, because a link
// carries none.
func TestParseLinkSSH(t *testing.T) {
	keys := newSSHLinkKey(t)

	t.Run("a password link", func(t *testing.T) {
		link, err := ParseLink(makeLink(22, linkFields(map[int]string{
			1:  "dnstt_ssh",
			15: linkTestSSHUser,
			16: linkTestSSHPass,
		})))
		if err != nil {
			t.Fatal(err)
		}
		if link.Profile.Exit.Type != "ssh" || link.Profile.Exit.User != linkTestSSHUser {
			t.Fatalf("exit = %+v", link.Profile.Exit)
		}
		if link.Profile.Exit.HostKey != "" {
			t.Fatalf("host_key = %q, want it empty for the import to ask", link.Profile.Exit.HostKey)
		}
		if link.Profile.Exit.PasswordRef != "" || link.Profile.Exit.KeyRef != "" {
			t.Fatalf("the references are not empty before the store: %+v", link.Profile.Exit)
		}
		if link.Password != linkTestSSHPass {
			t.Fatal("the password is not returned separately")
		}
		if link.Key != "" {
			t.Fatal("a password link returned a key")
		}
	})

	t.Run("a key link", func(t *testing.T) {
		link, err := ParseLink(makeLink(22, linkFields(map[int]string{
			1:  "dnstt_ssh",
			15: linkTestSSHUser,
			23: " Key ", // trimmed and lower-cased
			24: base64.StdEncoding.EncodeToString(keys.plain),
		})))
		if err != nil {
			t.Fatal(err)
		}
		if link.Profile.Exit.Type != "ssh" || link.Profile.Exit.User != linkTestSSHUser {
			t.Fatalf("exit = %+v", link.Profile.Exit)
		}
		if link.Key != string(keys.plain) {
			t.Fatal("the unencrypted key did not come back unchanged")
		}
		if link.Password != "" {
			t.Fatal("a key link returned a password")
		}
	})

	t.Run("an encrypted key with its passphrase", func(t *testing.T) {
		link, err := ParseLink(makeLink(22, linkFields(map[int]string{
			1:  "dnstt_ssh",
			15: linkTestSSHUser,
			23: "key",
			24: base64.StdEncoding.EncodeToString(keys.encrypted),
			25: base64.StdEncoding.EncodeToString([]byte(linkTestPassphrase)),
		})))
		if err != nil {
			t.Fatal(err)
		}
		signer, err := exit.ParseKey([]byte(link.Key), "")
		if err != nil {
			t.Fatalf("the decrypted key does not parse without a passphrase: %v", err)
		}
		if !bytes.Equal(signer.PublicKey().Marshal(), keys.signer.PublicKey().Marshal()) {
			t.Fatal("the decrypted key is another key")
		}
		message := []byte("synthetic message")
		signature, err := signer.Sign(rand.Reader, message)
		if err != nil {
			t.Fatalf("the decrypted key does not sign: %v", err)
		}
		if err := keys.signer.PublicKey().Verify(message, signature); err != nil {
			t.Fatalf("the original public key does not verify the signature: %v", err)
		}
		if strings.Contains(link.Key, linkTestPassphrase) {
			t.Fatal("the passphrase reached the key")
		}
	})

	t.Run("no authentication type", func(t *testing.T) {
		// SlipGate's older lines carry no type: the value in the field decides.
		password, err := ParseLink(makeLink(22, linkFields(map[int]string{
			1:  "dnstt_ssh",
			15: linkTestSSHUser,
			16: linkTestSSHPass,
		})))
		if err != nil {
			t.Fatal(err)
		}
		if password.Password != linkTestSSHPass || password.Key != "" {
			t.Fatalf("password = %q, key set: %v; want the password", password.Password, password.Key != "")
		}

		key, err := ParseLink(makeLink(22, linkFields(map[int]string{
			1:  "dnstt_ssh",
			15: linkTestSSHUser,
			24: base64.StdEncoding.EncodeToString(keys.plain),
		})))
		if err != nil {
			t.Fatal(err)
		}
		if key.Key != string(keys.plain) || key.Password != "" {
			t.Fatal("a line with only field 24 is not a key link")
		}
	})

	t.Run("a password type ignores the key", func(t *testing.T) {
		link, err := ParseLink(makeLink(22, linkFields(map[int]string{
			1:  "dnstt_ssh",
			15: linkTestSSHUser,
			16: linkTestSSHPass,
			23: "password",
			24: base64.StdEncoding.EncodeToString(keys.plain),
		})))
		if err != nil {
			t.Fatal(err)
		}
		if link.Profile.Exit.Type != "ssh" || link.Profile.Exit.User != linkTestSSHUser {
			t.Fatalf("exit = %+v", link.Profile.Exit)
		}
		if link.Password != linkTestSSHPass || link.Key != "" {
			t.Fatalf("password = %q, key set: %v; want the password only", link.Password, link.Key != "")
		}
	})

	t.Run("a password type with no password", func(t *testing.T) {
		// The explicit type decides: the key in field 24 belongs to the other method, so
		// this is a password link without a password, not a key link.
		link, err := ParseLink(makeLink(22, linkFields(map[int]string{
			1:  "dnstt_ssh",
			15: linkTestSSHUser,
			23: "password",
			24: base64.StdEncoding.EncodeToString(keys.plain),
		})))
		if err != nil {
			t.Fatal(err)
		}
		if link.Profile.Exit.Type != "ssh" {
			t.Fatalf("exit = %+v", link.Profile.Exit)
		}
		if link.Password != "" || link.Key != "" {
			t.Fatalf("password = %q, key set: %v; want neither", link.Password, link.Key != "")
		}
	})

	t.Run("the login comes from field 15", func(t *testing.T) {
		link, err := ParseLink(makeLink(22, linkFields(map[int]string{
			1:  "dnstt_ssh",
			12: "synthetic-socks-user",
			13: linkTestPassword,
			15: linkTestSSHUser,
			16: linkTestSSHPass,
		})))
		if err != nil {
			t.Fatal(err)
		}
		if link.Profile.Exit.User != linkTestSSHUser {
			t.Fatalf("user = %q, want the SSH login of field 15", link.Profile.Exit.User)
		}
		if link.Password != linkTestSSHPass {
			t.Fatal("the SOCKS5 password of field 13 was taken")
		}
	})
}

func TestParseLinkControlCharactersInCredentials(t *testing.T) {
	const control = "\x1b[2J\x1b[H"
	for _, tc := range []struct {
		name   string
		fields map[int]string
		field  string
	}{
		{"dnstt login", linkFields(map[int]string{12: "synthetic-user" + control, 13: linkTestPassword}), "field 12"},
		{"dnstt password", linkFields(map[int]string{12: "synthetic-user", 13: "synthetic-password" + control}), "field 13"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseLink(makeLink(22, tc.fields))
			if err == nil {
				t.Fatal("ParseLink accepted a credential with a control character")
			}
			if !strings.Contains(err.Error(), tc.field) || !strings.Contains(err.Error(), "control") {
				t.Fatalf("error = %v, want %s and the control-character rule", err, tc.field)
			}
			if strings.Contains(err.Error(), control) {
				t.Fatalf("the error repeats the value: %v", err)
			}
		})
	}
}

func TestParseLinkSecretsNotInErrors(t *testing.T) {
	// The password and the key are valid values of a link whose domain is bad: the error
	// must name the field only.
	link := makeLink(22, linkFields(map[int]string{
		3:  "bad_domain.example.com",
		12: "synthetic-user",
		13: linkTestPassword,
	}))
	_, err := ParseLink(link)
	if err == nil {
		t.Fatal("ParseLink accepted a bad domain")
	}
	if strings.Contains(err.Error(), linkTestPassword) || strings.Contains(err.Error(), linkTestKey) {
		t.Fatalf("the error repeats the password or the key: %v", err)
	}
	if strings.Contains(err.Error(), strings.TrimPrefix(link, "slipnet://")) {
		t.Fatalf("the error repeats the link: %v", err)
	}
}

func TestParseLinks(t *testing.T) {
	first := makeLink(22, linkFields(map[int]string{2: "first"}))
	second := makeLink(22, linkFields(map[int]string{2: "second", 12: "synthetic-user", 13: linkTestPassword}))
	text := strings.Join([]string{first, "not a link", "slipnet://%%%", second}, "\r\n")

	links, warnings := ParseLinks(text)
	if len(links) != 2 {
		t.Fatalf("links = %d, want 2", len(links))
	}
	if links[0].Name != "first" || links[1].Name != "second" {
		t.Fatalf("order = %q, %q", links[0].Name, links[1].Name)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %d, want 2", len(warnings))
	}
	if !strings.Contains(warnings[0].Error(), "line 2") || !strings.Contains(warnings[1].Error(), "line 3") {
		t.Fatalf("warnings do not name their lines: %v", warnings)
	}
	for _, warning := range warnings {
		if strings.Contains(warning.Error(), linkTestPassword) || strings.Contains(warning.Error(), "%%%") {
			t.Fatalf("the warning repeats content: %v", warning)
		}
	}
}

func TestParseLinksSkipsBlankLines(t *testing.T) {
	links, warnings := ParseLinks("\n  \r\n\t\n")
	if len(links) != 0 || len(warnings) != 0 {
		t.Fatalf("links = %d, warnings = %v; want nothing", len(links), warnings)
	}
}
