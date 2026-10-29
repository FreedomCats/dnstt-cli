// Added by dnstt-cli (scripts/vendorcore/_files): not part of the dnstt upstream.

package turbotunnel

import (
	"iter"
	"net"
)

// Closed returns a channel that is closed when the QueuePacketConn is closed.
func (c *QueuePacketConn) Closed() <-chan struct{} {
	return c.closed
}

// OutgoingUntilClosed yields the packets of OutgoingQueue(addr) until the QueuePacketConn
// is closed. The send loops of the transports range over it instead of over the queue,
// which is never closed, so that their goroutines do not outlive the transport.
func (c *QueuePacketConn) OutgoingUntilClosed(addr net.Addr) iter.Seq[[]byte] {
	return func(yield func([]byte) bool) {
		queue := c.OutgoingQueue(addr)
		for {
			select {
			case p := <-queue:
				if !yield(p) {
					return
				}
			case <-c.closed:
				return
			}
		}
	}
}
