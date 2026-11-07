package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FreedomCats/dnstt-cli/core/dns"
	"github.com/FreedomCats/dnstt-cli/core/server"
	"github.com/FreedomCats/dnstt-cli/internal/app"
	"github.com/FreedomCats/dnstt-cli/internal/config"
	"github.com/FreedomCats/dnstt-cli/internal/datapath"
	"github.com/FreedomCats/dnstt-cli/internal/exit"
	"github.com/FreedomCats/dnstt-cli/internal/supervisor"
)

func TestConnectHTTPProfile(t *testing.T) {
	for _, wrong := range []bool{false, true} {
		t.Run(fmt.Sprint("wrong password ", wrong), func(t *testing.T) {
			path := isolatedConnectConfig(t)
			upstream, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { upstream.Close() })
			const user, password = "synthetic-user", "synthetic-password"
			token := "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
			requests := make(chan *http.Request, 10)
			var accepted atomic.Int32
			go func() {
				for {
					c, err := upstream.Accept()
					if err != nil {
						return
					}
					accepted.Add(1)
					go func() {
						defer c.Close()
						c.SetDeadline(time.Now().Add(20 * time.Second))
						r := bufio.NewReader(c)
						req, err := http.ReadRequest(r)
						if err != nil {
							return
						}
						requests <- req
						if req.Header.Get("Proxy-Authorization") != token {
							io.WriteString(c, "HTTP/1.1 407 private-reason\r\nProxy-Authenticate: Basic realm=\"private-challenge\"\r\n\r\nprivate-body")
							return
						}
						if _, err := io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
							return
						}
						io.Copy(c, r)
					}()
				}
			}()
			domain, err := dns.ParseName("t.example.com")
			if err != nil {
				t.Fatal(err)
			}
			srv, err := server.StartTest(domain, upstream.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { srv.Close() })
			secret := password
			if wrong {
				secret = "synthetic-wrong-password"
			}
			ref, err := config.NewSecretStoreFor(path).Put("home.password", secret)
			if err != nil {
				t.Fatal(err)
			}
			p := &config.Profile{
				Domain: domain.String(), Pubkey: fmt.Sprintf("%x", srv.PubKey),
				Resolvers: []string{"udp:" + srv.Addr.String()}, Datapath: "socks",
				Exit:  config.Exit{Type: "http", User: user, PasswordRef: ref},
				Socks: config.Socks{Listen: "127.0.0.1:0"},
			}
			if err := config.SaveProfilesTo(path, &config.Profiles{DefaultProfile: "home", Profile: map[string]*config.Profile{"home": p}}); err != nil {
				t.Fatal(err)
			}
			output := &connectOutput{lines: make(chan string, 100)}
			e := &env{stdout: io.Discard, stderr: output, stdin: strings.NewReader("")}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			done := make(chan int, 1)
			go func() { done <- run(ctx, e, []string{"connect", "-v"}) }()
			defer func() { cancel(); waitConnectResult(t, done) }()
			var address string
			for address == "" {
				select {
				case line := <-output.lines:
					if after, ok := strings.CutPrefix(line, "[SOCKS] listening on "); ok {
						address = strings.TrimSpace(after)
					}
				case <-ctx.Done():
					t.Fatal("connect did not open its local listener")
				}
			}
			var c net.Conn
			var reply byte
			for {
				c, reply = connectSOCKSRequest(t, address)
				if reply != 3 {
					break
				}
				c.Close()
				select {
				case <-time.After(10 * time.Millisecond):
				case <-ctx.Done():
					t.Fatal("tunnel did not become ready")
				}
			}
			defer c.Close()
			if wrong {
				if reply != 1 {
					t.Fatalf("407 mapped to SOCKS5 code %d, want 1", reply)
				}
				if code := waitConnectResult(t, done); code != exitError {
					t.Fatalf("connect exit = %d, want %d", code, exitError)
				}
				if !strings.Contains(output.String(), "the HTTP exit refused authentication (status 407)") {
					t.Fatal("command did not report the fixed login failure")
				}
			} else {
				if reply != 0 {
					t.Fatalf("CONNECT returned SOCKS5 code %d", reply)
				}
				msg := []byte("CLI application through HTTP exit")
				if _, err := c.Write(msg); err != nil {
					t.Fatal(err)
				}
				got := make([]byte, len(msg))
				if _, err := io.ReadFull(c, got); err != nil || !bytes.Equal(got, msg) {
					t.Fatalf("application echo = %q, %v", got, err)
				}
				cancel()
				if code := waitConnectResult(t, done); code != exitOK {
					t.Fatalf("connect exit = %d, want %d", code, exitOK)
				}
			}
			if accepted.Load() != 1 {
				t.Fatalf("HTTP exit streams = %d, want the one application request", accepted.Load())
			}
			req := <-requests
			if req.Host != "echo.example:7" || req.RequestURI != req.Host || req.Method != "CONNECT" {
				t.Fatal("the command did not preserve the application target")
			}
			for _, private := range []string{user, password, secret, token, ref, "private-", path} {
				if strings.Contains(output.String(), private) {
					t.Fatalf("command diagnostics exposed %q", private)
				}
			}
		})
	}
}

func TestConnectSSHDeferred(t *testing.T) {
	path := isolatedConnectConfig(t)
	p := &config.Profile{Domain: "t.example.com", Pubkey: strings.Repeat("01", 32), Exit: config.Exit{Type: "ssh"}}
	if err := config.SaveProfilesTo(path, &config.Profiles{DefaultProfile: "home", Profile: map[string]*config.Profile{"home": p}}); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runCLI(t, "connect")
	if code != exitError || !strings.Contains(stderr, "SSH exit is not implemented yet (stage E5)") {
		t.Fatalf("SSH command = %d, %q", code, stderr)
	}
}

func TestFinishConnectOnlyDrainsHTTPLogin(t *testing.T) {
	for _, kind := range []string{"http", "socks5", ""} {
		for _, cause := range []error{nil, context.Canceled, &exit.LoginError{}, &exit.HTTPConnectError{StatusCode: 403}} {
			t.Run(fmt.Sprintf("%s/%T", kind, cause), func(t *testing.T) {
				calls := 0
				dp := &connectShutdown{shutdown: func(ctx context.Context) error {
					calls++
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) > supervisor.DefaultDrainTimeout || time.Until(deadline) < supervisor.DefaultDrainTimeout-time.Second {
						t.Fatal("HTTP cleanup did not use the existing drain limit")
					}
					return errors.New("cleanup must not replace the fatal cause")
				}}
				if err := finishConnect(cause, kind, dp, make(chan struct{})); err != cause {
					t.Fatal("cleanup replaced Run's result")
				}
				want := 0
				if kind == "http" && exit.IsLogin(cause) {
					want = 1
				}
				if calls != want {
					t.Fatalf("Shutdown calls = %d, want %d", calls, want)
				}
			})
		}
	}
}

func TestFinishConnectAbortForcesShutdown(t *testing.T) {
	entered := make(chan struct{})
	dp := &connectShutdown{shutdown: func(ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}}
	aborted := make(chan struct{})
	want := &exit.LoginError{}
	done := make(chan error, 1)
	go func() { done <- finishConnect(want, "http", dp, aborted) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("HTTP cleanup did not start")
	}
	close(aborted)
	select {
	case err := <-done:
		if err != want {
			t.Fatal("abort replaced the first fatal cause")
		}
	case <-time.After(time.Second):
		t.Fatal("abort did not cancel HTTP cleanup")
	}
}

type connectShutdown struct {
	shutdown func(context.Context) error
}

func (d *connectShutdown) Close() error                       { return nil }
func (d *connectShutdown) Shutdown(ctx context.Context) error { return d.shutdown(ctx) }

func isolatedConnectConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("SUDO_USER", "")
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("APPDATA", dir)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	if relative, err := filepath.Rel(dir, path); err != nil || strings.HasPrefix(relative, "..") || filepath.IsAbs(relative) {
		t.Fatal("config path escaped the isolated directory")
	}
	return path
}

type connectOutput struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	lines chan string
}

func (w *connectOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	select {
	case w.lines <- string(p):
	default:
	}
	return n, err
}

func (w *connectOutput) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func waitConnectResult(t *testing.T, done chan int) int {
	t.Helper()
	select {
	case code := <-done:
		done <- code
		return code
	case <-time.After(10 * time.Second):
		t.Fatal("connect did not finish")
		return -1
	}
}

func connectSOCKSRequest(t *testing.T, address string) (net.Conn, byte) {
	t.Helper()
	c, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(20 * time.Second))
	if _, err := c.Write([]byte{5, 1, 0}); err != nil {
		c.Close()
		t.Fatal(err)
	}
	var choice [2]byte
	if _, err := io.ReadFull(c, choice[:]); err != nil || choice != [2]byte{5, 0} {
		c.Close()
		t.Fatalf("local greeting = %v, %v", choice, err)
	}
	request := append([]byte{5, 1, 0, 3, byte(len("echo.example"))}, []byte("echo.example\x00\x07")...)
	if _, err := c.Write(request); err != nil {
		c.Close()
		t.Fatal(err)
	}
	var reply [10]byte
	if _, err := io.ReadFull(c, reply[:]); err != nil || reply[0] != 5 {
		c.Close()
		t.Fatalf("local reply = %v, %v", reply, err)
	}
	return c, reply[1]
}

// TestConnectWithInProcessServer tests that connect actually passes data through the tunnel.
// It uses the full chain: echo server → SOCKS5 server → dnstt server → our client.
func TestConnectWithInProcessServer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Start an echo server that will be behind the SOCKS5 server.
	echoAddr := startEchoServer(t)

	// Start a minimal SOCKS5 server (no auth) that routes to the echo server.
	socksAddr := startSOCKS5Server(t, echoAddr)

	// Start the in-process dnstt server with the SOCKS5 server as upstream.
	domain, err := dns.ParseName("t.example.com")
	if err != nil {
		t.Fatal(err)
	}
	ts, err := server.StartTest(domain, socksAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer ts.Close()

	// Create a profile that connects to the test server.
	prof := &config.Profile{
		Domain:     domain.String(),
		Pubkey:     fmt.Sprintf("%x", ts.PubKey),
		Resolvers:  []string{fmt.Sprintf("udp:%s", ts.Addr.String())},
		Datapath:   "socks",
		KillSwitch: false,
		Exit: config.Exit{
			Type: "socks5",
		},
		Socks: config.Socks{
			Listen: "127.0.0.1:0", // Random port
		},
	}

	// Build the tunnel.
	tunnel, err := app.BuildTunnel(app.TunnelOptions{
		Profile:     prof,
		ProfileName: "test",
		Logf:        t.Logf,
	})
	if err != nil {
		t.Fatalf("BuildTunnel failed: %v", err)
	}

	// Build the datapath.
	dp, err := app.BuildDatapath(tunnel, prof, t.Logf)
	if err != nil {
		t.Fatalf("BuildDatapath failed: %v", err)
	}
	defer dp.Close()

	// Get the SOCKS5 listen address from the datapath.
	socks, ok := dp.(*datapath.Socks)
	if !ok {
		t.Fatal("datapath is not *datapath.Socks")
	}
	socksListenAddr := socks.ListenAddr().String()

	// Run the tunnel in a goroutine.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- tunnel.Supervisor.Run(ctx)
	}()

	// Wait for the tunnel to connect.
	time.Sleep(3 * time.Second)

	// Now try to send data through the SOCKS5 proxy to the echo server.
	conn, err := net.Dial("tcp", socksListenAddr)
	if err != nil {
		t.Fatalf("Failed to connect to SOCKS5 proxy: %v", err)
	}
	defer conn.Close()

	// Send a simple SOCKS5 handshake (no auth) and CONNECT to 127.0.0.1:<echo port>
	// SOCKS5 greeting: version 5, 1 method, method 0 (no auth)
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		t.Fatalf("SOCKS5 greeting write failed: %v", err)
	}

	// Read choice
	choice := make([]byte, 2)
	if _, err := io.ReadFull(conn, choice); err != nil {
		t.Fatalf("SOCKS5 choice read failed: %v", err)
	}
	if choice[0] != 5 || choice[1] != 0 {
		t.Fatalf("SOCKS5 choice unexpected: %v", choice)
	}

	// CONNECT request to the echo server
	host, portStr, _ := net.SplitHostPort(echoAddr)
	port := 0
	fmt.Sscanf(portStr, "%d", &port)

	// Build CONNECT: version 5, CONNECT (1), reserved (0), IPv4 (1), address, port
	req := []byte{5, 1, 0, 1}
	ip := net.ParseIP(host).To4()
	req = append(req, ip...)
	req = append(req, byte(port>>8), byte(port))

	if _, err := conn.Write(req); err != nil {
		t.Fatalf("SOCKS5 CONNECT write failed: %v", err)
	}

	// Read reply
	reply := make([]byte, 10) // Enough for IPv4 reply
	if _, err := io.ReadFull(conn, reply[:4]); err != nil {
		t.Fatalf("SOCKS5 reply read failed: %v", err)
	}
	if reply[0] != 5 || reply[1] != 0 {
		t.Fatalf("SOCKS5 CONNECT failed: reply %v", reply[:4])
	}

	// Read the bound address (IPv4 + port = 6 bytes)
	if _, err := io.ReadFull(conn, reply[:6]); err != nil {
		t.Fatalf("SOCKS5 bound address read failed: %v", err)
	}

	// Now we're connected through the tunnel to the echo server.
	// Send some data and verify it echoes back.
	testData := []byte("Hello through dnstt tunnel!")
	if _, err := conn.Write(testData); err != nil {
		t.Fatalf("Write to echo server failed: %v", err)
	}

	received := make([]byte, len(testData))
	if _, err := io.ReadFull(conn, received); err != nil {
		t.Fatalf("Read from echo server failed: %v", err)
	}

	if !bytes.Equal(testData, received) {
		t.Fatalf("Echo mismatch: sent %q, got %q", testData, received)
	}

	t.Logf("Successfully passed data through the tunnel!")

	// Cancel the context to stop the tunnel.
	cancel()

	// Wait for the tunnel to finish.
	select {
	case err := <-done:
		if err != nil && err != context.Canceled {
			t.Logf("Tunnel returned error (may be expected): %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("tunnel did not stop after context cancellation")
	}
}

// startEchoServer starts a TCP echo server and returns its address.
func startEchoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				io.Copy(conn, conn)
			}()
		}
	}()

	return ln.Addr().String()
}

// startSOCKS5Server starts a minimal SOCKS5 server (no auth) that forwards to targetAddr.
func startSOCKS5Server(t *testing.T, targetAddr string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleSOCKS5(conn, targetAddr)
		}
	}()

	return ln.Addr().String()
}

// handleSOCKS5 handles a single SOCKS5 connection (minimal implementation, no auth).
func handleSOCKS5(client net.Conn, targetAddr string) {
	defer client.Close()

	// Read greeting
	buf := make([]byte, 257)
	n, err := client.Read(buf)
	if err != nil || n < 2 || buf[0] != 5 {
		return
	}

	// Send choice: no auth (0)
	if _, err := client.Write([]byte{5, 0}); err != nil {
		return
	}

	// Read request
	n, err = client.Read(buf)
	if err != nil || n < 7 || buf[0] != 5 || buf[1] != 1 {
		return
	}

	// Connect to target (ignore the requested address, always connect to targetAddr)
	target, err := net.Dial("tcp", targetAddr)
	if err != nil {
		// Send failure reply
		client.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer target.Close()

	// Send success reply
	if _, err := client.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}

	// Relay data
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(target, client)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(client, target)
		done <- struct{}{}
	}()
	<-done
}
