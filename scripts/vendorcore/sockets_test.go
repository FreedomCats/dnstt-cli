package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withSockets replaces the list of socket places for the duration of a test.
func withSockets(t *testing.T, ss ...socketSite) {
	t.Helper()
	saved := socketSites
	t.Cleanup(func() { socketSites = saved })
	socketSites = ss
}

const socketSource = `package client

import (
	stdnet "net"
	"net/http"
	"strings"
)

var client = http.Client{}

type dialer struct{}

func (d *dialer) dial() {
	_ = &stdnet.Dialer{Control: nil}
	_ = stdnet.Dialer{}
	_, _ = stdnet.ResolveUDPAddr("udp", "x")
	_ = strings.Title // not watched
	var _ stdnet.Conn // not watched: holds a socket made elsewhere
	go func() { stdnet.Dial("tcp", "x") }()
}
`

// Identifiers are found by the package they come from, whatever the import is called,
// with their function — closures count in their enclosing function — and with Control
// on literals.
func TestFindSockets(t *testing.T) {
	hits, err := findSockets("client/a.go", []byte(socketSource))
	if err != nil {
		t.Fatal(err)
	}
	count := func(fn, name string) int { return len(hits[siteKey{"client/a.go", fn, name}]) }
	for _, c := range []struct {
		fn, name string
		n        int
	}{
		{"", "net/http.Client", 1},
		{"dialer.dial", "net.Dialer", 2},
		{"dialer.dial", "net.ResolveUDPAddr", 1},
		{"dialer.dial", "net.Dial", 1},
	} {
		if got := count(c.fn, c.name); got != c.n {
			t.Errorf("%s in %q: %d, want %d", c.name, c.fn, got, c.n)
		}
	}
	if len(hits) != 4 {
		t.Errorf("hits: %v", hits)
	}
	dialers := hits[siteKey{"client/a.go", "dialer.dial", "net.Dialer"}]
	if !dialers[0].literal || !dialers[0].control || !dialers[1].literal || dialers[1].control {
		t.Errorf("literals and Control: %+v", dialers)
	}

	if _, err := findSockets("a.go", []byte("package a\nimport . \"net\"\n")); err == nil || !strings.Contains(err.Error(), "dot import") {
		t.Errorf("dot import: %v", err)
	}
}

func TestCheckSockets(t *testing.T) {
	plan := map[string][]byte{
		"client/a.go":      []byte(socketSource),
		"client/a_test.go": []byte("package client\nimport \"net\"\nvar _, _ = net.Dial(\"tcp\", \"x\")\n"),
	}
	sites := []socketSite{
		{File: "client/a.go", Func: "", Name: "net/http.Client", Count: 1},
		{File: "client/a.go", Func: "dialer.dial", Name: "net.ResolveUDPAddr", Count: 1},
		{File: "client/a.go", Func: "dialer.dial", Name: "net.Dial", Count: 1},
		{File: "client/a.go", Func: "dialer.dial", Name: "net.Dialer", Count: 2},
	}
	withSockets(t, sites...)
	if err := checkSockets(plan); err != nil {
		t.Fatalf("every place is listed, test files are not scanned: %v", err)
	}

	// The literal without Control.
	withSockets(t, append(sites[:3:3], socketSite{File: "client/a.go", Func: "dialer.dial", Name: "net.Dialer", Count: 2, Control: true})...)
	if err := checkSockets(plan); err == nil || !strings.Contains(err.Error(), "client/a.go:15:6: net.Dialer without Control") {
		t.Errorf("Dialer without Control: %v", err)
	}

	// A place not listed and a count that changed.
	withSockets(t, sites[1:]...)
	plan["client/b.go"] = []byte("package client\nimport \"net\"\nfunc f() { net.ListenUDP(\"udp\", nil); net.ResolveUDPAddr(\"udp\", \"x\") }\n")
	err := checkSockets(plan)
	for _, want := range []string{
		"client/a.go:9:14: net/http.Client in package level — a new place",
		"client/b.go:3:12: net.ListenUDP in f — a new place",
		"client/b.go:3:39: net.ResolveUDPAddr in f — a new place",
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error without %q:\n%v", want, err)
		}
	}
	delete(plan, "client/b.go")
	plan["client/a.go"] = []byte(strings.Replace(socketSource, `go func() { stdnet.Dial("tcp", "x") }()`, "", 1))
	withSockets(t, sites...)
	if err := checkSockets(plan); err == nil || !strings.Contains(err.Error(), "net.Dial in dialer.dial — found 0, socketSites has 1") {
		t.Errorf("a place gone: %v", err)
	}
}

// A socket in a file that lands in core/ fails the transfer before anything is written.
func TestRunSocketGuard(t *testing.T) {
	withRules(t)
	root, up := t.TempDir(), fakeUpstream(t)
	write(t, filepath.Join(up, "dnstt-client", "dial.go"), "package main\nimport \"net\"\nfunc f() { net.Dial(\"udp\", \"192.0.2.1:53\") }\n")
	_, err := run(root, up, false)
	if err == nil || !strings.Contains(err.Error(), "client/dial.go:3:12: net.Dial in f — a new place") {
		t.Errorf("error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "core")); !os.IsNotExist(err) {
		t.Errorf("core/ created: %v", err)
	}
}
