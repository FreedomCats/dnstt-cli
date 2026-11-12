package exit

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// SSH is the exit of a server with an SSH server behind it. One SSH connection lives in one
// tunnel stream and is reused by every connection of the session; each proxied connection is
// a direct-tcpip channel on that connection (RFC 4254 §7.2). When the stream dies, the next
// Dial opens a new stream and authenticates again.
type SSH struct {
	// Open opens a new stream to the server's fixed exit.
	Open OpenStream
	// User is the login of the exit; required.
	User string
	// Password is the password of the exit; "" — password authentication is not
	// offered.
	Password string
	// Signer is the private key of the exit; nil — public key authentication is not
	// offered.
	Signer ssh.Signer
	// HostKey is the pinned fingerprint of the server's host key, SHA256:…, normalized
	// by ParseFingerprint; required.
	HostKey string

	mu      sync.Mutex
	client  *ssh.Client
	gen     uint64
	opening chan struct{}
}

var _ Dialer = (*SSH)(nil)

// ErrKeyNeedsPassphrase is the reason ParseKey and DecryptKey give for an encrypted key
// when no passphrase was supplied: a caller can tell "ask for a passphrase" from "ask for
// another key" with errors.Is. The text is the message itself.
var ErrKeyNeedsPassphrase = errors.New("the private key needs a passphrase")

// ParseKey parses a PEM private key for public key authentication. passphrase is used only
// when the key is encrypted; an empty passphrase for such a key is
// ErrKeyNeedsPassphrase. The errors name the reason only: no key material and no path gets
// into the message.
func ParseKey(pemBytes []byte, passphrase string) (ssh.Signer, error) {
	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err == nil {
		return signer, nil
	}
	var missing *ssh.PassphraseMissingError
	if !errors.As(err, &missing) {
		return nil, errors.New("the private key cannot be parsed")
	}
	if passphrase == "" {
		return nil, ErrKeyNeedsPassphrase
	}
	signer, err = ssh.ParsePrivateKeyWithPassphrase(pemBytes, []byte(passphrase))
	if err != nil {
		return nil, errors.New("the passphrase is wrong or the key is damaged")
	}
	return signer, nil
}

// DecryptKey returns the key in PEM without a passphrase: the form the secret store keeps
// for exit.key_ref, which architecture.md §10 has no passphrase field for. With an empty
// passphrase the key must be unencrypted, and the input comes back unchanged; a key that
// needs one gives ErrKeyNeedsPassphrase. With a passphrase the key is parsed and marshalled
// again in the OpenSSH PEM form, so the result is not byte-identical to the input. The
// errors name the reason only: no key material, no passphrase and no path gets into the
// message.
func DecryptKey(pemBytes []byte, passphrase string) ([]byte, error) {
	if passphrase == "" {
		if _, err := ssh.ParseRawPrivateKey(pemBytes); err != nil {
			var missing *ssh.PassphraseMissingError
			if errors.As(err, &missing) {
				return nil, ErrKeyNeedsPassphrase
			}
			return nil, errors.New("the private key cannot be parsed")
		}
		return pemBytes, nil
	}

	key, err := ssh.ParseRawPrivateKeyWithPassphrase(pemBytes, []byte(passphrase))
	if err != nil {
		return nil, errors.New("the passphrase is wrong or the key is damaged")
	}
	block, err := ssh.MarshalPrivateKey(key, "")
	if err != nil {
		// A key type the marshaller has no OpenSSH form for, DSA above all.
		return nil, errors.New("the private key type is not supported")
	}
	return pem.EncodeToMemory(block), nil
}

// ParseFingerprint checks a pinned host key fingerprint and returns it normalized. It
// accepts SHA256:<43 base64 characters>, as ssh-keygen -lf prints it, and the same body
// without the prefix; whitespace around the value is trimmed.
func ParseFingerprint(s string) (string, error) {
	value := strings.TrimSpace(s)
	body, prefixed := strings.CutPrefix(value, "SHA256:")
	if !prefixed {
		body = value
	}
	raw, err := base64.RawStdEncoding.DecodeString(body)
	if err != nil || len(raw) != sha256.Size {
		return "", errors.New("exit.host_key: the pinned fingerprint must be SHA256: followed by 43 base64 characters, as ssh-keygen -lf prints it; an MD5 or other form is not accepted")
	}
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(raw), nil
}

// Dial opens a direct-tcpip channel to address on the cached SSH connection, establishing
// the connection first when there is none. A refused login is a *LoginError, a host key
// mismatch a *HostKeyError; a refused channel open is an ordinary error of that connection.
func (s *SSH) Dial(ctx context.Context, address string) (net.Conn, error) {
	client, gen, err := s.connection(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := client.DialContext(ctx, "tcp", address)
	if err == nil {
		return &deadlineConn{Conn: conn}, nil
	}
	// A refusal of the target is the answer of a live connection: no retry.
	var refused *ssh.OpenChannelError
	if errors.As(err, &refused) || !s.dropped(gen) || ctx.Err() != nil {
		return nil, err
	}
	// The cached connection died between the lookup and the channel open: reconnect
	// once and retry this one connection on the fresh connection.
	client, _, err = s.connection(ctx)
	if err != nil {
		return nil, err
	}
	conn, err = client.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	return &deadlineConn{Conn: conn}, nil
}

// deadlineConn is a direct-tcpip channel with the deadlines the datapath needs. x/crypto/ssh
// channels have no deadlines of their own: SetDeadline on them always fails. A deadline here
// ends the connection instead (a background Close) rather than producing a net.Error with
// Timeout(): the datapath sets a deadline in the past to unblock a read or write and then
// closes. A per-operation timeout that keeps the connection usable would need a goroutine
// per read or write.
type deadlineConn struct {
	net.Conn
	mu     sync.Mutex
	timer  *time.Timer
	closed bool
}

// SetDeadline implements net.Conn.
func (c *deadlineConn) SetDeadline(t time.Time) error { return c.arm(t) }

// SetReadDeadline implements net.Conn.
func (c *deadlineConn) SetReadDeadline(t time.Time) error { return c.arm(t) }

// SetWriteDeadline implements net.Conn.
func (c *deadlineConn) SetWriteDeadline(t time.Time) error { return c.arm(t) }

// arm disarms the previous deadline and, unless t is zero, ends the connection at t. A
// deadline in the past ends it at once.
func (c *deadlineConn) arm(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	if t.IsZero() {
		return nil
	}
	if d := time.Until(t); d > 0 {
		c.timer = time.AfterFunc(d, c.end)
	} else {
		c.endLocked()
	}
	return nil
}

// end is the timer callback: the deadline has come.
func (c *deadlineConn) end() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.endLocked()
}

// endLocked ends the connection in the background: Close may wait for the channel's write
// lock, which a stalled write can hold, and the caller must not wait for it. The caller
// holds c.mu.
func (c *deadlineConn) endLocked() {
	if c.closed {
		return
	}
	c.closed = true
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	go c.Conn.Close()
}

// Close stops the deadline timer and closes the connection. When a deadline has already
// ended it, the background close is in progress and Close returns at once.
func (c *deadlineConn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	c.mu.Unlock()
	return c.Conn.Close()
}

// Check opens a stream, completes the handshake and the authentication, and closes the
// connection at once: whether the exit is an SSH server, pins the same host key and lets us
// in. It does not cache its connection: the supervisor checks a new session before it
// publishes it, the same as the SOCKS5 check.
func (s *SSH) Check(ctx context.Context) error {
	client, _, err := s.open(ctx)
	if err != nil {
		return err
	}
	client.Close()
	return nil
}

// connection returns the cached SSH connection, opening one when there is none. One open
// runs at a time; the other callers wait for its result and reuse it, or take over when it
// failed. The generation identifies the cached connection and is bumped both by a publish
// and by the cleanup of a dead connection, so a late cleanup cannot clear a newer one.
func (s *SSH) connection(ctx context.Context) (*ssh.Client, uint64, error) {
	for {
		s.mu.Lock()
		if s.client != nil {
			client, gen := s.client, s.gen
			s.mu.Unlock()
			return client, gen, nil
		}
		if open := s.opening; open != nil {
			s.mu.Unlock()
			select {
			case <-open:
				continue
			case <-ctx.Done():
				return nil, 0, context.Cause(ctx)
			}
		}
		open := make(chan struct{})
		s.opening = open
		s.mu.Unlock()

		client, stream, err := s.open(ctx)

		s.mu.Lock()
		s.opening = nil
		var gen uint64
		if err == nil {
			s.gen++
			s.client = client
			gen = s.gen
			go s.watch(client, stream, gen)
		}
		s.mu.Unlock()
		close(open)
		if err != nil {
			return nil, 0, err
		}
		return client, gen, nil
	}
}

// open opens a stream, completes the SSH handshake and authentication, and clears the stream
// deadline before returning it.
func (s *SSH) open(ctx context.Context) (*ssh.Client, net.Conn, error) {
	cfg, err := s.config()
	if err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	stream, err := s.Open(ctx)
	if err != nil {
		return nil, nil, err
	}
	// An ended ctx breaks the handshake: a deadline in the past ends the read in
	// progress, and the AfterFunc also ends a write stuck in a stalled session.
	deadline, _ := ctx.Deadline()
	if err := stream.SetDeadline(deadline); err != nil {
		stream.Close()
		return nil, nil, err
	}
	stop := context.AfterFunc(ctx, func() { stream.SetDeadline(time.Unix(1, 0)) })
	conn, chans, reqs, err := ssh.NewClientConn(stream, "dnstt-exit", cfg)
	stopped := stop()
	if err != nil {
		// NewClientConn closes the stream itself. An answer that came as ctx ended still
		// stands; anything else is the cancellation.
		herr := sshHandshakeError(err)
		if !stopped && !IsLogin(herr) && !IsHostKeyMismatch(herr) {
			return nil, nil, context.Cause(ctx)
		}
		return nil, nil, herr
	}
	if !stopped {
		// The handshake came up just as ctx ended: the deadline in the stream makes the
		// connection unusable.
		stream.Close()
		return nil, nil, context.Cause(ctx)
	}
	client := ssh.NewClient(conn, chans, reqs)
	if err := stream.SetDeadline(time.Time{}); err != nil {
		client.Close()
		return nil, nil, err
	}
	return client, stream, nil
}

// config is the client configuration of the exit. Algorithms stay at the library defaults.
func (s *SSH) config() (*ssh.ClientConfig, error) {
	if s.User == "" {
		return nil, errors.New("SSH exit: the user is required")
	}
	want, err := ParseFingerprint(s.HostKey)
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User: s.User,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			if got := ssh.FingerprintSHA256(key); got != want {
				return &HostKeyError{Got: got, Want: want}
			}
			return nil
		},
	}
	if s.Password != "" {
		cfg.Auth = append(cfg.Auth, ssh.Password(s.Password))
	}
	if s.Signer != nil {
		cfg.Auth = append(cfg.Auth, ssh.PublicKeys(s.Signer))
	}
	return cfg, nil
}

// watch waits for the connection to die, clears it from the cache and closes its stream. The
// generation check keeps a late cleanup from clearing a connection published after it.
func (s *SSH) watch(client *ssh.Client, stream net.Conn, gen uint64) {
	client.Wait()
	s.mu.Lock()
	if s.gen == gen {
		s.client = nil
		s.gen++
	}
	s.mu.Unlock()
	stream.Close()
}

// dropped reports whether the cached connection of gen has already been cleared.
func (s *SSH) dropped(gen uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gen != gen
}

// sshHandshakeError classifies a failed handshake. A host key mismatch is final as it is.
// The library exports no type for a refused authentication in v0.54.0: the only signal is
// the text "ssh: unable to authenticate" (client_auth.go), so it is matched here. That is
// the safe direction: a changed text turns a wrong login into a retry with backoff, never
// into a wrong stop.
func sshHandshakeError(err error) error {
	if IsHostKeyMismatch(err) {
		return err
	}
	if strings.Contains(err.Error(), "ssh: unable to authenticate") {
		return &LoginError{"the SSH exit refused the login: wrong user, password or key"}
	}
	return err
}
