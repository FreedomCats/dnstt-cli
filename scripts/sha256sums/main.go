// Command sha256sums writes dir/SHA256SUMS.txt, the hashes of all files in the release
// directory: the "SHA256SUMS.txt" step in .github/workflows/release.yml.
//
//	go run ./scripts/sha256sums [-dir dist]
//
// Lines are "<sha256>  <name>", as from sha256sum, in Sort-Object order; line endings
// are LF, so `sha256sum -c` on Linux can check the file too. A previous SHA256SUMS.txt
// is not included in the list. The written file is printed.
//
// Ported from PowerShell (a release.yml step).
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/FreedomCats/dnstt-cli/scripts/internal/script"
)

const (
	self    = "sha256sums"
	outName = "SHA256SUMS.txt"
)

func main() {
	dir := flag.String("dir", "dist", "release directory")
	flag.Parse()
	if flag.NArg() > 0 {
		flag.Usage()
		os.Exit(2)
	}
	text, err := run(*dir)
	if err != nil {
		script.Fail(self, err)
	}
	fmt.Print(text)
}

func run(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() && !strings.EqualFold(e.Name(), outName) {
			names = append(names, e.Name())
		}
	}
	script.SortNames(names)

	var lines []string
	for _, name := range names {
		sum, err := fileSHA256(filepath.Join(dir, name))
		if err != nil {
			return "", err
		}
		lines = append(lines, sum+"  "+name)
	}
	text := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, outName), []byte(text), 0o644); err != nil {
		return "", err
	}
	return text, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
