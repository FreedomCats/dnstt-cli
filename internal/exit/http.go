package exit

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// The header budget includes status lines, blank lines and all informational responses.
// It does not limit target traffic after a successful CONNECT.
const (
	httpHeaderLimit        = 32 * 1024
	httpStatusLineLimit    = 4096
	httpInformationalLimit = 8
)

// ErrHTTPProtocol identifies a malformed, non-HTTP or oversized CONNECT response.
// Incomplete responses and failures of the stream do not wrap it.
var ErrHTTPProtocol = errors.New("the exit returned an invalid HTTP CONNECT response")

// HTTPConnectError is a final HTTP refusal of CONNECT, other than authentication (407).
// It carries no peer-supplied text and is separate from SOCKS5 reply codes.
type HTTPConnectError struct {
	StatusCode int
}

func (e *HTTPConnectError) Error() string {
	return fmt.Sprintf("the HTTP exit refused CONNECT (status %d)", e.StatusCode)
}

// HTTP is the exit of a server with an HTTP/1.x CONNECT proxy behind it.
type HTTP struct {
	// Open opens a stream to the server's fixed exit, not a direct proxy socket.
	Open OpenStream
	// User and Password are the Basic credentials (RFC 7617); an empty User omits them.
	User     string
	Password string
}

var _ Dialer = (*HTTP)(nil)

// Dial asks the HTTP exit to connect to address. A complete 407 is a *LoginError, other
// final non-2xx statuses a *HTTPConnectError. The returned connection has no HTTP framing.
func (h *HTTP) Dial(ctx context.Context, address string) (net.Conn, error) {
	authority, err := httpAuthority(address)
	if err != nil {
		return nil, err
	}
	if err := h.checkLogin(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	conn, err := h.open(ctx)
	if err != nil {
		if contextErr := httpContextError(ctx); contextErr != nil {
			err = contextErr
		}
		return nil, httpStreamFailure(err)
	}
	success := false
	defer func() {
		if !success {
			// smux Close may wait to send FIN through a stalled session. Cleanup must not
			// extend the caller's handshake deadline.
			go conn.Close()
		}
	}()
	if err := httpContextError(ctx); err != nil {
		return nil, httpStreamFailure(err)
	}
	// smux reads the write deadline when Write starts. Cancellation expires pending reads
	// and closes the stream; Dial also stops waiting if an in-flight smux write stays stuck.
	d, _ := ctx.Deadline()
	if err := conn.SetDeadline(d); err != nil {
		return nil, httpStreamFailure(err)
	}
	stop := context.AfterFunc(ctx, func() {
		conn.SetDeadline(time.Unix(1, 0))
		conn.Close()
	})
	r := bufio.NewReader(conn)
	exchanged := make(chan error, 1)
	go func() { exchanged <- h.exchange(conn, r, authority) }()
	select {
	case err = <-exchanged:
	case <-ctx.Done():
		// Prefer a complete response already received to the cancellation it raced with.
		select {
		case err = <-exchanged:
		default:
			err = httpStreamFailure(ctx.Err())
		}
	}
	stopped := stop()
	contextErr := httpContextError(ctx)
	if !stopped || contextErr != nil {
		// A complete refusal or proven protocol error still stands. A successful or
		// interrupted exchange cannot return a connection after cancellation won.
		var refusal *HTTPConnectError
		if !IsLogin(err) && !errors.As(err, &refusal) && !errors.Is(err, ErrHTTPProtocol) {
			err = httpStreamFailure(contextErr)
		}
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return nil, httpStreamFailure(err)
	}
	if err := httpContextError(ctx); err != nil {
		return nil, httpStreamFailure(err)
	}
	success = true
	return &httpConn{Conn: conn, reader: r}, nil
}

func httpContextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// A net.Conn deadline can fire just before the context's timer is scheduled.
	if d, ok := ctx.Deadline(); ok && !time.Now().Before(d) {
		return context.DeadlineExceeded
	}
	return nil
}

// open bounds even an OpenStream that returns late. Its result is closed if the caller
// has already left; a cooperative OpenStream also stops its work when ctx ends.
func (h *HTTP) open(ctx context.Context) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	type result struct {
		conn net.Conn
		err  error
	}
	opened := make(chan result)
	go func() {
		conn, err := h.Open(ctx)
		select {
		case opened <- result{conn, err}:
		case <-ctx.Done():
			if conn != nil {
				conn.Close()
			}
		}
	}()
	select {
	case res := <-opened:
		if res.err != nil && res.conn != nil {
			go res.conn.Close()
		}
		return res.conn, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (h *HTTP) checkLogin() error {
	if strings.Contains(h.User, ":") || strings.ContainsFunc(h.User, unicode.IsControl) {
		return errors.New("HTTP exit: invalid user")
	}
	if (h.User == "" && h.Password != "") || strings.ContainsFunc(h.Password, unicode.IsControl) {
		return errors.New("HTTP exit: invalid password")
	}
	return nil
}

// httpAuthority validates without DNS and keeps names for the remote proxy to resolve.
func httpAuthority(address string) (string, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return "", errors.New("HTTP exit: invalid address")
	}
	for _, c := range portText {
		if c < '0' || c > '9' {
			return "", errors.New("HTTP exit: invalid port")
		}
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return "", errors.New("HTTP exit: invalid port")
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.Zone() != "" || (ip.Is4() && strings.HasPrefix(address, "[")) {
			return "", errors.New("HTTP exit: invalid host")
		}
		host = ip.String()
	} else {
		if strings.HasPrefix(address, "[") || !httpHostname(host) {
			return "", errors.New("HTTP exit: invalid host")
		}
	}
	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

func httpHostname(host string) bool {
	name := strings.TrimSuffix(host, ".")
	if len(name) == 0 || len(name) > 253 {
		return false
	}
	numeric := true
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if c >= '0' && c <= '9' {
				continue
			}
			numeric = false
			if c != '-' && !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') {
				return false
			}
		}
	}
	// Do not treat a malformed dotted IPv4 literal as a DNS name.
	return !numeric || !strings.Contains(host, ".")
}

func (h *HTTP) exchange(conn net.Conn, r *bufio.Reader, authority string) error {
	request := "CONNECT " + authority + " HTTP/1.1\r\nHost: " + authority + "\r\n"
	if h.User != "" {
		token := base64.StdEncoding.EncodeToString([]byte(h.User + ":" + h.Password))
		request += "Proxy-Authorization: Basic " + token + "\r\n"
	}
	request += "\r\n"
	n, err := io.WriteString(conn, request)
	if err == nil && n != len(request) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return httpStreamFailure(err)
	}
	remaining := httpHeaderLimit
	for informational := 0; ; informational++ {
		code, err := readHTTPHeader(r, &remaining)
		if err != nil {
			return err
		}
		if code < 200 {
			if informational >= httpInformationalLimit {
				return ErrHTTPProtocol
			}
			continue
		}
		if code == http.StatusProxyAuthRequired {
			return &LoginError{"the HTTP exit refused authentication (status 407)"}
		}
		if code < 300 {
			return nil
		}
		return &HTTPConnectError{StatusCode: code}
	}
}

// readHTTPHeader parses only headers. http.ReadResponse also parses body framing, which
// must be ignored on CONNECT success, including invalid Content-Length/Transfer-Encoding.
func readHTTPHeader(r *bufio.Reader, remaining *int) (int, error) {
	line, err := readHTTPLine(r, remaining, httpStatusLineLimit, true)
	if err != nil {
		return 0, err
	}
	proto, status, ok := strings.Cut(strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r"), " ")
	major, _, valid := http.ParseHTTPVersion(proto)
	if !ok || !valid || major != 1 {
		return 0, ErrHTTPProtocol
	}
	status = strings.TrimLeft(status, " ")
	codeText, _, _ := strings.Cut(status, " ")
	code, err := strconv.Atoi(codeText)
	if len(codeText) != 3 || err != nil || code < 100 || code > 999 {
		return 0, ErrHTTPProtocol
	}
	for _, c := range []byte(status) {
		if (c < 0x20 && c != '\t') || c == 0x7f {
			return 0, ErrHTTPProtocol
		}
	}
	var header bytes.Buffer
	for {
		line, err := readHTTPLine(r, remaining, httpHeaderLimit, false)
		if err != nil {
			return 0, err
		}
		header.Write(line)
		if bytes.Equal(line, []byte("\r\n")) || bytes.Equal(line, []byte("\n")) {
			break
		}
	}
	if _, err := textproto.NewReader(bufio.NewReader(&header)).ReadMIMEHeader(); err != nil {
		// Parser errors may quote response text, challenges or credentials. Never wrap them.
		return 0, ErrHTTPProtocol
	}
	return code, nil
}

func readHTTPLine(r *bufio.Reader, remaining *int, limit int, statusLine bool) ([]byte, error) {
	var line []byte
	if statusLine {
		// A SOCKS or SSH service may wait for more input without sending a newline.
		// Reject a proven non-HTTP prefix without waiting for the rest of a status line.
		for _, want := range []byte("HTTP/") {
			b, err := r.ReadByte()
			if err != nil {
				if err == io.EOF && len(line) != 0 {
					err = io.ErrUnexpectedEOF
				}
				return nil, httpStreamFailure(err)
			}
			*remaining--
			if *remaining < 0 || b != want {
				return nil, ErrHTTPProtocol
			}
			line = append(line, b)
		}
	}
	for {
		part, err := r.ReadSlice('\n')
		*remaining -= len(part)
		if *remaining < 0 || len(line)+len(part) > limit {
			return nil, ErrHTTPProtocol
		}
		line = append(line, part...)
		switch err {
		case nil:
			return line, nil
		case bufio.ErrBufferFull:
			if *remaining == 0 || len(line) == limit {
				return nil, ErrHTTPProtocol
			}
		case io.EOF:
			if len(line) != 0 {
				err = io.ErrUnexpectedEOF
			}
			return nil, httpStreamFailure(err)
		default:
			return nil, httpStreamFailure(err)
		}
	}
}

// Keep the cause available to errors.Is/As without letting a stream error echo request
// bytes or other private input in diagnostics. SOCKS5's existing errors are unchanged.
type httpStreamError struct{ cause error }

func (e *httpStreamError) Error() string { return "HTTP exit: stream failure during CONNECT" }
func (e *httpStreamError) Unwrap() error { return e.cause }
func (e *httpStreamError) Format(s fmt.State, verb rune) {
	fmt.Fprint(s, e.Error())
}

func httpStreamFailure(err error) error { return &httpStreamError{cause: err} }

type httpConn struct {
	net.Conn
	readMu sync.Mutex
	reader *bufio.Reader
}

func (c *httpConn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	return c.reader.Read(p)
}
