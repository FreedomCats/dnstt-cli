// Command vendorcore carries the dnstt core from a clone of the upstream mirror into
// core/.
//
//	go run ./scripts/vendorcore
//	go run ./scripts/vendorcore -upstream <clone of FreedomCats/dnstt>
//	go run ./scripts/vendorcore -check
//
// The only way to update the core (R1 in doc/architecture.md). Our patches live as rules
// in rules.go and as files in scripts/vendorcore/_files, so an update comes down to running
// this command and reviewing the diff.
//
// What it does:
//
//  1. Copies the directories of vendoredDirs into core/ whole, except the files of
//     excludedFiles, and the upstream COPYING into core/COPYING.
//  2. Converts Go files (convertGo): the package name becomes the name of the directory in
//     core/ (dnstt-client is package main upstream, client here), and the upstream module
//     path in imports becomes the path of core/ in this module.
//  3. Applies the rules of rules.go.
//  4. Adds our files from scripts/vendorcore/_files (addedDir).
//  5. Checks the places where the core creates sockets against the list of hooked ones
//     (sockets.go): a new socket is an error.
//  6. Writes core/CORE_VERSION: the upstream commit and its date, the version from the
//     first heading of CHANGELOG, numbers of files, rules and added files.
//  7. Removes from core/ everything that is neither upstream nor in addedDir: core/
//     belongs to the command entirely, nothing is put there by hand.
//
// Text files are written in UTF-8 with LF, as they are in the upstream git: a clone on
// Windows may give them with CRLF. -check does not tell line endings apart either.
//
// -upstream is the directory of the upstream clone; by default dnstt next to this
// repository.
//
// -check writes nothing, it only reports whether core/ has drifted from the upstream and
// the rules: an extra, missing or changed file is named, exit code 1.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/FreedomCats/dnstt-cli/scripts/internal/script"
)

const self = "vendorcore"

// upstreamURL goes into CORE_VERSION.
const upstreamURL = "https://github.com/FreedomCats/dnstt"

func main() {
	upstream := flag.String("upstream", "", "directory of a clone of "+upstreamURL+" (default: dnstt next to this repository)")
	check := flag.Bool("check", false, "write nothing, only report differences (exit code 1 if there are any)")
	flag.Parse()
	if flag.NArg() > 0 {
		flag.Usage()
		os.Exit(2)
	}

	root, err := script.Root()
	if err != nil {
		script.Fail(self, err)
	}
	code, err := run(root, *upstream, *check)
	if err != nil {
		script.Fail(self, err)
	}
	os.Exit(code)
}

type upstreamVersion struct {
	Commit, Date string
	Dirty        bool
}

func getUpstreamVersion(dir string) (upstreamVersion, error) {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return upstreamVersion{Commit: "unknown", Date: "unknown"}, nil
	}
	git := func(args ...string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Stderr = os.Stderr
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return strings.TrimSpace(string(out)), nil
	}
	commit, err := git("rev-parse", "HEAD")
	if err != nil {
		return upstreamVersion{}, err
	}
	date, err := git("log", "-1", "--format=%cI")
	if err != nil {
		return upstreamVersion{}, err
	}
	paths := []string{"status", "--porcelain", "--", changelog}
	for _, d := range vendoredDirs {
		paths = append(paths, d.Upstream)
	}
	status, err := git(append(paths, upstreamFileNames()...)...)
	if err != nil {
		return upstreamVersion{}, err
	}
	return upstreamVersion{Commit: commit, Date: date, Dirty: status != ""}, nil
}

func upstreamFileNames() []string {
	var names []string
	for name := range vendoredFiles {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// isText reports whether a file is text. The core has no binary files, but if some appear,
// they may be neither patched by rules nor have their line endings converted.
func isText(raw []byte) bool { return !bytes.Contains(raw, []byte{0}) }

// normalize is a file's content for writing and comparing: CRLF in text becomes LF, a
// binary file stays as it is.
func normalize(raw []byte) []byte {
	if !isText(raw) {
		return raw
	}
	return bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
}

// listFiles lists the paths of all files under dir relative to base, through "/". A
// missing dir is an empty list.
func listFiles(base, dir string) ([]string, error) {
	start := filepath.Join(base, dir)
	if _, err := os.Stat(start); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var files []string
	err := filepath.WalkDir(start, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(base, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		switch {
		case d.IsDir():
		case d.Type().IsRegular():
			files = append(files, rel)
		default:
			return fmt.Errorf("%s: not a regular file", p)
		}
		return nil
	})
	slices.Sort(files)
	return files, err
}

// changelog is the upstream file with the version: its first heading is "## v…".
const changelog = "CHANGELOG"

var changelogVersion = regexp.MustCompile(`(?m)^## (v\S+)`)

func readVersion(upstream string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(upstream, changelog))
	if err != nil {
		return "", err
	}
	m := changelogVersion.FindSubmatch(raw)
	if m == nil {
		return "", fmt.Errorf("no %s in %s — upstream changed its format, fix scripts/vendorcore", changelogVersion, changelog)
	}
	return string(m[1]), nil
}

var (
	packageMain = regexp.MustCompile(`(?m)^package main(_test)?$`)
	quotedPath  = regexp.MustCompile(`"` + regexp.QuoteMeta(importPrefix) + `([^"]*)"`)
)

// convertGo turns an upstream Go file into a file of the package dir of core/: package
// main becomes the name of dir, and import paths of the upstream module point into core/.
// An import of an upstream package that is not vendored is an error: the core would not
// build, and the rules are not the place to fix it.
func convertGo(dir, text string) (string, error) {
	text = packageMain.ReplaceAllString(text, "package "+path.Base(dir)+"${1}")

	var missing []string
	text = quotedPath.ReplaceAllStringFunc(text, func(quoted string) string {
		pkg := quotedPath.FindStringSubmatch(quoted)[1]
		top, sub, _ := strings.Cut(pkg, "/")
		i := slices.IndexFunc(vendoredDirs, func(d vendoredDir) bool { return d.Upstream == top })
		if i < 0 {
			missing = append(missing, pkg)
			return quoted
		}
		return `"` + path.Join(coreImport+vendoredDirs[i].Core, sub) + `"`
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("imports upstream packages that are not vendored: %s", strings.Join(missing, ", "))
	}
	return text, nil
}

func run(root, upstream string, check bool) (int, error) {
	target := filepath.Join(root, "core")
	if upstream == "" {
		upstream = filepath.Join(filepath.Dir(root), "dnstt")
	}
	for _, d := range vendoredDirs {
		if fi, err := os.Stat(filepath.Join(upstream, d.Upstream)); err != nil || !fi.IsDir() {
			return 0, fmt.Errorf("no directory %s in %s — give a clone of %s with -upstream", d.Upstream, upstream, upstreamURL)
		}
	}

	// --- building the new content ------------------------------------------------

	version, err := getUpstreamVersion(upstream)
	if err != nil {
		return 0, err
	}
	coreVersion, err := readVersion(upstream)
	if err != nil {
		return 0, err
	}

	// sources: upstream path → path in core/, both through "/".
	sources := map[string]string{}
	for _, d := range vendoredDirs {
		files, err := listFiles(upstream, d.Upstream)
		if err != nil {
			return 0, err
		}
		for _, rel := range files {
			sources[rel] = d.Core + strings.TrimPrefix(rel, d.Upstream)
		}
	}

	// An excluded file gone from upstream is a reason to stop: most likely it was
	// renamed, and what we were throwing away came back with it.
	for _, f := range excludedFiles {
		if _, ok := sources[f.Path]; !ok {
			return 0, fmt.Errorf("excluded file %s is not upstream — it was renamed or removed", f.Path)
		}
		delete(sources, f.Path)
	}
	for _, from := range upstreamFileNames() {
		sources[from] = vendoredFiles[from]
	}

	// plan is what must be in core/: path through "/" → content.
	plan := map[string][]byte{}
	for from, to := range sources {
		raw, err := os.ReadFile(filepath.Join(upstream, filepath.FromSlash(from)))
		if err != nil {
			return 0, err
		}
		data := normalize(raw)
		if strings.HasSuffix(to, ".go") && isText(data) {
			text, err := convertGo(path.Dir(to), string(data))
			if err != nil {
				return 0, fmt.Errorf("%s: %w", from, err)
			}
			data = []byte(text)
		}
		plan[to] = data
	}

	// The counter inside applyRules does not fire if upstream renamed the file: the rule
	// would then apply zero times and vanish silently.
	for _, r := range rules {
		data, ok := plan[r.File]
		if !ok {
			return 0, fmt.Errorf("a rule refers to %s, which is not upstream", r.File)
		}
		if !isText(data) {
			return 0, fmt.Errorf("a rule refers to the binary file %s", r.File)
		}
	}
	for name, data := range plan {
		if !isText(data) {
			continue
		}
		text, err := applyRules(name, string(data))
		if err != nil {
			return 0, err
		}
		plan[name] = []byte(text)
	}

	addedRoot := filepath.Join(root, filepath.FromSlash(addedDir))
	added, err := listFiles(addedRoot, ".")
	if err != nil {
		return 0, err
	}
	for _, rel := range added {
		if _, ok := plan[rel]; ok {
			return 0, fmt.Errorf("%s is both upstream and in %s — upstream started its own file with the same path", rel, addedDir)
		}
		raw, err := os.ReadFile(filepath.Join(addedRoot, filepath.FromSlash(rel)))
		if err != nil {
			return 0, err
		}
		plan[rel] = normalize(raw)
	}

	// The socket guard: a new socket in the core fails both the transfer and -check.
	if err := checkSockets(plan); err != nil {
		return 0, err
	}

	header := []string{
		"# Version of the dnstt core carried into core/.",
		"# Written by go run ./scripts/vendorcore — do not edit by hand.",
		"",
		"upstream = " + upstreamURL,
		"commit   = " + version.Commit,
		"date     = " + version.Date,
		"version  = " + coreVersion,
		fmt.Sprintf("files    = %d", len(plan)),
		fmt.Sprintf("rules    = %d", len(rules)),
		fmt.Sprintf("added    = %d", len(added)),
	}
	if version.Dirty {
		header = append(header, "dirty    = true  # upstream has uncommitted changes")
	}
	plan["CORE_VERSION"] = []byte(strings.Join(header, "\n") + "\n")

	names := make([]string, 0, len(plan))
	for name := range plan {
		names = append(names, name)
	}
	slices.Sort(names)

	present, err := listFiles(target, ".")
	if err != nil {
		return 0, err
	}
	var extra []string
	for _, rel := range present {
		if _, ok := plan[rel]; !ok {
			extra = append(extra, rel)
		}
	}

	// --- check mode ----------------------------------------------------------------

	if check {
		var problems []string
		for _, name := range names {
			current, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(name)))
			if errors.Is(err, fs.ErrNotExist) {
				problems = append(problems, "missing: "+name)
				continue
			}
			if err != nil {
				return 0, err
			}
			if !bytes.Equal(normalize(current), plan[name]) {
				problems = append(problems, "differs: "+name)
			}
		}
		for _, rel := range extra {
			line := "extra: " + rel
			for _, f := range excludedFiles {
				if corePath(f.Path) == rel {
					line += " (" + f.Reason + ")"
				}
			}
			problems = append(problems, line)
		}

		if len(problems) > 0 {
			script.Color(script.Yellow, fmt.Sprintf("core/ has drifted from upstream %s and the rules:", version.Commit))
			for _, p := range problems {
				script.Say("  " + p)
			}
			return 1, nil
		}
		script.Color(script.Green, fmt.Sprintf("core/ matches upstream %s and the rules.", version.Commit))
		return 0, nil
	}

	// --- writing -----------------------------------------------------------------

	for _, name := range names {
		p := filepath.Join(target, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return 0, err
		}
		if err := os.WriteFile(p, plan[name], 0o644); err != nil {
			return 0, err
		}
	}
	for _, rel := range extra {
		if err := os.Remove(filepath.Join(target, filepath.FromSlash(rel))); err != nil {
			return 0, err
		}
		script.Say("  removed: " + rel)
		// Emptied directories follow; os.Remove does not take a non-empty one.
		for dir := path.Dir(rel); dir != "."; dir = path.Dir(dir) {
			if os.Remove(filepath.Join(target, filepath.FromSlash(dir))) != nil {
				break
			}
		}
	}

	script.Color(script.Green, fmt.Sprintf("dnstt core %s carried into core/: files %d, rules %d, own files %d.",
		coreVersion, len(plan)-1, len(rules), len(added)))
	script.Sayf("Excluded: %d", len(excludedFiles))
	script.Sayf("Upstream: %s of %s", version.Commit, version.Date)
	if version.Dirty {
		script.Color(script.Yellow, "WARNING: upstream has uncommitted changes.")
	}
	return 0, nil
}

// corePath is the path in core/ of an upstream file of a vendored directory.
func corePath(upstreamPath string) string {
	for _, d := range vendoredDirs {
		if rest, ok := strings.CutPrefix(upstreamPath, d.Upstream+"/"); ok {
			return d.Core + "/" + rest
		}
	}
	return upstreamPath
}
