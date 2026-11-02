package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/FreedomCats/dnstt-cli/core/dns"
	"github.com/FreedomCats/dnstt-cli/core/server"
	"github.com/FreedomCats/dnstt-cli/internal/app"
	"github.com/FreedomCats/dnstt-cli/internal/config"
	"github.com/FreedomCats/dnstt-cli/internal/datapath"
)

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
