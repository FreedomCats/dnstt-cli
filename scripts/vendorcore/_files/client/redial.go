// Added by dnstt-cli (scripts/vendorcore/_files): not part of the dnstt upstream.

package client

import (
	"net"
	"time"
)

// The backoff of P2 between failed redials of the DoT connection; variables for tests.
var (
	redialMinDelay = 1 * time.Second
	redialMaxDelay = 64 * time.Second
)

// redial dials a new connection to the resolver after the old one broke (P2). A failed
// dial is retried after 1 s, then 2, 4… up to 64 s, until the TLSPacketConn is closed.
// Upstream gave up after one failed redial: the transport closed, and the session hung
// until its keepalive timeout. The error is net.ErrClosed, when the TLSPacketConn is
// closed.
func (c *TLSPacketConn) redial(dial func() (net.Conn, error)) (net.Conn, error) {
	delay := redialMinDelay
	for {
		conn, err := dial()
		select {
		case <-c.QueuePacketConn.Closed():
			if conn != nil {
				conn.Close()
			}
			return nil, net.ErrClosed
		default:
		}
		if err == nil {
			return conn, nil
		}
		c.logf("dial tls: %v; next attempt in %v", err, delay)
		t := time.NewTimer(delay)
		select {
		case <-c.QueuePacketConn.Closed():
			t.Stop()
			return nil, net.ErrClosed
		case <-t.C:
		}
		delay = min(2*delay, redialMaxDelay)
	}
}
