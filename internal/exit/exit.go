// Package exit is the far end of a tunnel stream: what the dnstt server hands every stream
// to. The server knows one fixed TCP address, so the address of a connection is told to the
// exit inside the stream (architecture.md R3): SOCKS5, HTTP CONNECT or SSH.
// Datapaths see only Dialer.
package exit

import (
	"context"
	"errors"
	"fmt"
	"net"
)

// Dialer opens connections to host:port through the exit.
type Dialer interface {
	// Dial returns a connection to address (host:port, the host a name or an address)
	// once the exit has connected to it. The host name is resolved by the exit, on the
	// server's side.
	Dial(ctx context.Context, address string) (net.Conn, error)
}

// OpenStream opens a new stream to the server's exit: Session.OpenStream of the core, or
// the supervisor's, which waits for a session until ctx ends.
type OpenStream func(ctx context.Context) (net.Conn, error)

// LoginError is a refusal of the exit to let us in: a wrong login or password, or a login
// the exit requires and the profile does not have. Another attempt gets the same answer,
// so the supervisor stops on it (architecture.md §08) instead of reconnecting.
type LoginError struct {
	msg string
}

func (e *LoginError) Error() string { return e.msg }

// IsLogin reports whether err is a refusal of the exit to let us in.
func IsLogin(err error) bool {
	var le *LoginError
	return errors.As(err, &le)
}

// HostKeyError is a mismatch of the server's host key with the fingerprint the profile pins:
// the exit is not the server that was expected. Another attempt meets the same server and
// the same key, so the supervisor stops the tunnel on it (architecture.md §08) instead of
// reconnecting. The fingerprints are public values; the key itself is neither carried nor
// printed.
type HostKeyError struct {
	// Got is the fingerprint the exit presented, SHA256:….
	Got string
	// Want is the pinned fingerprint, SHA256:….
	Want string
}

func (e *HostKeyError) Error() string {
	return fmt.Sprintf("exit.host_key: the exit's host key fingerprint is %s, the profile pins %s; the tunnel is stopped",
		e.Got, e.Want)
}

// IsHostKeyMismatch reports whether err is a mismatch of the exit's host key with the pinned
// fingerprint.
func IsHostKeyMismatch(err error) bool {
	var he *HostKeyError
	return errors.As(err, &he)
}
