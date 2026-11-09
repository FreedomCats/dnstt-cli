package datapath

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"time"

	"github.com/FreedomCats/dnstt-cli/internal/exit"
	"golang.org/x/net/dns/dnsmessage"
)

const (
	// dnsPort is the DNS port on both sides: the listener on the adapter's address and the
	// upstream behind the exit.
	dnsPort = 53

	// dnsHeaderLen is the DNS header of RFC 1035: a shorter datagram is not a query.
	dnsHeaderLen = 12

	// dnsMaxSize is the largest message a two-byte length prefix can carry (RFC 1035
	// §4.2.2), and so the read buffer of the DNS listener.
	dnsMaxSize = 1<<16 - 1

	// dnsQueryTimeout bounds one exchange. A stub resolver gives up in seconds, much
	// earlier than the 60 s of a TCP dial; a later answer is useless.
	dnsQueryTimeout = 10 * time.Second

	// dnsInFlight is the number of exchanges in flight at once: a resolver has a handful
	// of queries at once, and every exchange is a stream through the tunnel.
	dnsInFlight = 64
)

// dnsExchange carries one DNS query to upstream as a DNS-over-TCP exchange through the
// exit (RFC 1035 §4.2.2) and returns the answer. The whole exchange is bounded by ctx:
// both a timeout and close unblock a read.
func dnsExchange(ctx context.Context, dialer exit.Dialer, upstream netip.Addr, query []byte) ([]byte, error) {
	if len(query) < dnsHeaderLen {
		return nil, errors.New("dns: the query is shorter than a DNS header")
	}
	conn, err := dialer.Dial(ctx, net.JoinHostPort(upstream.String(), strconv.Itoa(dnsPort)))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	// An expired ctx sets the deadline in the past; both the timeout and close end a
	// read. The same pattern as internal/resolvers.
	stop := context.AfterFunc(ctx, func() { conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()
	framed := binary.BigEndian.AppendUint16(nil, uint16(len(query)))
	if _, err := conn.Write(append(framed, query...)); err != nil {
		return nil, err
	}
	var size [2]byte
	if _, err := io.ReadFull(conn, size[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint16(size[:])
	if n < dnsHeaderLen {
		return nil, fmt.Errorf("dns: a %d-byte answer is below the DNS header", n)
	}
	answer := make([]byte, n)
	if _, err := io.ReadFull(conn, answer); err != nil {
		return nil, err
	}
	if binary.BigEndian.Uint16(answer) != binary.BigEndian.Uint16(query) {
		return nil, errors.New("dns: the answer is for another query")
	}
	if answer[2]&0x80 == 0 {
		return nil, errors.New("dns: the answer is not a response")
	}
	return answer, nil
}

// dnsServfail builds the SERVFAIL answer to query: the same ID, opcode and RD bit, the
// questions echoed, so a stub resolver matches the answer to its query, and no other
// sections. nil only when even the 12-byte header does not parse; the listener requires
// the length already.
func dnsServfail(query []byte) []byte {
	var p dnsmessage.Parser
	hdr, err := p.Start(query)
	if err != nil {
		return nil
	}
	hdr.Response = true
	hdr.RCode = dnsmessage.RCodeServerFailure
	b := dnsmessage.NewBuilder(make([]byte, 0, dnsHeaderLen), hdr)
	// A corrupt question section still gets a header-only answer.
	if err := b.StartQuestions(); err == nil {
		for {
			q, err := p.Question()
			if err != nil {
				break
			}
			if err := b.Question(q); err != nil {
				return nil
			}
		}
	}
	answer, err := b.Finish()
	if err != nil {
		return nil
	}
	return answer
}
