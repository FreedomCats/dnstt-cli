package datapath

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// dnsQuery is a query for name built with dnsmessage, as a stub resolver sends it.
func dnsQuery(t *testing.T, id uint16, name string) []byte {
	t.Helper()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: id, RecursionDesired: true})
	if err := b.StartQuestions(); err != nil {
		t.Fatal(err)
	}
	if err := b.Question(dnsmessage.Question{
		Name:  dnsmessage.MustNewName(name),
		Type:  dnsmessage.TypeA,
		Class: dnsmessage.ClassINET,
	}); err != nil {
		t.Fatal(err)
	}
	msg, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

// dnsARecord builds an answer to the first question of query: an A record with address a
// and the question echoed. It is pure, so a fake server can call it.
func dnsARecord(query []byte, a [4]byte) ([]byte, error) {
	var p dnsmessage.Parser
	hdr, err := p.Start(query)
	if err != nil {
		return nil, err
	}
	q, err := p.Question()
	if err != nil {
		return nil, err
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{
		ID:                 hdr.ID,
		Response:           true,
		RecursionDesired:   hdr.RecursionDesired,
		RecursionAvailable: true,
	})
	if err := b.StartQuestions(); err != nil {
		return nil, err
	}
	if err := b.Question(q); err != nil {
		return nil, err
	}
	if err := b.StartAnswers(); err != nil {
		return nil, err
	}
	if err := b.AResource(dnsmessage.ResourceHeader{
		Name:  q.Name,
		Type:  dnsmessage.TypeA,
		Class: dnsmessage.ClassINET,
		TTL:   60,
	}, dnsmessage.AResource{A: a}); err != nil {
		return nil, err
	}
	return b.Finish()
}

// dnsQuestionName is the name of the first question of query.
func dnsQuestionName(query []byte) (string, error) {
	var p dnsmessage.Parser
	if _, err := p.Start(query); err != nil {
		return "", err
	}
	q, err := p.Question()
	if err != nil {
		return "", err
	}
	return q.Name.String(), nil
}

// readDNSFrame reads one length-prefixed DNS message, the DNS-over-TCP framing of RFC
// 1035 §4.2.2.
func readDNSFrame(r io.Reader) ([]byte, error) {
	var size [2]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return nil, err
	}
	msg := make([]byte, binary.BigEndian.Uint16(size[:]))
	if _, err := io.ReadFull(r, msg); err != nil {
		return nil, err
	}
	return msg, nil
}

// writeDNSFrame writes one length-prefixed DNS message.
func writeDNSFrame(w io.Writer, msg []byte) error {
	framed := binary.BigEndian.AppendUint16(nil, uint16(len(msg)))
	_, err := w.Write(append(framed, msg...))
	return err
}

// A SERVFAIL answer keeps the query's ID, opcode and RD bit and echoes its question; a
// query that is only a header gets a parsable header-only answer.
func TestDNSServfail(t *testing.T) {
	t.Run("question", func(t *testing.T) {
		query := dnsQuery(t, 0x1234, "t.example.com.")
		msg := dnsServfail(query)
		var p dnsmessage.Parser
		hdr, err := p.Start(msg)
		if err != nil {
			t.Fatalf("the answer does not parse: %v", err)
		}
		if hdr.ID != 0x1234 || !hdr.Response || hdr.RCode != dnsmessage.RCodeServerFailure {
			t.Fatalf("the answer: ID %#x, response %v, rcode %v", hdr.ID, hdr.Response, hdr.RCode)
		}
		if !hdr.RecursionDesired {
			t.Fatal("the answer lost the RD bit")
		}
		qs, err := p.AllQuestions()
		if err != nil {
			t.Fatal(err)
		}
		want := dnsmessage.Question{
			Name:  dnsmessage.MustNewName("t.example.com."),
			Type:  dnsmessage.TypeA,
			Class: dnsmessage.ClassINET,
		}
		if len(qs) != 1 || qs[0] != want {
			t.Fatalf("the questions are %v, want [%v]", qs, want)
		}
	})

	t.Run("header only", func(t *testing.T) {
		// A query with QDCOUNT 0 is still a 12-byte header, and the listener accepts it.
		query := make([]byte, dnsHeaderLen)
		binary.BigEndian.PutUint16(query[0:2], 0x5678)
		query[2] = 0x01 // RD
		msg := dnsServfail(query)
		var p dnsmessage.Parser
		hdr, err := p.Start(msg)
		if err != nil {
			t.Fatalf("the answer does not parse: %v", err)
		}
		if hdr.ID != 0x5678 || !hdr.Response || hdr.RCode != dnsmessage.RCodeServerFailure {
			t.Fatalf("the answer: ID %#x, response %v, rcode %v", hdr.ID, hdr.Response, hdr.RCode)
		}
		if qs, err := p.AllQuestions(); err != nil || len(qs) != 0 {
			t.Fatalf("the answer has questions %v, %v", qs, err)
		}
	})

	t.Run("no header", func(t *testing.T) {
		if msg := dnsServfail([]byte{0x12, 0x34, 0x01}); msg != nil {
			t.Fatalf("a datagram below the header gave %d bytes", len(msg))
		}
	})
}

// exchange runs one dnsExchange against a fake server on a net.Pipe: serve is the far end
// of the pipe. The dial is checked to be upstream:53.
func exchange(t *testing.T, ctx context.Context, upstream netip.Addr, query []byte, serve func(net.Conn)) ([]byte, error) {
	t.Helper()
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer server.Close()
		serve(server)
	}()
	var target string
	got, err := dnsExchange(ctx, dialerFunc(func(_ context.Context, address string) (net.Conn, error) {
		target = address
		return client, nil
	}), upstream, query)
	if want := "203.0.113.53:53"; target != want {
		t.Errorf("the exit was dialed at %q, want %q", target, want)
	}
	client.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the fake server did not finish")
	}
	return got, err
}

func TestDNSExchange(t *testing.T) {
	query := dnsQuery(t, 0x1234, "t.example.com.")
	answer, err := dnsARecord(query, [4]byte{203, 0, 113, 80})
	if err != nil {
		t.Fatal(err)
	}
	upstream := netip.MustParseAddr("203.0.113.53")

	t.Run("answer", func(t *testing.T) {
		got, err := exchange(t, context.Background(), upstream, query, func(c net.Conn) {
			q, err := readDNSFrame(c)
			if err != nil {
				t.Errorf("the server read no query: %v", err)
				return
			}
			if !bytes.Equal(q, query) {
				t.Errorf("the server got another query")
			}
			_ = writeDNSFrame(c, answer)
		})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, answer) {
			t.Fatalf("the answer changed in the exchange: %d bytes", len(got))
		}
	})

	t.Run("zero length prefix", func(t *testing.T) {
		_, err := exchange(t, context.Background(), upstream, query, func(c net.Conn) {
			if _, err := readDNSFrame(c); err != nil {
				return
			}
			_, _ = c.Write([]byte{0, 0})
		})
		if err == nil {
			t.Fatal("a zero length prefix gave an answer")
		}
	})

	t.Run("short length prefix", func(t *testing.T) {
		_, err := exchange(t, context.Background(), upstream, query, func(c net.Conn) {
			if _, err := readDNSFrame(c); err != nil {
				return
			}
			_, _ = c.Write([]byte{0, 5})
		})
		if err == nil {
			t.Fatal("a length below the DNS header gave an answer")
		}
	})

	t.Run("another id", func(t *testing.T) {
		wrong := append([]byte(nil), answer...)
		binary.BigEndian.PutUint16(wrong, 0x4321)
		_, err := exchange(t, context.Background(), upstream, query, func(c net.Conn) {
			if _, err := readDNSFrame(c); err != nil {
				return
			}
			_ = writeDNSFrame(c, wrong)
		})
		if err == nil {
			t.Fatal("an answer for another query was accepted")
		}
	})

	t.Run("not a response", func(t *testing.T) {
		wrong := append([]byte(nil), answer...)
		wrong[2] &^= 0x80
		_, err := exchange(t, context.Background(), upstream, query, func(c net.Conn) {
			if _, err := readDNSFrame(c); err != nil {
				return
			}
			_ = writeDNSFrame(c, wrong)
		})
		if err == nil {
			t.Fatal("an answer without QR was accepted")
		}
	})

	t.Run("stream ends", func(t *testing.T) {
		_, err := exchange(t, context.Background(), upstream, query, func(c net.Conn) {
			_, _ = readDNSFrame(c)
		})
		if err == nil {
			t.Fatal("a stream that ended before the answer gave one")
		}
	})

	t.Run("timeout", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, err := exchange(t, ctx, upstream, query, func(c net.Conn) {
			if _, err := readDNSFrame(c); err != nil {
				return
			}
			_, _ = io.Copy(io.Discard, c)
		})
		if err == nil {
			t.Fatal("a silent server gave an answer")
		}
	})
}
