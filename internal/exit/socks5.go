package exit

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
)

// SOCKS5 client by RFC 1928 (CONNECT only) and RFC 1929 (username and password).
//
// The messages go one at a time, each after the answer to the previous one, although the
// tunnel is slow and every round trip costs. Sending the greeting, the login and the
// request in one write saves two round trips, but a server that reads one message per recv
// drops the rest and hangs. Whether danted takes it is to be seen live (plan.md S8).

const socksVersion = 5

const (
	methodNone         = 0x00
	methodPassword     = 0x02
	methodNoAcceptable = 0xff

	// passwordVersion is the version of the RFC 1929 subnegotiation.
	passwordVersion = 0x01
)

const (
	cmdConnect = 0x01

	atypIPv4   = 0x01
	atypDomain = 0x03
	atypIPv6   = 0x04
)

// handshakeTimeout bounds the whole exchange with the exit, the wait for a stream and the
// exit's own connect to the target included: a silent exit does not hold a connection of an app forever. Every
// message is a round trip through DNS, so it is generous.
const handshakeTimeout = 60 * time.Second

// ErrNotSOCKS5 is wrapped by the errors of an exit that does not answer as a SOCKS5 server:
// the dnstt server hands its streams to something else.
var ErrNotSOCKS5 = errors.New("the exit does not answer as a SOCKS5 server")

// ConnectError is a refusal of the exit to connect to an address: the exit and the tunnel
// work, the target does not. The datapath passes Code on to the app.
type ConnectError struct {
	// Address is the host:port asked for.
	Address string
	// Code is the reply of the exit (RFC 1928 §6), never 0.
	Code byte
}

func (e *ConnectError) Error() string {
	return fmt.Sprintf("the exit cannot connect to %s: %s", e.Address, replyText(e.Code))
}

// replyText is the meaning of a reply code (RFC 1928 §6).
func replyText(code byte) string {
	switch code {
	case 0x01:
		return "general failure"
	case 0x02:
		return "not allowed by the exit's rules"
	case 0x03:
		return "network unreachable"
	case 0x04:
		return "host unreachable"
	case 0x05:
		return "connection refused"
	case 0x06:
		return "TTL expired"
	case 0x07:
		return "command not supported"
	case 0x08:
		return "address type not supported"
	}
	return fmt.Sprintf("reply code %#02x", code)
}

// SOCKS5 is the exit of a server with a SOCKS5 proxy behind it (danted).
type SOCKS5 struct {
	// Open opens the stream every connection goes through.
	Open OpenStream
	// User and Password are the login of the exit (RFC 1929); an empty User is
	// authentication "none".
	User     string
	Password string
}

var _ Dialer = (*SOCKS5)(nil)

// Dial asks the exit to connect to address. A refusal of the login is a *LoginError, a
// refusal of the address a *ConnectError; any other error is of the stream or the session.
func (s *SOCKS5) Dial(ctx context.Context, address string) (net.Conn, error) {
	req, err := connectRequest(address)
	if err != nil {
		return nil, err
	}
	return s.handshake(ctx, address, req)
}

// Check opens a stream and logs in, without asking for a connection: whether the exit is a
// SOCKS5 server and lets us in. The supervisor checks it on a new session, so a wrong
// password is one clear error at once, not a failure of every connection.
func (s *SOCKS5) Check(ctx context.Context) error {
	conn, err := s.handshake(ctx, "", nil)
	if conn != nil {
		conn.Close()
	}
	return err
}

// handshake opens a stream, logs in and, with a request, asks for the connection. The
// stream it returns has no deadlines left.
func (s *SOCKS5) handshake(ctx context.Context, address string, req []byte) (net.Conn, error) {
	if err := s.checkLogin(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	conn, err := s.Open(ctx)
	if err != nil {
		return nil, err
	}
	// An ended ctx breaks the exchange: a deadline in the past ends the read in progress.
	// A smux stream reads its write deadline only when a Write starts, so the deadline of
	// ctx is set beforehand too: a write stuck in a stalled session ends with it.
	d, _ := ctx.Deadline()
	conn.SetDeadline(d)
	stop := context.AfterFunc(ctx, func() { conn.SetDeadline(time.Unix(1, 0)) })

	err = s.exchange(conn, address, req)
	if !stop() {
		// ctx ended, and its deadline may have come after the exchange: the stream is not
		// usable. An answer of the exit that came before is still the answer.
		conn.Close()
		var le *LoginError
		var ce *ConnectError
		if !errors.As(err, &le) && !errors.As(err, &ce) {
			err = fmt.Errorf("SOCKS5 exit: %w", context.Cause(ctx))
		}
		return nil, err
	}
	if err != nil {
		conn.Close()
		return nil, err
	}
	conn.SetDeadline(time.Time{})
	return conn, nil
}

func (s *SOCKS5) checkLogin() error {
	if len(s.User) > 255 || len(s.Password) > 255 {
		return errors.New("SOCKS5 exit: a login or password longer than 255 bytes")
	}
	if s.User == "" && s.Password != "" {
		return errors.New("SOCKS5 exit: a password without a login")
	}
	return nil
}

func (s *SOCKS5) exchange(conn net.Conn, address string, req []byte) error {
	// The greeting offers "none" in any case: an exit that lets anyone in picks it, and
	// the password is not sent at all.
	greeting := []byte{socksVersion, 1, methodNone}
	if s.User != "" {
		greeting = []byte{socksVersion, 2, methodNone, methodPassword}
	}
	if _, err := conn.Write(greeting); err != nil {
		return streamError(err)
	}
	var choice [2]byte
	if _, err := io.ReadFull(conn, choice[:]); err != nil {
		return streamError(err)
	}
	if choice[0] != socksVersion {
		return fmt.Errorf("%w (version %d in the answer to the greeting)", ErrNotSOCKS5, choice[0])
	}
	switch choice[1] {
	case methodNone:
	case methodPassword:
		if s.User == "" {
			return fmt.Errorf("%w (it chose a login, which was not offered)", ErrNotSOCKS5)
		}
		if err := s.login(conn); err != nil {
			return err
		}
	case methodNoAcceptable:
		if s.User == "" {
			return &LoginError{"the SOCKS5 exit requires a login, and the profile has none"}
		}
		return &LoginError{"the SOCKS5 exit accepts neither a login nor connections without one"}
	default:
		return fmt.Errorf("%w (it chose authentication method %#02x, which was not offered)",
			ErrNotSOCKS5, choice[1])
	}
	if req == nil {
		return nil
	}

	if _, err := conn.Write(req); err != nil {
		return streamError(err)
	}
	var head [4]byte
	if _, err := io.ReadFull(conn, head[:2]); err != nil {
		return streamError(err)
	}
	if head[0] != socksVersion {
		return fmt.Errorf("%w (version %d in the reply to CONNECT)", ErrNotSOCKS5, head[0])
	}
	if head[1] != 0 {
		// The stream is not used after a refusal: the rest of the reply, which some
		// servers cut short before closing, is not waited for.
		return &ConnectError{Address: address, Code: head[1]}
	}
	// The bound address is read off to the end, so the first byte of the stream that
	// follows is the target's.
	if _, err := io.ReadFull(conn, head[2:]); err != nil {
		return streamError(err)
	}
	var n int
	switch head[3] {
	case atypIPv4:
		n = 4
	case atypIPv6:
		n = 16
	case atypDomain:
		var l [1]byte
		if _, err := io.ReadFull(conn, l[:]); err != nil {
			return streamError(err)
		}
		n = int(l[0])
	default:
		return fmt.Errorf("%w (address type %#02x in the reply to CONNECT)", ErrNotSOCKS5, head[3])
	}
	if _, err := io.ReadFull(conn, make([]byte, n+2)); err != nil {
		return streamError(err)
	}
	return nil
}

// login is the RFC 1929 subnegotiation.
func (s *SOCKS5) login(conn net.Conn) error {
	b := make([]byte, 0, 3+len(s.User)+len(s.Password))
	b = append(b, passwordVersion, byte(len(s.User)))
	b = append(b, s.User...)
	b = append(b, byte(len(s.Password)))
	b = append(b, s.Password...)
	if _, err := conn.Write(b); err != nil {
		return streamError(err)
	}
	var status [2]byte
	if _, err := io.ReadFull(conn, status[:]); err != nil {
		// A stream closed instead of an answer is no proof of a refusal: the session may
		// have ended.
		return streamError(err)
	}
	if status[0] != passwordVersion {
		return fmt.Errorf("%w (version %d in the answer to the login)", ErrNotSOCKS5, status[0])
	}
	if status[1] != 0 {
		return &LoginError{"the SOCKS5 exit refused the login: wrong user name or password"}
	}
	return nil
}

// streamError is a failure of the stream in the middle of the exchange. An exit that
// closes the stream at once is most likely not a SOCKS5 server, but it may as well be the
// session ending, so it is no reason to stop.
func streamError(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("SOCKS5 exit: the stream closed in the middle of the handshake: %w", err)
	}
	return fmt.Errorf("SOCKS5 exit: %w", err)
}

// connectRequest is the CONNECT request for host:port. A host that parses as an address
// goes as an address, anything else as a name.
func connectRequest(address string) ([]byte, error) {
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("SOCKS5 exit: %w", err)
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil || port == 0 {
		return nil, fmt.Errorf("SOCKS5 exit: bad port in %q", address)
	}
	req := []byte{socksVersion, cmdConnect, 0}
	if a, err := netip.ParseAddr(host); err == nil {
		if a.Zone() != "" {
			return nil, fmt.Errorf("SOCKS5 exit: an address with a zone in %q", address)
		}
		if a = a.Unmap(); a.Is4() {
			req = append(req, atypIPv4)
		} else {
			req = append(req, atypIPv6)
		}
		req = append(req, a.AsSlice()...)
	} else {
		if host == "" || len(host) > 255 {
			return nil, fmt.Errorf("SOCKS5 exit: bad host name in %q", address)
		}
		req = append(req, atypDomain, byte(len(host)))
		req = append(req, host...)
	}
	return binary.BigEndian.AppendUint16(req, uint16(port)), nil
}
