package exit

import (
	"bufio"
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

// httpProxy is independent of the dialer's parser and request construction. Every target
// must be mapped explicitly to a loopback service: the fixture never resolves a name.
type httpProxy struct {
	user, password string
	targets        map[string]string
	first          string
	headers        string

	mu       sync.Mutex
	requests []*http.Request
}

func (s *httpProxy) seen() []*http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*http.Request(nil), s.requests...)
}

func (s *httpProxy) pipe(t *testing.T) OpenStream {
	t.Helper()
	return func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		t.Cleanup(func() { client.Close(); server.Close() })
		go s.serve(server)
		return client, nil
	}
}

func (s *httpProxy) listen(t *testing.T) string {
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

func (s *httpProxy) serve(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	r := bufio.NewReader(conn)
	req, err := http.ReadRequest(r)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.requests = append(s.requests, req)
	s.mu.Unlock()
	if req.Method != "CONNECT" || req.Proto != "HTTP/1.1" || req.Host != req.RequestURI ||
		req.ContentLength != 0 || len(req.TransferEncoding) != 0 {
		io.WriteString(conn, "HTTP/1.1 400 Invalid request\r\n\r\n")
		return
	}
	if s.user != "" {
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte(s.user+":"+s.password))
		if req.Header.Get("Proxy-Authorization") != want {
			io.WriteString(conn, "HTTP/1.1 407 Auth required\r\nProxy-Authenticate: Basic realm=\"fixture\"\r\n\r\n")
			return
		}
	}
	address, ok := s.targets[req.RequestURI]
	if !ok {
		io.WriteString(conn, "HTTP/1.1 403 Blocked target\r\n\r\n")
		return
	}
	target, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		io.WriteString(conn, "HTTP/1.1 502 Unavailable target\r\n\r\n")
		return
	}
	defer target.Close()
	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n"+s.headers+"\r\n"+s.first); err != nil {
		return
	}
	conn.SetDeadline(time.Time{})
	go func() {
		io.Copy(target, r)
		target.Close()
	}()
	io.Copy(conn, target)
}
