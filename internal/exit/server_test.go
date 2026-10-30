package exit

import (
	"context"
	"encoding/binary"
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

	mu   sync.Mutex
	seen []string // what each connection did: "login user", "connect domain host:port"…
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
	var head [2]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil || head[0] != socksVersion {
		return
	}
	methods := make([]byte, head[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}
	want := byte(methodNone)
	if s.user != "" {
		want = methodPassword
	}
	if !contains(methods, want) {
		conn.Write([]byte{socksVersion, methodNoAcceptable})
		return
	}
	if _, err := conn.Write([]byte{socksVersion, want}); err != nil {
		return
	}
	if want == methodPassword && !s.login(conn) {
		return
	}

	var req [4]byte
	if _, err := io.ReadFull(conn, req[:]); err != nil {
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
		if _, err := io.ReadFull(conn, b); err != nil {
			return
		}
		a, _ := netip.AddrFromSlice(b)
		host = a.String()
	case atypDomain:
		var n [1]byte
		if _, err := io.ReadFull(conn, n[:]); err != nil {
			return
		}
		b := make([]byte, n[0])
		if _, err := io.ReadFull(conn, b); err != nil {
			return
		}
		host, kind = string(b), "domain"
	default:
		return
	}
	var port [2]byte
	if _, err := io.ReadFull(conn, port[:]); err != nil {
		return
	}
	address := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port[:]))))
	s.log("connect " + kind + " " + address)

	if s.refuse != 0 {
		s.reply(conn, s.refuse)
		return
	}
	if kind == "domain" {
		target, ok := s.names[host]
		if !ok {
			s.reply(conn, 0x04)
			return
		}
		address = target
	}
	target, err := net.Dial("tcp", address)
	if err != nil {
		// The tests dial only loopback: a failure is a closed port.
		s.reply(conn, 0x05)
		return
	}
	defer target.Close()
	if s.reply(conn, 0) != nil {
		return
	}
	go func() {
		io.Copy(target, conn)
		target.Close()
	}()
	io.Copy(conn, target)
}

func (s *socksServer) login(conn net.Conn) bool {
	var ver [2]byte
	if _, err := io.ReadFull(conn, ver[:]); err != nil || ver[0] != passwordVersion {
		return false
	}
	user := make([]byte, ver[1])
	if _, err := io.ReadFull(conn, user); err != nil {
		return false
	}
	var n [1]byte
	if _, err := io.ReadFull(conn, n[:]); err != nil {
		return false
	}
	password := make([]byte, n[0])
	if _, err := io.ReadFull(conn, password); err != nil {
		return false
	}
	s.log("login " + string(user))
	if string(user) != s.user || string(password) != s.password {
		conn.Write([]byte{passwordVersion, 0x01})
		return false
	}
	_, err := conn.Write([]byte{passwordVersion, 0})
	return err == nil
}

func (s *socksServer) reply(conn net.Conn, code byte) error {
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
	_, err := conn.Write(b)
	return err
}

func contains(b []byte, c byte) bool {
	for _, x := range b {
		if x == c {
			return true
		}
	}
	return false
}
