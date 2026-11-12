package exit

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// generateEd25519 makes a key for the test server, a client or a pin that does not match.
func generateEd25519(t *testing.T) (ed25519.PrivateKey, ssh.Signer) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return priv, signer
}

// sshTestServer is an SSH server behind an OpenStream: one net.Pipe per stream, no sockets.
// It authenticates one user with a password or a public key, presents a generated host key
// and asks dialTarget for the far end of every direct-tcpip channel.
type sshTestServer struct {
	user     string
	password string
	allowed  ssh.PublicKey
	hostKey  ssh.Signer

	dialTarget func(hostport string) (net.Conn, error)

	mu         sync.Mutex
	attempts   int // streams the server was given, a finished handshake or not
	handshakes int // NewServerConn succeeded
	targets    []string
	conns      []*ssh.ServerConn
}

func newSSHServer(t *testing.T) *sshTestServer {
	t.Helper()
	_, host := generateEd25519(t)
	return &sshTestServer{
		user:       "user",
		hostKey:    host,
		dialTarget: echoTarget,
	}
}

// Open is the OpenStream of the test server.
func (s *sshTestServer) Open(ctx context.Context) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	client, server := bufferedPipe()
	go s.serve(server)
	return client, nil
}

// bufferedPipe is a net.Pipe with queued writes. The SSH version exchange has both sides
// write before they read, which deadlocks on an unbuffered pipe.
func bufferedPipe() (net.Conn, net.Conn) {
	client, server := net.Pipe()
	return newBufferedConn(client), newBufferedConn(server)
}

// bufferedConn queues Writes to the pipe and sends them in order from one goroutine. Reads,
// deadlines and Close go to the pipe itself.
type bufferedConn struct {
	net.Conn
	mu     sync.Mutex
	cond   *sync.Cond
	queue  [][]byte
	closed bool
}

func newBufferedConn(conn net.Conn) *bufferedConn {
	c := &bufferedConn{Conn: conn}
	c.cond = sync.NewCond(&c.mu)
	go c.flush()
	return c
}

func (c *bufferedConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, io.ErrClosedPipe
	}
	c.queue = append(c.queue, append([]byte(nil), p...))
	c.cond.Signal()
	return len(p), nil
}

func (c *bufferedConn) flush() {
	for {
		c.mu.Lock()
		for len(c.queue) == 0 && !c.closed {
			c.cond.Wait()
		}
		if c.closed {
			c.mu.Unlock()
			return
		}
		next := c.queue[0]
		c.queue = c.queue[1:]
		c.mu.Unlock()
		if _, err := c.Conn.Write(next); err != nil {
			c.Close()
			return
		}
	}
}

func (c *bufferedConn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.cond.Broadcast()
	c.mu.Unlock()
	return c.Conn.Close()
}

func (s *sshTestServer) serve(conn net.Conn) {
	defer conn.Close()
	s.mu.Lock()
	s.attempts++
	s.mu.Unlock()

	cfg := &ssh.ServerConfig{
		PasswordCallback: func(meta ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if s.password != "" && meta.User() == s.user && string(pass) == s.password {
				return nil, nil
			}
			return nil, errors.New("authentication refused")
		},
		PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if s.allowed != nil && meta.User() == s.user && bytes.Equal(key.Marshal(), s.allowed.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("authentication refused")
		},
	}
	cfg.AddHostKey(s.hostKey)
	server, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer server.Close()
	s.mu.Lock()
	s.handshakes++
	s.conns = append(s.conns, server)
	s.mu.Unlock()
	go ssh.DiscardRequests(reqs)
	for newChannel := range chans {
		if newChannel.ChannelType() != "direct-tcpip" {
			newChannel.Reject(ssh.UnknownChannelType, "only direct-tcpip")
			continue
		}
		var request struct {
			Host     string
			Port     uint32
			FromHost string
			FromPort uint32
		}
		if err := ssh.Unmarshal(newChannel.ExtraData(), &request); err != nil {
			newChannel.Reject(ssh.ConnectionFailed, "bad direct-tcpip request")
			continue
		}
		hostport := net.JoinHostPort(request.Host, strconv.Itoa(int(request.Port)))
		s.mu.Lock()
		s.targets = append(s.targets, hostport)
		dialTarget := s.dialTarget
		s.mu.Unlock()
		far, err := dialTarget(hostport)
		if err != nil {
			newChannel.Reject(ssh.ConnectionFailed, "the target refused")
			continue
		}
		channel, channelReqs, err := newChannel.Accept()
		if err != nil {
			far.Close()
			continue
		}
		go ssh.DiscardRequests(channelReqs)
		go pipeChannel(channel, far)
	}
}

// pipeChannel copies data both ways until one side ends, then closes both.
func pipeChannel(channel ssh.Channel, far net.Conn) {
	defer channel.Close()
	defer far.Close()
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(channel, far)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(far, channel)
		done <- struct{}{}
	}()
	<-done
}

// echoTarget is the far end of a channel: a pipe that writes back what it is sent.
func echoTarget(string) (net.Conn, error) {
	far, echo := net.Pipe()
	go func() {
		defer echo.Close()
		io.Copy(echo, echo)
	}()
	return far, nil
}

// silentTarget is a direct-tcpip target that never writes: a read of the client stays
// blocked until the connection ends.
func silentTarget(t *testing.T) func(string) (net.Conn, error) {
	t.Helper()
	far, other := net.Pipe()
	t.Cleanup(func() { other.Close() })
	return func(string) (net.Conn, error) { return far, nil }
}

// readStartedConn signals when Read is entered, so a deadline test knows the read is
// blocked before it sets the deadline.
type readStartedConn struct {
	net.Conn
	started chan struct{}
}

func (c *readStartedConn) Read(p []byte) (int, error) {
	close(c.started)
	return c.Conn.Read(p)
}

func (s *sshTestServer) counts() (attempts, handshakes, channels int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts, s.handshakes, len(s.targets)
}

func (s *sshTestServer) targetList() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.targets...)
}

// closeConnections ends the transport of every handshake the server completed.
func (s *sshTestServer) closeConnections() {
	s.mu.Lock()
	conns := append([]*ssh.ServerConn(nil), s.conns...)
	s.mu.Unlock()
	for _, conn := range conns {
		conn.Close()
	}
}

// pin is the fingerprint of the server's host key.
func (s *sshTestServer) pin() string {
	return ssh.FingerprintSHA256(s.hostKey.PublicKey())
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestSSHDialPassword(t *testing.T) {
	srv := newSSHServer(t)
	srv.password = "secret"
	// The bare body without the prefix is accepted too.
	d := &SSH{
		Open:     srv.Open,
		User:     srv.user,
		Password: "secret",
		HostKey:  strings.TrimPrefix(srv.pin(), "SHA256:"),
	}
	conn, err := d.Dial(dialCtx(t), "echo.example.com:7")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	echo(t, conn, []byte("hello"))
	if got := srv.targetList(); len(got) != 1 || got[0] != "echo.example.com:7" {
		t.Errorf("the server saw targets %q", got)
	}
	if attempts, handshakes, channels := srv.counts(); attempts != 1 || handshakes != 1 || channels != 1 {
		t.Errorf("attempts %d, handshakes %d, channels %d; want 1, 1, 1",
			attempts, handshakes, channels)
	}
}

func TestSSHDialKey(t *testing.T) {
	srv := newSSHServer(t)
	_, clientKey := generateEd25519(t)
	srv.allowed = clientKey.PublicKey()
	d := &SSH{Open: srv.Open, User: srv.user, Signer: clientKey, HostKey: srv.pin()}
	conn, err := d.Dial(dialCtx(t), "echo.example.com:7")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	echo(t, conn, []byte("hello"))
	if attempts, handshakes, _ := srv.counts(); attempts != 1 || handshakes != 1 {
		t.Errorf("attempts %d, handshakes %d; want 1, 1", attempts, handshakes)
	}
}

func TestSSHParseKey(t *testing.T) {
	priv, original := generateEd25519(t)
	plainBlock, err := ssh.MarshalPrivateKey(priv, "test")
	if err != nil {
		t.Fatal(err)
	}
	plain := pem.EncodeToMemory(plainBlock)
	const passphrase = "correct horse battery staple"
	encryptedBlock, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "test", []byte(passphrase))
	if err != nil {
		t.Fatal(err)
	}
	encrypted := pem.EncodeToMemory(encryptedBlock)

	signer, err := ParseKey(plain, "")
	if err != nil {
		t.Fatalf("an unencrypted key: %v", err)
	}
	if !bytes.Equal(signer.PublicKey().Marshal(), original.PublicKey().Marshal()) {
		t.Error("the unencrypted key parsed to another public key")
	}
	if _, err := ParseKey(encrypted, ""); err == nil || !strings.Contains(err.Error(), "needs a passphrase") {
		t.Errorf("an encrypted key without a passphrase: %v", err)
	}
	const wrong = "the wrong passphrase"
	if _, err := ParseKey(encrypted, wrong); err == nil || !strings.Contains(err.Error(), "passphrase is wrong") {
		t.Errorf("an encrypted key with a wrong passphrase: %v", err)
	} else if strings.Contains(err.Error(), wrong) {
		t.Errorf("the error repeats the passphrase: %v", err)
	}
	if signer, err = ParseKey(encrypted, passphrase); err != nil {
		t.Errorf("an encrypted key with the right passphrase: %v", err)
	} else if !bytes.Equal(signer.PublicKey().Marshal(), original.PublicKey().Marshal()) {
		t.Error("the encrypted key parsed to another public key")
	}
	if _, err := ParseKey([]byte("not a key"), ""); err == nil || !strings.Contains(err.Error(), "cannot be parsed") {
		t.Errorf("junk: %v", err)
	}
}

func TestSSHParseFingerprint(t *testing.T) {
	_, signer := generateEd25519(t)
	fingerprint := ssh.FingerprintSHA256(signer.PublicKey())
	body := strings.TrimPrefix(fingerprint, "SHA256:")
	for _, in := range []string{fingerprint, body, "  " + fingerprint + "\n", "\t" + body + " "} {
		got, err := ParseFingerprint(in)
		if err != nil || got != fingerprint {
			t.Errorf("%q: got %q, %v; want %q", in, got, err, fingerprint)
		}
	}
	for _, bad := range []string{
		"MD5:aa:bb:cc:dd",
		"aa:bb:cc:dd:ee",
		"",
		"SHA256:",
		"SHA256:short",
		"not a fingerprint",
		"256 " + fingerprint + " user@example (ED25519)",
	} {
		got, err := ParseFingerprint(bad)
		if err == nil || got != "" {
			t.Errorf("%q: got %q, %v; want an error", bad, got, err)
		}
		if err != nil && !strings.Contains(err.Error(), "exit.host_key") {
			t.Errorf("%q: %v does not name exit.host_key", bad, err)
		}
	}
}

func TestSSHDialLoginRefused(t *testing.T) {
	srv := newSSHServer(t)
	srv.password = "secret"
	const wrong = "the wrong password"
	d := &SSH{Open: srv.Open, User: srv.user, Password: wrong, HostKey: srv.pin()}
	_, err := d.Dial(dialCtx(t), "echo.example.com:7")
	if !IsLogin(err) {
		t.Fatalf("%v: not a login error", err)
	}
	if strings.Contains(err.Error(), wrong) {
		t.Errorf("the error repeats the password: %v", err)
	}
	if attempts, handshakes, channels := srv.counts(); attempts != 1 || handshakes != 0 || channels != 0 {
		t.Errorf("attempts %d, handshakes %d, channels %d; want 1, 0, 0",
			attempts, handshakes, channels)
	}
}

func TestSSHDialHostKeyMismatch(t *testing.T) {
	srv := newSSHServer(t)
	srv.password = "secret"
	_, other := generateEd25519(t)
	want := ssh.FingerprintSHA256(other.PublicKey())
	streams := &streamCounter{open: srv.Open}
	d := &SSH{Open: streams.Open, User: srv.user, Password: "secret", HostKey: want}
	_, err := d.Dial(dialCtx(t), "echo.example.com:7")
	if !IsHostKeyMismatch(err) {
		t.Fatalf("%v: not a host key mismatch", err)
	}
	var mismatch *HostKeyError
	if !errors.As(err, &mismatch) {
		t.Fatalf("%v: not a *HostKeyError", err)
	}
	if mismatch.Got != srv.pin() || mismatch.Want != want {
		t.Errorf("got %q, want %q; the server presents %q", mismatch.Got, mismatch.Want, srv.pin())
	}
	if msg := err.Error(); !strings.Contains(msg, "exit.host_key") ||
		!strings.Contains(msg, mismatch.Got) || !strings.Contains(msg, mismatch.Want) {
		t.Errorf("the message does not name exit.host_key and both fingerprints: %v", err)
	}
	if attempts, handshakes, _ := srv.counts(); attempts != 1 || handshakes != 0 {
		t.Errorf("attempts %d, handshakes %d; want exactly one attempt and no completed handshake",
			attempts, handshakes)
	}
	// The handshake itself closes the stream. v0.54.0 closes it twice: from the kex
	// loop's shutdown (handshake.go:391) and from NewClientConn's error path
	// (client.go:84); our code adds no close.
	if opened, closed := streams.opened.Load(), streams.closed.Load(); opened != 1 || closed < 1 {
		t.Errorf("streams opened %d, closed %d; want 1 and the stream closed", opened, closed)
	}
}

func TestSSHCheck(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		srv := newSSHServer(t)
		srv.password = "secret"
		d := &SSH{Open: srv.Open, User: srv.user, Password: "secret", HostKey: srv.pin()}
		if err := d.Check(dialCtx(t)); err != nil {
			t.Fatal(err)
		}
		if attempts, handshakes, _ := srv.counts(); attempts != 1 || handshakes != 1 {
			t.Errorf("attempts %d, handshakes %d; want 1, 1", attempts, handshakes)
		}
		d.mu.Lock()
		cached := d.client
		d.mu.Unlock()
		if cached != nil {
			t.Error("Check cached its connection")
		}
	})
	t.Run("login refused", func(t *testing.T) {
		srv := newSSHServer(t)
		srv.password = "secret"
		d := &SSH{Open: srv.Open, User: srv.user, Password: "wrong", HostKey: srv.pin()}
		if err := d.Check(dialCtx(t)); !IsLogin(err) {
			t.Fatalf("%v: not a login error", err)
		}
	})
	t.Run("host key mismatch", func(t *testing.T) {
		srv := newSSHServer(t)
		_, other := generateEd25519(t)
		d := &SSH{Open: srv.Open, User: srv.user, Password: "secret", HostKey: ssh.FingerprintSHA256(other.PublicKey())}
		if err := d.Check(dialCtx(t)); !IsHostKeyMismatch(err) {
			t.Fatalf("%v: not a host key mismatch", err)
		}
	})
}

func TestSSHCheckBrokenStream(t *testing.T) {
	open := func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		server.Close()
		return client, nil
	}
	d := &SSH{Open: open, User: "user", Password: "secret", HostKey: strings.Repeat("A", 43)}
	err := d.Check(dialCtx(t))
	if err == nil {
		t.Fatal("Check of a broken stream succeeded")
	}
	if IsLogin(err) || IsHostKeyMismatch(err) {
		t.Errorf("%v: a broken stream must stay retryable", err)
	}
}

// A deadline in the past ends a read blocked on a direct-tcpip channel.
func TestSSHDeadlinePast(t *testing.T) {
	srv := newSSHServer(t)
	srv.password = "secret"
	srv.dialTarget = silentTarget(t)
	d := &SSH{Open: srv.Open, User: srv.user, Password: "secret", HostKey: srv.pin()}
	dialed, err := d.Dial(dialCtx(t), "echo.example.com:7")
	if err != nil {
		t.Fatal(err)
	}
	defer dialed.Close()
	conn := &readStartedConn{Conn: dialed, started: make(chan struct{})}
	read := make(chan error, 1)
	go func() {
		_, err := conn.Read(make([]byte, 1))
		read <- err
	}()
	<-conn.started
	if err := conn.SetDeadline(time.Unix(1, 0)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}
	select {
	case err := <-read:
		if err == nil {
			t.Error("the read returned a byte")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the read did not end after the deadline")
	}
}

// A future deadline ends the channel too, with no read or write in flight that its own
// SetDeadline could fail on.
func TestSSHDeadlineFuture(t *testing.T) {
	srv := newSSHServer(t)
	srv.password = "secret"
	srv.dialTarget = silentTarget(t)
	d := &SSH{Open: srv.Open, User: srv.user, Password: "secret", HostKey: srv.pin()}
	dialed, err := d.Dial(dialCtx(t), "echo.example.com:7")
	if err != nil {
		t.Fatal(err)
	}
	defer dialed.Close()
	conn := &readStartedConn{Conn: dialed, started: make(chan struct{})}
	read := make(chan error, 1)
	go func() {
		_, err := conn.Read(make([]byte, 1))
		read <- err
	}()
	<-conn.started
	if err := conn.SetWriteDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatalf("SetWriteDeadline: %v", err)
	}
	select {
	case err := <-read:
		if err == nil {
			t.Error("the read returned a byte")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the read did not end at the deadline")
	}
}

// A zero deadline disarms the timer: the connection still carries data afterwards.
func TestSSHDeadlineDisarmed(t *testing.T) {
	srv := newSSHServer(t)
	srv.password = "secret"
	d := &SSH{Open: srv.Open, User: srv.user, Password: "secret", HostKey: srv.pin()}
	conn, err := d.Dial(dialCtx(t), "echo.example.com:7")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}
	// Well past the disarmed deadline: a timer left running would have closed the channel.
	time.Sleep(150 * time.Millisecond)
	echo(t, conn, []byte("still there"))
}

func TestSSHOneConnection(t *testing.T) {
	srv := newSSHServer(t)
	srv.password = "secret"
	d := &SSH{Open: srv.Open, User: srv.user, Password: "secret", HostKey: srv.pin()}
	first, err := d.Dial(dialCtx(t), "echo.example.com:7")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	echo(t, first, []byte("first"))
	second, err := d.Dial(dialCtx(t), "echo.example.com:8")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	echo(t, second, []byte("second"))
	if attempts, handshakes, channels := srv.counts(); attempts != 1 || handshakes != 1 || channels != 2 {
		t.Errorf("attempts %d, handshakes %d, channels %d; want 1, 1, 2",
			attempts, handshakes, channels)
	}
}

func TestSSHReconnect(t *testing.T) {
	srv := newSSHServer(t)
	srv.password = "secret"
	d := &SSH{Open: srv.Open, User: srv.user, Password: "secret", HostKey: srv.pin()}
	conn, err := d.Dial(dialCtx(t), "echo.example.com:7")
	if err != nil {
		t.Fatal(err)
	}
	echo(t, conn, []byte("before"))
	conn.Close()

	srv.closeConnections()
	waitFor(t, "the dead connection to be cleared", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.client == nil
	})

	again, err := d.Dial(dialCtx(t), "echo.example.com:7")
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	echo(t, again, []byte("after"))
	if attempts, handshakes, _ := srv.counts(); attempts != 2 || handshakes != 2 {
		t.Errorf("attempts %d, handshakes %d; want 2, 2", attempts, handshakes)
	}
}

func TestSSHRefusedChannel(t *testing.T) {
	srv := newSSHServer(t)
	srv.password = "secret"
	var n atomic.Int32
	srv.dialTarget = func(hostport string) (net.Conn, error) {
		if n.Add(1) == 1 {
			return nil, errors.New("the target is down")
		}
		return echoTarget(hostport)
	}
	d := &SSH{Open: srv.Open, User: srv.user, Password: "secret", HostKey: srv.pin()}
	_, err := d.Dial(dialCtx(t), "dead.example.com:7")
	if err == nil {
		t.Fatal("Dial to a refused target succeeded")
	}
	if IsLogin(err) || IsHostKeyMismatch(err) {
		t.Errorf("a refused channel open: %v; want an ordinary error", err)
	}
	var openErr *ssh.OpenChannelError
	if !errors.As(err, &openErr) {
		t.Errorf("a refused channel open: %v; want a *ssh.OpenChannelError", err)
	}
	// The connection is still up: the next connection reuses it.
	conn, err := d.Dial(dialCtx(t), "echo.example.com:7")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	echo(t, conn, []byte("after the refusal"))
	if attempts, handshakes, channels := srv.counts(); attempts != 1 || handshakes != 1 || channels != 2 {
		t.Errorf("attempts %d, handshakes %d, channels %d; want 1, 1, 2",
			attempts, handshakes, channels)
	}
}

func TestSSHConcurrentDial(t *testing.T) {
	srv := newSSHServer(t)
	srv.password = "secret"
	d := &SSH{Open: srv.Open, User: srv.user, Password: "secret", HostKey: srv.pin()}
	const n = 8
	ctx := dialCtx(t)
	start := make(chan struct{})
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			conn, err := d.Dial(ctx, "echo.example.com:7")
			if err != nil {
				errs <- err
				return
			}
			conn.Close()
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if attempts, handshakes, channels := srv.counts(); attempts != 1 || handshakes != 1 || channels != n {
		t.Errorf("attempts %d, handshakes %d, channels %d; want 1, 1, %d",
			attempts, handshakes, channels, n)
	}
}
