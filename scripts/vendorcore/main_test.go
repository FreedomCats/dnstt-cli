package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The check against the real upstream is `go run ./scripts/vendorcore -upstream <clone>
// -check`: tests have no upstream clone. Here are the rule engine, the Go conversion and
// the transfer from a fake upstream.

// withRules replaces the rule set for the duration of a test.
func withRules(t *testing.T, rs ...rule) {
	t.Helper()
	saved := rules
	t.Cleanup(func() { rules = saved })
	rules = rs
}

// withExcluded replaces the excluded files for the duration of a test.
func withExcluded(t *testing.T, fs ...excludedFile) {
	t.Helper()
	saved := excludedFiles
	t.Cleanup(func() { excludedFiles = saved })
	excludedFiles = fs
}

// Whole replacement and insertions before and after — with the indentation of the matched
// line.
func TestApplyRules(t *testing.T) {
	withRules(t,
		rule{File: "a.go", Count: 1, Pattern: `(?m)^(\t+)const t = 60$`, Replace: to("${1}const t = 180")},
		rule{File: "a.go", Count: 1, Pattern: `(?m)^(\t+)s := bind\(\)$`, InsertAfter: "guard(s)"},
		rule{File: "a.go", Count: 1, Pattern: `(?m)^(\t+)run\(s\)$`, InsertBefore: `log("run")`},
		rule{File: "b.go", Count: 1, Pattern: `x`, Replace: to("y")},
	)
	in := "func f() {\n\tconst t = 60\n\t\ts := bind()\n\trun(s)\n}\n"
	got, err := applyRules("a.go", in)
	if err != nil {
		t.Fatal(err)
	}
	want := "func f() {\n\tconst t = 180\n\t\ts := bind()\n\t\tguard(s)\n\tlog(\"run\")\n\trun(s)\n}\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A pattern that did not match the required number of times is an error with the file
// path and the count, not a silent skip.
func TestApplyRulesCountMismatch(t *testing.T) {
	withRules(t, rule{File: "client/utls.go", Count: 1, Pattern: `nothing`, Replace: to("")})
	_, err := applyRules("client/utls.go", "package client\n")
	if err == nil || !strings.Contains(err.Error(), "client/utls.go") || !strings.Contains(err.Error(), "found 0") {
		t.Errorf("error: %v", err)
	}

	withRules(t, rule{File: "a.go", Count: 1, Pattern: `x`, Replace: to("y")})
	if _, err := applyRules("a.go", "x x\n"); err == nil || !strings.Contains(err.Error(), "found 2") {
		t.Errorf("two matches instead of one: %v", err)
	}
}

func TestApplyRulesNoAction(t *testing.T) {
	withRules(t, rule{File: "a.go", Count: 1, Pattern: `x`})
	if _, err := applyRules("a.go", "x\n"); err == nil || !strings.Contains(err.Error(), "sets none") {
		t.Errorf("error: %v", err)
	}
}

// package main takes the name of the directory, test packages too; upstream imports point
// into core/, with the renamed directory and subpackages; other packages stay.
func TestConvertGo(t *testing.T) {
	in := "// Comment about package main.\npackage main\n\nimport (\n" +
		"\t\"fmt\"\n" +
		"\t\"" + importPrefix + "dns\"\n" +
		"\t\"" + importPrefix + "dnstt-client/sub\"\n" +
		")\n"
	got, err := convertGo("client", in)
	if err != nil {
		t.Fatal(err)
	}
	want := "// Comment about package main.\npackage client\n\nimport (\n" +
		"\t\"fmt\"\n" +
		"\t\"" + coreImport + "dns\"\n" +
		"\t\"" + coreImport + "client/sub\"\n" +
		")\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}

	if got, _ := convertGo("server", "package main_test\n"); got != "package server_test\n" {
		t.Errorf("test package: %q", got)
	}
	if got, _ := convertGo("dns", "package dns\n"); got != "package dns\n" {
		t.Errorf("a library package changed: %q", got)
	}

	_, err = convertGo("client", "import \""+importPrefix+"unknown/pkg\"\n")
	if err == nil || !strings.Contains(err.Error(), "unknown/pkg") {
		t.Errorf("import of a package that is not vendored: %v", err)
	}
}

// --- transfer from a fake upstream ---------------------------------------------------

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// fakeUpstream is an upstream laid out like the real one: files with CRLF, as a clone on
// Windows gives them, the client as package main, files that stay upstream.
//
// It has no sockets: the list of socket places is empty for the duration of the test.
func fakeUpstream(t *testing.T) string {
	t.Helper()
	withSockets(t)
	up := t.TempDir()
	write(t, filepath.Join(up, "COPYING"), "CC0\r\n")
	write(t, filepath.Join(up, "CHANGELOG"), "## v9.9.9\r\n\r\nText.\r\n\r\n## v1.0.0\r\n")
	write(t, filepath.Join(up, "README"), "readme\r\n")
	write(t, filepath.Join(up, "go.mod"), "module "+strings.TrimSuffix(importPrefix, "/")+"\r\n")
	for _, d := range vendoredDirs {
		pkg := d.Upstream
		if d.Core != d.Upstream {
			pkg = "main"
		}
		write(t, filepath.Join(up, d.Upstream, "a.go"), "package "+strings.ReplaceAll(pkg, "-", "")+"\r\n")
	}
	write(t, filepath.Join(up, "dnstt-client", "main.go"),
		"package main\r\n\r\nimport \""+importPrefix+"dns\"\r\n\r\nconst t = 60\r\n")
	return up
}

func mustRun(t *testing.T, root, up string, check bool) int {
	t.Helper()
	code, err := run(root, up, check)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func TestRunWriteAndCheck(t *testing.T) {
	withRules(t, rule{File: "client/main.go", Count: 1, Pattern: `(?m)^()const t = 60$`, Replace: to("${1}const t = 180")})
	root, up := t.TempDir(), fakeUpstream(t)
	core := filepath.Join(root, "core")

	if code := mustRun(t, root, up, false); code != 0 {
		t.Fatalf("transfer: code %d", code)
	}
	want := "package client\n\nimport \"" + coreImport + "dns\"\n\nconst t = 180\n"
	if got := read(t, filepath.Join(core, "client", "main.go")); got != want {
		t.Errorf("client/main.go: conversion or rule not applied, or CRLF left:\n%q", got)
	}
	if got := read(t, filepath.Join(core, "COPYING")); got != "CC0\n" {
		t.Errorf("COPYING: %q", got)
	}
	version := read(t, filepath.Join(core, "CORE_VERSION"))
	files := len(vendoredDirs) + 2 // a.go in every directory, client/main.go, COPYING
	for _, line := range []string{"version  = v9.9.9\n", fmt.Sprintf("files    = %d\n", files), "rules    = 1\n", "added    = 0\n"} {
		if !strings.Contains(version, line) {
			t.Errorf("CORE_VERSION without %q:\n%s", line, version)
		}
	}
	for _, rel := range []string{"README", "CHANGELOG", "go.mod", "dnstt-client"} {
		if _, err := os.Stat(filepath.Join(core, rel)); !os.IsNotExist(err) {
			t.Errorf("%s carried: %v", rel, err)
		}
	}

	if code := mustRun(t, root, up, true); code != 0 {
		t.Fatalf("-check right after the transfer: code %d", code)
	}

	// A second transfer changes nothing.
	before := read(t, filepath.Join(core, "client", "main.go"))
	mustRun(t, root, up, false)
	if after := read(t, filepath.Join(core, "client", "main.go")); after != before {
		t.Errorf("a second transfer changed client/main.go")
	}

	// git on Windows may check core/ out with CRLF — that is not a difference.
	write(t, filepath.Join(core, "dns", "a.go"), "package dns\r\n")
	if code := mustRun(t, root, up, true); code != 0 {
		t.Errorf("-check on CRLF: code %d", code)
	}
}

// Every difference fails -check: an edit by hand, an extra file, an excluded one among
// them, and a missing file.
func TestRunCheckFails(t *testing.T) {
	withRules(t)
	withExcluded(t, excludedFile{"dnstt-client/extra.go", "not needed"})
	up := fakeUpstream(t)
	write(t, filepath.Join(up, "dnstt-client", "extra.go"), "package main\n")
	for name, spoil := range map[string]func(t *testing.T, core string){
		"edit": func(t *testing.T, core string) {
			write(t, filepath.Join(core, "noise", "a.go"), "package noise // !\n")
		},
		"extra": func(t *testing.T, core string) {
			write(t, filepath.Join(core, "dns", "extra.go"), "\n")
		},
		"excluded": func(t *testing.T, core string) {
			write(t, filepath.Join(core, "client", "extra.go"), "package client\n")
		},
		"missing": func(t *testing.T, core string) {
			if err := os.Remove(filepath.Join(core, "COPYING")); err != nil {
				t.Fatal(err)
			}
		},
		"CORE_VERSION": func(t *testing.T, core string) {
			write(t, filepath.Join(core, "CORE_VERSION"), "x\n")
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			mustRun(t, root, up, false)
			if _, err := os.Stat(filepath.Join(root, "core", "client", "extra.go")); !os.IsNotExist(err) {
				t.Errorf("excluded file carried: %v", err)
			}
			spoil(t, filepath.Join(root, "core"))
			if code := mustRun(t, root, up, true); code != 1 {
				t.Errorf("-check: code %d, want 1", code)
			}
			// The transfer brings core/ back to the upstream.
			mustRun(t, root, up, false)
			if code := mustRun(t, root, up, true); code != 0 {
				t.Errorf("-check after the transfer: code %d", code)
			}
		})
	}
}

// Our files from addedDir land in core/ with LF, -check compares them, and a file with the
// same path upstream is an error before anything is written.
func TestRunAddedFiles(t *testing.T) {
	withRules(t)
	root, up := t.TempDir(), fakeUpstream(t)
	core := filepath.Join(root, "core")
	write(t, filepath.Join(root, filepath.FromSlash(addedDir), "client", "session.go"), "package client\r\n")

	mustRun(t, root, up, false)
	if got := read(t, filepath.Join(core, "client", "session.go")); got != "package client\n" {
		t.Errorf("session.go: %q", got)
	}
	if version := read(t, filepath.Join(core, "CORE_VERSION")); !strings.Contains(version, "added    = 1\n") {
		t.Errorf("CORE_VERSION:\n%s", version)
	}
	if code := mustRun(t, root, up, true); code != 0 {
		t.Errorf("-check after the transfer: code %d", code)
	}
	write(t, filepath.Join(core, "client", "session.go"), "package client // changed\n")
	if code := mustRun(t, root, up, true); code != 1 {
		t.Errorf("-check after an edit of our file in core/: code %d, want 1", code)
	}

	root = t.TempDir()
	write(t, filepath.Join(root, filepath.FromSlash(addedDir), "client", "main.go"), "\n")
	if _, err := run(root, up, false); err == nil || !strings.Contains(err.Error(), "both upstream") {
		t.Errorf("error %v, want one about the same path upstream", err)
	}
	if _, err := os.Stat(filepath.Join(root, "core")); !os.IsNotExist(err) {
		t.Errorf("core/ created: %v", err)
	}
}

// The transfer removes extra files together with emptied directories.
func TestRunRemovesExtra(t *testing.T) {
	withRules(t)
	root, up := t.TempDir(), fakeUpstream(t)
	core := filepath.Join(root, "core")
	write(t, filepath.Join(core, "client", "old", "gone.go"), "\n")
	mustRun(t, root, up, false)
	if _, err := os.Stat(filepath.Join(core, "client", "old")); !os.IsNotExist(err) {
		t.Errorf("directory old is left: %v", err)
	}
}

// Upstream errors come before writing: core/ is not created.
func TestRunUpstreamErrors(t *testing.T) {
	cases := map[string]struct {
		rules    []rule
		excluded []excludedFile
		spoil    func(t *testing.T, up string)
		want     string
	}{
		"rule on a missing file": {
			rules: []rule{{File: "client/gone.go", Count: 1, Pattern: `x`, Replace: to("y")}},
			want:  "a rule refers to client/gone.go",
		},
		"rule did not match": {
			rules: []rule{{File: "client/main.go", Count: 1, Pattern: `nothing`, Replace: to("")}},
			want:  "found 0",
		},
		"excluded renamed": {
			excluded: []excludedFile{{"dnstt-client/gone.go", "test"}},
			want:     "excluded file dnstt-client/gone.go",
		},
		"no noise": {
			spoil: func(t *testing.T, up string) {
				if err := os.RemoveAll(filepath.Join(up, "noise")); err != nil {
					t.Fatal(err)
				}
			},
			want: "no directory noise",
		},
		"no version": {
			spoil: func(t *testing.T, up string) {
				write(t, filepath.Join(up, "CHANGELOG"), "Changes.\n")
			},
			want: "CHANGELOG",
		},
		"import not vendored": {
			spoil: func(t *testing.T, up string) {
				write(t, filepath.Join(up, "dns", "b.go"), "package dns\nimport \""+importPrefix+"man\"\n")
			},
			want: "dns/b.go: imports upstream packages that are not vendored: man",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			withRules(t, c.rules...)
			withExcluded(t, c.excluded...)
			root, up := t.TempDir(), fakeUpstream(t)
			if c.spoil != nil {
				c.spoil(t, up)
			}
			_, err := run(root, up, false)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %v, want one with %q", err, c.want)
			}
			if _, err := os.Stat(filepath.Join(root, "core")); !os.IsNotExist(err) {
				t.Errorf("core/ created: %v", err)
			}
		})
	}
}
