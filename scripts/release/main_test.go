package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FreedomCats/dnstt-cli/scripts/internal/dist"
	"github.com/FreedomCats/dnstt-cli/scripts/internal/script"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// layout is a platform directory with the whole archive content (dist.Files).
func layout(t *testing.T, parent, name, goos string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	files := map[string]string{}
	for _, f := range dist.Files(goos) {
		files[f] = "content of " + f + "\n"
	}
	writeFiles(t, dir, files)
	return dir
}

// sorted is the archive order of a file list.
func sorted(names []string) []string {
	out := append([]string(nil), names...)
	script.SortNames(out)
	return out
}

// touch sets the time and the permissions of every file of dir — what differs from machine
// to machine and must not get into the archive.
func touch(t *testing.T, dir string, at time.Time, perm os.FileMode) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, perm); err != nil {
			t.Fatal(err)
		}
	}
}

// The same files with different times and permissions on disk give the same tar.gz;
// inside is the directory, then the files by name, 0755 for the binary and 0644 for the
// rest, the owner 0:0 without names, the time archiveTime, the format ustar.
func TestTarGzReproducible(t *testing.T) {
	const name = "dnstt-cli_1.2.3_linux_amd64"
	a := layout(t, t.TempDir(), name, "linux")
	b := layout(t, t.TempDir(), name, "linux")
	touch(t, a, time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC), 0o600)
	touch(t, b, time.Now(), 0o755)

	pa, pb := filepath.Join(t.TempDir(), "a.tar.gz"), filepath.Join(t.TempDir(), "b.tar.gz")
	for _, p := range [][2]string{{a, pa}, {b, pb}} {
		if err := writeTarGz(p[0], p[1], name, dist.Executables("linux")); err != nil {
			t.Fatal(err)
		}
	}
	ba, _ := os.ReadFile(pa)
	bb, _ := os.ReadFile(pb)
	if !bytes.Equal(ba, bb) {
		t.Fatal("archives from the same files differ")
	}

	gz, err := gzip.NewReader(bytes.NewReader(ba))
	if err != nil {
		t.Fatal(err)
	}
	if !gz.ModTime.IsZero() || gz.Name != "" || gz.OS != 3 {
		t.Errorf("gzip header: time %v, name %q, OS %d", gz.ModTime, gz.Name, gz.OS)
	}
	tr := tar.NewReader(gz)
	var got []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, h.Name)
		if h.Uid != 0 || h.Gid != 0 || h.Uname != "" || h.Gname != "" || !h.ModTime.Equal(archiveTime) || h.Format != tar.FormatUSTAR || len(h.PAXRecords) != 0 {
			t.Errorf("%s: owner %d:%d %q:%q, time %v, format %v", h.Name, h.Uid, h.Gid, h.Uname, h.Gname, h.ModTime, h.Format)
		}
		want := int64(0o644)
		switch base := strings.TrimPrefix(h.Name, name+"/"); {
		case h.Typeflag == tar.TypeDir, base == "dnstt-cli":
			want = 0o755
		}
		if h.Mode != want {
			t.Errorf("%s: mode %o, want %o", h.Name, h.Mode, want)
		}
		if h.Typeflag == tar.TypeReg {
			body, _ := io.ReadAll(tr)
			if string(body) != "content of "+strings.TrimPrefix(h.Name, name+"/")+"\n" {
				t.Errorf("%s: content %q", h.Name, body)
			}
		}
	}
	want := []string{name + "/"}
	for _, f := range sorted(dist.Files("linux")) {
		want = append(want, name+"/"+f)
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("entries %v, want %v", got, want)
	}
}

// The zip is reproducible too: a directory named after the archive, the entries by name,
// no directory entries and no data descriptors, the time archiveTime, Unix 0644
// attributes, no extended time field.
func TestZipReproducible(t *testing.T) {
	const name = "dnstt-cli_1.2.3_windows_amd64"
	a := layout(t, t.TempDir(), name, "windows")
	b := layout(t, t.TempDir(), name, "windows")
	touch(t, a, time.Date(2021, 10, 17, 4, 39, 20, 0, time.Local), 0o600)
	touch(t, b, time.Now(), 0o644)

	pa, pb := filepath.Join(t.TempDir(), "a.zip"), filepath.Join(t.TempDir(), "b.zip")
	for _, p := range [][2]string{{a, pa}, {b, pb}} {
		if err := writeZip(p[0], p[1]); err != nil {
			t.Fatal(err)
		}
	}
	ba, _ := os.ReadFile(pa)
	bb, _ := os.ReadFile(pb)
	if !bytes.Equal(ba, bb) {
		t.Fatal("archives from the same files differ")
	}

	z, err := zip.NewReader(bytes.NewReader(ba), int64(len(ba)))
	if err != nil {
		t.Fatal(err)
	}
	want := sorted(dist.Files("windows"))
	if len(z.File) != len(want) {
		t.Fatalf("%d entries, want %d", len(z.File), len(want))
	}
	d, c := dosTime(archiveTime)
	for i, f := range z.File {
		if full := name + "/" + want[i]; f.Name != full {
			t.Errorf("entry %d: %s, want %s", i, f.Name, full)
		}
		if f.Flags&0x8 != 0 || f.Method != zip.Deflate || f.CreatorVersion != zipCreator || f.ExternalAttrs != zipAttrs || len(f.Extra) != 0 {
			t.Errorf("%s: flags %#x, method %d, creator %#x, attrs %#x, extra %d bytes", f.Name, f.Flags, f.Method, f.CreatorVersion, f.ExternalAttrs, len(f.Extra))
		}
		if f.ModifiedDate != d || f.ModifiedTime != c {
			t.Errorf("%s: time %v", f.Name, f.Modified)
		}
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r)
		r.Close()
		if err != nil || string(body) != "content of "+want[i]+"\n" {
			t.Errorf("%s: content %q, %v", f.Name, body, err)
		}
	}
	if fi := z.File[0].FileInfo(); fi.Mode().Perm() != 0o644 {
		t.Errorf("mode in the zip: %v", fi.Mode())
	}
}

func TestFilesRejectsSubdir(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := files(dir); err == nil {
		t.Error("a subdirectory passed")
	}
}

// The platform directory and both of its archives of a previous run go; other files stay.
func TestResetPlatform(t *testing.T) {
	out := t.TempDir()
	const name = "dnstt-cli_1.2.3_linux_amd64"
	layout(t, out, name, "linux")
	writeFiles(t, out, map[string]string{name + ".tar.gz": "old", name + ".zip": "old", "dnstt-cli_1.2.3_windows_amd64.zip": "other"})
	if err := resetPlatform(out, name); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "dnstt-cli_1.2.3_windows_amd64.zip" {
		t.Errorf("left: %v", entries)
	}
	if err := resetPlatform(out, name); err != nil {
		t.Errorf("again, with nothing to remove: %v", err)
	}
}

func TestCheckComposition(t *testing.T) {
	for _, goos := range []string{"windows", "linux"} {
		dir := layout(t, t.TempDir(), "r", goos)
		if err := checkComposition(dir, goos); err != nil {
			t.Errorf("%s, full: %v", goos, err)
		}
		binary := dist.Executables(goos)[0]
		if err := os.Remove(filepath.Join(dir, binary)); err != nil {
			t.Fatal(err)
		}
		writeFiles(t, dir, map[string]string{"LICENSE.md": "x"})
		err := checkComposition(dir, goos)
		if err == nil || !strings.Contains(err.Error(), "missing: "+binary) || !strings.Contains(err.Error(), "extra: LICENSE.md") {
			t.Errorf("%s, without the binary and with an extra file: %v", goos, err)
		}
	}
	if err := checkComposition(layout(t, t.TempDir(), "r", "linux"), "windows"); err == nil {
		t.Error("a Linux directory passed as Windows")
	}
}

func TestPlan(t *testing.T) {
	builds, err := plan(options{Platforms: []string{"windows/amd64", "linux/amd64"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(builds) != 2 || builds[0].GOOS != "windows" || builds[1].GOARCH != "amd64" {
		t.Errorf("builds: %+v", builds)
	}
	for _, c := range []struct {
		name string
		o    options
		err  string
	}{
		{"macOS", options{Platforms: []string{"darwin/arm64"}}, "not a release platform"},
		{"linux/arm64", options{Platforms: []string{"linux/arm64"}}, "not a release platform"},
		{"no arch", options{Platforms: []string{"linux"}}, "os/arch"},
		{"extra slash", options{Platforms: []string{"linux/amd64/v3"}}, "os/arch"},
		{"twice", options{Platforms: []string{"linux/amd64", "linux/amd64"}}, "twice"},
		{"empty", options{Platforms: []string{""}}, "os/arch"},
	} {
		if _, err := plan(c.o); err == nil || !strings.Contains(err.Error(), c.err) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func TestSanitizeVersion(t *testing.T) {
	if got := sanitizeVersion("feature/x y\\z w"); got != "feature-x-y-z-w" {
		t.Errorf("%q", got)
	}
}

// The documents are flattened into the platform directory: COPYING is the core's license
// under the archive's name.
func TestAddDocs(t *testing.T) {
	root := t.TempDir()
	for _, f := range dist.Docs {
		if f == "COPYING" {
			continue
		}
		writeFiles(t, root, map[string]string{f: "content of " + f + "\n"})
	}
	writeFiles(t, root, map[string]string{"COPYING": "not the core's license\n"})
	writeFiles(t, filepath.Join(root, "core"), map[string]string{"COPYING": "core license\n"})

	b := build{GOOS: "linux", Dir: t.TempDir()}
	if err := addDocs(root, b); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(b.Dir, "COPYING"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "core license\n" {
		t.Errorf("COPYING: %q", got)
	}
	entries, err := os.ReadDir(b.Dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(sorted(names), " ") != strings.Join(sorted(dist.Docs), " ") {
		t.Errorf("documents: %v, want %v", sorted(names), sorted(dist.Docs))
	}

	missing := t.TempDir()
	for _, f := range dist.Docs {
		if f != "THIRD_PARTY_NOTICES.txt" {
			writeFiles(t, missing, map[string]string{f: "x"})
		}
	}
	writeFiles(t, filepath.Join(missing, "core"), map[string]string{"COPYING": "x"})
	err = addDocs(missing, build{GOOS: "linux", Dir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "go run ./scripts/notices -dest .") {
		t.Errorf("a missing THIRD_PARTY_NOTICES.txt: %v", err)
	}
}

func TestSizeMB(t *testing.T) {
	for size, want := range map[int64]string{
		18 << 20:                "18",
		(18 << 20) + (2<<20)/10: "18.2",
		(1 << 20) / 4:           "0.2", // 0.25 to even
		(1 << 20) * 3 / 4:       "0.8", // 0.75 to even
	} {
		path := filepath.Join(t.TempDir(), "f")
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(path, size); err != nil {
			t.Fatal(err)
		}
		if got, _ := sizeMB(path); got != want {
			t.Errorf("%d bytes: %s, want %s", size, got, want)
		}
	}
}
