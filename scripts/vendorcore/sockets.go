package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"slices"
	"strconv"
	"strings"
)

// The socket guard (doc/architecture.md §06): every socket of the core bypasses the tunnel
// through Config.Control. checkSockets finds every place in core/ that names an API
// creating a socket, resolving a name or building an HTTP client, and checks it against
// socketSites. A new place fails the transfer and -check: upstream started a socket
// without the hook, and in tun it would go into the tunnel.
//
// Places are found in the syntax tree, not by text: by the package an identifier is
// imported from, whatever name the import is given. Test files are not scanned: they are
// not built into dnstt-cli.

// watched are the identifiers of the APIs: import path → names; a name ending in "*" is a
// prefix. Types that only hold a socket made elsewhere (net.Conn, net.Listener) are not
// watched: the place that made it is.
var watched = map[string][]string{
	"net": {
		"Dial", "DialTimeout", "DialTCP", "DialUDP", "DialIP", "DialUnix", "Dialer",
		"Listen", "ListenPacket", "ListenTCP", "ListenUDP", "ListenIP", "ListenUnix", "ListenUnixgram",
		"ListenMulticastUDP", "ListenConfig", "FileConn", "FilePacketConn", "FileListener",
		"Resolve*", "Lookup*", "Resolver", "DefaultResolver",
	},
	"crypto/tls":                            {"Dial", "DialWithDialer", "Dialer", "Listen"},
	"net/http":                              {"Transport", "DefaultTransport", "Client", "DefaultClient", "Get", "Head", "Post", "PostForm"},
	"golang.org/x/net/http2":                {"Transport"},
	"github.com/refraction-networking/utls": {"Dial", "DialWithDialer"},
	"github.com/xtaci/kcp-go/v5":            {"Dial", "DialWithOptions", "Listen", "ListenWithOptions"},
	"syscall":                               {"Socket", "Socketpair"},
	"golang.org/x/sys/unix":                 {"Socket", "Socketpair"},
	"golang.org/x/sys/windows":              {"Socket", "WSASocket"},
}

// socketSite is a place where the core names a watched identifier.
type socketSite struct {
	// File is the path in core/; Func is the top-level function, "Type.Method" for a
	// method, "" for package level; Name is the identifier with its import path.
	File, Func, Name string
	// Count is how many times the function names it.
	Count int
	// Control: every composite literal of Name in the function sets the Control field.
	Control bool
	// Reason is what the socket is and why it is covered.
	Reason string
}

// socketSites are all places in core/ after the rules.
var socketSites = []socketSite{
	// The hooked points of §06 — all in the session's sockets (transport.go).
	{File: "client/transport.go", Func: "sockets.dial", Name: "net.Dialer", Count: 1, Control: true,
		Reason: "points 2–4: TCP to DoH and DoT resolvers, with uTLS and without"},
	{File: "client/transport.go", Func: "sockets.listenUDP", Name: "net.ListenConfig", Count: 1, Control: true,
		Reason: "point 1: UDP-mode queries, a socket per query (P3)"},
	{File: "client/transport.go", Func: "openTransport", Name: "net/http.Transport", Count: 1,
		Reason: "DoH without uTLS: DialContext is sockets.dial, no proxy"},
	{File: "client/utls.go", Func: "makeRoundTripper", Name: "golang.org/x/net/http2.Transport", Count: 1,
		Reason: "DoH with uTLS over HTTP/2: DialTLS is utlsDialContext with sockets.dial"},
	{File: "client/utls.go", Func: "makeRoundTripper", Name: "net/http.DefaultTransport", Count: 1,
		Reason: "DoH with uTLS over HTTP/1.1: a clone; DialContext and DialTLSContext through sockets.dial, no proxy (rule)"},
	{File: "client/utls.go", Func: "makeRoundTripper", Name: "net/http.Transport", Count: 1,
		Reason: "the type assertion of the clone above"},
	{File: "client/utls.go", Func: "utlsRoundTripper.RoundTrip", Name: "net/http.DefaultTransport", Count: 1,
		Reason: "http:// DoH URLs: Dial accepts only https://, so never reached"},
	{File: "client/http.go", Func: "", Name: "net/http.Client", Count: 1,
		Reason: "the field of HTTPPacketConn; its Transport is given by the caller"},
	{File: "client/http.go", Func: "NewHTTPPacketConn", Name: "net/http.Client", Count: 1,
		Reason: "an http.Client with the caller's Transport"},

	// Upstream main and run of the client: dnstt-cli does not call them (§04).
	{File: "client/main.go", Func: "run", Name: "net.ListenTCP", Count: 1, Reason: "upstream run, not called"},
	{File: "client/main.go", Func: "main", Name: "net.ResolveTCPAddr", Count: 1, Reason: "upstream main, not called"},
	{File: "client/main.go", Func: "main", Name: "net.ResolveUDPAddr", Count: 1, Reason: "upstream main, not called"},
	{File: "client/main.go", Func: "main", Name: "net.ListenUDP", Count: 1, Reason: "upstream main, not called"},
	{File: "client/main.go", Func: "main", Name: "net.Dialer", Count: 2, Reason: "upstream main, not called"},
	{File: "client/main.go", Func: "main", Name: "crypto/tls.Dialer", Count: 1, Reason: "upstream main, not called"},
	{File: "client/main.go", Func: "main", Name: "net/http.DefaultTransport", Count: 1, Reason: "upstream main, not called"},
	{File: "client/main.go", Func: "main", Name: "net/http.Transport", Count: 1, Reason: "upstream main, not called"},

	// The server: built only into the tests of the client (core/server/testing.go).
	{File: "server/main.go", Func: "handleStream", Name: "net.Dialer", Count: 1, Reason: "server, tests only: the upstream TCP address"},
	{File: "server/main.go", Func: "main", Name: "net.ResolveIPAddr", Count: 1, Reason: "server, upstream main, not called"},
	{File: "server/main.go", Func: "main", Name: "net.ListenPacket", Count: 1, Reason: "server, upstream main, not called"},
	{File: "server/testing.go", Func: "StartTest", Name: "net.ListenUDP", Count: 1, Reason: "server, tests only: loopback UDP"},
}

// siteKey identifies a place: file, function, identifier.
type siteKey struct{ File, Func, Name string }

// hit is one occurrence of a watched identifier.
type hit struct {
	pos     token.Position
	literal bool // a composite literal of the type
	control bool // ... that sets Control
}

// watchedName reports whether name of the package path is watched.
func watchedName(pkg, name string) bool {
	for _, w := range watched[pkg] {
		if prefix, ok := strings.CutSuffix(w, "*"); ok && strings.HasPrefix(name, prefix) || w == name {
			return true
		}
	}
	return false
}

// scannedForSockets: Go files of core/ that are built into the binary.
func scannedForSockets(rel string) bool {
	return strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go")
}

// findSockets parses one file and returns the occurrences of watched identifiers by
// function.
func findSockets(name string, src []byte) (map[siteKey][]hit, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	imports := map[string]string{} // local name → import path
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return nil, err
		}
		local := path.Base(p)
		if strings.HasPrefix(local, "v") && strings.Contains(p, "/") {
			// A major version suffix: kcp-go/v5 is package kcp.
			if _, err := strconv.Atoi(local[1:]); err == nil {
				local = path.Base(path.Dir(p))
				local = strings.TrimSuffix(local, "-go")
			}
		}
		if imp.Name != nil {
			local = imp.Name.Name
		}
		if local == "." {
			return nil, fmt.Errorf("%s: dot import of %s — the guard cannot see its identifiers", fset.Position(imp.Pos()), p)
		}
		if _, ok := watched[p]; ok {
			imports[local] = p
		}
	}

	hits := map[siteKey][]hit{}
	// literals: selector → its composite literal, to tell `net.Dialer{…}` apart.
	literals := map[*ast.SelectorExpr]*ast.CompositeLit{}
	visit := func(fn string, node ast.Node) {
		ast.Inspect(node, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CompositeLit:
				if sel, ok := n.Type.(*ast.SelectorExpr); ok {
					literals[sel] = n
				}
			case *ast.SelectorExpr:
				id, ok := n.X.(*ast.Ident)
				if !ok {
					return true
				}
				pkg, ok := imports[id.Name]
				if !ok || !watchedName(pkg, n.Sel.Name) {
					return true
				}
				h := hit{pos: fset.Position(n.Pos())}
				if lit, ok := literals[n]; ok {
					h.literal = true
					for _, elt := range lit.Elts {
						if kv, ok := elt.(*ast.KeyValueExpr); ok {
							if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Control" {
								h.control = true
							}
						}
					}
				}
				k := siteKey{name, fn, pkg + "." + n.Sel.Name}
				hits[k] = append(hits[k], h)
			}
			return true
		})
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			visit("", decl)
			continue
		}
		visit(funcName(fn), fn)
	}
	return hits, nil
}

// funcName is "name" for a function and "Type.Method" for a method.
func funcName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	t := fn.Recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if idx, ok := t.(*ast.IndexExpr); ok {
		t = idx.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name + "." + fn.Name.Name
	}
	return fn.Name.Name
}

// checkSockets checks the places in plan against socketSites.
func checkSockets(plan map[string][]byte) error {
	found := map[siteKey][]hit{}
	var problems []string
	for name, src := range plan {
		if !scannedForSockets(name) {
			continue
		}
		hits, err := findSockets(name, src)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		for k, h := range hits {
			found[k] = h
		}
	}

	known := map[siteKey]bool{}
	for _, s := range socketSites {
		k := siteKey{s.File, s.Func, s.Name}
		known[k] = true
		at := found[k]
		if len(at) != s.Count {
			problems = append(problems, fmt.Sprintf("%s: %s in %s — found %d, socketSites has %d; lines %s",
				s.File, s.Name, funcLabel(s.Func), len(at), s.Count, lineNumbers(at)))
			continue
		}
		if !s.Control {
			continue
		}
		for _, h := range at {
			if !h.literal || !h.control {
				problems = append(problems, fmt.Sprintf("%s: %s without Control — a socket without the hook", h.pos, s.Name))
			}
		}
	}
	for k, at := range found {
		if known[k] {
			continue
		}
		for _, h := range at {
			problems = append(problems, fmt.Sprintf("%s: %s in %s — a new place where the core creates a socket or resolves a name; "+
				"put it on the hook (§06) and into socketSites", h.pos, k.Name, funcLabel(k.Func)))
		}
	}
	if len(problems) > 0 {
		slices.Sort(problems)
		return fmt.Errorf("socket guard — sockets of the core outside the hook (scripts/vendorcore/sockets.go):\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}

func funcLabel(fn string) string {
	if fn == "" {
		return "package level"
	}
	return fn
}

func lineNumbers(at []hit) string {
	if len(at) == 0 {
		return "—"
	}
	s := make([]string, len(at))
	for i, h := range at {
		s[i] = strconv.Itoa(h.pos.Line)
	}
	return strings.Join(s, ", ")
}
