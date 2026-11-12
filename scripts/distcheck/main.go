// Command distcheck checks the content of the release archives in dist/ — the archive
// content step of .github/workflows/release.yml.
//
//	go run ./scripts/distcheck [-dir dist]
//
// An archive holds what a user needs to start: without the binary there is nothing to run,
// without wintun.dll the tun adapter on Windows does not work, without README.md there is
// no way to know where to begin, and without the licenses the archive cannot be given out.
// The list is the one scripts/release lays out — scripts/internal/dist; a file that fell
// out means there is no release.
//
//   - a zip: every entry is inside a directory named after the archive, with "/" as the
//     separator (with "\" an unpacking on Linux gives files with backslashes in their
//     names); the files are exactly dist.Files("windows"), wintun.dll and
//     wintun-LICENSE.txt among them; dnstt-cli.exe is a PE file for x86-64 and is not run
//     here;
//   - a tar.gz: every entry is inside a directory named after the archive; the files are
//     exactly dist.Files("linux"); dnstt-cli is 0755 and the rest 0644;
//   - on Linux the Linux binary is unpacked and run: `version` must print the version from
//     the archive's name and not "dev", `help` must work, and no arguments with no
//     terminal must print the command list and exit 0.
//
// No archive in the directory is an error too: there was nothing to check.
//
// Problems are printed as GitHub annotations (::error::) and the exit code is 1 if there
// was at least one.
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"debug/pe"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/FreedomCats/dnstt-cli/scripts/internal/dist"
	"github.com/FreedomCats/dnstt-cli/scripts/internal/script"
)

const self = "distcheck"

// exe is the Windows binary: checked by its header, never run here.
var exe = dist.Executables("windows")[0]

func main() {
	dir := flag.String("dir", "dist", "directory with the archives")
	flag.Parse()
	if flag.NArg() > 0 {
		flag.Usage()
		os.Exit(2)
	}
	failed, err := run(*dir)
	if err != nil {
		script.Fail(self, err)
	}
	if failed {
		os.Exit(1)
	}
}

type checker struct{ failed bool }

func (c *checker) errorf(format string, a ...any) {
	script.Sayf("::error::"+format, a...)
	c.failed = true
}

// files compares the entry names got with want, ignoring case, as -notcontains did: a
// missing and an extra file are both errors, so the archive is exactly the list.
func (c *checker) files(archive string, got, want []string) {
	var missing, extra []string
	for _, w := range want {
		if !slices.ContainsFunc(got, func(g string) bool { return strings.EqualFold(g, w) }) {
			missing = append(missing, w)
		}
	}
	for _, g := range got {
		if !slices.ContainsFunc(want, func(w string) bool { return strings.EqualFold(g, w) }) {
			extra = append(extra, g)
		}
	}
	if len(missing) > 0 {
		c.errorf("%s — missing files: %s", archive, strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		c.errorf("%s — extra files: %s", archive, strings.Join(extra, ", "))
	}
	if len(missing)+len(extra) == 0 {
		script.Sayf("%s — %s", archive, strings.Join(got, ", "))
	}
}

// archive is a release archive name, dnstt-cli_<version>_<os>_<arch>.zip or .tar.gz.
type archive struct {
	name, version, goos, goarch string
	// dir is the directory inside the archive: the name without the extension.
	dir string
}

// parseArchive reads the archive name. The version may contain "_" only in theory: the
// system and the architecture are taken from the end.
func parseArchive(name string) (archive, bool) {
	stem, ok := strings.CutSuffix(name, ".zip")
	if !ok {
		if stem, ok = strings.CutSuffix(name, ".tar.gz"); !ok {
			return archive{}, false
		}
	}
	rest, ok := strings.CutPrefix(stem, "dnstt-cli_")
	if !ok {
		return archive{}, false
	}
	parts := strings.Split(rest, "_")
	if len(parts) < 3 {
		return archive{}, false
	}
	n := len(parts)
	return archive{
		name:    name,
		dir:     stem,
		version: strings.Join(parts[:n-2], "_"),
		goos:    parts[n-2],
		goarch:  parts[n-1],
	}, true
}

// archives are the files of dir with the suffix, in Sort-Object order.
func archives(dir, suffix string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(strings.ToLower(e.Name()), suffix) {
			names = append(names, e.Name())
		}
	}
	script.SortNames(names)
	return names, nil
}

func run(dir string) (bool, error) {
	c := &checker{}

	zips, err := archives(dir, ".zip")
	if err != nil {
		return false, err
	}
	for _, name := range zips {
		if err := c.zip(dir, name); err != nil {
			return false, err
		}
	}

	tgzs, err := archives(dir, ".tar.gz")
	if err != nil {
		return false, err
	}
	for _, name := range tgzs {
		if err := c.tarGz(dir, name); err != nil {
			return false, err
		}
	}
	if len(zips)+len(tgzs) == 0 {
		c.errorf("%s — no archives (.zip, .tar.gz)", dir)
	}
	return c.failed, nil
}

// zip checks a Windows archive: the entry names, the composition and the header of the
// exe. The exe is not run: this build machine has no Windows.
func (c *checker) zip(dir, name string) error {
	a, ok := parseArchive(name)
	if !ok {
		c.errorf("%s — not a release archive name dnstt-cli_<version>_<os>_<arch>", name)
		return nil
	}
	if a.goos != "windows" {
		c.errorf("%s — a .zip is the Windows archive, this name says %s", name, a.goos)
		return nil
	}
	z, err := zip.OpenReader(filepath.Join(dir, name))
	if err != nil {
		return fmt.Errorf("zip did not read %s: %w", name, err)
	}
	defer z.Close()

	prefix := a.dir + "/"
	var outside, names []string
	for _, f := range z.File {
		entry := f.Name
		if !strings.HasPrefix(entry, prefix) {
			outside = append(outside, entry)
			continue
		}
		rest := entry[len(prefix):]
		if rest != "" {
			names = append(names, rest)
		}
	}
	c.outside(name, prefix, outside)
	c.separators(name, names)
	c.files(name, names, dist.Files("windows"))

	f := find(z.File, prefix+exe)
	if f == nil {
		return nil
	}
	data, err := readZipEntry(f)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if err := checkPE(data); err != nil {
		c.errorf("%s — %s: %v", name, exe, err)
	}
	return nil
}

// tarGz checks a Linux archive: the entry names, the composition, the modes, and on Linux
// the binary itself.
func (c *checker) tarGz(dir, name string) error {
	a, ok := parseArchive(name)
	if !ok {
		c.errorf("%s — not a release archive name dnstt-cli_<version>_<os>_<arch>", name)
		return nil
	}
	if a.goos != "linux" {
		c.errorf("%s — a .tar.gz is the Linux archive, this name says %s", name, a.goos)
		return nil
	}

	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("tar did not read %s: %w", name, err)
	}
	tr := tar.NewReader(gz)

	prefix := a.dir + "/"
	var outside, names []string
	var binary []byte
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar did not read %s: %w", name, err)
		}
		// An entry outside the archive's directory unpacks next to it — as in a zip,
		// that is an error, and such an entry is not part of the composition.
		if !strings.HasPrefix(h.Name, prefix) {
			outside = append(outside, h.Name)
			continue
		}
		rest := h.Name[len(prefix):]
		if rest == "" {
			continue
		}
		names = append(names, rest)
		if h.Typeflag != tar.TypeReg {
			continue
		}
		// What tar -tv shows as "-rwxr-xr-x": the binary 0755, everything else 0644,
		// whatever is on disk on the build machine.
		want := int64(0o644)
		if rest == dist.Executables("linux")[0] {
			want = 0o755
		}
		if h.Mode&0o7777 != want {
			c.errorf("%s — %s has mode %o, want %o", name, rest, h.Mode&0o7777, want)
		}
		if rest != dist.Executables("linux")[0] {
			continue
		}
		if binary, err = io.ReadAll(tr); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	c.outside(name, prefix, outside)
	c.separators(name, names)
	c.files(name, names, dist.Files("linux"))
	if binary != nil {
		c.linuxBinary(a, binary)
	}
	return nil
}

// outside reports entries that would unpack outside the archive's directory.
func (c *checker) outside(name, prefix string, entries []string) {
	if len(entries) > 0 {
		c.errorf("%s — outside the directory %s: %s", name, prefix, strings.Join(entries, ", "))
	}
}

// separators reports entry names with a backslash: an unpacking on Linux would give files
// with backslashes in their names. The names already inside the archive's directory are
// checked, so the directory itself does not count.
func (c *checker) separators(name string, entries []string) {
	var bad []string
	for _, e := range entries {
		if strings.Contains(e, `\`) {
			bad = append(bad, e)
		}
	}
	if len(bad) > 0 {
		c.errorf("%s — a backslash instead of / in: %s", name, strings.Join(bad, ", "))
	}
}

// linuxBinary unpacks the binary and runs it. A user runs the binary, not a listing: the
// version must be the one in the archive's name, and the commands must answer.
func (c *checker) linuxBinary(a archive, binary []byte) {
	if runtime.GOOS != "linux" {
		script.Sayf("%s — the Linux binary was not run: this is not Linux", a.name)
		return
	}
	dir, err := os.MkdirTemp("", "dnstt-cli-distcheck-")
	if err != nil {
		c.errorf("%s — %v", a.name, err)
		return
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, dist.Executables("linux")[0])
	if err := os.WriteFile(path, binary, 0o755); err != nil {
		c.errorf("%s — %v", a.name, err)
		return
	}
	c.linuxVersion(a, path)
	c.linuxHelp(a, path)
	c.linuxNoArgs(a, path)
}

// linuxVersion runs `version`: the version and the commit must have come through
// -ldflags, so the version is the one from the archive's name and not "dev".
func (c *checker) linuxVersion(a archive, path string) {
	out, code, err := capture(path, "version")
	if err != nil {
		c.errorf("%s — dnstt-cli version: %v", a.name, err)
		return
	}
	if code != 0 {
		c.errorf("%s — dnstt-cli version exited with code %d: %s", a.name, code, strings.TrimSpace(out))
		return
	}
	got, err := printedVersion(out)
	if err != nil {
		c.errorf("%s — %v", a.name, err)
		return
	}
	switch {
	case got == "dev" && a.version != "dev":
		c.errorf("%s — version still says dev: it did not arrive through -ldflags", a.name)
	case got != a.version:
		c.errorf("%s — version says %q, the archive name says %q", a.name, got, a.version)
	}
}

// linuxHelp runs `help`: every command has help, and the command list answers what the
// program can do.
func (c *checker) linuxHelp(a archive, path string) {
	out, code, err := capture(path, "help")
	switch {
	case err != nil:
		c.errorf("%s — dnstt-cli help: %v", a.name, err)
	case code != 0:
		c.errorf("%s — dnstt-cli help exited with code %d", a.name, code)
	case !strings.Contains(out, "Usage: dnstt-cli"):
		c.errorf("%s — dnstt-cli help printed no usage line", a.name)
	}
}

// linuxNoArgs runs the binary with no arguments, no terminal on either side: the command
// list is the answer, and the exit code is zero.
func (c *checker) linuxNoArgs(a archive, path string) {
	out, code, err := capture(path)
	switch {
	case err != nil:
		c.errorf("%s — dnstt-cli with no arguments: %v", a.name, err)
	case code != 0:
		c.errorf("%s — dnstt-cli with no arguments exited with code %d", a.name, code)
	case !strings.Contains(out, "Commands:"):
		c.errorf("%s — dnstt-cli with no arguments printed no command list", a.name)
	default:
		script.Sayf("%s — runs: version, help and the command list", a.name)
	}
}

// capture runs the binary and returns its standard output and exit code. Standard input
// is the null device and standard output is a pipe: the program sees no terminal, as it
// would in a script.
func capture(path string, args ...string) (string, int, error) {
	cmd := exec.Command(path, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		return out.String(), exit.ExitCode(), nil
	case err != nil:
		return out.String(), 0, err
	}
	return out.String(), 0, nil
}

// printedVersion is the version from the first line of `version`:
// "dnstt-cli <version> (<commit>)".
func printedVersion(out string) (string, error) {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[0] != "dnstt-cli" {
		return "", fmt.Errorf("unexpected version output: %q", strings.TrimSpace(out))
	}
	return fields[1], nil
}

// find is the zip entry with the name.
func find(entries []*zip.File, name string) *zip.File {
	for _, f := range entries {
		if f.Name == name {
			return f
		}
	}
	return nil
}

func readZipEntry(f *zip.File) ([]byte, error) {
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// checkPE checks that data is an executable for x86-64: the DOS header, the PE signature
// and the machine in the COFF header.
func checkPE(data []byte) error {
	if !bytes.HasPrefix(data, []byte("MZ")) {
		return errors.New("not a PE file: no MZ signature")
	}
	f, err := pe.NewFile(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("not a PE file: %w", err)
	}
	defer f.Close()
	if f.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		return fmt.Errorf("PE for machine %#x, not x86-64", f.Machine)
	}
	return nil
}
