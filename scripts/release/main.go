// Command release builds a dnstt-cli release: for every platform the binary, the
// documents beside it and an archive.
//
//	go run ./scripts/release
//	go run ./scripts/release -platforms linux/amd64 -version 0.1.0
//	go run ./scripts/release -wintun-archive ~/Downloads/wintun-0.14.1.zip
//
// dnstt-cli is Go only, so it is built for any platform on any machine:
//
//	go build -trimpath -ldflags "-s -w -X main.version=… -X main.commit=…"
//
// with CGO_ENABLED=0 — cgo is not needed, and without it a cross-build does not look for
// another C compiler. The version and the commit reach `dnstt-cli version` through the
// variables of cmd/dnstt-cli/main.go.
//
// The layout (dist/ by default):
//
//	dist/dnstt-cli_<version>_windows_amd64/
//	dist/dnstt-cli_<version>_linux_amd64/
//	dist/dnstt-cli_<version>_windows_amd64.zip
//	    the directory dnstt-cli_<version>_windows_amd64/ with dnstt-cli.exe, wintun.dll,
//	    wintun-LICENSE.txt, README.md, LICENSE, COPYING, THIRD_PARTY_NOTICES.txt and
//	    dnstt-cli.example.toml
//	dist/dnstt-cli_<version>_linux_amd64.tar.gz
//	    the directory dnstt-cli_<version>_linux_amd64/ with dnstt-cli and the documents
//
// The list is scripts/internal/dist, shared with distcheck; the directory is compared with
// it before packing. COPYING is the dnstt core's license (core/COPYING), flattened into
// the archive root. THIRD_PARTY_NOTICES.txt is the committed file: the release workflow
// checks that it is current. wintun.dll (the tun adapter on Windows) comes from
// scripts/internal/wintun — a fixed version, the archive hash checked, and on Windows the
// DLL's signature too. It is not in the repository.
//
// The archives are reproducible: the same input files give the same bytes on any system
// and at any time. The documents are copied as they are, so a clone that checks them out
// differently gives a different archive; that is why .gitattributes keeps them in LF.
// Inside both archives the files sit in a directory named after the archive, so that an
// unpacking does not mix them with neighbours; the order is by name, every entry has the
// same time (archiveTime), the owner in the tar.gz is 0:0 without names. In the tar.gz
// dnstt-cli has the right to execute (0755) and the rest is 0644 whatever is on disk; in
// the zip everything is 0644. The archive is written here, not by a system tar or zip.
//
// At the end the binary of the host platform runs `version` and must print the version and
// the commit that went into the build.
//
// Flags:
//
//	-version         the version string; by default `git describe --tags --always
//	                 --dirty`, that is the tag if there is one, the short commit
//	                 otherwise, with a -dirty suffix for uncommitted changes
//	-out             the output directory; by default dist in the repository root. What
//	                 is built is rewritten, other files are left alone
//	-platforms       platforms, comma-separated, as os/arch; by default
//	                 windows/amd64,linux/amd64
//	-nozip           write no archives and fetch no wintun.dll: the directories hold the
//	                 binary only
//	-wintun-archive  an already downloaded wintun-<version>.zip — a build without network
package main

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/FreedomCats/dnstt-cli/scripts/internal/dist"
	"github.com/FreedomCats/dnstt-cli/scripts/internal/script"
	"github.com/FreedomCats/dnstt-cli/scripts/internal/wintun"
)

const self = "release"

// releasePlatforms are the platforms a release has. More is a later decision: the archive
// names, the Windows DLL and the checks in distcheck are built around these two.
var releasePlatforms = []string{"windows/amd64", "linux/amd64"}

type options struct {
	Version       string
	Out           string
	Platforms     []string
	NoZip         bool
	WintunArchive string
}

type build struct {
	Platform, GOOS, GOARCH string
	Dir, Name, Exe         string
}

// buildEnv is what the build sets for go build and removes afterwards: other go commands
// (git, notices from a person) must not run with another platform's GOOS.
var buildEnv = []string{"GOOS", "GOARCH", "CGO_ENABLED"}

// cleanEnv is the environment without buildEnv.
func cleanEnv() []string {
	return slices.DeleteFunc(os.Environ(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return slices.ContainsFunc(buildEnv, func(v string) bool { return strings.EqualFold(v, name) })
	})
}

// gitOutput is git's output in root without whitespace at the edges; "" is git finishing
// with an error. A missing git is an error.
func gitOutput(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// sanitizeVersion keeps the version usable in directory and file names: slashes and
// spaces have no place there.
func sanitizeVersion(v string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || unicode.IsSpace(r) {
			return '-'
		}
		return r
	}, v)
}

// docs are the archive documents whose file in the repository has another name. A
// document not in the map comes from a file of the same name in the repository root.
var docs = map[string]string{"COPYING": filepath.Join("core", "COPYING")}

func main() {
	var o options
	platforms := flag.String("platforms", "windows/amd64,linux/amd64", "platforms, comma-separated, as os/arch")
	flag.StringVar(&o.Version, "version", "", "version string (default git describe --tags --always --dirty)")
	flag.StringVar(&o.Out, "out", "", "output directory (default dist in the repository root)")
	flag.BoolVar(&o.NoZip, "nozip", false, "write no archives and fetch no wintun.dll")
	flag.StringVar(&o.WintunArchive, "wintun-archive", "", "an already downloaded wintun-"+wintun.Version+".zip — a build without network")
	flag.Parse()
	if flag.NArg() > 0 {
		flag.Usage()
		os.Exit(2)
	}
	o.Platforms = strings.Split(*platforms, ",")
	// Absolute at once: the commands run from the repository root, and a relative path
	// would put their files somewhere else.
	if o.Out != "" {
		out, err := filepath.Abs(o.Out)
		if err != nil {
			script.Fail(self, err)
		}
		o.Out = out
	}

	root, err := script.Root()
	if err != nil {
		script.Fail(self, err)
	}
	if err := run(root, o); err != nil {
		script.Fail(self, err)
	}
}

// plan checks the platforms and gives each of them a name, a directory and the path of
// the binary. Everything that would stop the release is one error before any work.
func plan(o options) ([]build, error) {
	var builds []build
	for _, platform := range o.Platforms {
		goos, goarch, ok := strings.Cut(platform, "/")
		if !ok || goos == "" || goarch == "" || strings.Contains(goarch, "/") {
			return nil, fmt.Errorf("a platform is os/arch: %q", platform)
		}
		if !slices.Contains(releasePlatforms, platform) {
			return nil, fmt.Errorf("platform %s is not a release platform: the release has %s; more is a later decision",
				platform, strings.Join(releasePlatforms, ", "))
		}
		if slices.ContainsFunc(builds, func(b build) bool { return b.Platform == platform }) {
			return nil, fmt.Errorf("platform %s is given twice", platform)
		}
		builds = append(builds, build{Platform: platform, GOOS: goos, GOARCH: goarch})
	}
	if len(builds) == 0 {
		return nil, errors.New("no platforms: -platforms is empty")
	}
	return builds, nil
}

func run(root string, o options) error {
	if o.Out == "" {
		o.Out = filepath.Join(root, "dist")
	}
	builds, err := plan(o)
	if err != nil {
		return err
	}

	// --- build information ---------------------------------------------------

	version := o.Version
	if version == "" {
		v, err := gitOutput(root, "describe", "--tags", "--always", "--dirty")
		if err != nil {
			return err
		}
		version = v
		if version == "" {
			version = "dev"
		}
	}
	version = sanitizeVersion(version)

	commit, err := gitOutput(root, "rev-parse", "--short", "HEAD")
	if err != nil {
		return err
	}
	if commit == "" {
		commit = "unknown"
	} else {
		status, err := gitOutput(root, "status", "--porcelain")
		if err != nil {
			return err
		}
		if status != "" {
			commit += "-dirty"
		}
	}
	ldflags := fmt.Sprintf("-s -w -X main.version=%s -X main.commit=%s", version, commit)

	script.Sayf("Version: %s, commit: %s", version, commit)
	script.Say("Output: " + o.Out)

	// The past of every platform goes before the first build: a failure in the middle
	// leaves no old archive of any platform that distcheck would take for a new one.
	for i := range builds {
		b := &builds[i]
		b.Name = fmt.Sprintf("dnstt-cli_%s_%s_%s", version, b.GOOS, b.GOARCH)
		b.Dir = filepath.Join(o.Out, b.Name)
		b.Exe = filepath.Join(b.Dir, dist.Executables(b.GOOS)[0])
		if err := resetPlatform(o.Out, b.Name); err != nil {
			return err
		}
	}

	// --- build ---------------------------------------------------------------

	if err := os.MkdirAll(o.Out, 0o755); err != nil {
		return err
	}
	for _, b := range builds {
		if err := os.MkdirAll(b.Dir, 0o755); err != nil {
			return err
		}
		script.Sayf("Building %s → %s", b.Platform, b.Exe)
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", b.Exe, "./cmd/dnstt-cli")
		cmd.Dir = root
		cmd.Env = append(cleanEnv(), "GOOS="+b.GOOS, "GOARCH="+b.GOARCH, "CGO_ENABLED=0")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("go build for %s: %s", b.Platform, exitText(err))
		}
	}

	// --- archives ------------------------------------------------------------

	var archives []string
	if !o.NoZip {
		for _, b := range builds {
			if err := addDocs(root, b); err != nil {
				return err
			}
			if b.GOOS == "windows" {
				// The release is amd64 only (releasePlatforms): the DLL is the amd64 one.
				if err := wintun.Fetch(wintun.Options{Arch: "amd64", Dest: b.Dir, Archive: o.WintunArchive}); err != nil {
					return err
				}
			}
			if err := checkComposition(b.Dir, b.GOOS); err != nil {
				return err
			}
			archive := filepath.Join(o.Out, b.Name+".tar.gz")
			write := func(path string) error { return writeTarGz(b.Dir, path, b.Name, dist.Executables(b.GOOS)) }
			// Inside the zip is a directory named after the archive, as in the tar.gz:
			// "Extract all" does not scatter the files, and dnstt-cli.exe keeps the
			// documents and wintun.dll it needs beside it.
			if b.GOOS == "windows" {
				archive = filepath.Join(o.Out, b.Name+".zip")
				write = func(path string) error { return writeZip(b.Dir, path) }
			}
			if err := removeFile(archive); err != nil {
				return err
			}
			if err := write(archive); err != nil {
				return err
			}
			archives = append(archives, archive)
			script.Say("Archive: " + archive)
		}
	}

	// --- check ---------------------------------------------------------------

	native := slices.IndexFunc(builds, func(b build) bool { return b.Platform == runtime.GOOS+"/"+runtime.GOARCH })
	if native >= 0 {
		b := builds[native]
		cmd := exec.Command(b.Exe, "version")
		cmd.Stderr = os.Stderr
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("dnstt-cli version: %s", exitText(err))
		}
		versionOut := strings.TrimSpace(strings.ReplaceAll(string(out), "\r\n", "\n"))
		lower := strings.ToLower(versionOut)
		if !strings.Contains(lower, strings.ToLower(version)) || !strings.Contains(lower, strings.ToLower(commit)) {
			return fmt.Errorf("dnstt-cli version does not show the version and the commit: %q", versionOut)
		}
		script.Say("Check: " + versionOut)
	} else {
		script.Sayf("Check dnstt-cli version skipped: no binary for %s/%s among the platforms", runtime.GOOS, runtime.GOARCH)
	}

	// --- result --------------------------------------------------------------

	script.Say("")
	script.Say("Done:")
	for _, b := range builds {
		size, err := sizeMB(b.Exe)
		if err != nil {
			return err
		}
		script.Sayf("  %-14s %s  (%s MB)", b.Platform, b.Exe, size)
	}
	for _, archive := range archives {
		size, err := sizeMB(archive)
		if err != nil {
			return err
		}
		kind := "tar.gz"
		if strings.HasSuffix(archive, ".zip") {
			kind = "zip"
		}
		script.Sayf("  %-14s %s  (%s MB)", kind, archive, size)
	}
	return nil
}

// addDocs puts the documents of the archive beside the binary, flattened: COPYING is the
// core's license under the name the archive uses.
func addDocs(root string, b build) error {
	for _, name := range dist.Docs {
		src := name
		if p, ok := docs[name]; ok {
			src = p
		}
		err := script.CopyFile(filepath.Join(root, src), filepath.Join(b.Dir, name))
		if errors.Is(err, os.ErrNotExist) && name == "THIRD_PARTY_NOTICES.txt" {
			return fmt.Errorf("%v — run go run ./scripts/notices -dest .", err)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// checkComposition compares the directory before packing with the archive's content
// (scripts/internal/dist): a missing or an extra file is an error here, not in distcheck
// afterwards.
func checkComposition(dir, goos string) error {
	names, err := files(dir)
	if err != nil {
		return err
	}
	want := dist.Files(goos)
	var missing, extra []string
	for _, w := range want {
		if !slices.Contains(names, w) {
			missing = append(missing, w)
		}
	}
	for _, n := range names {
		if !slices.Contains(want, n) {
			extra = append(extra, n)
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		return fmt.Errorf("%s: the content is not right — missing: %s; extra: %s",
			dir, strings.Join(missing, ", "), strings.Join(extra, ", "))
	}
	return nil
}

// exitText is "with code N" for a command that finished, the error text otherwise.
func exitText(err error) string {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return fmt.Sprintf("exited with code %d", exit.ExitCode())
	}
	if err == nil {
		return "succeeded"
	}
	return "failed: " + err.Error()
}

// resetPlatform removes a platform's directory and both of its archives of a previous
// run: a build that fails in the middle leaves no old archive behind.
func resetPlatform(out, name string) error {
	if err := os.RemoveAll(filepath.Join(out, name)); err != nil {
		return err
	}
	for _, ext := range []string{".zip", ".tar.gz"} {
		if err := removeFile(filepath.Join(out, name+ext)); err != nil {
			return err
		}
	}
	return nil
}

func removeFile(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// sizeMB is the size in megabytes with one decimal, rounded to even; a whole number has
// no ".0".
func sizeMB(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	mb := math.RoundToEven(float64(fi.Size())/(1<<20)*10) / 10
	return strconv.FormatFloat(mb, 'f', -1, 64), nil
}
