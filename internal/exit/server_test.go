package exit

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"testing"
)

// socksServer is a SOCKS5 server for the tests (RFC 1928 CONNECT, RFC 1929 login), in the
// role of danted behind the dnstt server.
type socksServer struct {
	// user and password are the login it requires; an empty user lets anyone in.
	user, password string
	// names resolves host names; a name not in it is "host unreachable".
	names map[string]string
	// refuse, if not 0, is the reply to every CONNECT.
	refuse byte
	// bindDomain makes the bound address of the reply a name, not an IPv4 address.
	bindDomain bool
	// first is sent in one write with the reply, as the first bytes of the target.
	first []byte

	// logGreeting logs the methods of every greeting as "greeting 00 02"; off by default,
	// because the older cases compare the log exactly.
	logGreeting bool
	// chooseSet makes the server answer choose regardless of the methods offered; a method
	// the client did not offer ends the exchange with that answer.
	chooseSet bool
	choose    byte
	// singleRead reads one message per Read: the first Read takes exactly one client
	// Write, which is how the tests see the whole one-write batch. The rest is read off
	// the connection, so a sequential exchange still works.
	singleRead bool
	// dropBatch closes the stream after the first message, as a server that reads one
	// message per recv and hangs up.
	dropBatch bool
	// coalesce holds the method choice and the login status and writes them together with
	// the CONNECT reply, with first after them.
	coalesce bool
	// dribble writes every reply byte in its own Write.
	dribble bool

	mu    sync.Mutex
	seen  []string // what each connection did: "login user", "connect domain host:port"…
	reads []int    // sizes of the first reads in singleRead mode
}

func (s *socksServer) log(event string) {
	s.mu.Lock()
	s.seen = append(s.seen, event)
	s.mu.Unlock()
}

func (s *socksServer) events() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

// readSizes are the sizes of the first reads in singleRead mode, one per connection.
func (s *socksServer) readSizes() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int(nil), s.reads...)
}

func (s *socksServer) recordRead(n int) {
	s.mu.Lock()
	s.reads = append(s.reads, n)
	s.mu.Unlock()
}

// pipe is an OpenStream that serves every stream in memory.
func (s *socksServer) pipe() OpenStream {
	return func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		go s.serve(server)
		return client, nil
	}
}

// listen serves on loopback TCP and returns the address.
func (s *socksServer) listen(t *testing.T) string {
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
			go s.serve(conn)
		}
	}()
	return ln.Addr().String()
}

func (s *socksServer) serve(conn net.Conn) {
	defer conn.Close()
	var r io.Reader = conn
	if s.singleRead {
		buf := make([]byte, 1024)
		n, err := conn.Read(buf)
		s.recordRead(n)
		if err != nil || s.dropBatch {
			return
		}
		// The rest of the exchange is read off the connection: a sequential client sends
		// its next message only after the previous reply.
		r = io.MultiReader(bytes.NewReader(buf[:n]), conn)
	}
	var pending []byte
	write := func(b []byte) error {
		switch {
		case s.coalesce:
			pending = append(pending, b...)
			return nil
		case s.dribble:
			for _, c := range b {
				if _, err := conn.Write([]byte{c}); err != nil {
					return err
				}
			}
			return nil
		}
		_, err := conn.Write(b)
		return err
	}
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		_, err := conn.Write(pending)
		pending = nil
		return err
	}

	var head [2]byte
	if _, err := io.ReadFull(r, head[:]); err != nil || head[0] != socksVersion {
		return
	}
	methods := make([]byte, head[1])
	if _, err := io.ReadFull(r, methods); err != nil {
		return
	}
	if s.logGreeting {
		s.log(fmt.Sprintf("greeting % x", methods))
	}
	want := byte(methodNone)
	if s.user != "" {
		want = methodPassword
	}
	if s.chooseSet {
		want = s.choose
	} else if !contains(methods, want) {
		write([]byte{socksVersion, methodNoAcceptable})
		flush()
		return
	}
	if (s.chooseSet && !contains(methods, want)) || (want != methodNone && want != methodPassword) {
		// A method the client did not offer or cannot go on with: answer it and stop,
		// without reading the login or the request that may follow the greeting.
		write([]byte{socksVersion, want})
		flush()
		return
	}
	if err := write([]byte{socksVersion, want}); err != nil {
		return
	}
	if want == methodPassword && !s.login(r, write) {
		flush()
		return
	}

	var req [4]byte
	if _, err := io.ReadFull(r, req[:]); err != nil {
		return
	}
	var host, kind string
	switch req[3] {
	case atypIPv4, atypIPv6:
		b := make([]byte, 4)
		kind = "ipv4"
		if req[3] == atypIPv6 {
			b, kind = make([]byte, 16), "ipv6"
		}
		if _, err := io.ReadFull(r, b); err != nil {
			return
		}
		a, _ := netip.AddrFromSlice(b)
		host = a.String()
	case atypDomain:
		var n [1]byte
		if _, err := io.ReadFull(r, n[:]); err != nil {
			return
		}
		b := make([]byte, n[0])
		if _, err := io.ReadFull(r, b); err != nil {
			return
		}
		host, kind = string(b), "domain"
	default:
		return
	}
	var port [2]byte
	if _, err := io.ReadFull(r, port[:]); err != nil {
		return
	}
	address := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port[:]))))
	s.log("connect " + kind + " " + address)

	if s.refuse != 0 {
		write(s.replyBytes(s.refuse))
		flush()
		return
	}
	if kind == "domain" {
		target, ok := s.names[host]
		if !ok {
			write(s.replyBytes(0x04))
			flush()
			return
		}
		address = target
	}
	target, err := net.Dial("tcp", address)
	if err != nil {
		// The tests dial only loopback: a failure is a closed port.
		write(s.replyBytes(0x05))
		flush()
		return
	}
	defer target.Close()
	if err := write(s.replyBytes(0)); err != nil {
		return
	}
	if err := flush(); err != nil {
		return
	}
	go func() {
		io.Copy(target, conn)
		target.Close()
	}()
	io.Copy(conn, target)
}

func (s *socksServer) login(r io.Reader, write func([]byte) error) bool {
	var ver [2]byte
	if _, err := io.ReadFull(r, ver[:]); err != nil || ver[0] != passwordVersion {
		return false
	}
	user := make([]byte, ver[1])
	if _, err := io.ReadFull(r, user); err != nil {
		return false
	}
	var n [1]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return false
	}
	password := make([]byte, n[0])
	if _, err := io.ReadFull(r, password); err != nil {
		return false
	}
	s.log("login " + string(user))
	if string(user) != s.user || string(password) != s.password {
		write([]byte{passwordVersion, 0x01})
		return false
	}
	return write([]byte{passwordVersion, 0}) == nil
}

func (s *socksServer) replyBytes(code byte) []byte {
	b := []byte{socksVersion, code, 0}
	if s.bindDomain {
		b = append(b, atypDomain, byte(len("exit.example.com")))
		b = append(b, "exit.example.com"...)
	} else {
		b = append(b, atypIPv4, 192, 0, 2, 1)
	}
	b = append(b, 0x1f, 0x90)
	if code == 0 {
		b = append(b, s.first...)
	}
	return b
}

func contains(b []byte, c byte) bool {
	for _, x := range b {
		if x == c {
			return true
		}
	}
	return false
}
