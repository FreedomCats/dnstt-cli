package datapath

import (
	"context"
	"errors"
	"io"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestShutdownBoundsPendingHandshakes(t *testing.T) {
	for _, phase := range []string{"silent", "request", "dial", "reply"} {
		t.Run(phase, func(t *testing.T) {
			dialed, canceled := make(chan struct{}), make(chan struct{})
			dp := newSocks(t, SocksOptions{Dialer: dialerFunc(func(ctx context.Context, _ string) (net.Conn, error) {
				close(dialed)
				if phase == "dial" {
					<-ctx.Done()
					close(canceled)
					return nil, ctx.Err()
				}
				return nil, errors.New("synthetic refusal")
			})})
			client := pipeSocksClient(t, dp)
			if phase != "silent" {
				if _, err := client.Write([]byte{5, 1, 0}); err != nil {
					t.Fatal(err)
				}
				var choice [2]byte
				if _, err := io.ReadFull(client, choice[:]); err != nil || choice != [2]byte{5, 0} {
					t.Fatalf("greeting = %v, %v", choice, err)
				}
				request := []byte{5, 1, 0, 3}
				if phase != "request" {
					request = append(request, byte(len("echo.example")))
					request = append(request, []byte("echo.example\x00\x07")...)
				}
				if _, err := client.Write(request); err != nil {
					t.Fatal(err)
				}
				if phase != "request" {
					select {
					case <-dialed:
					case <-time.After(5 * time.Second):
						t.Fatal("handshake did not reach the exit dialer")
					}
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			start := time.Now()
			if err := dp.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Shutdown = %v, want deadline exceeded", err)
			}
			if time.Since(start) > 2*time.Second {
				t.Fatal("pending handshake held shutdown past its deadline")
			}
			if _, err := client.Read(make([]byte, 1)); err == nil {
				t.Fatal("forced shutdown left the local connection open")
			}
			if phase == "dial" {
				select {
				case <-canceled:
				default:
					t.Fatal("forced shutdown did not cancel the pending dial")
				}
			}
			if err := dp.Close(); err != nil {
				t.Fatal(err)
			}
			dp.mu.Lock()
			defer dp.mu.Unlock()
			if len(dp.conns) != 0 || !dp.closing {
				t.Fatal("shutdown left tracked connections behind")
			}
		})
	}
}

func TestShutdownCancellationAndImmediateClose(t *testing.T) {
	for _, force := range []string{"cancel", "close"} {
		t.Run(force, func(t *testing.T) {
			dp := newSocks(t, SocksOptions{Dialer: dialerFunc(nil)})
			client := pipeSocksClient(t, dp)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			observed := &socksShutdownContext{Context: ctx, started: make(chan struct{})}
			done := make(chan error, 1)
			go func() { done <- dp.Shutdown(observed) }()
			select {
			case <-observed.started:
			case <-ctx.Done():
				t.Fatal("shutdown did not begin")
			}
			if c, err := net.DialTimeout("tcp", dp.ListenAddr().String(), time.Second); err == nil {
				c.Close()
				t.Fatal("shutdown still accepted connections")
			}
			if force == "cancel" {
				cancel()
			} else {
				// Immediate Close must not wait for the graceful shutdown's deadline.
				var wg sync.WaitGroup
				for range 8 {
					wg.Add(1)
					go func() { defer wg.Done(); dp.Close() }()
				}
				wg.Wait()
			}
			select {
			case err := <-done:
				if force == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled Shutdown = %v", err)
				}
				if force == "close" && err != nil {
					t.Fatalf("forced Shutdown = %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("force-close did not interrupt graceful shutdown")
			}
			if _, err := client.Read(make([]byte, 1)); err == nil {
				t.Fatal("force-close left the silent client open")
			}
			if err := dp.Close(); err != nil {
				t.Fatal(err)
			}
			if err := dp.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestShutdownClosesRelaysAndLeavesNothing(t *testing.T) {
	echo := echoServer(t)
	before := runtime.NumGoroutine()
	dp := newSocks(t, SocksOptions{Dialer: directDialer{}})
	conn, err := socksClient(t, dp).Dial("tcp", echo)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	echoThrough(t, conn, []byte("before graceful shutdown"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := dp.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("shutdown left an established relay open")
	}
	conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<20)
			n := runtime.Stack(buf, true)
			t.Fatalf("goroutines left after Shutdown: %d before, %d after\n%s", before, runtime.NumGoroutine(), buf[:n])
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A pipe makes blocked greeting/reply writes deterministic without TCP buffering.
func pipeSocksClient(t *testing.T, dp *Socks) net.Conn {
	t.Helper()
	a, b := net.Pipe()
	if !dp.start(a) {
		b.Close()
		t.Fatal("datapath refused the test connection")
	}
	go dp.serve(a)
	b.SetDeadline(time.Now().Add(5 * time.Second))
	t.Cleanup(func() { b.Close() })
	return b
}

type socksShutdownContext struct {
	context.Context
	started chan struct{}
	once    sync.Once
}

func (c *socksShutdownContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.started) })
	return c.Context.Done()
}
