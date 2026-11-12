package app

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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FreedomCats/dnstt-cli/core/dns"
	"github.com/FreedomCats/dnstt-cli/core/server"
	"github.com/FreedomCats/dnstt-cli/internal/config"
	"github.com/FreedomCats/dnstt-cli/internal/datapath"
	"github.com/FreedomCats/dnstt-cli/internal/exit"
	"github.com/FreedomCats/dnstt-cli/internal/supervisor"
	"golang.org/x/net/proxy"
)

func TestBuildHTTPExitCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, user, ref, password, want string
		lookupErr                       error
		lookups                         int
	}{
		{name: "minimal"},
		{name: "empty password", user: "synthetic-user"},
		{name: "secret", user: "synthetic-user", ref: "file:synthetic-id", password: "synthetic-password", lookups: 1},
		{name: "colon", user: "private:user", want: "invalid user"},
		{name: "control", user: "private\nuser", want: "invalid user"},
		{name: "reference without user", ref: "file:private-id", want: "password_ref requires a user"},
		{name: "invalid password", user: "synthetic-user", ref: "file:synthetic-id", password: "private\npassword", lookups: 1, want: "invalid password"},
		{name: "missing secret", user: "synthetic-user", ref: "file:private-id", lookups: 1, lookupErr: errors.New("private reference file:private-id not found in private-store"), want: "cannot read password from the secret store"},
		{name: "foreign secret", user: "synthetic-user", ref: "foreign:private-id", lookups: 1, lookupErr: errors.New("private foreign scheme and payload"), want: "cannot read password from the secret store"},
		{name: "unreadable secret", user: "synthetic-user", ref: "file:private-id", lookups: 1, lookupErr: errors.New("private-store: private payload"), want: "cannot read password from the secret store"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := testProfile(config.Exit{Type: "http", User: tc.user, PasswordRef: tc.ref})
			calls := 0
			tun, err := buildTunnel(TunnelOptions{Profile: p}, func(ref string) (string, error) {
				calls++
				if ref != tc.ref {
					t.Error("lookup used a different reference")
				}
				return tc.password, tc.lookupErr
			})
			if calls != tc.lookups {
				t.Fatalf("secret lookups = %d, want %d", calls, tc.lookups)
			}
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) || tun != nil {
					t.Fatalf("BuildTunnel = %v, %v", tun, err)
				}
				for _, text := range []string{fmt.Sprint(err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
					if strings.Contains(text, "private") {
						t.Fatalf("construction leaked private input: %s", text)
					}
				}
				return
			}
			if err != nil || tun.Supervisor.State() != supervisor.StateIdle {
				t.Fatalf("BuildTunnel = %v, %v", tun, err)
			}
			h := tun.Exit.(*fatalExit).dialer.(*exit.HTTP)
			if h.User != tc.user || h.Password != tc.password || h.Open == nil {
				t.Error("HTTP credentials or opener were not bound at construction")
			}
		})
	}
}

func testProfile(ex config.Exit) *config.Profile {
	return &config.Profile{
		Domain: "t.example.com", Pubkey: strings.Repeat("01", 32),
		Resolvers: []string{"udp:192.0.2.53:53"}, Exit: ex,
		Socks: config.Socks{Listen: "127.0.0.1:0"},
	}
}

func TestBuildOtherExitContracts(t *testing.T) {
	for _, kind := range []string{"", "socks5"} {
		t.Run("SOCKS5 "+kind, func(t *testing.T) {
			p := testProfile(config.Exit{Type: kind, User: "user:\r", PasswordRef: "file:synthetic-id"})
			want := errors.New("synthetic store failure")
			if _, err := buildTunnel(TunnelOptions{Profile: p}, func(string) (string, error) { return "", want }); !errors.Is(err, want) {
				t.Fatalf("SOCKS5 lookup changed: %v", err)
			}
			tun, err := buildTunnel(TunnelOptions{Profile: p}, func(string) (string, error) { return "synthetic-password", nil })
			if err != nil {
				t.Fatal(err)
			}
			s5 := tun.Exit.(*exit.SOCKS5)
			if s5.Open == nil || s5.User != p.Exit.User {
				t.Fatal("SOCKS5 opener or credentials changed")
			}
			// BuildDatapath must not replace an already bound exit opener.
			sentinel := errors.New("immutable opener")
			s5.Open = func(context.Context) (net.Conn, error) { return nil, sentinel }
			dp, err := BuildDatapath(tun, p, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer dp.Close()
			if _, err := s5.Dial(context.Background(), "echo.example:7"); err != sentinel {
				t.Fatalf("BuildDatapath replaced the opener: %v", err)
			}
		})
	}
}

// A password without a user is a profile error, not a retryable login refusal: BuildTunnel
// refuses it before the supervisor starts looping with only "reconnecting..." on screen.
func TestBuildSOCKS5PasswordNeedsUser(t *testing.T) {
	p := testProfile(config.Exit{Type: "socks5", PasswordRef: "file:synthetic-id"})
	if _, err := buildTunnel(TunnelOptions{Profile: p}, func(string) (string, error) {
		t.Error("the secret store was opened without a user")
		return "", nil
	}); err == nil || err.Error() != "SOCKS5 exit: password_ref requires a user" {
		t.Fatalf("BuildTunnel = %v, want the password_ref refusal", err)
	}

	p.Exit.User = "synthetic-user"
	tun, err := buildTunnel(TunnelOptions{Profile: p}, func(string) (string, error) {
		return "synthetic-password", nil
	})
	if err != nil {
		t.Fatalf("BuildTunnel with a user = %v", err)
	}
	if _, ok := tun.Exit.(*exit.SOCKS5); !ok {
		t.Fatalf("the socks5 profile gave %T, want *exit.SOCKS5", tun.Exit)
	}
}

func TestHTTPSecretStoreErrorsAreSafe(t *testing.T) {
	t.Setenv("SUDO_USER", "")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	for _, mode := range []string{"missing", "foreign", "unreadable", "corrupt payload"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "private-store", config.FileName)
			store := config.NewSecretStoreFor(path)
			ref, err := store.Put("home.password", "synthetic-password")
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "missing":
				if err := store.Delete(ref); err != nil {
					t.Fatal(err)
				}
			case "foreign":
				ref = "foreign:private-reference"
			case "unreadable", "corrupt payload":
				secretPath := filepath.Join(filepath.Dir(path), config.SecretsFileName)
				// Only the freshly generated fixture store is replaced.
				if _, err := os.ReadFile(secretPath); err != nil {
					t.Fatal(err)
				}
				if mode == "unreadable" {
					if err := os.Remove(secretPath); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(secretPath, 0o700); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(secretPath, []byte(`{"secrets":{"private-payload":invalid}}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			p := testProfile(config.Exit{Type: "http", User: "synthetic-user", PasswordRef: ref})
			if _, err := buildTunnel(TunnelOptions{Profile: p}, store.Get); err == nil || err.Error() != "HTTP exit: cannot read password from the secret store" {
				t.Fatalf("unsafe store error: %v", err)
			}
		})
	}
}

func TestSOCKS5StartupCheckRemainsSessionLocal(t *testing.T) {
	for _, kind := range []string{"", "socks5"} {
		for _, refuse := range []bool{false, true} {
			t.Run(fmt.Sprint(kind, refuse), func(t *testing.T) {
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { ln.Close() })
				checked := make(chan struct{})
				var once sync.Once
				go func() {
					for {
						c, err := ln.Accept()
						if err != nil {
							return
						}
						go func() {
							defer c.Close()
							c.SetDeadline(time.Now().Add(10 * time.Second))
							var greeting [3]byte
							if _, err := io.ReadFull(c, greeting[:]); err != nil || greeting != [3]byte{5, 1, 0} {
								return
							}
							method := byte(0)
							if refuse {
								method = 0xff
							}
							once.Do(func() { close(checked) })
							if _, err := c.Write([]byte{5, method}); err != nil || refuse {
								return
							}
							var request [4]byte
							if _, err := io.ReadFull(c, request[:]); err != nil {
								// The startup checker closes after greeting, without CONNECT.
								return
							}
							if request != [4]byte{5, 1, 0, 3} {
								return
							}
							var size [1]byte
							if _, err := io.ReadFull(c, size[:]); err != nil {
								return
							}
							address := make([]byte, int(size[0])+2)
							if _, err := io.ReadFull(c, address); err != nil || string(address) != "echo.example\x00\x07" {
								return
							}
							if _, err := c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
								return
							}
							io.Copy(c, c)
						}()
					}
				}()
				tun, dp, events, done, cancel := startAppTunnel(t, ln.Addr().String(), kind, "")
				if refuse {
					if err := appRunResult(t, done); !exit.IsLogin(err) {
						t.Fatalf("SOCKS5 refusal = %v", err)
					}
					for tr := range events {
						if tr.To == supervisor.StateConnected {
							t.Fatal("SOCKS5 login refusal published Connected")
						}
						if tr.To == supervisor.StateStopping {
							break
						}
					}
					return
				}
				waitAppState(t, events, supervisor.StateConnected)
				select {
				case <-checked:
				default:
					t.Fatal("SOCKS5 became Connected before the startup check")
				}
				client, err := proxyClient(dp)
				if err != nil {
					t.Fatal(err)
				}
				c, err := client.DialContext(appContext(t), "tcp", "echo.example:7")
				if err != nil {
					t.Fatal(err)
				}
				appEcho(t, c)
				c.Close()
				// Check every new session while application goroutines use the shared dialer.
				ctx := appContext(t)
				var wg sync.WaitGroup
				for range 8 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						c, err := tun.Exit.Dial(ctx, "echo.example:7")
						if err == nil {
							c.Close()
						}
					}()
				}
				tun.Supervisor.Restart(errors.New("synthetic session replacement"))
				waitAppState(t, events, supervisor.StateReconnecting)
				waitAppState(t, events, supervisor.StateConnected)
				wg.Wait()
				c, err = client.DialContext(ctx, "tcp", "echo.example:7")
				if err != nil {
					t.Fatal(err)
				}
				appEcho(t, c)
				c.Close()
				cancel()
				if err := appRunResult(t, done); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

// The profile's one_write reaches the datapath dialer only: the startup check greets
// sequentially and sends no CONNECT, while a connection through the local proxy takes the
// one-write batch to the target over the generated tunnel.
func TestSOCKS5OneWriteBinding(t *testing.T) {
	fx := newAppSOCKSExit(t, "synthetic-user", "synthetic-password")
	tun, dp, events, done, cancel := startAppTunnelWithExit(t, fx.addr,
		config.Exit{Type: "socks5", OneWrite: true}, "synthetic-password")
	if !tun.Exit.(*exit.SOCKS5).OneWrite {
		t.Fatal("the datapath dialer lost exit.one_write")
	}
	waitAppState(t, events, supervisor.StateConnected)
	// The check comes first and is the sequential one: both methods, a login, no CONNECT.
	if got := appStreamLine(t, fx.streams); got != "check: sequential, no CONNECT" {
		t.Fatalf("startup check stream: %s", got)
	}
	if got := fx.total.Load(); got != 1 {
		t.Fatalf("streams = %d after the startup check, want 1", got)
	}

	client, err := proxyClient(dp)
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.DialContext(appContext(t), "tcp", "echo.example:7")
	if err != nil {
		t.Fatal(err)
	}
	appEcho(t, c)
	c.Close()
	if got := appStreamLine(t, fx.streams); got != "datapath: connected" {
		t.Fatalf("datapath stream: %s", got)
	}
	if got := fx.total.Load(); got != 2 {
		t.Fatalf("streams = %d after the datapath connection, want 2", got)
	}
	cancel()
	if err := appRunResult(t, done); err != nil {
		t.Fatal(err)
	}
}

// An exit that reads one message per recv drops the batch: the error names exit.one_write,
// the exit sees no second stream for it, the supervisor keeps running, and the same session
// still reaches the target.
func TestSOCKS5OneWriteDroppedBatch(t *testing.T) {
	fx := newAppSOCKSExit(t, "synthetic-user", "synthetic-password")
	fx.dropBatch.Store(1)
	tun, dp, events, done, cancel := startAppTunnelWithExit(t, fx.addr,
		config.Exit{Type: "socks5", OneWrite: true}, "synthetic-password")
	waitAppState(t, events, supervisor.StateConnected)
	if got := appStreamLine(t, fx.streams); got != "check: sequential, no CONNECT" {
		t.Fatalf("startup check stream: %s", got)
	}
	if got := fx.total.Load(); got != 1 {
		t.Fatalf("streams = %d after the startup check, want 1", got)
	}

	// The exit dialer directly: the datapath would turn the error into a SOCKS5 reply code.
	_, err := tun.Exit.Dial(appContext(t), "echo.example:7")
	if err == nil || !strings.Contains(err.Error(), "exit.one_write") {
		t.Fatalf("dropped batch = %v, want an error naming exit.one_write", err)
	}
	if exit.IsLogin(err) {
		t.Fatalf("dropped batch = %v is a login error", err)
	}
	if got := appStreamLine(t, fx.streams); got != "datapath: dropped the batch" {
		t.Fatalf("dropped stream: %s", got)
	}
	// One stream for the failed batch: a sequential retry would have opened another one.
	if got := fx.total.Load(); got != 2 {
		t.Fatalf("streams = %d after the dropped batch, want 2", got)
	}
	if got := fx.batches.Load(); got != 1 {
		t.Fatalf("one-write streams = %d, want 1: the batch was retried", got)
	}
	if state := tun.Supervisor.State(); state != supervisor.StateConnected && state != supervisor.StateDegraded {
		t.Fatalf("supervisor state = %s after a dropped batch", state)
	}

	client, err := proxyClient(dp)
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.DialContext(appContext(t), "tcp", "echo.example:7")
	if err != nil {
		t.Fatalf("the session stopped serving after a dropped batch: %v", err)
	}
	appEcho(t, c)
	c.Close()
	if got := appStreamLine(t, fx.streams); got != "datapath: connected" {
		t.Fatalf("later datapath stream: %s", got)
	}
	if got := fx.total.Load(); got != 3 {
		t.Fatalf("streams = %d after the later connection, want 3", got)
	}
	if got := fx.batches.Load(); got != 2 {
		t.Fatalf("one-write streams = %d after the later connection, want 2", got)
	}
	cancel()
	if err := appRunResult(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPExitSignalsOnce(t *testing.T) {
	want := &exit.LoginError{}
	var signals atomic.Int32
	h := &fatalExit{dialer: appDialerFunc(func(context.Context, string) (net.Conn, error) {
		return nil, want
	}), fatal: func(err error) {
		if err != want {
			t.Error("fatal signal lost the error")
		}
		signals.Add(1)
	}}
	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := h.Dial(context.Background(), "echo.example:7"); err != want {
				t.Error("HTTP wrapper lost the login error")
			}
		}()
	}
	wg.Wait()
	if got := signals.Load(); got != 1 {
		t.Fatalf("fatal signals = %d, want 1", got)
	}
}

func TestHTTPApplicationCanceledAcquisition(t *testing.T) {
	tun, err := buildTunnel(TunnelOptions{Profile: testProfile(config.Exit{Type: "http"})}, func(string) (string, error) {
		t.Fatal("empty reference opened the secret store")
		return "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := tun.Exit.Dial(ctx, "echo.example:7"); !errors.Is(err, context.DeadlineExceeded) || exit.IsLogin(err) {
		t.Fatalf("pending HTTP acquisition = %v", err)
	}
	if tun.Supervisor.State() != supervisor.StateIdle {
		t.Fatal("canceled acquisition stopped the supervisor")
	}
}

type appDialerFunc func(context.Context, string) (net.Conn, error)

func (f appDialerFunc) Dial(ctx context.Context, target string) (net.Conn, error) {
	return f(ctx, target)
}

func TestHTTPApplicationThroughTunnel(t *testing.T) {
	proxy := newAppHTTPProxy(t)
	tun, dp, events, done, cancel := startAppTunnel(t, proxy.address, "http", proxy.password)
	waitAppState(t, events, supervisor.StateConnected)
	if got := proxy.accepted.Load(); got != 0 {
		t.Fatalf("HTTP readiness opened %d hidden exit streams", got)
	}
	if st := tun.Supervisor.Status(); st.State != supervisor.StateConnected || st.Stats.Streams != 0 {
		t.Fatalf("tunnel-only readiness: %+v", st)
	}
	client, err := proxyClient(dp)
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.DialContext(appContext(t), "tcp", "echo.example:7")
	if err != nil {
		t.Fatal(err)
	}
	appEcho(t, c)
	c.Close()
	first := <-proxy.requests
	if first.Method != "CONNECT" || first.Host != "echo.example:7" || first.RequestURI != first.Host || first.Header.Get("Proxy-Authorization") != proxy.token() {
		t.Fatal("the first real request did not carry the target and Basic authentication")
	}
	for _, target := range []string{"blocked.example:443", "malformed.example:443", "truncated.example:443"} {
		if _, err := client.DialContext(appContext(t), "tcp", target); err == nil || !strings.Contains(err.Error(), "general SOCKS server failure") {
			t.Fatalf("%s: %v, want a generic local failure", target, err)
		}
	}
	for _, tc := range []struct {
		target string
		check  func(error) bool
	}{
		{"blocked.example:443", func(err error) bool { var e *exit.HTTPConnectError; return errors.As(err, &e) && e.StatusCode == 403 }},
		{"malformed.example:443", func(err error) bool { return errors.Is(err, exit.ErrHTTPProtocol) }},
		{"truncated.example:443", func(err error) bool { return errors.Is(err, io.ErrUnexpectedEOF) }},
	} {
		if _, err := tun.Exit.Dial(appContext(t), tc.target); !tc.check(err) {
			t.Fatalf("%s: unexpected error %v", tc.target, err)
		} else if strings.Contains(err.Error(), "private-") {
			t.Fatalf("%s: exposed response text: %v", tc.target, err)
		}
	}
	// Cancel during CONNECT without stopping the supervisor or the next application.
	ctx, stop := context.WithCancel(appContext(t))
	result := make(chan error, 1)
	go func() { _, err := tun.Exit.Dial(ctx, "silent.example:443"); result <- err }()
	for waiting := true; waiting; {
		select {
		case req := <-proxy.requests:
			waiting = req.Host != "silent.example:443"
		case <-ctx.Done():
			t.Fatal("proxy did not receive the cancellable request")
		}
	}
	stop()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled CONNECT: %v", err)
	}
	// Concurrent callers use the immutable opener across a session replacement.
	var wg sync.WaitGroup
	concurrentCtx := appContext(t)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := tun.Exit.Dial(concurrentCtx, "echo.example:7")
			if err == nil {
				c.Close()
			}
		}()
	}
	tun.Supervisor.Restart(errors.New("synthetic session replacement"))
	waitAppState(t, events, supervisor.StateReconnecting)
	waitAppState(t, events, supervisor.StateConnected)
	wg.Wait()
	c, err = client.DialContext(appContext(t), "tcp", "echo.example:7")
	if err != nil {
		t.Fatal(err)
	}
	appEcho(t, c)
	c.Close()
	cancel()
	if err := appRunResult(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPApplicationWrongSecretStops(t *testing.T) {
	p := newAppHTTPProxy(t)
	tun, dp, events, done, _ := startAppTunnel(t, p.address, "http", "synthetic-wrong-password")
	waitAppState(t, events, supervisor.StateConnected)
	client, err := proxyClient(dp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.DialContext(appContext(t), "tcp", "echo.example:7"); err == nil || !strings.Contains(err.Error(), "general SOCKS server failure") {
		t.Fatalf("wrong password reached the local client as %v", err)
	}
	err = appRunResult(t, done)
	if !exit.IsLogin(err) || err.Error() != "the HTTP exit refused authentication (status 407)" {
		t.Fatalf("Run = %v, want the final login error", err)
	}
	tr := waitAppState(t, events, supervisor.StateStopping)
	if tr.Cause != err {
		t.Fatal("Stopping lost the fatal cause")
	}
	if tun.Supervisor.State() != supervisor.StateStopping || p.accepted.Load() != 1 {
		t.Fatal("407 did not stop without an extra exit stream")
	}
	select {
	case tr := <-events:
		t.Fatalf("transition after fatal stop: %+v", tr)
	default:
	}
	if _, err := tun.Exit.Dial(appContext(t), "echo.example:7"); !errors.Is(err, supervisor.ErrStopped) {
		t.Fatalf("new application after fatal: %v", err)
	}
}

func TestHTTPFatalReplyDuringShutdown(t *testing.T) {
	p := newAppHTTPProxy(t)
	tun, dp, events, done, _ := startAppTunnel(t, p.address, "http", "synthetic-wrong-password")
	waitAppState(t, events, supervisor.StateConnected)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	h := tun.Exit.(*fatalExit)
	fatal := h.fatal
	h.fatal = func(err error) {
		fatal(err)
		// Model preemption after the fatal signal, before Dial returns to the datapath.
		<-release
	}
	client, err := proxyClient(dp)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		c, err := client.DialContext(ctx, "tcp", "echo.example:7")
		if c != nil {
			c.Close()
		}
		result <- err
	}()
	cause := appRunResult(t, done)
	if !exit.IsLogin(cause) || waitAppState(t, events, supervisor.StateStopping).Cause != cause {
		t.Fatal("fatal shutdown lost the first login error")
	}
	if _, err := tun.Supervisor.OpenStream(ctx); !errors.Is(err, supervisor.ErrStopped) {
		t.Fatalf("stream after fatal signal = %v", err)
	}
	// CLI uses the same bounded Shutdown after Run, before its deferred immediate Close.
	cleanupCtx := &appShutdownContext{Context: ctx, started: make(chan struct{})}
	cleaned := make(chan error, 1)
	go func() { cleaned <- dp.Shutdown(cleanupCtx) }()
	select {
	case <-cleanupCtx.started:
	case <-ctx.Done():
		t.Fatal("datapath cleanup did not begin")
	}
	select {
	case err := <-cleaned:
		t.Fatalf("cleanup did not wait for the pending reply: %v", err)
	default:
	}
	unblock()
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "general SOCKS server failure") {
			t.Fatalf("local result = %v, want the generic SOCKS5 failure", err)
		}
	case <-ctx.Done():
		t.Fatal("local SOCKS5 failure did not arrive")
	}
	select {
	case err := <-cleaned:
		if err != nil {
			t.Fatalf("datapath shutdown = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("datapath cleanup did not finish")
	}
	if p.accepted.Load() != 1 || tun.Supervisor.State() != supervisor.StateStopping {
		t.Fatal("reply cleanup restarted the tunnel")
	}
}

// Done is observed only by Shutdown, after it has stopped accepting connections.
type appShutdownContext struct {
	context.Context
	started chan struct{}
	once    sync.Once
}

func (c *appShutdownContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.started) })
	return c.Context.Done()
}

// All server identities are generated by StartTest. Secret lookup is an isolated platform
// store passed to the builder, never the default config directory or the user's keyring.
func startAppTunnel(t *testing.T, upstream, kind, password string) (*Tunnel, *datapath.Socks, <-chan supervisor.Transition, chan error, context.CancelFunc) {
	t.Helper()
	return startAppTunnelWithExit(t, upstream, config.Exit{Type: kind}, password)
}

// startAppTunnelWithExit is startAppTunnel for an exit that carries more than its type: a
// test can set one_write together with the generated login.
func startAppTunnelWithExit(t *testing.T, upstream string, ex config.Exit, password string) (*Tunnel, *datapath.Socks, <-chan supervisor.Transition, chan error, context.CancelFunc) {
	t.Helper()
	domain, err := dns.ParseName("t.example.com")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := server.StartTest(domain, upstream)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	p := testProfile(ex)
	p.Pubkey = fmt.Sprintf("%x", srv.PubKey)
	p.Resolvers = []string{"udp:" + srv.Addr.String()}
	var lookup func(string) (string, error) = func(string) (string, error) {
		t.Error("empty reference opened the secret store")
		return "", errors.New("unexpected lookup")
	}
	if password != "" {
		t.Setenv("SUDO_USER", "")
		t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
		t.Setenv("XDG_RUNTIME_DIR", "")
		s := config.NewSecretStoreFor(filepath.Join(t.TempDir(), config.FileName))
		ref, err := s.Put("home.password", password)
		if err != nil {
			t.Fatal(err)
		}
		p.Exit.User, p.Exit.PasswordRef = "synthetic-user", ref
		lookup = s.Get
	}
	events := make(chan supervisor.Transition, 100)
	tun, err := buildTunnel(TunnelOptions{Profile: p, OnTransition: func(tr supervisor.Transition) { events <- tr }}, lookup)
	if err != nil {
		t.Fatal(err)
	}
	dp, err := BuildDatapath(tun, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dp.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	done := make(chan error, 1)
	go func() { done <- tun.Supervisor.Run(ctx) }()
	t.Cleanup(func() { cancel(); appRunResult(t, done) })
	return tun, dp.(*datapath.Socks), events, done, cancel
}

func appContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func appRunResult(t *testing.T, done chan error) error {
	t.Helper()
	select {
	case err := <-done:
		done <- err
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("supervisor did not finish")
		return nil
	}
}

func waitAppState(t *testing.T, events <-chan supervisor.Transition, state supervisor.State) supervisor.Transition {
	t.Helper()
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	for {
		select {
		case tr := <-events:
			if tr.To == state {
				return tr
			}
			if tr.To == supervisor.StateStopping {
				t.Fatalf("stopped while waiting for %s: %v", state, tr.Cause)
			}
		case <-timer.C:
			t.Fatalf("no %s transition", state)
		}
	}
}

func proxyClient(dp *datapath.Socks) (proxy.ContextDialer, error) {
	d, err := proxy.SOCKS5("tcp", dp.ListenAddr().String(), nil, &net.Dialer{Timeout: 5 * time.Second})
	if err != nil {
		return nil, err
	}
	return d.(proxy.ContextDialer), nil
}

func appEcho(t *testing.T, c net.Conn) {
	t.Helper()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	msg := []byte("application data through Basic HTTP CONNECT")
	if _, err := c.Write(msg); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(c, got); err != nil || !bytes.Equal(got, msg) {
		t.Fatalf("echo = %q, %v", got, err)
	}
}

// appStreamLine reads the next line of the exit fixture. A stream that never reaches the
// fixture fails the test instead of blocking it until the package timeout.
func appStreamLine(t *testing.T, lines <-chan string) string {
	t.Helper()
	select {
	case line := <-lines:
		return line
	case <-time.After(20 * time.Second):
		t.Fatal("the exit fixture saw no new stream")
		return ""
	}
}

// appSOCKSExit is the exit behind the tunneled streams: it speaks SOCKS5 with an RFC 1929
// login like the measured danted and reports what every stream did. The startup check is
// answered sequentially whatever the profile says, and a set dropBatch makes it read only
// the greeting of that many one-write batches and close, as a server that reads one message
// per recv does.
type appSOCKSExit struct {
	user, password string
	addr           string
	total          atomic.Int32 // every stream accepted, of any kind
	dropBatch      atomic.Int32 // one-write batches to drop after the greeting
	batches        atomic.Int32 // one-write streams accepted
	streams        chan string  // one line per stream, as the streams finish
}

func newAppSOCKSExit(t *testing.T, user, password string) *appSOCKSExit {
	t.Helper()
	e := &appSOCKSExit{user: user, password: password, streams: make(chan string, 16)}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	e.addr = ln.Addr().String()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go e.serve(c)
		}
	}()
	return e
}

func (e *appSOCKSExit) serve(c net.Conn) {
	e.total.Add(1)
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))

	greeting, err := readAppSOCKS5Greeting(c)
	if err != nil {
		e.streams <- "closed before the greeting"
		return
	}
	if !bytes.Equal(greeting, []byte{5, 1, 0x02}) {
		e.serveCheck(c, greeting)
		return
	}

	// The one-write batch: only 0x02 is offered, so the login and the CONNECT follow in it.
	e.batches.Add(1)
	if e.dropBatch.Load() > 0 {
		// The server that reads one message per recv: the greeting is all it takes, and it
		// closes before answering, so the rest of the batch is lost.
		e.dropBatch.Add(-1)
		e.streams <- "datapath: dropped the batch"
		return
	}
	if _, err := c.Write([]byte{5, 0x02}); err != nil {
		e.streams <- "datapath: " + err.Error()
		return
	}
	if err := e.readLogin(c); err != nil {
		e.streams <- "datapath: " + err.Error()
		return
	}
	if _, err := c.Write([]byte{1, 0}); err != nil {
		e.streams <- "datapath: " + err.Error()
		return
	}
	target, err := readAppSOCKS5Connect(c)
	if err != nil {
		e.streams <- "datapath: " + err.Error()
		return
	}
	if target != "echo.example:7" {
		e.streams <- fmt.Sprintf("datapath: CONNECT %q", target)
		return
	}
	if _, err := c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		e.streams <- "datapath: " + err.Error()
		return
	}
	e.streams <- "datapath: connected"
	io.Copy(c, c)
}

// serveCheck answers the sequential startup check: both methods offered, a login, and a
// stream closed without a CONNECT.
func (e *appSOCKSExit) serveCheck(c net.Conn, greeting []byte) {
	if !bytes.Equal(greeting, []byte{5, 2, 0, 0x02}) {
		e.streams <- fmt.Sprintf("unexpected greeting %v", greeting)
		return
	}
	if _, err := c.Write([]byte{5, 0x02}); err != nil {
		e.streams <- "check: " + err.Error()
		return
	}
	if err := e.readLogin(c); err != nil {
		e.streams <- "check: " + err.Error()
		return
	}
	if _, err := c.Write([]byte{1, 0}); err != nil {
		e.streams <- "check: " + err.Error()
		return
	}
	var next [1]byte
	_, err := c.Read(next[:])
	switch {
	case err == nil:
		e.streams <- fmt.Sprintf("check: sent %v after the login, want no CONNECT", next)
	case !errors.Is(err, io.EOF):
		e.streams <- "check: " + err.Error()
	default:
		e.streams <- "check: sequential, no CONNECT"
	}
}

// readAppSOCKS5Greeting reads a method selection message: the version, the number of
// methods and the methods.
func readAppSOCKS5Greeting(c net.Conn) ([]byte, error) {
	var head [2]byte
	if _, err := io.ReadFull(c, head[:]); err != nil {
		return nil, err
	}
	methods := make([]byte, int(head[1]))
	if _, err := io.ReadFull(c, methods); err != nil {
		return nil, err
	}
	return append(head[:], methods...), nil
}

// readLogin reads the RFC 1929 subnegotiation and checks it against the fixture's login;
// the error names no value.
func (e *appSOCKSExit) readLogin(c net.Conn) error {
	var head [2]byte
	if _, err := io.ReadFull(c, head[:]); err != nil {
		return err
	}
	if head[0] != 1 {
		return fmt.Errorf("login version %d", head[0])
	}
	user := make([]byte, int(head[1]))
	if _, err := io.ReadFull(c, user); err != nil {
		return err
	}
	var size [1]byte
	if _, err := io.ReadFull(c, size[:]); err != nil {
		return err
	}
	password := make([]byte, int(size[0]))
	if _, err := io.ReadFull(c, password); err != nil {
		return err
	}
	if string(user) != e.user || string(password) != e.password {
		return errors.New("wrong login")
	}
	return nil
}

// readAppSOCKS5Connect reads a CONNECT request and returns its target address.
func readAppSOCKS5Connect(c net.Conn) (string, error) {
	var head [4]byte
	if _, err := io.ReadFull(c, head[:]); err != nil {
		return "", err
	}
	if head != [4]byte{5, 1, 0, 3} {
		return "", fmt.Errorf("CONNECT head %v", head)
	}
	var size [1]byte
	if _, err := io.ReadFull(c, size[:]); err != nil {
		return "", err
	}
	address := make([]byte, int(size[0])+2)
	if _, err := io.ReadFull(c, address); err != nil {
		return "", err
	}
	port := int(address[len(address)-2])<<8 | int(address[len(address)-1])
	return fmt.Sprintf("%s:%d", address[:len(address)-2], port), nil
}

type appHTTPProxy struct {
	address, password, echo string
	accepted                atomic.Int32
	requests                chan *http.Request
}

func (p *appHTTPProxy) token() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte("synthetic-user:"+p.password))
}

func newAppHTTPProxy(t *testing.T) *appHTTPProxy {
	t.Helper()
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { echo.Close() })
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	p := &appHTTPProxy{address: ln.Addr().String(), password: "synthetic-password", echo: echo.Addr().String(), requests: make(chan *http.Request, 100)}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			p.accepted.Add(1)
			go p.serve(c)
		}
	}()
	return p
}

func (p *appHTTPProxy) serve(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(20 * time.Second))
	r := bufio.NewReader(c)
	req, err := http.ReadRequest(r)
	if err != nil {
		return
	}
	p.requests <- req
	if req.Header.Get("Proxy-Authorization") != p.token() {
		io.WriteString(c, "HTTP/1.1 407 private-reason\r\nProxy-Authenticate: Basic realm=\"private-challenge\"\r\n\r\nprivate-body")
		return
	}
	switch req.Host {
	case "blocked.example:443":
		io.WriteString(c, "HTTP/1.1 403 private-reason\r\nX-Private: private-header\r\n\r\nprivate-body")
		return
	case "malformed.example:443":
		io.WriteString(c, "SSH-2.0-private-banner\r\n")
		return
	case "truncated.example:443":
		io.WriteString(c, "HTTP/1.1 200 private-reason\r\nX-Private: interrupted")
		return
	case "silent.example:443":
		io.Copy(io.Discard, r)
		return
	case "echo.example:7":
	default:
		io.WriteString(c, "HTTP/1.1 403 Forbidden\r\n\r\n")
		return
	}
	target, err := net.DialTimeout("tcp", p.echo, 5*time.Second)
	if err != nil {
		return
	}
	defer target.Close()
	if _, err := io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		return
	}
	go func() { io.Copy(target, r); target.Close() }()
	io.Copy(c, target)
}
