// Command releasenotes writes the beginning of a release description — the "Download"
// block with links to the archives in the order Windows, Linux, then the sources and
// SHA256SUMS.txt. It is the release notes step of .github/workflows/release.yml.
//
//	go run ./scripts/releasenotes [-dir dist] [-repo owner/name] [-out file]
//
// The block is needed because GitHub lists a release's assets alphabetically and always
// puts the "Source code" archives last: the order is ours only in the description above
// the list. The rest of the description — what changed — is added by
// `gh release create --generate-notes`, so nothing dated or environmental is committed.
//
// The version and the tag come from the archive names dnstt-cli_<version>_<os>_<arch>: a
// release is built with -version equal to the tag. Archives of different versions in one
// directory, a directory without archives or without SHA256SUMS.txt are errors. What was
// written is printed.
package main

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/FreedomCats/dnstt-cli/scripts/internal/script"
)

const (
	self     = "releasenotes"
	prefix   = "dnstt-cli_"
	sumsName = "SHA256SUMS.txt"
)

// osOrder is the order of the systems in the block; unknown ones come after, by name.
var osOrder = []string{"windows", "linux"}

var osTitles = map[string]string{"windows": "Windows", "linux": "Linux"}

var archTitles = map[string]string{"amd64": "x86-64", "arm64": "ARM64", "386": "x86"}

// osNotes is a caveat after the system, as in the README. Windows has no tun adapter until
// stage 04-tun-windows is done: the note goes away with that stage.
var osNotes = map[string]string{"windows": "socks mode; tun is not available yet"}

func main() {
	dir := flag.String("dir", "dist", "release directory")
	repo := flag.String("repo", os.Getenv("GITHUB_REPOSITORY"), "repository on GitHub, owner/name")
	out := flag.String("out", "", "where to write the description; empty — only print it")
	flag.Parse()
	if flag.NArg() > 0 {
		flag.Usage()
		os.Exit(2)
	}
	text, err := run(*dir, *repo)
	if err != nil {
		script.Fail(self, err)
	}
	if *out != "" {
		if err := os.WriteFile(*out, []byte(text), 0o644); err != nil {
			script.Fail(self, err)
		}
	}
	fmt.Print(text)
}

// archive is a release archive, parsed from its name.
type archive struct {
	name, version, goos, goarch string
}

func run(dir, repo string) (string, error) {
	if repo == "" {
		return "", errors.New("no repository: -repo owner/name (in Actions — GITHUB_REPOSITORY)")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var archives []archive
	sums := false
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		if e.Name() == sumsName {
			sums = true
			continue
		}
		if a, ok := parseArchive(e.Name()); ok {
			archives = append(archives, a)
		}
	}
	if len(archives) == 0 {
		return "", fmt.Errorf("%s has no archives %s<version>_<os>_<arch>", dir, prefix)
	}
	if !sums {
		return "", fmt.Errorf("%s has no %s", dir, sumsName)
	}
	tag := archives[0].version
	for _, a := range archives[1:] {
		if a.version != tag {
			return "", fmt.Errorf("archives of different versions: %s and %s", archives[0].name, a.name)
		}
	}
	slices.SortFunc(archives, func(a, b archive) int {
		if c := osRank(a.goos) - osRank(b.goos); c != 0 {
			return c
		}
		if c := strings.Compare(a.goos, b.goos); c != 0 {
			return c
		}
		return strings.Compare(a.goarch, b.goarch)
	})

	base := "https://github.com/" + repo
	download := func(name string) string {
		return base + "/releases/download/" + url.PathEscape(tag) + "/" + url.PathEscape(name)
	}

	var b strings.Builder
	b.WriteString("## Download\n\n")
	for _, a := range archives {
		fmt.Fprintf(&b, "- **%s** (%s", title(osTitles, a.goos), title(archTitles, a.goarch))
		if note := osNotes[a.goos]; note != "" {
			b.WriteString(", " + note)
		}
		fmt.Fprintf(&b, "): [%s](%s)\n", a.name, download(a.name))
	}
	src := base + "/archive/refs/tags/" + url.PathEscape(tag)
	fmt.Fprintf(&b, "- **Sources**: [tar.gz](%s.tar.gz) · [zip](%s.zip)\n", src, src)
	fmt.Fprintf(&b, "- **SHA-256 checksums**: [%s](%s)\n", sumsName, download(sumsName))
	fmt.Fprintf(&b, "\nStart here — [README](%s/blob/%s/README.md).\n", base, url.PathEscape(tag))
	return b.String(), nil
}

// parseArchive reads dnstt-cli_<version>_<os>_<arch>.zip or .tar.gz. The version may
// contain "_" only in theory: the system and the architecture are taken from the end.
func parseArchive(name string) (archive, bool) {
	stem, ok := strings.CutSuffix(name, ".zip")
	if !ok {
		if stem, ok = strings.CutSuffix(name, ".tar.gz"); !ok {
			return archive{}, false
		}
	}
	rest, ok := strings.CutPrefix(stem, prefix)
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
		version: strings.Join(parts[:n-2], "_"),
		goos:    parts[n-2],
		goarch:  parts[n-1],
	}, true
}

func osRank(goos string) int {
	if i := slices.Index(osOrder, goos); i >= 0 {
		return i
	}
	return len(osOrder)
}

func title(titles map[string]string, key string) string {
	if t, ok := titles[key]; ok {
		return t
	}
	return key
}
