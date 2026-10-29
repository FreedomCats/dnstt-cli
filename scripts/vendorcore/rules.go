package main

// =============================== patches ========================================
// Everything that changes in the upstream code is described here and only here (R1 in
// doc/architecture.md, patches P1–P7 — §07).

// vendoredDirs are upstream directories carried into core/ whole: upstream directory →
// directory in core/. The Go conversion (convertGo in main.go) makes the package name of
// every file the name of its directory in core/, so dnstt-client stops being package
// main.
var vendoredDirs = []vendoredDir{
	{"dns", "dns"},
	{"noise", "noise"},
	{"turbotunnel", "turbotunnel"},
	{"dnstt-client", "client"},
	// The server is used only by tests: they start it in-process (core/server/testing.go).
	{"dnstt-server", "server"},
}

type vendoredDir struct {
	Upstream, Core string
}

// vendoredFiles are single upstream files: path upstream → path in core/. The CC0 text
// goes with the code.
var vendoredFiles = map[string]string{
	"COPYING": "COPYING",
}

// addedDir is the repository directory with our own files for core/: patches that are
// easier written as a separate file than as insertions into upstream code (the session API
// of P1). Paths under it are the same as in core/; files are carried as they are, rules
// do not apply to them. A file with the same path upstream is an error: upstream started
// its own, and ours would silently overwrite it.
const addedDir = "scripts/vendorcore/files"

// importPrefix is the upstream module path; convertGo replaces it with coreImport.
const (
	importPrefix = "www.bamsoftware.com/git/dnstt.git/"
	coreImport   = "github.com/FreedomCats/dnstt-cli/core/"
)

// excludedFile is an upstream file in a vendored directory that dnstt-cli does not need.
// Reason goes into the message of the check.
//
// Its presence upstream is checked: if it is renamed, the command fails instead of
// silently carrying what we threw away. An excluded file left in core/ is removed by the
// transfer and counts as extra in -check.
type excludedFile struct {
	// Path is the upstream path through "/", for example dnstt-client/main.go.
	Path, Reason string
}

var excludedFiles = []excludedFile{}

// rule is a replacement in a core file.
//
// Count is checked: if upstream rewrites the line and the pattern stops matching, the
// command fails. Losing a patch silently is worse than failing.
type rule struct {
	// File is the path of the file in core/ through "/", for example client/utls.go.
	File string
	// Count is how many times the pattern must match.
	Count int
	// Pattern is a regexp over the text with LF, after the Go conversion; group 1 must
	// capture the indentation.
	Pattern string
	// Exactly one of three. Replace is what replaces the whole match (nil — not set, "" —
	// cut out); InsertAfter is a line added after the match with the same indentation;
	// InsertBefore is the same, but before the match.
	Replace      *string
	InsertAfter  string
	InsertBefore string
}

// to is a rule's Replace: a string, possibly empty.
func to(s string) *string { return &s }

// rules are the patches. Empty so far: the package and import conversion is done by
// convertGo, not by rules.
var rules = []rule{}
