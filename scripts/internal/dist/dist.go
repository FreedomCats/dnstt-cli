// Package dist is the content of a release archive: what scripts/release lays out and
// what scripts/distcheck checks. One list, so the two cannot drift.
package dist

// Docs are the files in every archive. COPYING is the license of the dnstt core
// (core/COPYING), flattened into the archive root under the name the upstream dnstt uses
// for it.
var Docs = []string{"README.md", "LICENSE", "COPYING", "THIRD_PARTY_NOTICES.txt", "dnstt-cli.example.toml"}

// Wintun are the Wintun files in the Windows archive (scripts/internal/wintun).
var Wintun = []string{"wintun.dll", "wintun-LICENSE.txt"}

// Executables are the program in the archive for goos. In a tar.gz it has the right to
// execute.
func Executables(goos string) []string {
	if goos == "windows" {
		return []string{"dnstt-cli.exe"}
	}
	return []string{"dnstt-cli"}
}

// Files is the whole content of the archive for goos.
func Files(goos string) []string {
	files := Executables(goos)
	if goos == "windows" {
		files = append(files, Wintun...)
	}
	return append(files, Docs...)
}
