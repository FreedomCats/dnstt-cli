package datapath

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"slices"
	"strconv"
)

// The server half of SOCKS5 (RFC 1928, CONNECT only): the one a program speaks to. The
// client half — the one the shell speaks to the exit — is internal/exit, and the two are
// not shared on purpose: the local side has no login, the exit side may have one, and the
// local side refuses what the tunnel cannot carry instead of asking the exit.
//
// The messages go one at a time, each after the answer to the previous one: programs that
// use a SOCKS5 proxy are ready for that.

const socksVersion = 5

const (
	methodNone         = 0x00
	methodNoAcceptable = 0xff
)

const (
	cmdConnect = 0x01

	atypIPv4   = 0x01
	atypDomain = 0x03
	atypIPv6   = 0x04
)

// Reply codes (RFC 1928 §6), the ones this side uses.
const (
	repSucceeded           = 0x00
	repFailure             = 0x01
	repNetworkUnreachable  = 0x03
	repHostUnreachable     = 0x04
	repCommandNotSupported = 0x07
	repAddressNotSupported = 0x08
)

// errProtocol is a client that does not speak SOCKS5: there is nothing to answer with.
var errProtocol = errors.New("not SOCKS5")

// refusal is a request that cannot be served; code is the reply to the program.
type refusal struct {
	code byte
	err  error
}

func (r *refusal) Error() string { return r.err.Error() }
func (r *refusal) Unwrap() error { return r.err }

// request is a CONNECT request: a host name or an address, and a port.
type request struct {
	host string
	addr netip.Addr
	port uint16
}

// address is what the exit is asked for: a name stays a name and is resolved on the
// server's side, an address goes as an address.
func (r request) address() string {
	if r.host != "" {
		return net.JoinHostPort(r.host, strconv.Itoa(int(r.port)))
	}
	return netip.AddrPortFrom(r.addr, r.port).String()
}

func (r request) String() string {
	if r.host == "" && !r.addr.IsValid() {
		return "a request with no address"
	}
	return r.address()
}

// greet reads the greeting and picks "none". There is no password on the local side, so a
// client that offers nothing else is told so (0xFF) and is not spoken to again.
func greet(c io.ReadWriter) error {
	var head [2]byte
	if _, err := io.ReadFull(c, head[:]); err != nil {
		return err
	}
	if head[0] != socksVersion {
		return fmt.Errorf("%w (version %d in the greeting)", errProtocol, head[0])
	}
	methods := make([]byte, head[1])
	if _, err := io.ReadFull(c, methods); err != nil {
		return err
	}
	if !slices.Contains(methods, byte(methodNone)) {
		if _, err := c.Write([]byte{socksVersion, methodNoAcceptable}); err != nil {
			return err
		}
		return errors.New("the client accepts no method but a login")
	}
	_, err := c.Write([]byte{socksVersion, methodNone})
	return err
}

// readRequest reads a request. A *refusal is a request that was read and cannot be served:
// the program gets its code. Any other error is a client that left or does not speak
// SOCKS5, and there is nobody to answer.
func readRequest(c io.Reader) (request, error) {
	var head [4]byte
	if _, err := io.ReadFull(c, head[:]); err != nil {
		return request{}, err
	}
	if head[0] != socksVersion {
		return request{}, fmt.Errorf("%w (version %d in the request)", errProtocol, head[0])
	}
	var req request
	switch head[3] {
	case atypIPv4:
		var b [4]byte
		if _, err := io.ReadFull(c, b[:]); err != nil {
			return request{}, err
		}
		req.addr = netip.AddrFrom4(b)
	case atypIPv6:
		var b [16]byte
		if _, err := io.ReadFull(c, b[:]); err != nil {
			return request{}, err
		}
		req.addr = netip.AddrFrom16(b)
	case atypDomain:
		var n [1]byte
		if _, err := io.ReadFull(c, n[:]); err != nil {
			return request{}, err
		}
		name := make([]byte, n[0])
		if _, err := io.ReadFull(c, name); err != nil {
			return request{}, err
		}
		req.host = string(name)
		if req.host == "" {
			// An empty name has no address to ask the exit for; the dial would fail with
			// an error of the shell, not the program's.
			return req, &refusal{repHostUnreachable, errors.New("an empty host name")}
		}
	default:
		// The length of the address is unknown, so the rest of the request cannot be
		// read; the reply can still be sent.
		return request{}, &refusal{repAddressNotSupported,
			fmt.Errorf("address type %#02x", head[3])}
	}
	var port [2]byte
	if _, err := io.ReadFull(c, port[:]); err != nil {
		return request{}, err
	}
	req.port = binary.BigEndian.Uint16(port[:])

	if head[1] != cmdConnect {
		// BIND and UDP ASSOCIATE included: the tunnel carries streams only, and a refusal
		// at once lets the program fall back instead of waiting.
		return req, &refusal{repCommandNotSupported, fmt.Errorf("command %#02x", head[1])}
	}
	return req, nil
}

// writeReply writes the reply to the request. The bound address is 0.0.0.0:0 in both
// cases: the connection goes through the exit, and the address the exit bound on its side
// to reach the target is not told to the shell.
func writeReply(c io.Writer, rep byte) error {
	_, err := c.Write([]byte{socksVersion, rep, 0, atypIPv4, 0, 0, 0, 0, 0, 0})
	return err
}
