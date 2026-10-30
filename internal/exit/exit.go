// Package exit is the far end of a tunnel stream: what the dnstt server hands every stream
// to. The server knows one fixed TCP address, so the address of a connection is told to the
// exit inside the stream (architecture.md R3): SOCKS5 now, SSH in E5. Datapaths see only
// Dialer.
package exit

import (
	"context"
	"errors"
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
