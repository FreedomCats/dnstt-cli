// Added by dnstt-cli (scripts/vendorcore/_files): not part of the dnstt upstream.

package client

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/FreedomCats/dnstt-cli/core/dns"
)

// tcpStub is the resolver a plain-TCP session talks to: it reads length-prefixed DNS
// messages from a TCP connection (RFC 7766) and passes them on over UDP to the in-process
// dnstt server, which speaks UDP only, then writes the answers back with a length.
type tcpStub struct {
	ln       net.Listener
	upstream *net.UDPAddr
	// dropFirst closes the first connection after its first message, unanswered.
	dropFirst bool

	mu       sync.Mutex
	conns    map[net.Conn]struct{}
	accepted int    // connections taken so far
	wire     []byte // the length prefix and the message of the first message ever read
	closed   bool
	wg       sync.WaitGroup

	connErrs chan error    // the first failed read or write on a connection, with its error
	dropped  chan struct{} // closed when dropFirst closes the first connection
}

func newTCPStub(t *testing.T, upstream *net.UDPAddr, dropFirst bool) *tcpStub {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &tcpStub{
		ln:        ln,
		upstream:  upstream,
		dropFirst: dropFirst,
		conns:     map[net.Conn]struct{}{},
		connErrs:  make(chan error, 1),
		dropped:   make(chan struct{}),
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				conn.Close()
				return
			}
			s.conns[conn] = struct{}{}
			s.accepted++
			first := s.accepted == 1
			s.wg.Add(1)
			s.mu.Unlock()
			go func() {
				defer s.wg.Done()
				defer func() {
					conn.Close()
					s.mu.Lock()
					delete(s.conns, conn)
					s.mu.Unlock()
				}()
				s.serve(conn, first)
			}()
		}
	}()
	t.Cleanup(s.close)
	return s
}

func (s *tcpStub) addr() string { return s.ln.Addr().String() }

func (s *tcpStub) connections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accepted
}

func (s *tcpStub) firstMessage() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.wire...)
}

func (s *tcpStub) close() {
	s.ln.Close()
	s.mu.Lock()
	s.closed = true
	for conn := range s.conns {
		conn.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

// serve reads a message, forwards it and writes the answer back, one message at a time:
// the session's queries queue up in the TCP stream without waiting for each other.
func (s *tcpStub) serve(conn net.Conn, firstConn bool) {
	for {
		var prefix [2]byte
		if _, err := io.ReadFull(conn, prefix[:]); err != nil {
			s.noteConnErr(err)
			return
		}
		n := binary.BigEndian.Uint16(prefix[:])
		query := make([]byte, n)
		if _, err := io.ReadFull(conn, query); err != nil {
			s.noteConnErr(err)
			return
		}
		s.noteWire(prefix[:], query)
		if firstConn && s.dropFirst {
			close(s.dropped)
			return
		}
		answer, err := s.forward(query)
		if err != nil {
			return
		}
		if err := writeMessage(conn, answer); err != nil {
			s.noteConnErr(err)
			return
		}
	}
}

// writeMessage writes msg with the two-octet length prefix of DNS over TCP.
func writeMessage(conn net.Conn, msg []byte) error {
	if err := binary.Write(conn, binary.BigEndian, uint16(len(msg))); err != nil {
		return err
	}
	_, err := conn.Write(msg)
	return err
}

// forward passes a query on to the dnstt server and returns its answer.
func (s *tcpStub) forward(query []byte) ([]byte, error) {
	udp, err := net.DialUDP("udp", nil, s.upstream)
	if err != nil {
		return nil, err
	}
	defer udp.Close()
	udp.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := udp.Write(query); err != nil {
		return nil, err
	}
	answer := make([]byte, 65535)
	n, err := udp.Read(answer)
	if err != nil {
		return nil, err
	}
	return answer[:n], nil
}

func (s *tcpStub) noteConnErr(err error) {
	select {
	case s.connErrs <- err:
	default:
	}
}

func (s *tcpStub) noteWire(prefix, msg []byte) {
	s.mu.Lock()
	if s.wire == nil {
		s.wire = append(append([]byte(nil), prefix...), msg...)
	}
	s.mu.Unlock()
}

// A session through a plain-TCP resolver: the tunnel runs, and every socket of the session
// goes through Control as a TCP socket to the stub (s.dial, §06 of doc/architecture.md).
func TestTCPSession(t *testing.T) {
	srv := startServer(t)
	stub := newTCPStub(t, srv.Addr, false)
	cfg := udpConfig(srv)
	cfg.Resolver = Resolver{Transport: TCP, Address: stub.addr()}
	var mu sync.Mutex
	var calls []string
	cfg.Control = func(network, address string, _ syscall.RawConn) error {
		mu.Lock()
		calls = append(calls, network+" "+address)
		mu.Unlock()
		return nil
	}
	s := dial(t, cfg)
	if err := echo(s, []byte("through a plain TCP resolver")); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := "tcp4 " + stub.addr()
	for _, call := range calls {
		if call == want {
			return
		}
	}
	t.Errorf("Control calls %q, want %q", calls, want)
}

// The wire is DNS over TCP: a two-octet length and exactly that many bytes of a DNS query.
// A TLS ClientHello would begin 0x16 0x03 and fail here.
func TestTCPWireFormat(t *testing.T) {
	srv := startServer(t)
	stub := newTCPStub(t, srv.Addr, false)
	cfg := udpConfig(srv)
	cfg.Resolver = Resolver{Transport: TCP, Address: stub.addr()}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, err := Dial(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := echo(s, []byte("on the wire")); err != nil {
		t.Fatal(err)
	}

	wire := stub.firstMessage()
	if len(wire) < 14 {
		t.Fatalf("the stub read %d bytes, too few for a length prefix and a DNS header", len(wire))
	}
	if wire[0] == 0x16 && wire[1] == 0x03 {
		t.Fatalf("the connection starts with a TLS record: % x", wire[:2])
	}
	n := binary.BigEndian.Uint16(wire[:2])
	if len(wire) != 2+int(n) {
		t.Fatalf("the length prefix says %d bytes, the stub read %d", n, len(wire)-2)
	}
	msg, err := dns.MessageFromWireFormat(wire[2:])
	if err != nil {
		t.Fatalf("not a DNS message after the length prefix: %v", err)
	}
	if len(msg.Question) != 1 {
		t.Fatalf("%d questions in the first message", len(msg.Question))
	}
	if _, ok := msg.Question[0].Name.TrimSuffix(testDomain); !ok {
		t.Errorf("question %s is not under %s", msg.Question[0].Name, testDomain)
	}
	if msg.Flags&0x8000 != 0 {
		t.Error("the first message is a response, not a query")
	}
}

// Closing the session closes its TCP connection: the stub's pending read returns, or, when
// a message was already being written, that write fails.
func TestTCPCloseClosesConn(t *testing.T) {
	srv := startServer(t)
	stub := newTCPStub(t, srv.Addr, false)
	cfg := udpConfig(srv)
	cfg.Resolver = Resolver{Transport: TCP, Address: stub.addr()}
	s := dial(t, cfg)
	if err := echo(s, []byte("before the close")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-stub.connErrs:
		t.Fatalf("the stub's connection ended before Close: %v", err)
	default:
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stub.connErrs:
	case <-time.After(5 * time.Second):
		t.Fatal("the stub's connection did not end after the session closed")
	}
}

// A dropped TCP connection is redialed (P2); the messages that were in flight arrive on the
// new one. The stub drops its first connection after its first message, so the session
// setup itself has to recover. The backoff globals stay at their defaults: shortRedial's
// cleanup restores them while a transport goroutine may still be in redial as the session
// closes, which -race reports as an unsynchronized access.
func TestTCPRedialAfterDrop(t *testing.T) {
	srv := startServer(t)
	stub := newTCPStub(t, srv.Addr, true)
	cfg := udpConfig(srv)
	cfg.Resolver = Resolver{Transport: TCP, Address: stub.addr()}
	s := dial(t, cfg)
	select {
	case <-stub.dropped:
	default:
		t.Fatal("the stub did not drop the first connection before Dial returned")
	}
	if n := stub.connections(); n < 2 {
		t.Fatalf("%d connections, want the redial after the drop", n)
	}
	if err := echo(s, []byte("after the redial")); err != nil {
		t.Fatal(err)
	}
}
