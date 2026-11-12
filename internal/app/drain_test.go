package app

import (
	"testing"

	"github.com/FreedomCats/dnstt-cli/internal/config"
	"github.com/FreedomCats/dnstt-cli/internal/supervisor"
)

// A stop of a socks-mode tunnel drains the datapath: the listener stops taking
// connections, but a relay that is already open finishes while the session is still up
// (architecture.md §08), and Run waits for it.
func TestSocksStopDrainsOpenRelay(t *testing.T) {
	fx := newAppSOCKSExit(t, "synthetic-user", "synthetic-password")
	_, dp, events, done, cancel := startAppTunnelWithExit(t, fx.addr,
		config.Exit{Type: "socks5", OneWrite: true}, "synthetic-password")
	waitAppState(t, events, supervisor.StateConnected)

	client, err := proxyClient(dp)
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.DialContext(appContext(t), "tcp", "echo.example:7")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	appEcho(t, c)

	cancel()
	waitAppState(t, events, supervisor.StateStopping)

	// The session outlives the stop until the open relay ends: the data still flows, and
	// Run is still waiting for the drain.
	appEcho(t, c)
	select {
	case err := <-done:
		t.Fatalf("Run returned with an open relay: %v", err)
	default:
	}

	c.Close()
	if err := appRunResult(t, done); err != nil {
		t.Fatalf("Run = %v", err)
	}
}
