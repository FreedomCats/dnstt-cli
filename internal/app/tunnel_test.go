package app

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/FreedomCats/dnstt-cli/internal/resolvers"
)

func TestProfileBootstrapNetworkDNSError(t *testing.T) {
	want := errors.New("network DNS unavailable")
	b, err := newProfileBootstrap(nil, nil, func() ([]netip.Addr, error) { return nil, want }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.LookupIP(context.Background(), "doh.example"); !errors.Is(err, want) {
		t.Fatalf("lookup error %v, want %v", err, want)
	}
}

func TestProfileBootstrapUsesUDPFallbackAfterNetworkDNSError(t *testing.T) {
	server := startBootstrapDNSServer(t, "192.0.2.12")
	list, err := resolvers.Expand([]string{"udp:" + server.String()})
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("network DNS unavailable")
	b, err := newProfileBootstrap(nil, list, func() ([]netip.Addr, error) { return nil, want }, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := b.LookupIP(ctx, "doh.example")
	if err != nil || len(got) != 1 || got[0] != netip.MustParseAddr("192.0.2.12") {
		t.Fatalf("LookupIP = %v, %v", got, err)
	}
}

func TestProfileBootstrapExplicitServersOverrideNetwork(t *testing.T) {
	server := startBootstrapDNSServer(t, "192.0.2.13")
	want := errors.New("must not read network DNS")
	b, err := newProfileBootstrap([]string{server.String()}, nil, func() ([]netip.Addr, error) {
		return nil, want
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := b.LookupIP(ctx, "doh.example")
	if err != nil || len(got) != 1 || got[0] != netip.MustParseAddr("192.0.2.13") {
		t.Fatalf("LookupIP = %v, %v", got, err)
	}
}

func startBootstrapDNSServer(t *testing.T, answer string) netip.AddrPort {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			var parser dnsmessage.Parser
			header, err := parser.Start(buf[:n])
			if err != nil {
				continue
			}
			question, err := parser.Question()
			if err != nil {
				continue
			}
			builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: header.ID, Response: true, RecursionDesired: true, RecursionAvailable: true})
			if err := builder.StartQuestions(); err != nil {
				continue
			}
			if err := builder.Question(question); err != nil {
				continue
			}
			if err := builder.StartAnswers(); err != nil {
				continue
			}
			addr := netip.MustParseAddr(answer).As4()
			if err := builder.AResource(dnsmessage.ResourceHeader{Name: question.Name, Class: dnsmessage.ClassINET, TTL: 60}, dnsmessage.AResource{A: addr}); err != nil {
				continue
			}
			msg, err := builder.Finish()
			if err == nil {
				pc.WriteTo(msg, from)
			}
		}
	}()
	addr, err := netip.ParseAddrPort(pc.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	return addr
}
