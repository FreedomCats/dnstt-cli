package app

import (
	"strings"
	"testing"

	"github.com/FreedomCats/dnstt-cli/internal/config"
	"github.com/FreedomCats/dnstt-cli/internal/datapath"
)

// tun_dns is an IPv4 address without a port; empty is the default, and anything else is
// refused with a reason that names the profile key and the value.
func TestParseTunDNS(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
		wantErr        string
	}{
		{name: "empty", in: "", want: "1.1.1.1"},
		{name: "address", in: "9.9.9.9", want: "9.9.9.9"},
		{name: "spaces", in: " 9.9.9.9\t", want: "9.9.9.9"},
		{name: "port", in: "1.1.1.1:53", wantErr: `tun_dns "1.1.1.1:53" is not an IPv4 address`},
		{name: "IPv6", in: "2001:db8::53", wantErr: `tun_dns "2001:db8::53" is not an IPv4 address`},
		{name: "IPv4-mapped IPv6", in: "::ffff:192.0.2.53", wantErr: `tun_dns "::ffff:192.0.2.53" is not an IPv4 address`},
		{name: "name", in: "dns.example", wantErr: `tun_dns "dns.example" is not an IPv4 address`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTunDNS(tc.in)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("parseTunDNS(%q) = %v, %v; want %q", tc.in, got, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseTunDNS(%q): %v", tc.in, err)
			}
			if got.String() != tc.want {
				t.Errorf("parseTunDNS(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
	if got, err := parseTunDNS(""); err != nil || got != defaultTunDNS || defaultTunDNS.String() != "1.1.1.1" {
		t.Errorf("the default tun_dns = %v, %v, want 1.1.1.1", got, err)
	}
}

// The tun profile gives a *datapath.Tun that asks for root and netguard and counts as
// ready: the adapter itself opens on the first Start, so nothing here needs /dev/net/tun.
func TestBuildDatapathTun(t *testing.T) {
	p := testProfile(config.Exit{Type: "socks5"})
	p.Datapath = "tun"
	p.TunDNS = "9.9.9.9"
	tun, err := buildTunnel(TunnelOptions{Profile: p}, func(string) (string, error) { return "", nil })
	if err != nil {
		t.Fatal(err)
	}
	dp, err := BuildDatapath(tun, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dp.Close()
	if _, ok := dp.(*datapath.Tun); !ok {
		t.Fatalf("the tun profile gave %T, want *datapath.Tun", dp)
	}
	if req := dp.Requires(); !req.Admin || !req.Netguard || !req.DNSGuard {
		t.Errorf("Requires = %+v, want Admin, Netguard and DNSGuard", req)
	}
}

// A bad tun_dns is refused by BuildDatapath, before an adapter could be opened.
func TestBuildDatapathBadTunDNS(t *testing.T) {
	for _, dns := range []string{"1.1.1.1:53", "2001:db8::53", "dns.example"} {
		t.Run(dns, func(t *testing.T) {
			p := testProfile(config.Exit{Type: "socks5"})
			p.Datapath = "tun"
			p.TunDNS = dns
			tun, err := buildTunnel(TunnelOptions{Profile: p}, func(string) (string, error) { return "", nil })
			if err != nil {
				t.Fatal(err)
			}
			dp, err := BuildDatapath(tun, p, nil)
			if err == nil || dp != nil {
				t.Fatalf("BuildDatapath = %v, %v; want a tun_dns refusal", dp, err)
			}
			if !strings.Contains(err.Error(), "tun_dns") || !strings.Contains(err.Error(), dns) {
				t.Errorf("error %q does not name tun_dns and its value", err)
			}
		})
	}
}

// The socks profile still gives *datapath.Socks, with nothing required.
func TestBuildDatapathSocks(t *testing.T) {
	p := testProfile(config.Exit{Type: "socks5"})
	p.Datapath = "socks"
	tun, err := buildTunnel(TunnelOptions{Profile: p}, func(string) (string, error) { return "", nil })
	if err != nil {
		t.Fatal(err)
	}
	dp, err := BuildDatapath(tun, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dp.Close()
	if _, ok := dp.(*datapath.Socks); !ok {
		t.Fatalf("the socks profile gave %T, want *datapath.Socks", dp)
	}
	if req := dp.Requires(); req != (datapath.Requirements{}) {
		t.Errorf("Requires = %+v, want the zero value", req)
	}
}
