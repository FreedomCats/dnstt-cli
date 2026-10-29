package netguard

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
	"testing"
)

// fakeConn is a syscall.RawConn whose Control just calls the function.
type fakeConn struct{}

func (fakeConn) Control(f func(fd uintptr)) error  { f(42); return nil }
func (fakeConn) Read(func(fd uintptr) bool) error  { return errors.New("not needed") }
func (fakeConn) Write(func(fd uintptr) bool) error { return errors.New("not needed") }

var _ syscall.RawConn = fakeConn{}

// recorder records setter calls: "4=12", "6=15".
type recorder struct {
	calls []string
	fail  error
}

func (r *recorder) set(fd uintptr, ipv6 bool, index uint32) error {
	family := "4"
	if ipv6 {
		family = "6"
	}
	r.calls = append(r.calls, fmt.Sprintf("%s=%d", family, index))
	return r.fail
}

func TestApply(t *testing.T) {
	for _, tc := range []struct {
		name             string
		index4, index6   uint32
		network, address string
		want             string // calls, comma-separated
		wantErr          string // substring of the error
		// wantSingle is the calls where there is one binding per socket (Linux,
		// bindPerFamily = false); empty means the same as want.
		wantSingle string
	}{
		{"tcp4", 12, 15, "tcp4", "203.0.113.80:443", "4=12", "", ""},
		{"udp4", 12, 15, "udp4", "203.0.113.165:3478", "4=12", "", ""},
		{"tcp6", 12, 15, "tcp6", "[2001:db8:bd::1]:443", "6=15", "", ""},
		{"udp6 with zone", 12, 15, "udp6", "[fe80::1%3]:53", "6=15", "", ""},
		{"loopback IPv4", 12, 15, "udp4", "127.0.0.1:9000", "", "", ""},
		{"loopback IPv6", 12, 15, "tcp6", "[::1]:9000", "", "", ""},
		{"loopback mapped to IPv6", 12, 15, "udp6", "[::ffff:127.0.0.1]:9000", "", "", ""},
		{"no IPv4", 0, 15, "tcp4", "1.1.1.1:443", "", "no physical interface for IPv4", ""},
		{"no IPv6", 12, 0, "tcp6", "[2606:4700::1]:443", "", "no physical interface for IPv6", ""},
		{"nothing chosen", 0, 0, "udp4", "1.1.1.1:53", "", "socket udp4 1.1.1.1:53 is not created", ""},
		{"dual-stack listener", 12, 15, "udp6", "[::]:0", "4=12,6=15", "", "4=12"},
		{"dual-stack, empty host", 12, 15, "udp6", ":0", "4=12,6=15", "", "4=12"},
		{"dual-stack on 0.0.0.0", 12, 15, "udp6", "0.0.0.0:0", "4=12,6=15", "", "4=12"},
		{"dual-stack without IPv6", 12, 0, "udp6", "[::]:0", "4=12", "", ""},
		{"dual-stack without IPv4", 0, 15, "udp6", "[::]:0", "6=15", "", ""},
		{"dual-stack, nothing chosen", 0, 0, "udp6", "[::]:0", "", "for IPv4 and IPv6", ""},
		{"IPv4 listener on 0.0.0.0", 12, 15, "udp4", "0.0.0.0:0", "4=12", "", ""},
		{"bare network", 12, 15, "tcp", "1.1.1.1:443", "", "cannot tell the family", ""},
		{"address without port", 12, 15, "tcp4", "1.1.1.1", "", "cannot parse address", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var g Guard
			g.SetInterfaces(tc.index4, tc.index6)
			var r recorder
			err := g.apply(tc.network, tc.address, fakeConn{}, r.set)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %v, want one with %q", err, tc.wantErr)
				}
				if len(r.calls) != 0 {
					t.Fatalf("setter called despite the error: %v", r.calls)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := tc.want
			if !bindPerFamily && tc.wantSingle != "" {
				want = tc.wantSingle
			}
			if got := strings.Join(r.calls, ","); got != want {
				t.Fatalf("calls %q, want %q", got, want)
			}
		})
	}
}

func TestApplySetterError(t *testing.T) {
	var g Guard
	g.SetInterfaces(12, 15)
	r := recorder{fail: errors.New("WSAEINVAL")}
	err := g.apply("udp6", "[::]:0", fakeConn{}, r.set)
	if err == nil || !strings.Contains(err.Error(), option4+"=12") || !strings.Contains(err.Error(), "WSAEINVAL") {
		t.Fatalf("error %v", err)
	}
	// After the first option fails, the second is not set.
	if len(r.calls) != 1 {
		t.Fatalf("calls %v", r.calls)
	}
}

func TestSetInterfacesOnTheFly(t *testing.T) {
	var g Guard
	if a, b := g.Interfaces(); a != 0 || b != 0 {
		t.Fatalf("zero Guard: %d, %d", a, b)
	}
	g.SetInterfaces(3, 0)
	g.SetInterfaces(7, 9)
	var r recorder
	if err := g.apply("tcp4", "1.1.1.1:443", fakeConn{}, r.set); err != nil {
		t.Fatal(err)
	}
	if r.calls[0] != "4=7" {
		t.Fatalf("calls %v", r.calls)
	}
}

func TestLogf(t *testing.T) {
	var lines []string
	g := Guard{Logf: func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }}
	g.SetInterfaces(3, 0)
	var r recorder
	if err := g.apply("tcp4", "203.0.113.80:443", fakeConn{}, r.set); err != nil {
		t.Fatal(err)
	}
	// Loopback and a refusal give no lines.
	_ = g.apply("udp4", "127.0.0.1:9000", fakeConn{}, r.set)
	_ = g.apply("tcp6", "[2a00::1]:443", fakeConn{}, r.set)
	if len(lines) != 1 || lines[0] != "[NETGUARD] tcp4 203.0.113.80:443 → interface 3" {
		t.Fatalf("lines %q", lines)
	}
}
