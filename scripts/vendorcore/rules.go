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
// The leading underscore keeps go build ./... out: the files compile only in core/.
const addedDir = "scripts/vendorcore/_files"

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

// rules are the patches. The package and import conversion is done by convertGo, not by
// rules.
var rules = []rule{
	// P1: every socket of the core comes from the dialer of the session (§06). uTLS
	// connections take the dial function from their caller instead of building
	// &net.Dialer{} themselves; dialFunc is declared in _files/client/transport.go.
	{
		File: "client/utls.go", Count: 1,
		Pattern: `(?m)^()func utlsDialContext\(ctx context\.Context, network, addr string, config \*utls\.Config, id \*utls\.ClientHelloID\) \(\*utls\.UConn, error\) \{$`,
		Replace: to("func utlsDialContext(ctx context.Context, network, addr string, config *utls.Config, id *utls.ClientHelloID, dial dialFunc) (*utls.UConn, error) {"),
	},
	{
		File: "client/utls.go", Count: 1,
		Pattern: `(?m)^(\t)dialer := &net\.Dialer\{\}\n\tconn, err := dialer\.DialContext\(ctx, network, addr\)$`,
		Replace: to("${1}conn, err := dial(ctx, network, addr)"),
	},
	{
		File: "client/utls.go", Count: 1,
		Pattern:     `(?m)^(\t)inner         http\.RoundTripper$`,
		InsertAfter: "dial          dialFunc",
	},
	{
		File: "client/utls.go", Count: 1,
		Pattern: `(?m)^()func NewUTLSRoundTripper\(config \*utls\.Config, id \*utls\.ClientHelloID\) \*utlsRoundTripper \{$`,
		Replace: to("func NewUTLSRoundTripper(config *utls.Config, id *utls.ClientHelloID, dial dialFunc) *utlsRoundTripper {"),
	},
	{
		File: "client/utls.go", Count: 1,
		Pattern:     `(?m)^(\t\t)config:        config,$`,
		InsertAfter: "dial:          dial,",
	},
	{
		File: "client/utls.go", Count: 1,
		Pattern: `makeRoundTripper\(req, rt\.config, rt\.clientHelloID\)`,
		Replace: to("makeRoundTripper(req, rt.config, rt.clientHelloID, rt.dial)"),
	},
	{
		File: "client/utls.go", Count: 1,
		Pattern: `(?m)^()func makeRoundTripper\(req \*http\.Request, config \*utls\.Config, id \*utls\.ClientHelloID\) \(http\.RoundTripper, error\) \{$`,
		Replace: to("func makeRoundTripper(req *http.Request, config *utls.Config, id *utls.ClientHelloID, dial dialFunc) (http.RoundTripper, error) {"),
	},
	{
		File: "client/utls.go", Count: 2,
		Pattern: `utlsDialContext\((req\.Context\(\)|ctx), "tcp", addr, config, id\)`,
		Replace: to(`utlsDialContext(${1}, "tcp", addr, config, id, dial)`),
	},
	// The HTTP/1.1 transport of uTLS clones http.DefaultTransport: its DialContext and
	// its proxy from the environment would take a connection past the hook (to the proxy).
	{
		File: "client/utls.go", Count: 1,
		Pattern:     `(?m)^(\t\t)tr\.DialTLSContext = dialTLSContext$`,
		InsertAfter: "tr.DialContext = dial\n\t\ttr.Proxy = nil",
	},
	// Upstream main, not called by dnstt-cli, keeps building: it gets the dialer it had.
	{
		File: "client/main.go", Count: 1,
		Pattern: `NewUTLSRoundTripper\(nil, utlsClientHelloID\)`,
		Replace: to("NewUTLSRoundTripper(nil, utlsClientHelloID, (&net.Dialer{}).DialContext)"),
	},
	{
		File: "client/main.go", Count: 1,
		Pattern: `utlsDialContext\(ctx, network, addr, nil, utlsClientHelloID\)`,
		Replace: to("utlsDialContext(ctx, network, addr, nil, utlsClientHelloID, (&net.Dialer{}).DialContext)"),
	},

	// P1: the goroutines of a transport end when it is closed, instead of living on after
	// the session: upstream creates one session per process and never closes it, while
	// dnstt-cli creates a session per connection to the server. QueuePacketConn.Closed and
	// OutgoingUntilClosed are declared in _files/turbotunnel/closed.go.
	//
	// DNSPacketConn: the send loop waits on the close too.
	{
		File: "client/dns.go", Count: 1,
		Pattern:     `(?m)^(\t\t\t)case <-pollTimer\.C:\n\t\t\t\tpollTimerExpired = true$`,
		InsertAfter: "case <-c.QueuePacketConn.Closed():\n\t\t\t\treturn nil",
	},
	// HTTPPacketConn and TLSPacketConn: the send loops range over the outgoing queue
	// until the close, not over the queue itself, which is never closed.
	{
		File: "client/http.go", Count: 1,
		Pattern: `(?m)^(\t)for p := range c\.QueuePacketConn\.OutgoingQueue\(turbotunnel\.DummyAddr\{\}\) \{$`,
		Replace: to("${1}for p := range c.QueuePacketConn.OutgoingUntilClosed(turbotunnel.DummyAddr{}) {"),
	},
	{
		File: "client/tls.go", Count: 1,
		Pattern: `(?m)^(\t)for p := range c\.QueuePacketConn\.OutgoingQueue\(turbotunnel\.DummyAddr\{\}\) \{$`,
		Replace: to("${1}for p := range c.QueuePacketConn.OutgoingUntilClosed(turbotunnel.DummyAddr{}) {"),
	},
	// TLSPacketConn redials after every broken connection; a closed one does not.
	{
		File: "client/tls.go", Count: 1,
		Pattern:     `(?m)^(\t\t\t)conn\.Close\(\)$`,
		InsertAfter: "select {\n\t\t\tcase <-c.QueuePacketConn.Closed():\n\t\t\t\treturn\n\t\t\tdefault:\n\t\t\t}",
	},
}
