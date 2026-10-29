// Added by dnstt-cli (scripts/vendorcore/_files): not part of the dnstt upstream.

package client

import (
	"context"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	utls "github.com/refraction-networking/utls"
)

// P5: every fingerprint of DefaultUTLS is known and carries a DoH request to a Go TLS
// server that speaks HTTP/2, through the dial function it is given. "random" is a new
// fingerprint every time, so it is tried many times: with uTLS v1.8 about one in seven
// fails (DefaultUTLS).
func TestDefaultUTLSFingerprints(t *testing.T) {
	_, labels, err := parseWeightedList(DefaultUTLS)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/dns-message")
		w.Write(body)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())

	for _, label := range labels {
		id := utlsLookup(label)
		if id == nil {
			t.Errorf("DefaultUTLS has the unknown label %q", label)
			continue
		}
		tries := 1
		if label == "random" {
			tries = 50
		}
		t.Run(label, func(t *testing.T) {
			for range tries {
				var dials int
				dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
					dials++
					var d net.Dialer
					return d.DialContext(ctx, network, addr)
				}
				rt := NewUTLSRoundTripper(&utls.Config{RootCAs: roots}, id, dial)
				req, err := http.NewRequest("POST", srv.URL+"/dns-query", strings.NewReader("query"))
				if err != nil {
					t.Fatal(err)
				}
				resp, err := rt.RoundTrip(req)
				if err != nil {
					t.Fatalf("RoundTrip: %v", err)
				}
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK || string(body) != "query" {
					t.Fatalf("%s %q", resp.Status, body)
				}
				if dials == 0 {
					t.Fatal("the connection did not go through the dial function")
				}
				t.Logf("%s %s", id.Client+" "+id.Version, resp.Proto)
			}
		})
	}
}
