package datapath

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

// Drain waits for a relay that is already open: the stop lets the program finish its
// request while the session is still up, and refuses new connections meanwhile.
func TestDrainWaitsForRelays(t *testing.T) {
	echo := echoServer(t)
	dp := newSocks(t, SocksOptions{Dialer: directDialer{}})
	conn, err := socksClient(t, dp).Dial("tcp", echo)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	echoThrough(t, conn, []byte("before drain"))

	done := make(chan error, 1)
	go func() { done <- dp.Drain(context.Background()) }()
	waitListenerClosed(t, dp)

	select {
	case err := <-done:
		t.Fatalf("Drain returned with the relay open: %v", err)
	default:
	}
	echoThrough(t, conn, []byte("while draining"))

	conn.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Drain = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Drain did not return after the relay ended")
	}
}

// A relay that never ends cannot hold the stop past ctx: the caller bounds the wait, and
// Close then forces the connection.
func TestDrainBoundedByContext(t *testing.T) {
	peer := make(chan net.Conn, 1)
	dp := newSocks(t, SocksOptions{Dialer: dialerFunc(func(context.Context, string) (net.Conn, error) {
		a, b := net.Pipe()
		peer <- b
		return a, nil
	})})
	conn, err := socksClient(t, dp).Dial("tcp", "echo.example:7")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := dp.Drain(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Drain = %v, want the context deadline", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Drain held the stop for %v", elapsed)
	}

	if err := dp.Close(); err != nil {
		t.Fatal(err)
	}
	(<-peer).Close()
}

// Concurrent Drain and Close calls are safe: Close forces the relays, which ends every
// drain; a repeated call after Close returns at once.
func TestDrainConcurrentClose(t *testing.T) {
	peer := make(chan net.Conn, 1)
	dp := newSocks(t, SocksOptions{Dialer: dialerFunc(func(context.Context, string) (net.Conn, error) {
		a, b := net.Pipe()
		peer <- b
		return a, nil
	})})
	conn, err := socksClient(t, dp).Dial("tcp", "echo.example:7")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	drains := make(chan error, 2)
	for range 2 {
		go func() { drains <- dp.Drain(context.Background()) }()
	}
	waitListenerClosed(t, dp)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); _ = dp.Close() }()
	}
	wg.Wait()
	for range 2 {
		select {
		case err := <-drains:
			if err != nil {
				t.Fatalf("Drain after Close = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a drain did not return after Close")
		}
	}
	if err := dp.Drain(context.Background()); err != nil {
		t.Errorf("Drain after Close = %v", err)
	}
	if err := dp.Close(); err != nil {
		t.Errorf("Close after Close = %v", err)
	}
	(<-peer).Close()
}

// waitListenerClosed waits until the datapath's listener refuses new connections.
func waitListenerClosed(t *testing.T, dp *Socks) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", dp.ListenAddr().String(), time.Second)
		if err != nil {
			return
		}
		c.Close()
		if time.Now().After(deadline) {
			t.Fatal("the datapath still accepts connections")
		}
		time.Sleep(time.Millisecond)
	}
}
