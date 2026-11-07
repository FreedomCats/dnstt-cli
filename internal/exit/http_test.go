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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xtaci/smux"
)

func TestHTTPAuthorities(t *testing.T) {
	for _, tc := range []struct{ address, want string }{
		{"t.example.com:443", "t.example.com:443"},
		{"Echo.Example.:00007", "Echo.Example.:7"},
		{"localhost:1", "localhost:1"},
		{"192.0.2.1:80", "192.0.2.1:80"},
		{"[2001:db8::1]:65535", "[2001:db8::1]:65535"},
		{"[::ffff:192.0.2.1]:443", "[::ffff:192.0.2.1]:443"},
	} {
		t.Run(tc.address, func(t *testing.T) {
			open := httpResponse(t, "HTTP/1.1 200 OK\r\n\r\n", func(request string) {
				want := "CONNECT " + tc.want + " HTTP/1.1\r\nHost: " + tc.want + "\r\n\r\n"
				if request != want {
					t.Error("CONNECT authority, Host or request framing differs")
				}
			})
			conn, err := (&HTTP{Open: open}).Dial(dialCtx(t), tc.address)
			if err != nil {
				t.Fatal(err)
			}
			conn.Close()
		})
	}
}

func TestHTTPInvalidAuthorities(t *testing.T) {
	for i, address := range []string{
		"", ":80", "t.example.com", "t.example.com:", "t.example.com:0",
		"t.example.com:65536", "t.example.com:-1", "t.example.com:+1", "t.example.com:http",
		"t.example.com: 80", "t.example.com:80\r\nX-Marker: injected",
		" t.example.com:80", "t.example.com :80", "t.\texample.com:80", "t.\nexample.com:80",
		"t.\x00example.com:80", "t.\x7fexample.com:80", "t.\u00a0example.com:80",
		"http://t.example.com:80", "https://t.example.com:443", "user@t.example.com:80",
		"t.example.com/path:80", "t.example.com\\path:80", "t.example.com?query:80",
		"t.example.com#fragment:80", "t%2eexample.com:80", "t.example.com:80/",
		"t.example.com;marker:80", "t.example.com,marker:80", "t.example.com\"marker:80",
		"[2001:db8::1%zone]:80", "[2001:db8::1%25zone]:80", "2001:db8::1:80",
		"[invalid]:80", "[192.0.2.1]:80", "[2001:db8::zzz]:80", "2001:db8::1",
		"192.0.2.999:80", "192.000.2.1:80", "t..example.com:80", "-t.example.com:80",
		"t-.example.com:80", strings.Repeat("x", 64) + ".example:80",
		strings.Repeat("label.", 50) + "example:80",
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			open := func(context.Context) (net.Conn, error) {
				t.Error("invalid authority opened a stream")
				return nil, errors.New("unexpected Open")
			}
			_, err := (&HTTP{Open: open}).Dial(dialCtx(t), address)
			if err == nil || IsLogin(err) || errors.Is(err, ErrHTTPProtocol) {
				t.Fatalf("invalid authority: %v", err)
			}
			if !strings.HasPrefix(err.Error(), "HTTP exit: invalid ") {
				t.Errorf("unexpected validation category: %v", err)
			}
			assertHTTPPrivate(t, err, address)
		})
	}
}

func TestHTTPCredentials(t *testing.T) {
	for _, tc := range []struct{ name, user, password string }{
		{"none", "", ""},
		{"basic", "synthetic-user", "synthetic-password"},
		{"empty password", "synthetic-user", ""},
		{"password colon", "synthetic-user", "synthetic:password"},
		{"spaces and unicode", "synthetic user", "made up \u00e9"},
		{"long credentials", strings.Repeat("u", 300), strings.Repeat("p", 300)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			open := httpResponse(t, "HTTP/1.1 200 OK\r\n\r\n", func(request string) {
				want := "CONNECT t.example.com:443 HTTP/1.1\r\nHost: t.example.com:443\r\n"
				if tc.user != "" {
					want += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(tc.user+":"+tc.password)) + "\r\n"
				}
				want += "\r\n"
				if request != want {
					t.Error("request differs from the exact CONNECT and optional Basic header")
				}
			})
			conn, err := (&HTTP{Open: open, User: tc.user, Password: tc.password}).Dial(dialCtx(t), "t.example.com:443")
			if err != nil {
				t.Fatal(err)
			}
			conn.Close()
		})
	}
}

func TestHTTPInvalidCredentials(t *testing.T) {
	tests := []struct{ user, password, field string }{
		{"", "synthetic-password", "password"},
		{"synthetic:user", "synthetic-password", "user"},
	}
	for _, c := range []rune{0, '\r', '\n', '\t', 0x1f, 0x7f, 0x85, 0x9f} {
		tests = append(tests,
			struct{ user, password, field string }{"synthetic" + string(c) + "user", "synthetic-password", "user"},
			struct{ user, password, field string }{"synthetic-user", "synthetic" + string(c) + "password", "password"})
	}
	for i, tc := range tests {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			open := func(context.Context) (net.Conn, error) {
				t.Error("invalid credentials opened a stream")
				return nil, errors.New("unexpected Open")
			}
			_, err := (&HTTP{Open: open, User: tc.user, Password: tc.password}).Dial(dialCtx(t), "t.example.com:443")
			if err == nil || err.Error() != "HTTP exit: invalid "+tc.field || IsLogin(err) {
				t.Fatalf("invalid credentials: %v", err)
			}
			assertHTTPPrivate(t, err, tc.user, tc.password, base64.StdEncoding.EncodeToString([]byte(tc.user+":"+tc.password)))
		})
	}
}

func TestHTTPProxy(t *testing.T) {
	for _, authenticated := range []bool{false, true} {
		t.Run(fmt.Sprint(authenticated), func(t *testing.T) {
			srv := &httpProxy{targets: map[string]string{"echo.example:7": echoServer(t)}}
			if authenticated {
				srv.user, srv.password = "synthetic-user", "synthetic-password"
			}
			d := &HTTP{Open: srv.pipe(t), User: srv.user, Password: srv.password}
			conn, err := d.Dial(dialCtx(t), "echo.example:7")
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			// If the dialer repeats CONNECT or Proxy-Authorization, the echo differs.
			echo(t, conn, bytes.Repeat([]byte("target data"), 10000))
			reqs := srv.seen()
			if len(reqs) != 1 || reqs[0].RequestURI != "echo.example:7" {
				t.Fatal("the proxy did not receive the reserved target name")
			}
		})
	}
}

func TestHTTPFinalStatuses(t *testing.T) {
	const marker = "malicious-response-marker"
	for _, code := range []int{200, 201, 204, 299, 300, 301, 400, 403, 407, 500, 502, 599, 999} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			response := fmt.Sprintf("HTTP/1.1 %d %s\r\nProxy-Authenticate: Basic realm=\"%s\"\r\nX-Marker: %s\r\n\r\n%s", code, marker, marker, marker, marker)
			open := httpResponse(t, response, nil)
			conn, err := (&HTTP{Open: open, User: "synthetic-user", Password: "synthetic-password"}).Dial(dialCtx(t), "t.example.com:443")
			if code < 300 {
				if err != nil {
					t.Fatal(err)
				}
				conn.Close()
				return
			}
			if err == nil || conn != nil {
				t.Fatal("final refusal returned a connection")
			}
			if code == 407 {
				if !IsLogin(err) || err.Error() != "the HTTP exit refused authentication (status 407)" {
					t.Fatalf("407: %v", err)
				}
			} else {
				var refusal *HTTPConnectError
				if !errors.As(err, &refusal) || refusal.StatusCode != code || IsLogin(err) {
					t.Fatalf("status %d: %v", code, err)
				}
			}
			var socks *ConnectError
			if errors.As(err, &socks) || errors.Is(err, ErrHTTPProtocol) {
				t.Errorf("HTTP refusal has the wrong category: %v", err)
			}
			assertHTTPPrivate(t, err, marker, "synthetic-user", "synthetic-password", "c3ludGhldGljLXVzZXI6c3ludGhldGljLXBhc3N3b3Jk")
		})
	}
}

func TestHTTPProtocolErrors(t *testing.T) {
	const marker = "malicious-response-marker"
	for _, tc := range []struct{ name, response string }{
		{"not HTTP", marker + "\r\n\r\n"},
		{"binary protocol", "\x05\x00"},
		{"HTTP2", "HTTP/2.0 200 " + marker + "\r\n\r\n"},
		{"bad version", "HTTP/1.X 200 " + marker + "\r\n\r\n"},
		{"no status", "HTTP/1.1 " + marker + "\r\n\r\n"},
		{"short status", "HTTP/1.1 20 " + marker + "\r\n\r\n"},
		{"bad status", "HTTP/1.1 2xx " + marker + "\r\n\r\n"},
		{"zero status", "HTTP/1.1 000 " + marker + "\r\n\r\n"},
		{"reason control", "HTTP/1.1 200 " + marker + "\x00\r\n\r\n"},
		{"bad header", "HTTP/1.1 200 OK\r\n" + marker + "\r\n\r\n"},
		{"bad header name", "HTTP/1.1 200 OK\r\nX-" + marker + "\x00: value\r\n\r\n"},
		{"bad header value", "HTTP/1.1 200 OK\r\nX-Marker: " + marker + "\x7f\r\n\r\n"},
		{"malformed 407", "HTTP/1.1 407 " + marker + "\r\n" + marker + "\r\n\r\n"},
		{"long status", "HTTP/1.1 200 " + strings.Repeat("x", httpStatusLineLimit) + "\r\n\r\n"},
		{"long header", "HTTP/1.1 200 OK\r\nX-Marker: " + strings.Repeat("x", httpHeaderLimit) + "\r\n\r\n"},
		{"many headers", "HTTP/1.1 200 OK\r\n" + strings.Repeat("X-Marker: "+marker+"\r\n", httpHeaderLimit/20) + "\r\n"},
		{"too many informational", strings.Repeat("HTTP/1.1 100 Continue\r\n\r\n", httpInformationalLimit+1) + "HTTP/1.1 200 OK\r\n\r\n"},
		{"total informational headers", strings.Repeat("HTTP/1.1 103 Early Hints\r\nX-Marker: "+strings.Repeat("x", httpHeaderLimit/2)+"\r\n\r\n", 3) + "HTTP/1.1 200 OK\r\n\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (&HTTP{Open: httpResponse(t, tc.response, nil)}).Dial(dialCtx(t), "t.example.com:443")
			if !errors.Is(err, ErrHTTPProtocol) || IsLogin(err) {
				t.Fatalf("got %v, want ErrHTTPProtocol", err)
			}
			assertHTTPPrivate(t, err, marker, tc.response)
		})
	}
}

func TestHTTPTruncatedResponses(t *testing.T) {
	for i, response := range []string{
		"", "H", "HTTP/1.1", "HTTP/1.1 200 OK", "HTTP/1.1 200 OK\r\n",
		"HTTP/1.1 200 OK\r\nX-Marker: truncated", "HTTP/1.1 200 OK\r\nX-Marker: truncated\r\n",
		"HTTP/1.1 407 Auth required\r\n", "HTTP/1.1 407 Auth required\r\nProxy-Authenticate: Basic\r\n",
		"HTTP/1.1 403 Forbidden\r\nX-Marker: truncated",
		"HTTP/1.1 100 Continue\r\n\r\n",
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			_, err := (&HTTP{Open: httpClosedResponse(t, response)}).Dial(dialCtx(t), "t.example.com:443")
			if err == nil || IsLogin(err) || errors.Is(err, ErrHTTPProtocol) ||
				(!errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF)) {
				t.Fatalf("truncation: %v", err)
			}
			var refusal *HTTPConnectError
			if errors.As(err, &refusal) {
				t.Error("an incomplete response is a refusal")
			}
			assertHTTPPrivate(t, err, "truncated", response)
		})
	}
}

func TestHTTPInformationalAndVersions(t *testing.T) {
	for _, version := range []string{"HTTP/1.0", "HTTP/1.1", "HTTP/1.9"} {
		t.Run(version, func(t *testing.T) {
			response := strings.Repeat("HTTP/1.1 103 Early Hints\r\nLink: <https://t.example.com/>\r\n\r\n", httpInformationalLimit) + version + " 200 OK\r\n\r\nbanner"
			conn, err := (&HTTP{Open: httpResponse(t, response, nil)}).Dial(dialCtx(t), "t.example.com:443")
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			got := make([]byte, len("banner"))
			_, err = io.ReadFull(conn, got)
			if err != nil || string(got) != "banner" {
				t.Fatalf("after informational responses: %d bytes, %v", len(got), err)
			}
		})
	}
	_, err := (&HTTP{Open: httpResponse(t, "HTTP/1.1 100 Continue\r\n\r\nHTTP/1.1 407 Auth required\r\n\r\n", nil)}).Dial(dialCtx(t), "t.example.com:443")
	if !IsLogin(err) {
		t.Fatalf("407 after informational response: %v", err)
	}
}

func TestHTTPHeaderBudgetBoundary(t *testing.T) {
	for _, extra := range []int{0, 1} {
		t.Run(fmt.Sprint(extra), func(t *testing.T) {
			prefix, suffix := "HTTP/1.1 200 OK\r\nX-Padding: ", "\r\n\r\n"
			response := prefix + strings.Repeat("x", httpHeaderLimit-len(prefix)-len(suffix)+extra) + suffix + "target"
			conn, err := (&HTTP{Open: httpResponse(t, response, nil)}).Dial(dialCtx(t), "t.example.com:443")
			if extra != 0 {
				if !errors.Is(err, ErrHTTPProtocol) {
					t.Fatalf("over budget: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			got := make([]byte, len("target"))
			_, err = io.ReadFull(conn, got)
			if err != nil || string(got) != "target" {
				t.Fatalf("at budget: %d target bytes, %v", len(got), err)
			}
		})
	}
}

func TestHTTPStatusLineBoundary(t *testing.T) {
	for _, extra := range []int{0, 1} {
		prefix := "HTTP/1.1 200 "
		response := prefix + strings.Repeat("x", httpStatusLineLimit-len(prefix)-2+extra) + "\r\n\r\n"
		conn, err := (&HTTP{Open: httpResponse(t, response, nil)}).Dial(dialCtx(t), "t.example.com:443")
		if extra != 0 {
			if !errors.Is(err, ErrHTTPProtocol) {
				t.Fatalf("over status-line budget: %v", err)
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			conn.Close()
		}
	}
}

func TestHTTPTargetBytesAndBodyFraming(t *testing.T) {
	for _, headers := range []string{
		"", "Content-Length: 0\r\n", "Content-Length: 1\r\n", "Transfer-Encoding: chunked\r\n",
		"Content-Length: invalid\r\nContent-Length: conflicting\r\nTransfer-Encoding: unsupported\r\nTrailer: X-Marker\r\nConnection: close\r\n",
	} {
		t.Run(fmt.Sprint(len(headers)), func(t *testing.T) {
			srv := &httpProxy{
				targets: map[string]string{"echo.example:7": echoServer(t)},
				first:   "SSH-2.0-banner\r\n0\r\n\r\n",
				headers: headers,
			}
			conn, err := (&HTTP{Open: srv.pipe(t)}).Dial(dialCtx(t), "echo.example:7")
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			first := make([]byte, len(srv.first))
			if _, err := io.ReadFull(conn, first); err != nil || string(first) != srv.first {
				t.Fatalf("early target bytes: %d bytes, %v", len(first), err)
			}
			// Payload well beyond the handshake budget is not passed through a limited reader.
			echo(t, conn, bytes.Repeat([]byte("raw target traffic"), 10000))
		})
	}
}

func TestHTTPWriteFailures(t *testing.T) {
	failed := errors.New("malicious-write-marker synthetic-user synthetic-password")
	for _, tc := range []struct {
		name string
		err  error
		n    int
	}{
		{"zero write", nil, 0}, {"short write", nil, 7},
		{"failed write", failed, 0}, {"partial failed write", failed, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newHTTPTestConn("")
			c.write = func(p []byte) (int, error) {
				if c.deadline.IsZero() {
					t.Error("Write started without a deadline")
				}
				return tc.n, tc.err
			}
			_, err := (&HTTP{Open: c.open, User: "synthetic-user", Password: "synthetic-password"}).Dial(dialCtx(t), "t.example.com:443")
			want := tc.err
			if want == nil {
				want = io.ErrShortWrite
			}
			if !errors.Is(err, want) || IsLogin(err) || errors.Is(err, ErrHTTPProtocol) {
				t.Fatalf("write failure: %v", err)
			}
			awaitHTTP(t, c.closed, "failed stream closure")
			assertHTTPPrivate(t, err, "malicious-write-marker", "synthetic-user", "synthetic-password")
		})
	}
}

func TestHTTPStreamFailures(t *testing.T) {
	failed := errors.New("malicious-stream-marker")
	for _, tc := range []struct{ name, response string }{
		{"no response", ""}, {"status interruption", "HTTP/1.1 200"},
		{"header interruption", "HTTP/1.1 407 Auth required\r\nProxy-Authenticate: Basic"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newHTTPTestConn(tc.response)
			c.readErr = failed
			_, err := (&HTTP{Open: c.open}).Dial(dialCtx(t), "t.example.com:443")
			if !errors.Is(err, failed) || IsLogin(err) || errors.Is(err, ErrHTTPProtocol) {
				t.Fatalf("stream interruption: %v", err)
			}
			awaitHTTP(t, c.closed, "interrupted stream closure")
			assertHTTPPrivate(t, err, "malicious-stream-marker")
		})
	}
}

func TestHTTPFailureClosesStream(t *testing.T) {
	for _, response := range []string{
		"HTTP/1.1 407 Auth required\r\n\r\n", "HTTP/1.1 403 Forbidden\r\n\r\n",
		"not HTTP\r\n\r\n", "HTTP/1.1 200 OK\r\nX-Truncated: value",
		"HTTP/1.1 200 OK\r\nX-Long: " + strings.Repeat("x", httpHeaderLimit) + "\r\n\r\n",
	} {
		c := newHTTPTestConn(response)
		if conn, err := (&HTTP{Open: c.open}).Dial(dialCtx(t), "t.example.com:443"); err == nil || conn != nil {
			t.Fatal("failed handshake returned a connection")
		}
		awaitHTTP(t, c.closed, "failed stream closure")
	}
}

func TestHTTPDeadlineFailures(t *testing.T) {
	failed := errors.New("malicious-deadline-marker")
	for _, clear := range []bool{false, true} {
		t.Run(fmt.Sprint(clear), func(t *testing.T) {
			c := newHTTPTestConn("HTTP/1.1 200 OK\r\n\r\n")
			c.setDeadline = func(d time.Time) error {
				if d.IsZero() == clear {
					return failed
				}
				return nil
			}
			_, err := (&HTTP{Open: c.open}).Dial(dialCtx(t), "t.example.com:443")
			if !errors.Is(err, failed) {
				t.Fatalf("deadline failure: %v", err)
			}
			if !clear && c.written.Len() != 0 {
				t.Error("request sent without successfully setting a deadline")
			}
			awaitHTTP(t, c.closed, "deadline failure closure")
			assertHTTPPrivate(t, err, "malicious-deadline-marker")
		})
	}
}

func TestHTTPConnectionLifetime(t *testing.T) {
	c := newHTTPTestConn("HTTP/1.1 200 OK\r\n\r\nearly")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := (&HTTP{Open: c.open}).Dial(ctx, "t.example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if !c.deadline.IsZero() {
		t.Error("handshake deadline was not cleared")
	}
	cancel()
	select {
	case <-c.closed:
		t.Fatal("cancel after success closed the stream")
	default:
	}
	got, err := io.ReadAll(conn)
	if err != nil || string(got) != "early" {
		t.Fatalf("buffered target bytes: %d, %v", len(got), err)
	}
	d := time.Now().Add(10 * time.Second)
	conn.SetDeadline(d)
	conn.SetReadDeadline(d.Add(time.Second))
	conn.SetWriteDeadline(d.Add(2 * time.Second))
	if !c.deadline.Equal(d) || !c.readDeadline.Equal(d.Add(time.Second)) || !c.writeDeadline.Equal(d.Add(2*time.Second)) {
		t.Error("connection wrapper did not forward deadlines")
	}
	if conn.LocalAddr() != c.LocalAddr() || conn.RemoteAddr() != c.RemoteAddr() {
		t.Error("connection wrapper did not forward addresses")
	}
	conn.Close()
	awaitHTTP(t, c.closed, "successful connection closure")
}

func TestHTTPConcurrentReads(t *testing.T) {
	var payload []byte
	for value := range 256 {
		payload = append(payload, bytes.Repeat([]byte{byte(value)}, value+1)...)
	}
	open := httpResponse(t, "HTTP/1.1 200 OK\r\n\r\n"+string(payload), nil)
	conn, err := (&HTTP{Open: open}).Dial(dialCtx(t), "t.example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	type result struct {
		counts [256]int
		err    error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-start
			var res result
			var b [1]byte
			for range len(payload) / 2 {
				if _, err := io.ReadFull(conn, b[:]); err != nil {
					res.err = err
					break
				}
				res.counts[b[0]]++
			}
			results <- res
		}()
	}
	close(start)
	var counts [256]int
	for range 2 {
		res := <-results
		if res.err != nil {
			t.Fatalf("concurrent target read: %v", res.err)
		}
		for value, n := range res.counts {
			counts[value] += n
		}
	}
	// The readers share both read-ahead bytes and later buffer fills. Their allocation of
	// bytes is unspecified, but every byte must arrive exactly once across the two readers.
	for value, n := range counts {
		if want := value + 1; n != want {
			t.Errorf("target byte %d: got %d copies, want %d", value, n, want)
		}
	}
}

func TestHTTPReadInterrupted(t *testing.T) {
	for _, operation := range []string{"close", "deadline", "read deadline"} {
		t.Run(operation, func(t *testing.T) {
			open := httpResponse(t, "HTTP/1.1 200 OK\r\n\r\n", nil)
			var stream *httpObservedConn
			observedOpen := func(ctx context.Context) (net.Conn, error) {
				conn, err := open(ctx)
				if err != nil {
					return nil, err
				}
				stream = &httpObservedConn{Conn: conn, closed: make(chan struct{})}
				return stream, nil
			}
			conn, err := (&HTTP{Open: observedOpen}).Dial(dialCtx(t), "t.example.com:443")
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			reading := make(chan struct{})
			stream.beforeRead = func() { close(reading) }
			readResult := make(chan error, 1)
			go func() {
				var b [1]byte
				_, err := conn.Read(b[:])
				readResult <- err
			}()
			awaitHTTP(t, reading, "blocked target read")
			interrupted := make(chan struct{})
			go func() {
				switch operation {
				case "close":
					conn.Close()
				case "deadline":
					conn.SetDeadline(time.Now().Add(-time.Second))
				case "read deadline":
					conn.SetReadDeadline(time.Now().Add(-time.Second))
				}
				close(interrupted)
			}()
			awaitHTTP(t, interrupted, operation+" during Read")
			select {
			case err := <-readResult:
				if err == nil {
					t.Error("interrupted read succeeded")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("target Read stayed blocked")
			}
		})
	}
}

func TestHTTPCancelAfterSuccess(t *testing.T) {
	srv := &httpProxy{targets: map[string]string{"echo.example:7": echoServer(t)}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	conn, err := (&HTTP{Open: srv.pipe(t)}).Dial(ctx, "echo.example:7")
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	echo(t, conn, []byte("connection survives cancellation"))
}

func TestHTTPContextDuringIO(t *testing.T) {
	for _, stage := range []string{"write", "silent proxy"} {
		for _, deadline := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/deadline=%t", stage, deadline), func(t *testing.T) {
				client, server := net.Pipe()
				t.Cleanup(func() { client.Close(); server.Close() })
				var ctx context.Context
				var cancel context.CancelFunc
				if deadline {
					ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
				} else {
					ctx, cancel = context.WithCancel(context.Background())
				}
				defer cancel()
				started := make(chan struct{})
				closed := make(chan struct{})
				c := &httpObservedConn{Conn: client, closed: closed}
				if stage == "write" {
					c.beforeWrite = func() { close(started) }
				} else {
					c.beforeRead = func() { close(started) }
				}
				open := func(context.Context) (net.Conn, error) {
					return c, nil
				}
				if stage == "silent proxy" {
					go io.Copy(io.Discard, server)
				}
				if !deadline {
					go func() { <-started; cancel() }()
				}
				_, err := (&HTTP{Open: open}).Dial(ctx, "t.example.com:443")
				want := context.Canceled
				if deadline {
					want = context.DeadlineExceeded
				}
				if !errors.Is(err, want) || IsLogin(err) || errors.Is(err, ErrHTTPProtocol) {
					t.Fatalf("interrupted %s: %v", stage, err)
				}
				awaitHTTP(t, started, "handshake I/O")
				awaitHTTP(t, closed, "interrupted stream closure")
				server.SetReadDeadline(time.Now().Add(5 * time.Second))
				if stage == "write" {
					if _, err := server.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
						t.Fatalf("stream was not closed: %v", err)
					}
				}
			})
		}
	}
}

func TestHTTPStalledSMUX(t *testing.T) {
	for _, stage := range []string{"open", "write"} {
		t.Run(stage, func(t *testing.T) {
			raw, peer := net.Pipe()
			mux, err := smux.Client(raw, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { peer.Close(); mux.Close() })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered := make(chan struct{})
			closed := make(chan struct{})
			open := func(context.Context) (net.Conn, error) {
				if stage == "open" {
					close(entered)
				}
				stream, err := mux.OpenStream()
				if err != nil {
					return nil, err
				}
				return &httpObservedConn{Conn: stream, beforeWrite: func() { close(entered) }, closed: closed}, nil
			}
			if stage == "write" {
				// Let OpenStream send its SYN, then stop consuming the session's writes.
				go io.CopyN(io.Discard, peer, 8)
			}
			go func() { <-entered; cancel() }()
			result := make(chan error, 1)
			go func() {
				_, err := (&HTTP{Open: open}).Dial(ctx, "t.example.com:443")
				result <- err
			}()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("stalled smux %s: %v", stage, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Dial stayed blocked in a stalled smux session")
			}
			if stage == "write" {
				awaitHTTP(t, closed, "stalled smux stream closure")
			}
		})
	}
}

func TestHTTPOpenContext(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprint(deadline), func(t *testing.T) {
			var ctx context.Context
			var cancel context.CancelFunc
			if deadline {
				ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
			} else {
				ctx, cancel = context.WithCancel(context.Background())
			}
			defer cancel()
			open := func(ctx context.Context) (net.Conn, error) {
				d, ok := ctx.Deadline()
				if !ok || time.Until(d) > handshakeTimeout {
					t.Error("Open did not receive the handshake deadline")
				}
				if !deadline {
					cancel()
				}
				<-ctx.Done()
				return nil, ctx.Err()
			}
			_, err := (&HTTP{Open: open}).Dial(ctx, "t.example.com:443")
			want := context.Canceled
			if deadline {
				want = context.DeadlineExceeded
			}
			if !errors.Is(err, want) {
				t.Fatalf("stream acquisition: %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	open := func(context.Context) (net.Conn, error) {
		t.Error("Open called with canceled context")
		return nil, errors.New("unexpected Open")
	}
	if _, err := (&HTTP{Open: open}).Dial(ctx, "t.example.com:443"); !errors.Is(err, context.Canceled) {
		t.Fatalf("already canceled: %v", err)
	}
}

func TestHTTPOpenLateResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	c := newHTTPTestConn("")
	open := func(context.Context) (net.Conn, error) {
		cancel()
		<-release
		return c, nil
	}
	_, err := (&HTTP{Open: open}).Dial(ctx, "t.example.com:443")
	close(release)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("late stream acquisition: %v", err)
	}
	awaitHTTP(t, c.closed, "late stream closure")
}

func TestHTTPOpenFailure(t *testing.T) {
	failed := errors.New("malicious-open-marker")
	c := newHTTPTestConn("")
	open := func(context.Context) (net.Conn, error) { return c, failed }
	_, err := (&HTTP{Open: open}).Dial(dialCtx(t), "t.example.com:443")
	if !errors.Is(err, failed) {
		t.Fatalf("Open failure: %v", err)
	}
	awaitHTTP(t, c.closed, "failed Open result closure")
	assertHTTPPrivate(t, err, "malicious-open-marker")
}

func TestHTTPCancelOnCompletion(t *testing.T) {
	for _, response := range []string{"HTTP/1.1 200 OK\r\n\r\n", "HTTP/1.1 407 Auth required\r\n\r\n"} {
		ctx, cancel := context.WithCancel(context.Background())
		c := newHTTPTestConn(response)
		c.afterRead = cancel
		conn, err := (&HTTP{Open: c.open}).Dial(ctx, "t.example.com:443")
		cancel()
		if conn != nil || err == nil {
			t.Fatal("cancellation racing with completion returned a connection")
		}
		if !errors.Is(err, context.Canceled) && !IsLogin(err) {
			t.Fatalf("completion race: %v", err)
		}
		awaitHTTP(t, c.closed, "completion race closure")
	}
}

// httpResponse accepts a request as raw lines, independently of the production request
// code, then writes the whole response at once (including possible target bytes).
func httpResponse(t *testing.T, response string, inspect func(string)) OpenStream {
	t.Helper()
	return httpResponseStream(t, response, inspect, false)
}

func httpClosedResponse(t *testing.T, response string) OpenStream {
	t.Helper()
	return httpResponseStream(t, response, nil, true)
}

func httpResponseStream(t *testing.T, response string, inspect func(string), closeAfter bool) OpenStream {
	t.Helper()
	return func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		t.Cleanup(func() { client.Close(); server.Close() })
		go func() {
			defer server.Close()
			server.SetDeadline(time.Now().Add(5 * time.Second))
			r := bufio.NewReader(server)
			var request strings.Builder
			for {
				line, err := r.ReadString('\n')
				if err != nil {
					return
				}
				request.WriteString(line)
				if line == "\r\n" {
					break
				}
			}
			if inspect != nil {
				inspect(request.String())
			}
			io.WriteString(server, response)
			if !closeAfter {
				io.Copy(io.Discard, r)
			}
		}()
		return client, nil
	}
}

func assertHTTPPrivate(t *testing.T, err error, private ...string) {
	t.Helper()
	for _, text := range private {
		if len(text) < 4 {
			continue
		}
		for _, diagnostic := range []string{fmt.Sprint(err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
			if strings.Contains(diagnostic, text) {
				t.Error("an error diagnostic contains private input")
			}
		}
	}
}

func awaitHTTP(t *testing.T, done <-chan struct{}, operation string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", operation)
	}
}

// httpTestConn controls stream errors and observes connection forwarding. Only Close is
// called concurrently with the handshake; the remaining fields are read after Dial ends.
type httpTestConn struct {
	reader        *strings.Reader
	readErr       error
	afterRead     func()
	written       bytes.Buffer
	write         func([]byte) (int, error)
	deadline      time.Time
	readDeadline  time.Time
	writeDeadline time.Time
	setDeadline   func(time.Time) error
	closed        chan struct{}
	closeOnce     sync.Once
}

func newHTTPTestConn(response string) *httpTestConn {
	return &httpTestConn{reader: strings.NewReader(response), closed: make(chan struct{})}
}

func (c *httpTestConn) open(context.Context) (net.Conn, error) { return c, nil }
func (c *httpTestConn) Read(p []byte) (int, error) {
	n, err := c.reader.Read(p)
	if n != 0 && c.afterRead != nil {
		c.afterRead()
	}
	if err == io.EOF && c.readErr != nil {
		err = c.readErr
	}
	return n, err
}
func (c *httpTestConn) Write(p []byte) (int, error) {
	if c.write != nil {
		return c.write(p)
	}
	return c.written.Write(p)
}
func (c *httpTestConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}
func (c *httpTestConn) LocalAddr() net.Addr  { return httpTestAddr("local") }
func (c *httpTestConn) RemoteAddr() net.Addr { return httpTestAddr("remote") }
func (c *httpTestConn) SetDeadline(d time.Time) error {
	c.deadline = d
	if c.setDeadline != nil {
		return c.setDeadline(d)
	}
	return nil
}
func (c *httpTestConn) SetReadDeadline(d time.Time) error  { c.readDeadline = d; return nil }
func (c *httpTestConn) SetWriteDeadline(d time.Time) error { c.writeDeadline = d; return nil }

type httpTestAddr string

func (a httpTestAddr) Network() string { return "test" }
func (a httpTestAddr) String() string  { return string(a) }

var _ net.Conn = (*httpTestConn)(nil)

type httpObservedConn struct {
	net.Conn
	beforeWrite func()
	beforeRead  func()
	closed      chan struct{}
	closeOnce   sync.Once
}

func (c *httpObservedConn) Write(p []byte) (int, error) {
	if c.beforeWrite != nil {
		c.beforeWrite()
	}
	return c.Conn.Write(p)
}

func (c *httpObservedConn) Read(p []byte) (int, error) {
	if c.beforeRead != nil {
		c.beforeRead()
	}
	return c.Conn.Read(p)
}

func (c *httpObservedConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return c.Conn.Close()
}
