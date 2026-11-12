package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreedomCats/dnstt-cli/scripts/internal/script"
)

func TestClassify(t *testing.T) {
	const redist = "Redistribution and use in source and binary forms, with or without\nmodification, are permitted provided that the following conditions are met: "
	for _, c := range []struct {
		name, text, want string
	}{
		{"apache", "Apache License\n   Version 2.0, January 2004", "Apache-2.0"},
		{"mit", "Permission is hereby granted, free of charge, to any person", "MIT"},
		{"isc", "Permission to use, copy, modify, and/or distribute this software", "ISC"},
		{"isc without or", "Permission to use, copy, modify, and distribute this software", "ISC"},
		{"bsd-4", redist + "3. All advertising materials mentioning features", "BSD-4-Clause"},
		{"bsd-3", redist + "Neither the name of Google Inc. nor the names of its\ncontributors may be used to endorse or promote products", "BSD-3-Clause"},
		{"bsd-2", redist + "2. Redistributions in binary form must reproduce", "BSD-2-Clause"},
		// A module with parts under different licenses keeps all the texts in one
		// LICENSE: the label is the text that comes first, here BSD-3-Clause (as in
		// klauspost/compress, where Apache-2.0 and MIT follow for some of the parts).
		{"composite, BSD-3 first", "Copyright (c) 2012 Klaus Post.\n\n" + redist +
			"3. Neither the name of Google Inc. nor the names of its\ncontributors may be used to endorse or promote products\n\n" +
			"Apache License\nVersion 2.0, January 2004\n\n" +
			"Permission is hereby granted, free of charge", "BSD-3-Clause"},
		{"composite, MIT first", "Copyright (c) 2020 someone\n\n" +
			"Permission is hereby granted, free of charge, to any person\n\n" +
			"Apache License\nVersion 2.0, January 2004", "MIT"},
		{"unknown", "All rights reserved. Do whatever.", ""},
	} {
		if got := classify(c.text); got != c.want {
			t.Errorf("%s: classify = %q, want %q", c.name, got, c.want)
		}
	}
}

// The main files first; the case of a name does not matter (LICENSE.md, license-mit);
// directories and sources are not licenses.
func TestLicenseFilesMainFirst(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"PATENTS", "LICENSE.md", "NOTICE", "README.md", "license_test.go", "license-mit", "COPYRIGHT", "UNLICENSE"} {
		write(t, dir, name, "x")
	}
	if err := os.Mkdir(filepath.Join(dir, "LICENSES"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := licenseFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range got {
		names = append(names, filepath.Base(p))
	}
	if want := "LICENSE.md UNLICENSE license-mit COPYRIGHT NOTICE PATENTS"; strings.Join(names, " ") != want {
		t.Errorf("licenseFiles = %v, want %s", names, want)
	}
}

func TestParseModules(t *testing.T) {
	list := `{"Standard": true}
{"Module": {"Path": "golang.org/x/sys", "Version": "v0.1.0", "Dir": "/m/sys"}}
{"Module": {"Path": "github.com/tailscale/wf", "Version": "v1", "Dir": "/m/wf",
  "Replace": {"Path": "github.com/FreedomCats/tailscale-wf", "Version": "v2", "Dir": "/m/fork"}}}
{"Module": {"Path": "github.com/FreedomCats/dnstt-cli", "Main": true, "Dir": "/src"}}
{"Module": {"Path": "example.com/other-main", "Main": true, "Dir": "/other"}}
{"Module": {"Path": "github.com/BurntSushi/toml", "Version": "v1.6.0", "Dir": "/m/toml"}}
`
	mods, err := parseModules([]byte(list))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range mods {
		got = append(got, m.title()+":"+m.Dir)
	}
	want := []string{
		"golang.org/x/sys v0.1.0:/m/sys",
		"github.com/FreedomCats/tailscale-wf v2:/m/fork",
		"github.com/BurntSushi/toml v1.6.0:/m/toml",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("parseModules:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if _, err := parseModules([]byte("{not json")); err == nil {
		t.Error("broken JSON passed")
	}
}

// The union keeps one entry per path and version; the order is by path, case ignored,
// then by version.
func TestMerge(t *testing.T) {
	a := []module{
		{Path: "golang.org/x/sys", Version: "v0.49.0", Dir: "/a"},
		{Path: "github.com/xtaci/smux", Version: "v1.5.24", Dir: "/b"},
	}
	b := []module{
		{Path: "golang.org/x/sys", Version: "v0.49.0", Dir: "/a"},
		{Path: "golang.org/x/sys", Version: "v0.48.0", Dir: "/c"},
		{Path: "GitHub.com/pkg/errors", Version: "v0.9.1", Dir: "/d"},
	}
	got := merge(a, b)
	var titles []string
	for _, m := range got {
		titles = append(titles, m.title())
	}
	want := []string{
		"GitHub.com/pkg/errors v0.9.1",
		"github.com/xtaci/smux v1.5.24",
		"golang.org/x/sys v0.48.0",
		"golang.org/x/sys v0.49.0",
	}
	if strings.Join(titles, "\n") != strings.Join(want, "\n") {
		t.Errorf("merge:\n%s\nwant\n%s", strings.Join(titles, "\n"), strings.Join(want, "\n"))
	}
}

func TestCollect(t *testing.T) {
	goroot := t.TempDir()
	write(t, goroot, "LICENSE", "Redistribution and use in source and binary forms ... endorse or promote")
	write(t, goroot, "PATENTS", "grant")

	withLicense := t.TempDir()
	write(t, withLicense, "COPYING", "Permission is hereby granted, free of charge\r\n")
	unknown := t.TempDir()
	write(t, unknown, "LICENSE", "Proprietary")
	empty := t.TempDir()

	sections, err := collect([]module{{Path: "example.com/mit", Version: "v1", Dir: withLicense}}, goroot)
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) != 2 || sections[0].Title != "Go standard library and runtime" || sections[0].Kind != "BSD-3-Clause" {
		t.Fatalf("the Go section: %+v", sections)
	}
	if s := sections[1]; s.Title != "example.com/mit v1" || s.Kind != "MIT" || len(s.Files) != 1 ||
		s.Files[0].Name != "COPYING" || s.Files[0].Text != "Permission is hereby granted, free of charge" {
		t.Errorf("the module with COPYING: %+v", s)
	}

	_, err = collect([]module{
		{Path: "example.com/unknown", Version: "v1", Dir: unknown},
		{Path: "example.com/empty", Version: "v1", Dir: empty},
		{Path: "example.com/missing", Version: "v1", Dir: filepath.Join(goroot, "nope")},
	}, goroot)
	for _, want := range []string{"example.com/unknown v1: license kind not recognised", "example.com/empty v1: no license file", "example.com/missing v1"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("no %q in the error: %v", want, err)
		}
	}
}

func TestOwnSections(t *testing.T) {
	root := t.TempDir()
	write(t, root, "LICENSE", "MIT License\n\nPermission is hereby granted, free of charge")
	write(t, filepath.Join(root, "core"), "COPYING", "Creative Commons Legal Code\n\nCC0 1.0 Universal")

	sections, err := ownSections(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) != 2 || sections[0].Title != "dnstt-cli (the shell)" || sections[0].Kind != "MIT" ||
		sections[1].Title != "dnstt core" || sections[1].Kind != "CC0-1.0" ||
		sections[1].Files[0].Name != "COPYING" {
		t.Errorf("own sections: %+v", sections)
	}

	write(t, root, "LICENSE", "Proprietary")
	if _, err := ownSections(root); err == nil {
		t.Error("a non-MIT shell license passed")
	}
}

func TestRender(t *testing.T) {
	sections := []section{
		{Title: "Go standard library and runtime", Kind: "BSD-3-Clause", Files: []licenseText{{Name: "LICENSE", Text: "a\nb"}}},
		{Title: "example.com/m v1", Kind: "MIT", Note: "a note", Files: []licenseText{{Name: "LICENSE", Text: "c"}}},
	}
	text := render(sections)
	if strings.Contains(text, "\r") {
		t.Error("the text must be LF only")
	}
	pad := strings.Repeat(" ", len("Go standard library and runtime")-len("example.com/m v1"))
	for _, want := range []string{
		"Third-party components of dnstt-cli",
		"Contents:",
		"Go standard library and runtime  BSD-3-Clause",
		"example.com/m v1" + pad + "  MIT",
		"Go standard library and runtime — BSD-3-Clause",
		"example.com/m v1 — MIT",
		"a note",
		"--- LICENSE ---\n\na\nb",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in:\n%s", want, text)
		}
	}
	if !strings.HasSuffix(text, "\n") {
		t.Error("the file must end with a newline")
	}
}

// The committed file is what the command writes: no stale version can be released, and no
// path of the build machine or toolchain version gets into it.
func TestCommittedFile(t *testing.T) {
	if testing.Short() {
		t.Skip("go list over the whole module")
	}
	root, err := script.Root()
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := run(dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dest, outName))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(root, outName))
	if err != nil {
		t.Fatalf("%v — run go run ./scripts/notices -dest .", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is stale: run go run ./scripts/notices -dest .", filepath.Join(root, outName))
	}
	goroot, err := goEnv("GOROOT")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(want, []byte(goroot)) {
		t.Errorf("%s names the machine's GOROOT", outName)
	}
	if bytes.Contains(want, []byte("\r")) {
		t.Errorf("%s has CRLF line endings", outName)
	}
}

func write(t *testing.T, dir, name, text string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
