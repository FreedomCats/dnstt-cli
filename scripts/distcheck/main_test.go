package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/FreedomCats/dnstt-cli/scripts/internal/dist"
)

// fakePE is a PE header for machine (0x8664 is x86-64): the DOS header with the pointer at
// 0x3c and the COFF header with the machine. debug/pe reads this much.
func fakePE(machine uint16) []byte {
	h := make([]byte, 128)
	copy(h, "MZ")
	binary.LittleEndian.PutUint32(h[0x3c:], 64)
	copy(h[64:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(h[68:], machine)
	return h
}

// writeZip writes entries with the given bodies. A body is the content; the rest is empty.
func writeZip(t *testing.T, path string, files map[string][]byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, name := range sortedKeys(files) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(files[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// writeTarGz writes entries with the given modes and bodies.
func writeTarGz(t *testing.T, path string, modes map[string]int64, bodies map[string][]byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, name := range sortedKeys(modes) {
		body := bodies[name]
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: modes[name], Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// The archive order does not matter to the checks; any fixed order does.
	sort.Strings(keys)
	return keys
}

// linuxLayout is the whole Linux archive: the files with their modes.
func linuxLayout() map[string]int64 {
	modes := map[string]int64{}
	for _, f := range dist.Files("linux") {
		modes[f] = 0o644
	}
	modes[dist.Executables("linux")[0]] = 0o755
	return modes
}

// fakeBinary answers like the real one: version, help and the command list.
const fakeBinary = `#!/bin/sh
case "$1" in
version) echo "dnstt-cli v0.1.0 (abc1234)";;
help) echo "Usage: dnstt-cli help [command]";;
*) echo "Commands:"; echo "  connect   connect to the dnstt server";;
esac
`

func prefixed(prefix string, modes map[string]int64) map[string]int64 {
	out := map[string]int64{}
	for name, mode := range modes {
		out[prefix+name] = mode
	}
	return out
}

// A full tar.gz passes; a wrong mode, a missing file, an extra one, an entry outside the
// directory, a flat archive and a backslash in a name are failures.
func TestTarGz(t *testing.T) {
	dir := t.TempDir()
	const name = "dnstt-cli_v0.1.0_linux_amd64.tar.gz"
	path := filepath.Join(dir, name)
	const prefix = "dnstt-cli_v0.1.0_linux_amd64/"
	full := prefixed(prefix, linuxLayout())
	bodies := map[string][]byte{prefix + dist.Executables("linux")[0]: []byte(fakeBinary)}

	writeTarGz(t, path, full, bodies)
	if failed, err := run(dir); err != nil || failed {
		t.Fatalf("a good archive: %v, %v", failed, err)
	}

	for _, c := range []struct {
		name  string
		modes map[string]int64
	}{
		{"the binary without the right to execute", func() map[string]int64 {
			m := prefixed(prefix, linuxLayout())
			m[prefix+dist.Executables("linux")[0]] = 0o644
			return m
		}()},
		{"a document with the right to execute", func() map[string]int64 {
			m := prefixed(prefix, linuxLayout())
			m[prefix+"README.md"] = 0o755
			return m
		}()},
		{"without COPYING", func() map[string]int64 {
			m := prefixed(prefix, linuxLayout())
			delete(m, prefix+"COPYING")
			return m
		}()},
		{"with an extra file", func() map[string]int64 {
			m := prefixed(prefix, linuxLayout())
			m[prefix+"notes.txt"] = 0o644
			return m
		}()},
		{"with an entry outside the directory", func() map[string]int64 {
			m := prefixed(prefix, linuxLayout())
			m["stray.txt"] = 0o644
			return m
		}()},
		{"with a backslash in a name", func() map[string]int64 {
			m := prefixed(prefix, linuxLayout())
			delete(m, prefix+"README.md")
			m[prefix+`docs\README.md`] = 0o644
			return m
		}()},
		{"flat, without the directory", linuxLayout()},
	} {
		writeTarGz(t, path, c.modes, bodies)
		if failed, err := run(dir); err != nil || !failed {
			t.Errorf("%s: %v, %v", c.name, failed, err)
		}
	}
}

// The version from the archive's name must be in the binary: "dev" means the version did
// not arrive through -ldflags. The check unpacks and runs the binary, so it is Linux only.
func TestTarGzVersion(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the Linux binary is run on Linux only")
	}
	const name = "dnstt-cli_v0.1.0_linux_amd64.tar.gz"
	const prefix = "dnstt-cli_v0.1.0_linux_amd64/"
	bodies := map[string][]byte{prefix + dist.Executables("linux")[0]: []byte(fakeBinary)}

	for _, c := range []struct {
		body string
		fail bool
	}{
		{fakeBinary, false},
		{"#!/bin/sh\necho 'dnstt-cli dev (unknown)'\n", true},
		{"#!/bin/sh\necho 'dnstt-cli v0.2.0 (abc1234)'\n", true},
		{"#!/bin/sh\necho 'something else'\n", true},
	} {
		dir := t.TempDir()
		bodies[prefix+dist.Executables("linux")[0]] = []byte(c.body)
		writeTarGz(t, filepath.Join(dir, name), prefixed(prefix, linuxLayout()), bodies)
		if failed, err := run(dir); err != nil || failed != c.fail {
			t.Errorf("%q: failed %v, %v", strings.Split(c.body, "\n")[1], failed, err)
		}
	}
}

func zipLayout() map[string][]byte {
	files := map[string][]byte{}
	for _, f := range dist.Files("windows") {
		files[f] = nil
	}
	files[exe] = fakePE(0x8664)
	return files
}

func prefixedBodies(prefix string, files map[string][]byte) map[string][]byte {
	out := map[string][]byte{}
	for name, body := range files {
		out[prefix+name] = body
	}
	return out
}

// A full zip passes; a missing wintun.dll or wintun-LICENSE.txt, an extra file, an entry
// outside the directory, a name that is not a Windows archive and an exe that is not PE
// are failures. The exe is never run.
func TestZip(t *testing.T) {
	dir := t.TempDir()
	const name = "dnstt-cli_v0.1.0_windows_amd64.zip"
	path := filepath.Join(dir, name)
	const prefix = "dnstt-cli_v0.1.0_windows_amd64/"

	for _, c := range []struct {
		name  string
		files map[string][]byte
		fail  bool
	}{
		{"full", prefixedBodies(prefix, zipLayout()), false},
		{"without wintun.dll", func() map[string][]byte {
			f := prefixedBodies(prefix, zipLayout())
			delete(f, prefix+"wintun.dll")
			return f
		}(), true},
		{"without wintun-LICENSE.txt", func() map[string][]byte {
			f := prefixedBodies(prefix, zipLayout())
			delete(f, prefix+"wintun-LICENSE.txt")
			return f
		}(), true},
		{"with an extra file", func() map[string][]byte {
			f := prefixedBodies(prefix, zipLayout())
			f[prefix+"notes.txt"] = nil
			return f
		}(), true},
		{"with an entry outside the directory", func() map[string][]byte {
			f := prefixedBodies(prefix, zipLayout())
			f["stray.txt"] = nil
			return f
		}(), true},
		{"with a backslash in a name", func() map[string][]byte {
			f := prefixedBodies(prefix, zipLayout())
			delete(f, prefix+"README.md")
			f[prefix+`docs\README.md`] = nil
			return f
		}(), true},
		{"whose exe is not PE", func() map[string][]byte {
			f := prefixedBodies(prefix, zipLayout())
			f[prefix+exe] = []byte("not an executable")
			return f
		}(), true},
		{"whose exe is for another machine", func() map[string][]byte {
			f := prefixedBodies(prefix, zipLayout())
			f[prefix+exe] = fakePE(0xaa64)
			return f
		}(), true},
	} {
		writeZip(t, path, c.files)
		if failed, err := run(dir); err != nil || failed != c.fail {
			t.Errorf("%s: failed %v, %v", c.name, failed, err)
		}
	}
}

// The system in the name must be the one the extension stands for: a renamed archive is
// checked against the wrong list otherwise.
func TestWrongSystem(t *testing.T) {
	dir := t.TempDir()
	writeZip(t, filepath.Join(dir, "dnstt-cli_v0.1.0_linux_amd64.zip"), prefixedBodies("dnstt-cli_v0.1.0_linux_amd64/", zipLayout()))
	if failed, err := run(dir); err != nil || !failed {
		t.Errorf("a Linux zip: %v, %v", failed, err)
	}
}

func TestNoArchives(t *testing.T) {
	if failed, err := run(t.TempDir()); err != nil || !failed {
		t.Errorf("an empty directory: %v, %v", failed, err)
	}
}

func TestParseArchive(t *testing.T) {
	for name, want := range map[string]archive{
		"dnstt-cli_v0.1.0_windows_amd64.zip":           {name: "dnstt-cli_v0.1.0_windows_amd64.zip", dir: "dnstt-cli_v0.1.0_windows_amd64", version: "v0.1.0", goos: "windows", goarch: "amd64"},
		"dnstt-cli_v0.1.0-3-gabc_linux_amd64.tar.gz":   {name: "dnstt-cli_v0.1.0-3-gabc_linux_amd64.tar.gz", dir: "dnstt-cli_v0.1.0-3-gabc_linux_amd64", version: "v0.1.0-3-gabc", goos: "linux", goarch: "amd64"},
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

func TestPrintedVersion(t *testing.T) {
	for _, c := range []struct {
		out, want, err string
	}{
		{"dnstt-cli v0.1.0 (abc1234)\nbuild: go1.26.2 linux/amd64\n", "v0.1.0", ""},
		{"dnstt-cli dev (unknown)\n", "dev", ""},
		{"something else\n", "", "unexpected version output"},
		{"", "", "unexpected version output"},
	} {
		got, err := printedVersion(c.out)
		if c.err == "" && (err != nil || got != c.want) {
			t.Errorf("%q: %q, %v", c.out, got, err)
		}
		if c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)) {
			t.Errorf("%q: %v", c.out, err)
		}
	}
}
