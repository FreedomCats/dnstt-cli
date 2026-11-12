package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFiles(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// The order is Windows, Linux, an unknown system, then the sources and the checksums, not
// alphabetical; extra files in the directory do not matter; the caveat of a system goes in
// brackets after the architecture.
func TestRun(t *testing.T) {
	saved := osNotes
	osNotes = map[string]string{"freebsd": "not checked", "windows": saved["windows"]}
	t.Cleanup(func() { osNotes = saved })
	dir := writeFiles(t,
		"dnstt-cli_v0.2.0_freebsd_amd64.tar.gz",
		"dnstt-cli_v0.2.0_linux_amd64.tar.gz",
		"dnstt-cli_v0.2.0_windows_amd64.zip",
		"SHA256SUMS.txt",
		"notes.txt",
	)
	got, err := run(dir, "owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	const d = "https://github.com/owner/repo/releases/download/v0.2.0/"
	want := "## Download\n\n" +
		"- **Windows** (x86-64, socks mode; tun is not available yet): [dnstt-cli_v0.2.0_windows_amd64.zip](" + d + "dnstt-cli_v0.2.0_windows_amd64.zip)\n" +
		"- **Linux** (x86-64): [dnstt-cli_v0.2.0_linux_amd64.tar.gz](" + d + "dnstt-cli_v0.2.0_linux_amd64.tar.gz)\n" +
		"- **freebsd** (x86-64, not checked): [dnstt-cli_v0.2.0_freebsd_amd64.tar.gz](" + d + "dnstt-cli_v0.2.0_freebsd_amd64.tar.gz)\n" +
		"- **Sources**: [tar.gz](https://github.com/owner/repo/archive/refs/tags/v0.2.0.tar.gz) · " +
		"[zip](https://github.com/owner/repo/archive/refs/tags/v0.2.0.zip)\n" +
		"- **SHA-256 checksums**: [SHA256SUMS.txt](" + d + "SHA256SUMS.txt)\n" +
		"\nStart here — [README](https://github.com/owner/repo/blob/v0.2.0/README.md).\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRunErrors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		repo  string
		files []string
		want  string
	}{
		{"no repository", "", []string{"dnstt-cli_v1_linux_amd64.tar.gz", "SHA256SUMS.txt"}, "no repository"},
		{"no archives", "o/r", []string{"SHA256SUMS.txt"}, "has no archives"},
		{"no checksums", "o/r", []string{"dnstt-cli_v1_linux_amd64.tar.gz"}, "has no SHA256SUMS.txt"},
		{"different versions", "o/r", []string{"dnstt-cli_v1_linux_amd64.tar.gz", "dnstt-cli_v2_windows_amd64.zip", "SHA256SUMS.txt"}, "different versions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := run(writeFiles(t, tc.files...), tc.repo)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v, want %q", err, tc.want)
			}
		})
	}
}

func TestParseArchive(t *testing.T) {
	for name, want := range map[string]archive{
		"dnstt-cli_v0.1.0_windows_amd64.zip":           {name: "dnstt-cli_v0.1.0_windows_amd64.zip", version: "v0.1.0", goos: "windows", goarch: "amd64"},
		"dnstt-cli_v0.1.0-3-gabc_linux_amd64.tar.gz":   {name: "dnstt-cli_v0.1.0-3-gabc_linux_amd64.tar.gz", version: "v0.1.0-3-gabc", goos: "linux", goarch: "amd64"},
		"dnstt-cli_v0.1.0_windows_amd64/dnstt-cli.exe": {},
		"other_v0.1.0_linux_amd64.tar.gz":              {},
		"dnstt-cli_linux.tar.gz":                       {},
	} {
		got, ok := parseArchive(name)
		if ok != (want.name != "") || got != want {
			t.Errorf("%s: %+v, %v", name, got, ok)
		}
	}
}
