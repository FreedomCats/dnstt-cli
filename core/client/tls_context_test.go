// Added by dnstt-cli (scripts/vendorcore/_files): not part of the dnstt upstream.

package client

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"errors"
	"io"
	"math/big"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"

	"github.com/FreedomCats/dnstt-cli/core/noise"
	"github.com/FreedomCats/dnstt-cli/core/server"
)

const tlsTestWait = 5 * time.Second

// tlsPeer owns its accept loop and every accepted connection, including silent peers.
type tlsPeer struct {
	ln       net.Listener
	accepted chan net.Conn
	done     chan struct{}
	mu       sync.Mutex
	conns    map[net.Conn]struct{}
	closed   bool
	wg       sync.WaitGroup
}

func newTLSPeer(t *testing.T, serve func(net.Conn)) *tlsPeer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &tlsPeer{ln: ln, accepted: make(chan net.Conn, 64), done: make(chan struct{}), conns: make(map[net.Conn]struct{})}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			p.mu.Lock()
			if p.closed {
				p.mu.Unlock()
				conn.Close()
				return
			}
			p.conns[conn] = struct{}{}
			p.wg.Add(1)
			p.mu.Unlock()
			select {
			case p.accepted <- conn:
			default:
			}
			go func() {
				defer p.wg.Done()
				defer func() {
					conn.Close()
					p.mu.Lock()
					delete(p.conns, conn)
					p.mu.Unlock()
				}()
				serve(conn)
			}()
		}
	}()
	go func() { p.wg.Wait(); close(p.done) }()
	t.Cleanup(func() { p.close(); waitTLS(t, p.done, "TLS peer shutdown") })
	return p
}

func (p *tlsPeer) drop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for conn := range p.conns {
		conn.Close()
	}
}

func (p *tlsPeer) close() {
	p.ln.Close()
	p.mu.Lock()
	p.closed = true
	for conn := range p.conns {
		conn.Close()
	}
	p.mu.Unlock()
}

func waitTLS(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(tlsTestWait):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func acceptTLS(t *testing.T, p *tlsPeer) net.Conn {
	t.Helper()
	select {
	case conn := <-p.accepted:
		return conn
	case <-time.After(tlsTestWait):
		t.Fatal("TCP peer did not accept a connection")
		return nil
	}
}

// silentTLS signals actual ClientHello traffic before discarding the rest. Acceptance
// alone would allow a close test to win before the TCP dial has entered TLS at all.
func silentTLS(ready chan<- struct{}) func(net.Conn) {
	return func(conn net.Conn) {
		var header [5]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			return
		}
		select {
		case ready <- struct{}{}:
		default:
		}
		io.Copy(io.Discard, conn)
	}
}

func doTConfig(t *testing.T, addr, variant string) Config {
	t.Helper()
	key, err := noise.GeneratePrivkey()
	if err != nil {
		t.Fatal(err)
	}
	return Config{Domain: testDomain, PubKey: noise.PubkeyFromPrivkey(key), Resolver: Resolver{DoT, addr}, UTLS: variant}
}

type tlsDialResult struct {
	s   *Session
	err error
}

// startTLSDial joins the actual Dial even when an assertion fails. Closing the fixture
// releases the pre-fix handshake too; a late successful session is always closed.
func startTLSDial(t *testing.T, ctx context.Context, cfg Config, release func()) <-chan tlsDialResult {
	t.Helper()
	result := make(chan tlsDialResult, 1)
	done := make(chan struct{})
	go func() {
		s, err := Dial(ctx, cfg)
		result <- tlsDialResult{s, err}
		close(done)
	}()
	t.Cleanup(func() {
		release()
		waitTLS(t, done, "outstanding Dial")
		select {
		case r := <-result:
			if r.s != nil {
				r.s.Close()
			}
		default:
		}
	})
	return result
}

func requireTLSDialError(t *testing.T, result <-chan tlsDialResult, want error) {
	t.Helper()
	select {
	case r := <-result:
		if r.s != nil {
			r.s.Close()
			t.Error("Dial returned a session")
		}
		if !errors.Is(r.err, want) {
			t.Errorf("Dial: %v, want %v", r.err, want)
		}
	case <-time.After(tlsTestWait):
		t.Fatal("Dial did not return after its context ended")
	}
}

func TestDoTDialSilentContext(t *testing.T) {
	for _, variant := range []string{"none", "Firefox_120"} {
		for _, mode := range []string{"deadline", "cancel"} {
			t.Run(variant+"/"+mode, func(t *testing.T) {
				ready := make(chan struct{}, 1)
				peer := newTLSPeer(t, silentTLS(ready))
				var ctx context.Context
				var cancel context.CancelFunc
				want := context.Canceled
				if mode == "deadline" {
					ctx, cancel = context.WithTimeout(context.Background(), 250*time.Millisecond)
					want = context.DeadlineExceeded
				} else {
					ctx, cancel = context.WithCancel(context.Background())
				}
				t.Cleanup(cancel)
				result := startTLSDial(t, ctx, doTConfig(t, peer.ln.Addr().String(), variant), func() { cancel(); peer.close() })
				acceptTLS(t, peer)
				waitTLS(t, ready, "initial ClientHello")
				if mode == "cancel" {
					cancel()
				}
				requireTLSDialError(t, result, want)
				eventuallyTLS(t, "silent peer connection closure", func() bool {
					peer.mu.Lock()
					defer peer.mu.Unlock()
					return len(peer.conns) == 0
				})
			})
		}
	}
}

// Cancel just the socket lifetime before closeAll closes raw sockets. The handshake
// must still see that cancellation after sockets.dial has returned, through the DoT
// setup bridge; otherwise a raw close can conceal a prematurely detached callback.
func TestDoTSetupLifetimeContext(t *testing.T) {
	for _, variant := range []string{"none", "Firefox_120"} {
		t.Run(variant, func(t *testing.T) {
			ready := make(chan struct{}, 1)
			peer := newTLSPeer(t, silentTLS(ready))
			cfg := doTConfig(t, peer.ln.Addr().String(), variant)
			s := newSockets(&cfg)
			done, result := make(chan struct{}), make(chan error, 1)
			go func() {
				defer close(done)
				_, conn, err := openTransport(context.Background(), &cfg, s, utlsLookup(variant))
				if conn != nil {
					conn.Close()
				}
				result <- err
			}()
			t.Cleanup(func() { s.closeAll(); peer.close(); waitTLS(t, done, "DoT setup completion") })
			acceptTLS(t, peer)
			waitTLS(t, ready, "DoT setup ClientHello")
			s.cancel()
			waitTLS(t, done, "socket-lifetime handshake cancellation")
			if err := <-result; !errors.Is(err, context.Canceled) {
				t.Errorf("DoT setup after socket lifetime cancellation: %v", err)
			}
			requireEmptySockets(t, s)
		})
	}
}

func eventuallyTLS(t *testing.T, what string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(tlsTestWait)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func requireEmptySockets(t *testing.T, s *sockets) {
	t.Helper()
	eventuallyTLS(t, "empty socket ownership", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.open) == 0
	})
}

func requireNoTLSLoops(t *testing.T) {
	t.Helper()
	left := func() string {
		var found []string
		for _, g := range clientGoroutines() {
			if strings.Contains(g, "core/client.newTLSPeer") || strings.Contains(g, "core/client.tlsDNSForwarder") {
				continue
			}
			found = append(found, g)
		}
		return strings.Join(found, "\n\n")
	}
	deadline := time.Now().Add(tlsTestWait)
	for stacks := left(); stacks != ""; stacks = left() {
		if time.Now().After(deadline) {
			t.Fatalf("client goroutines left after teardown:\n%s", stacks)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDoTDialLookupContext(t *testing.T) {
	for _, variant := range []string{"none", "Firefox_120"} {
		for _, mode := range []string{"precanceled", "expired", "cancel", "deadline"} {
			t.Run(variant+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				want := context.Canceled
				if mode == "expired" || mode == "deadline" {
					cancel()
					limit := 250 * time.Millisecond
					if mode == "expired" {
						limit = -time.Second
					}
					ctx, cancel = context.WithTimeout(context.Background(), limit)
					want = context.DeadlineExceeded
				} else if mode == "precanceled" {
					cancel()
				}
				t.Cleanup(cancel)
				entered, ended := make(chan struct{}), make(chan struct{})
				release := make(chan struct{})
				cfg := doTConfig(t, "dot.example:853", variant)
				cfg.LookupIP = func(ctx context.Context, host string) ([]netip.Addr, error) {
					defer close(ended)
					close(entered)
					if host != "dot.example" {
						t.Errorf("LookupIP(%q)", host)
					}
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-release:
						return nil, errors.New("fixture stopped")
					}
				}
				var controls atomic.Int32
				cfg.Control = func(string, string, syscall.RawConn) error { controls.Add(1); return nil }
				result := startTLSDial(t, ctx, cfg, func() { cancel(); close(release) })
				waitTLS(t, entered, "LookupIP entry")
				if mode == "cancel" {
					cancel()
				}
				requireTLSDialError(t, result, want)
				waitTLS(t, ended, "LookupIP completion")
				if controls.Load() != 0 {
					t.Error("a canceled lookup opened a socket")
				}
				requireNoTLSLoops(t)
			})
		}
	}
}

// A closed caller context must also reach the literal-address TCP path, which never
// calls LookupIP. No external unreachable address or long TCP timeout is involved.
func TestDoTDialCanceledTCP(t *testing.T) {
	for _, variant := range []string{"none", "Firefox_120"} {
		for _, expired := range []bool{false, true} {
			t.Run(variant+"/expired="+strconv.FormatBool(expired), func(t *testing.T) {
				peer := newTLSPeer(t, func(c net.Conn) { io.Copy(io.Discard, c) })
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
				result := startTLSDial(t, ctx, doTConfig(t, peer.ln.Addr().String(), variant), peer.close)
				requireTLSDialError(t, result, want)
				requireNoTLSLoops(t)
			})
		}
	}
}

func TestTLSInitialAndLegacyDeadlines(t *testing.T) {
	deadline := time.Now().Add(2 * dialTimeout)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	wantErr := errors.New("injected dial failure")
	for _, legacy := range []bool{false, true} {
		t.Run("legacy="+strconv.FormatBool(legacy), func(t *testing.T) {
			called := false
			callback := func(got context.Context, network, addr string) (net.Conn, error) {
				called = true
				limit, ok := got.Deadline()
				if !ok || network != "tcp" || addr != "dot.example:853" {
					t.Errorf("callback: deadline=%v network=%q addr=%q", ok, network, addr)
				}
				if legacy {
					if left := time.Until(limit); left > dialTimeout || left < dialTimeout-time.Second {
						t.Errorf("legacy timeout %v, want %v", left, dialTimeout)
					}
				} else if got != ctx || !limit.Equal(deadline) {
					t.Errorf("initial callback did not receive the caller's unchanged context/deadline")
				}
				return nil, wantErr
			}
			var err error
			if legacy {
				_, err = NewTLSPacketConn("dot.example:853", callback, func(string, ...any) {})
			} else {
				_, err = newTLSPacketConn(ctx, "dot.example:853", callback, func(string, ...any) {})
			}
			if !called || !errors.Is(err, wantErr) {
				t.Errorf("called=%v, error=%v", called, err)
			}
		})
	}
}

func TestTLSInitialCallbackContext(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			want := context.Canceled
			if mode == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 250*time.Millisecond)
				want = context.DeadlineExceeded
			}
			defer cancel()
			entered, done, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			result := make(chan error, 1)
			go func() {
				defer close(done)
				_, err := newTLSPacketConn(ctx, "dot.example:853", func(ctx context.Context, _, _ string) (net.Conn, error) {
					close(entered)
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-release:
						return nil, errors.New("fixture stopped")
					}
				}, func(string, ...any) {})
				result <- err
			}()
			t.Cleanup(func() { cancel(); close(release); waitTLS(t, done, "initial callback completion") })
			waitTLS(t, entered, "initial TCP callback")
			if mode == "cancel" {
				cancel()
			}
			waitTLS(t, done, "initial callback cancellation")
			if err := <-result; !errors.Is(err, want) {
				t.Errorf("initial callback: %v, want %v", err, want)
			}
		})
	}
}

func TestDoTInitialDeadlineThroughLookup(t *testing.T) {
	for _, variant := range []string{"none", "Firefox_120"} {
		t.Run(variant, func(t *testing.T) {
			deadline := time.Now().Add(2 * dialTimeout)
			ctx, cancel := context.WithDeadline(context.Background(), deadline)
			defer cancel()
			cfg := doTConfig(t, "dot.example:853", variant)
			want := errors.New("injected lookup failure")
			called := false
			cfg.LookupIP = func(got context.Context, _ string) ([]netip.Addr, error) {
				called = true
				if limit, ok := got.Deadline(); !ok || !limit.Equal(deadline) {
					t.Errorf("initial lookup deadline %v (%v), want %v", limit, ok, deadline)
				}
				return nil, want
			}
			if s, err := Dial(ctx, cfg); !called || !errors.Is(err, want) {
				if s != nil {
					s.Close()
				}
				t.Errorf("called=%v Dial=%v", called, err)
			}
			requireNoTLSLoops(t)
		})
	}
}

// The generated CA is trusted only by explicit root pools, or by the isolated full-Dial
// child process below. No system trust, production config or public hook is changed.
func tlsIdentity(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err = x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2), DNSNames: []string{"dot.example", "doh.example"},
		NotBefore: ca.NotBefore, NotAfter: ca.NotAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca, &leafKey.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	return tls.Certificate{Certificate: [][]byte{der, caDER}, PrivateKey: leafKey}, roots
}

// tlsDNSForwarder forwards each length-prefixed DNS query over a private loopback UDP
// socket. Reading queries and waiting for answers run independently so held polls do
// not serialize the dnstt stream. The peer owns and joins all forwarders on every exit.
func tlsDNSForwarder(t *testing.T, srv *server.TestServer, cert tls.Certificate, silent *atomic.Bool, hellos chan<- string, ready chan<- struct{}) *tlsPeer {
	t.Helper()
	return newTLSPeer(t, func(raw net.Conn) {
		if silent.Load() {
			silentTLS(ready)(raw)
			return
		}
		conn := tls.Server(raw, &tls.Config{
			Certificates: []tls.Certificate{cert},
			GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
				select {
				case hellos <- hello.ServerName:
				default:
				}
				return nil, nil
			},
		})
		if conn.Handshake() != nil {
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		defer wg.Wait()
		defer cancel()
		var writes sync.Mutex
		for {
			var n uint16
			if binary.Read(conn, binary.BigEndian, &n) != nil {
				return
			}
			query := make([]byte, n)
			if _, err := io.ReadFull(conn, query); err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				var d net.Dialer
				udp, err := d.DialContext(ctx, "udp", srv.Addr.String())
				if err != nil {
					return
				}
				defer udp.Close()
				stop := context.AfterFunc(ctx, func() { udp.Close() })
				defer stop()
				udp.SetDeadline(time.Now().Add(tlsTestWait))
				if _, err := udp.Write(query); err != nil {
					return
				}
				answer := make([]byte, 65535)
				n, err := udp.Read(answer)
				if err != nil {
					return
				}
				writes.Lock()
				defer writes.Unlock()
				if binary.Write(conn, binary.BigEndian, uint16(n)) == nil {
					conn.Write(answer[:n])
				}
			}()
		}
	})
}

func TestDoTSessionContext(t *testing.T) {
	if os.Getenv("DNSTT_CLI_TEST_DOT_CONTEXT_CHILD") != "1" {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, exe, "-test.run=^TestDoTSessionContext$", "-test.v", "-test.timeout=90s")
		cmd.Env = append(os.Environ(), "DNSTT_CLI_TEST_DOT_CONTEXT_CHILD=1", "GODEBUG=x509usefallbackroots=1")
		output, err := cmd.CombinedOutput()
		t.Logf("isolated full-Dial fixture:\n%s", output)
		if err != nil {
			t.Fatalf("full-Dial child: %v", err)
		}
		return
	}
	cert, roots := tlsIdentity(t)
	untrusted, _ := tlsIdentity(t)
	x509.SetFallbackRoots(roots)
	for _, variant := range []string{"none", "Firefox_120"} {
		for _, mode := range []string{"redial", "lookup", "tcp", "handshake", "untrusted", "wrongname"} {
			t.Run(variant+"/"+mode, func(t *testing.T) {
				shortRedial(t)
				srv := startServer(t)
				var silent atomic.Bool
				hellos := make(chan string, 16)
				ready := make(chan struct{}, 1)
				identity := cert
				if mode == "untrusted" {
					identity = untrusted
				}
				peer := tlsDNSForwarder(t, srv, identity, &silent, hellos, ready)
				_, port, _ := net.SplitHostPort(peer.ln.Addr().String())
				hostname := "dot.example"
				if mode == "wrongname" {
					hostname = "wrong.example"
				}
				cfg := doTConfig(t, hostname+":"+port, variant)
				cfg.PubKey = srv.PubKey
				var lookups, controls atomic.Int32
				var pending atomic.Bool
				entered, ended, hookRelease := make(chan struct{}), make(chan struct{}), make(chan struct{})
				cfg.LookupIP = func(ctx context.Context, host string) ([]netip.Addr, error) {
					lookups.Add(1)
					if host != hostname {
						t.Errorf("LookupIP(%q)", host)
					}
					if mode == "lookup" && pending.Load() {
						close(entered)
						<-ctx.Done()
						close(ended)
						return nil, ctx.Err()
					}
					return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
				}
				cfg.Control = func(network, address string, _ syscall.RawConn) error {
					controls.Add(1)
					if network != "tcp4" || address != peer.ln.Addr().String() {
						t.Errorf("Control(%q, %q)", network, address)
					}
					if mode == "tcp" && pending.Load() {
						close(entered)
						// Control has no context parameter. Teardown still owns the TCP
						// result when the hook returns, even if closure wins the race.
						<-hookRelease
						close(ended)
					}
					return nil
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				t.Cleanup(cancel)
				result := startTLSDial(t, ctx, cfg, func() { cancel(); peer.close() })
				if mode == "untrusted" || mode == "wrongname" {
					var r tlsDialResult
					select {
					case r = <-result:
					case <-time.After(tlsTestWait):
						t.Fatal("Dial did not reject invalid certificate")
					}
					if r.s != nil {
						r.s.Close()
						t.Error("Dial accepted an invalid certificate")
					}
					var unknown x509.UnknownAuthorityError
					var wrong x509.HostnameError
					if mode == "untrusted" && !errors.As(r.err, &unknown) || mode == "wrongname" && !errors.As(r.err, &wrong) {
						t.Errorf("Dial certificate error: %v", r.err)
					}
					select {
					case name := <-hellos:
						if name != hostname {
							t.Errorf("invalid-certificate SNI %q, want %q", name, hostname)
						}
					case <-time.After(tlsTestWait):
						t.Fatal("invalid-certificate peer saw no ClientHello")
					}
					requireNoTLSLoops(t)
					return
				}
				var s *Session
				select {
				case r := <-result:
					if r.err != nil {
						t.Fatal(r.err)
					}
					s = r.s
				case <-time.After(12 * time.Second):
					t.Fatal("full DoT/Noise Dial did not finish")
				}
				// Release a blocked hook before fallback session cleanup on assertion failure.
				var release sync.Once
				t.Cleanup(func() { release.Do(func() { close(hookRelease) }); s.Close() })
				cancel()
				if err := echoTLS(s, bytes.Repeat([]byte("after canceled dial context"), 40)); err != nil {
					t.Fatal(err)
				}
				acceptTLS(t, peer)
				select {
				case name := <-hellos:
					if name != "dot.example" {
						t.Errorf("SNI %q", name)
					}
				default:
					t.Fatal("no TLS ClientHello observed")
				}
				pending.Store(true)
				silent.Store(mode == "handshake")
				peer.drop()
				if mode == "redial" {
					if err := echoTLS(s, []byte("after transport redial")); err != nil {
						t.Fatal(err)
					}
					acceptTLS(t, peer)
					if lookups.Load() < 2 || controls.Load() < 2 {
						t.Errorf("redial bypassed hooks: lookups=%d controls=%d", lookups.Load(), controls.Load())
					}
				} else if mode == "handshake" {
					acceptTLS(t, peer)
					waitTLS(t, ready, "silent redial ClientHello")
				} else {
					waitTLS(t, entered, "pending redial "+mode)
				}
				closed := make(chan struct{})
				go func() { s.Close(); close(closed) }()
				if mode == "tcp" {
					waitTLS(t, s.sockets.ctx.Done(), "socket lifetime cancellation")
					release.Do(func() { close(hookRelease) })
				}
				waitTLS(t, closed, "session Close")
				waitTLS(t, s.Done(), "session Done")
				if mode == "lookup" {
					waitTLS(t, ended, "canceled redial lookup")
				}
				requireEmptySockets(t, s.sockets)
				requireNoTLSLoops(t)
				n, c := lookups.Load(), controls.Load()
				time.Sleep(3 * redialMaxDelay)
				if lookups.Load() != n || controls.Load() != c {
					t.Fatal("redial continued after session closure")
				}
			})
		}
	}
}

func echoTLS(s *Session, msg []byte) error {
	stream, err := s.OpenStream()
	if err != nil {
		return err
	}
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(tlsTestWait))
	if _, err := stream.Write(msg); err != nil {
		return err
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(stream, got); err != nil {
		return err
	}
	if !bytes.Equal(got, msg) {
		return errors.New("stream data changed")
	}
	return nil
}

func TestDoTCertificateVerification(t *testing.T) {
	cert, roots := tlsIdentity(t)
	for _, variant := range []string{"none", "Firefox_120"} {
		for _, mode := range []string{"trusted", "untrusted", "wrongname"} {
			t.Run(variant+"/"+mode, func(t *testing.T) {
				hellos := make(chan string, 1)
				peer := newTLSPeer(t, func(raw net.Conn) {
					conn := tls.Server(raw, &tls.Config{Certificates: []tls.Certificate{cert}, GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
						hellos <- h.ServerName
						return nil, nil
					}})
					if conn.Handshake() == nil {
						io.Copy(conn, conn)
					}
				})
				cfg := Config{LookupIP: func(context.Context, string) ([]netip.Addr, error) {
					return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
				}}
				s := newSockets(&cfg)
				t.Cleanup(s.closeAll)
				host := "dot.example"
				if mode == "wrongname" {
					host = "wrong.example"
				}
				_, port, _ := net.SplitHostPort(peer.ln.Addr().String())
				pool := roots
				if mode == "untrusted" {
					pool = x509.NewCertPool()
				}
				ctx, cancel := context.WithTimeout(context.Background(), tlsTestWait)
				defer cancel()
				var conn net.Conn
				var err error
				if variant == "none" {
					var raw net.Conn
					raw, err = s.dial(ctx, "tcp", net.JoinHostPort(host, port))
					if err == nil {
						tlsConn := tls.Client(raw, &tls.Config{ServerName: host, RootCAs: pool})
						conn, err = tlsConn, tlsConn.HandshakeContext(ctx)
					}
				} else {
					var uconn *utls.UConn
					uconn, err = utlsDialContext(ctx, "tcp", net.JoinHostPort(host, port), &utls.Config{RootCAs: pool}, utlsLookup(variant), s.dial)
					if uconn != nil {
						conn = uconn
					}
				}
				if conn != nil {
					conn.Close()
				}
				if mode == "trusted" && err != nil || mode != "trusted" && err == nil {
					t.Errorf("certificate result: %v", err)
				}
				if mode == "untrusted" {
					var unknown x509.UnknownAuthorityError
					if !errors.As(err, &unknown) {
						t.Errorf("untrusted certificate error: %v", err)
					}
				}
				if mode == "wrongname" {
					var wrong x509.HostnameError
					if !errors.As(err, &wrong) {
						t.Errorf("wrong-name certificate error: %v", err)
					}
				}
				select {
				case name := <-hellos:
					if name != host {
						t.Errorf("SNI %q, want %q", name, host)
					}
				case <-time.After(tlsTestWait):
					t.Fatal("no ClientHello")
				}
				requireEmptySockets(t, s)
			})
		}
	}
}
