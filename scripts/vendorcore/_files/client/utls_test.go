// Added by dnstt-cli (scripts/vendorcore/_files): not part of the dnstt upstream.

package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"

	"github.com/FreedomCats/dnstt-cli/core/turbotunnel"
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

func TestUTLSHandshakeContext(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			ready := make(chan struct{}, 1)
			peer := newTLSPeer(t, silentTLS(ready))
			s := newSockets(&Config{})
			t.Cleanup(s.closeAll)
			ctx, cancel := context.WithCancel(context.Background())
			want := context.Canceled
			if mode == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 250*time.Millisecond)
				want = context.DeadlineExceeded
			}
			done, result := make(chan struct{}), make(chan error, 1)
			go func() {
				defer close(done)
				conn, err := utlsDialContext(ctx, "tcp", peer.ln.Addr().String(), nil, utlsLookup("Firefox_120"), s.dial)
				if conn != nil {
					conn.Close()
				}
				result <- err
			}()
			t.Cleanup(func() { cancel(); peer.close(); waitTLS(t, done, "uTLS helper completion") })
			acceptTLS(t, peer)
			waitTLS(t, ready, "uTLS ClientHello")
			if mode == "cancel" {
				cancel()
			}
			waitTLS(t, done, "uTLS handshake cancellation")
			if err := <-result; !errors.Is(err, want) {
				t.Errorf("utlsDialContext: %v, want %v", err, want)
			}
			requireEmptySockets(t, s)
		})
	}
}

func TestUTLSPreendedHandshakeContext(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run("expired="+strconv.FormatBool(expired), func(t *testing.T) {
			peer := newTLSPeer(t, func(c net.Conn) { io.Copy(io.Discard, c) })
			s := newSockets(&Config{})
			t.Cleanup(s.closeAll)
			// Acquire TCP first, then inject it into the helper under an ended context.
			// This checks handshake error closure rather than net.Dialer's fast failure.
			raw, err := s.dial(context.Background(), "tcp", peer.ln.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			want := context.Canceled
			if expired {
				cancel()
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				want = context.DeadlineExceeded
			} else {
				cancel()
			}
			defer cancel()
			done, result := make(chan struct{}), make(chan error, 1)
			go func() {
				defer close(done)
				conn, err := utlsDialContext(ctx, "tcp", "dot.example:853", nil, utlsLookup("Firefox_120"), func(context.Context, string, string) (net.Conn, error) {
					return raw, nil
				})
				if conn != nil {
					conn.Close()
				}
				result <- err
			}()
			t.Cleanup(func() { raw.Close(); peer.close(); waitTLS(t, done, "pre-ended uTLS helper") })
			waitTLS(t, done, "pre-ended uTLS handshake")
			if err := <-result; !errors.Is(err, want) {
				t.Errorf("HandshakeContext: %v, want %v", err, want)
			}
			requireEmptySockets(t, s)
		})
	}
}

func TestUTLSConnectionOutlivesHandshakeContext(t *testing.T) {
	cert, roots := tlsIdentity(t)
	peer := newTLSPeer(t, func(raw net.Conn) {
		conn := tls.Server(raw, &tls.Config{Certificates: []tls.Certificate{cert}})
		if conn.Handshake() == nil {
			io.Copy(conn, conn)
		}
	})
	s := newSockets(&Config{})
	t.Cleanup(s.closeAll)
	ctx, cancel := context.WithTimeout(context.Background(), tlsTestWait)
	defer cancel()
	config := &utls.Config{RootCAs: roots}
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		return s.dial(ctx, network, peer.ln.Addr().String())
	}
	conn, err := utlsDialContext(ctx, "tcp", "dot.example:853", config, utlsLookup("Firefox_120"), dial)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if config.ServerName != "" || config.RootCAs != roots {
		t.Fatal("helper mutated the caller's TLS config")
	}
	if !conn.ConnectionState().HandshakeComplete {
		t.Fatal("uTLS helper returned before its eager handshake")
	}
	cancel()
	conn.SetDeadline(time.Now().Add(tlsTestWait))
	if _, err := conn.Write([]byte("after cancellation")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("after cancellation"))
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != "after cancellation" {
		t.Fatalf("Read after cancellation: %q, %v", got, err)
	}
	conn.Close()
	requireEmptySockets(t, s)
}

// Both ALPN branches use the caller's dial hook, reuse the bootstrap connection and
// dial again after the idle connection is closed. Only the HTTP/1 branch has a proxy
// field; it must not inherit one from http.DefaultTransport or the environment.
func TestUTLSDoHRoundTrips(t *testing.T) {
	cert, roots := tlsIdentity(t)
	for _, h2 := range []bool{false, true} {
		t.Run("http2="+strconv.FormatBool(h2), func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.TLS.ServerName != "doh.example" || r.URL.Path != "/dns-query" || r.Header.Get("Content-Type") != "application/dns-message" {
					t.Errorf("DoH request: SNI=%q path=%q content-type=%q", r.TLS.ServerName, r.URL.Path, r.Header.Get("Content-Type"))
				}
				w.Header().Set("Content-Type", "application/dns-message")
				io.Copy(w, r.Body)
			}))
			srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
			srv.EnableHTTP2 = h2
			srv.StartTLS()
			defer srv.Close()
			_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
			var lookups, controls atomic.Int32
			cfg := Config{
				LookupIP: func(_ context.Context, host string) ([]netip.Addr, error) {
					lookups.Add(1)
					if host != "doh.example" {
						t.Errorf("LookupIP(%q)", host)
					}
					return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
				},
				Control: func(network, addr string, _ syscall.RawConn) error {
					controls.Add(1)
					if network != "tcp4" || addr != srv.Listener.Addr().String() {
						t.Errorf("Control(%q, %q)", network, addr)
					}
					return nil
				},
			}
			s := newSockets(&cfg)
			t.Cleanup(s.closeAll)
			rt := NewUTLSRoundTripper(&utls.Config{RootCAs: roots}, utlsLookup("Firefox_120"), s.dial)
			for range 2 {
				ctx, cancel := context.WithTimeout(context.Background(), tlsTestWait)
				req, err := http.NewRequestWithContext(ctx, "POST", "https://doh.example:"+port+"/dns-query", strings.NewReader("query"))
				if err != nil {
					cancel()
					t.Fatal(err)
				}
				req.Header.Set("Content-Type", "application/dns-message")
				resp, err := rt.RoundTrip(req)
				if err != nil {
					cancel()
					t.Fatal(err)
				}
				body, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				cancel()
				if err != nil || string(body) != "query" || resp.ProtoMajor != map[bool]int{false: 1, true: 2}[h2] {
					t.Fatalf("DoH response: %s %q, %v", resp.Proto, body, err)
				}
				if tr, ok := rt.inner.(*http.Transport); ok && (tr.Proxy != nil || tr.DialContext == nil || tr.DialTLSContext == nil) {
					t.Fatal("HTTP/1 transport lost hook or proxy bypass")
				}
				rt.inner.(interface{ CloseIdleConnections() }).CloseIdleConnections()
			}
			if requests.Load() != 2 || lookups.Load() < 2 || controls.Load() != lookups.Load() {
				t.Errorf("requests=%d lookups=%d controls=%d", requests.Load(), lookups.Load(), controls.Load())
			}
			s.closeAll()
			requireEmptySockets(t, s)
		})
	}
}

// HTTP/2's existing DialTLS callback supplies Background; closure there is owned by
// sockets, while HTTP/1's callback also accepts a caller context. Do not assert a new
// HTTP/2 request-context contract or rewrite the shared HTTP machinery.
func TestUTLSDoHCallbackFailures(t *testing.T) {
	cert, roots := tlsIdentity(t)
	for _, protocol := range []string{"http/1.1", "h2"} {
		for _, phase := range []string{"bootstrap", "subsequent"} {
			t.Run(protocol+"/"+phase, func(t *testing.T) {
				good := newTLSPeer(t, func(raw net.Conn) {
					conn := tls.Server(raw, &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{protocol}})
					if conn.Handshake() == nil {
						io.Copy(io.Discard, conn)
					}
				})
				ready := make(chan struct{}, 1)
				silent := newTLSPeer(t, silentTLS(ready))
				s := newSockets(&Config{})
				t.Cleanup(s.closeAll)
				var fail atomic.Bool
				fail.Store(phase == "bootstrap")
				dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
					if fail.Load() {
						return s.dial(ctx, network, silent.ln.Addr().String())
					}
					return s.dial(ctx, network, good.ln.Addr().String())
				}
				ctx, cancel := context.WithTimeout(context.Background(), tlsTestWait)
				defer cancel()
				req, _ := http.NewRequestWithContext(ctx, "POST", "https://doh.example/dns-query", nil)
				done, result := make(chan struct{}), make(chan error, 1)
				var invoke func() error
				if phase == "bootstrap" {
					invoke = func() error {
						_, err := makeRoundTripper(req, &utls.Config{RootCAs: roots}, utlsLookup("Firefox_120"), dial)
						return err
					}
				} else {
					rt, err := makeRoundTripper(req, &utls.Config{RootCAs: roots}, utlsLookup("Firefox_120"), dial)
					if err != nil {
						t.Fatal(err)
					}
					var callback func() (net.Conn, error)
					switch tr := rt.(type) {
					case *http.Transport:
						if protocol != "http/1.1" || tr.Proxy != nil {
							t.Fatal("unexpected HTTP/1 ALPN or proxy")
						}
						callback = func() (net.Conn, error) { return tr.DialTLSContext(ctx, "tcp", "doh.example:443") }
					case *http2.Transport:
						if protocol != "h2" {
							t.Fatal("unexpected HTTP/2 ALPN")
						}
						callback = func() (net.Conn, error) { return tr.DialTLS("tcp", "doh.example:443", nil) }
					default:
						t.Fatalf("unexpected transport %T", rt)
					}
					bootstrap, err := callback()
					if err != nil {
						t.Fatal(err)
					}
					bootstrap.Close()
					fail.Store(true)
					invoke = func() error {
						conn, err := callback()
						if err == nil {
							conn.Close()
						}
						return err
					}
				}
				go func() { defer close(done); result <- invoke() }()
				t.Cleanup(func() {
					cancel()
					s.closeAll()
					silent.close()
					good.close()
					waitTLS(t, done, "DoH callback completion")
				})
				acceptTLS(t, silent)
				waitTLS(t, ready, "DoH callback ClientHello")
				if phase == "subsequent" && protocol == "h2" {
					s.closeAll()
				} else {
					cancel()
				}
				waitTLS(t, done, "DoH callback interruption")
				err := <-result
				if err == nil || (phase == "bootstrap" || protocol == "http/1.1") && !errors.Is(err, context.Canceled) {
					t.Errorf("DoH callback: %v", err)
				}
				requireEmptySockets(t, s)
			})
		}
	}
}

func TestUTLSDoHFailedDial(t *testing.T) {
	want := errors.New("injected dial failure")
	config := &utls.Config{ServerName: "doh.example", RootCAs: x509.NewCertPool()}
	rt := NewUTLSRoundTripper(config, utlsLookup("Firefox_120"), func(ctx context.Context, network, addr string) (net.Conn, error) {
		if network != "tcp" || addr != "doh.example:443" {
			t.Errorf("dial(%q, %q)", network, addr)
		}
		return nil, want
	})
	req, _ := http.NewRequest("POST", "https://doh.example/dns-query", nil)
	if _, err := rt.RoundTrip(req); !errors.Is(err, want) || rt.inner != nil {
		t.Errorf("failed bootstrap: %v, inner=%T", err, rt.inner)
	}
	if config.ServerName != "doh.example" {
		t.Error("failed dial mutated TLS config")
	}
}

func TestDoHCloseDuringTLSSetup(t *testing.T) {
	for _, variant := range []string{"none", "Firefox_120"} {
		t.Run(variant, func(t *testing.T) {
			ready := make(chan struct{}, 1)
			peer := newTLSPeer(t, silentTLS(ready))
			cfg := doTConfig(t, peer.ln.Addr().String(), variant)
			cfg.Resolver = Resolver{DoH, "https://" + peer.ln.Addr().String() + "/dns-query"}
			cfg.Logf = func(string, ...any) {}
			s := newSockets(&cfg)
			t.Cleanup(func() { s.closeAll(); peer.close(); requireNoTLSLoops(t) })
			_, conn, err := openTransport(context.Background(), &cfg, s, utlsLookup(variant))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := conn.WriteTo([]byte("query"), turbotunnel.DummyAddr{}); err != nil {
				t.Fatal(err)
			}
			acceptTLS(t, peer)
			waitTLS(t, ready, "DoH setup ClientHello")
			s.closeAll()
			requireEmptySockets(t, s)
			requireNoTLSLoops(t)
		})
	}
}
