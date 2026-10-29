package winsys

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// dnsProbeEnv switches on the helper mode: send a DNS query to the server from the value
// and exit with code 0 if a reply came.
const dnsProbeEnv = "DNSTT_CLI_TEST_DNS_PROBE"

// dnsProbeServer is a public resolver (Quad9) reached over plain UDP.
const dnsProbeServer = "9.9.9.9:53"

func TestMain(m *testing.M) {
	if server, ok := os.LookupEnv(dnsProbeEnv); ok {
		if err := dnsProbe(server); err != nil {
			os.Stderr.WriteString(err.Error())
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// dnsProbeTries is how many times the probe sends the query: a single datagram to the
// public resolver from the CI cloud gets lost even without protection.
const dnsProbeTries = 3

// dnsProbe asks server for the A record of example.com and waits for a reply with the same
// query ID; without a reply it repeats, up to dnsProbeTries times.
func dnsProbe(server string) error {
	conn, err := net.DialTimeout("udp4", server, 3*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	query := []byte{0x51, 0x7e, 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0}
	for _, label := range []string{"example", "com"} {
		query = append(append(query, byte(len(label))), label...)
	}
	query = append(query, 0, 0, 1, 0, 1)
	reply := make([]byte, 512)
	for try := 1; ; try++ {
		if _, err := conn.Write(query); err != nil {
			return err
		}
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := conn.Read(reply)
		if err != nil {
			if try < dnsProbeTries {
				continue
			}
			return err
		}
		if n < 12 || binary.BigEndian.Uint16(reply) != 0x517e {
			return errors.New("unexpected DNS reply")
		}
		return nil
	}
}

// shortDir returns a directory with a long name inside t.TempDir() and its path with short
// 8.3 names; short is empty if the volume does not create them.
func shortDir(t *testing.T) (long, short string) {
	t.Helper()
	long = filepath.Join(t.TempDir(), "dnstt long directory name")
	if err := os.Mkdir(long, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := windows.UTF16PtrFromString(long)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, windows.MAX_PATH)
	n, err := windows.GetShortPathName(p, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) >= len(buf) {
		return long, ""
	}
	if short = windows.UTF16ToString(buf[:n]); strings.EqualFold(short, long) {
		return long, ""
	}
	return long, short
}

// A path with short 8.3 names becomes long; a path without a file stays as it is.
func TestLongPath(t *testing.T) {
	long, short := shortDir(t)
	if short == "" {
		t.Skip("the volume does not create short 8.3 names")
	}
	// Comparing with long is not possible: t.TempDir() itself can be short (TEMP in CI).
	got := longPath(short)
	if _, err := os.Stat(got); err != nil || strings.Contains(got, "~") ||
		!strings.EqualFold(filepath.Base(got), filepath.Base(long)) {
		t.Errorf("longPath(%q) = %q (%v), want a path without short names to %s", short, got, err, filepath.Base(long))
	}
	missing := filepath.Join(short, "no such file.exe")
	if got := longPath(missing); got != missing {
		t.Errorf("longPath(%q) = %q, want the path as is", missing, got)
	}
}

// WFP recognises a program from programs by the path to its exe: under "closed" its DNS
// query to the public resolver passes, while the same program under another path is blocked. This
// is how the kill-switch lets through an extra program passed to OpenDNSGuard (dnstt-cli
// passes none: the core runs inside dnstt-cli.exe). The path has short 8.3 names if the
// volume creates them: WFP knows the process by the long ones (this is the case in GitHub
// CI, where TEMP is "RUNNER~1"). Administrator rights and access to the public resolver are
// required.
func TestDNSGuardPermitsPrograms(t *testing.T) {
	dir, short := shortDir(t)
	if short != "" {
		dir = short
	}
	allowed, other := filepath.Join(dir, "allowed.exe"), filepath.Join(dir, "other.exe")
	for _, path := range []string{allowed, other} {
		copyFile(t, os.Args[0], path)
	}
	probe := func(exe string) error {
		cmd := exec.Command(exe)
		cmd.Env = append(os.Environ(), dnsProbeEnv+"="+dnsProbeServer)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return errors.New(string(out))
		}
		return nil
	}
	for _, exe := range []string{allowed, other} {
		if err := probe(exe); err != nil {
			t.Skipf("the public resolver is unreachable even without protection (%s): %v", filepath.Base(exe), err)
		}
	}

	g, err := OpenDNSGuard(allowed)
	if err != nil {
		t.Skipf("WFP session did not open (administrator rights required): %v", err)
	}
	defer g.Close()
	if len(g.appIDs) != 2 {
		t.Fatalf("%d application IDs, want two", len(g.appIDs))
	}
	if err := g.Lockdown(); err != nil {
		t.Fatalf("Lockdown: %v", err)
	}
	if err := probe(allowed); err != nil {
		t.Errorf("under \"closed\" the query of the allowed program did not pass: %v", err)
	}
	if err := probe(other); err == nil {
		t.Error("under \"closed\" the query of another program passed")
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	src, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.Create(to)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		t.Fatal(err)
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
}
