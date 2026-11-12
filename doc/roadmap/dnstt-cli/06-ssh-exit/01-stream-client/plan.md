---
kind: task
status: done
detailed: true
---

# SSH client over the tunnel stream

## Task

Add the SSH exit at the protocol level: one SSH connection living inside one tunnel stream,
every proxied connection a `direct-tcpip` channel on it, password or key authentication, and
the server's host key pinned by fingerprint. `architecture.md` R3 (`ssh`), §08 (the
supervisor's final causes), §10 (`host_key`, `key_ref`).

The result is one reviewable change in `internal/exit` plus one classification line in
`internal/supervisor`: no profile field, no secret store access, no wizard, import or menu.
An SSH profile still does not connect after this task — the wiring is
[Profile and connect wiring](../02-profile-and-wiring/plan.md).

`internal/exit/socks5.go` is the pattern to follow: a `Dialer` with `Dial` and `Check`, the
shared `handshakeTimeout` (`internal/exit/socks5.go:48`), typed errors that carry no
peer-supplied text, and a stream whose deadline is cleared before it is handed to the app.
`golang.org/x/crypto` is already a direct dependency (`go.mod:15`, v0.54.0), so
`x/crypto/ssh` adds no module.

Two decisions belong to this task:

- The pinned value is a fingerprint string, not a key: `ssh.FingerprintSHA256` of the server
  key compared with the profile's `SHA256:<base64>` value. `ssh.FixedHostKey`
  (`client.go:355`) compares keys and cannot produce the message with both fingerprints.
- A mismatch is final: another attempt meets the same server and the same key, so the
  supervisor must stop instead of reconnecting (§08). That is the one line in
  `internal/supervisor`.

## Steps

1. `internal/exit/exit.go`: add `HostKeyError` next to `LoginError` — `Got` and `Want` as
   normalized `SHA256:…` strings, a message that names `exit.host_key`, both fingerprints and
   says the tunnel is stopped — and `IsHostKeyMismatch(err) bool` via `errors.As`. The
   fingerprints are public values; the key itself is never carried or printed.
2. `internal/exit/ssh.go`: the exit type.

   ```go
   type SSH struct {
       Open     OpenStream // a new stream to the server's fixed exit
       User     string     // required
       Password string     // "" — password authentication is not offered
       Signer   ssh.Signer // nil — public key authentication is not offered
       HostKey  string     // normalized SHA256:… fingerprint, required
   }
   ```

   `var _ Dialer = (*SSH)(nil)`. The doc comment says: one connection per stream, reused by
   every connection of the session, `direct-tcpip` per proxied connection, the connection
   replaced when its stream dies.
3. Key and fingerprint helpers in the same file, because the profile side must not own a
   second copy of the rule:
   - `ParseKey(pemBytes []byte, passphrase string) (ssh.Signer, error)` —
     `ssh.ParsePrivateKey` (`keys.go:1313`); on `*ssh.PassphraseMissingError` require a
     passphrase and use `ssh.ParsePrivateKeyWithPassphrase` (`keys.go:1325`). Errors name the
     reason only ("the private key needs a passphrase", "the passphrase is wrong or the key is
     damaged"); no key material, no file path in the message.
   - `ParseFingerprint(s string) (string, error)` — accepts `SHA256:<43 base64 chars>`, the
     form `ssh-keygen -lf` prints, and the same body without the prefix; returns the
     normalized `SHA256:…`. An MD5 `aa:bb:…` value is refused with a message that names
     `exit.host_key` and the accepted form. Whitespace around the value is trimmed.
4. `internal/exit/ssh.go`: `Dial(ctx, address)`.
   - Under one mutex keep the live `*ssh.Client` and a generation counter. With no client,
     call `Open`, take the deadline from `handshakeTimeout` (and from `ctx`), run
     `ssh.NewClientConn(stream, "dnstt-exit", cfg)` then `ssh.NewClient`, and start one
     goroutine `client.Wait()` that clears the cached client, closes the stream and bumps the
     generation, so the next `Dial` reconnects by itself.
   - `cfg` is `&ssh.ClientConfig{User, Auth, HostKeyCallback}`: `ssh.Password` when
     `Password != ""`, `ssh.PublicKeys(Signer)` when `Signer != nil`. `ClientConfig.Timeout`
     stays zero — it only bounds a TCP dial, and the deadline here belongs to the stream.
     Algorithms stay at the library defaults.
   - The host key callback compares `ssh.FingerprintSHA256(key)` with `HostKey` and returns
     `*HostKeyError` on a mismatch. `NewClientConn` passes its address argument to the
     callback as the host name; the real address is inside the tunnel, so a fixed placeholder
     is right. The library wraps a handshake failure as `ssh: handshake failed: %w` and closes
     the stream itself, so the typed error survives `errors.As` and the stream is not closed a
     second time.
   - Clear the stream deadline once the connection is up: a leftover deadline would kill the
     connection in the middle of a session, exactly as in the SOCKS5 handshake.
   - Then `client.DialContext(ctx, "tcp", address)` (`tcpip.go:379`) — that is the
     `direct-tcpip` channel — and return the channel as `net.Conn`. A failed channel open is
     an ordinary error for that connection (the target is unreachable on the server's side);
     it must not look like a login refusal.
   - A `Dial` that fails because the transport died between the check and the channel open
     reconnects once and retries that connection once. No other retry, and no second stream
     for one `Dial`.
5. `Check(ctx)`: open a stream, complete the handshake and the authentication, close it at
   once — the readiness check of §08 for this exit. It does not cache its connection for the
   dialer: the check runs before the supervisor publishes the session, the same as the SOCKS5
   check. Classification:
   - host key mismatch → `*HostKeyError`, final;
   - authentication refused → `*LoginError`, final. The library exports no type for it: the
     only signal in v0.54.0 is the text `ssh: unable to authenticate`
     (`client_auth.go:154`), so the match is on that text inside the wrapped handshake error,
     with a comment saying so and that a changed text degrades to a retry, never to a wrong
     stop;
   - a broken or closed stream, EOF, a cancelled context → unwrapped, retryable.
6. `internal/supervisor/tunnel.go`: add the mismatch to the final causes at
   `internal/supervisor/tunnel.go:513`
   (`exit.IsLogin(err) || errors.Is(err, exit.ErrNotSOCKS5) || exit.IsHostKeyMismatch(err)`)
   and update the two comments that list them: `Options.CheckExit`
   (`internal/supervisor/tunnel.go:78-82`) and `Run`
   (`internal/supervisor/tunnel.go:420-425`).
7. `internal/supervisor/tunnel_test.go`: a row in the `TestExitCheck` table
   (`internal/supervisor/tunnel_test.go:585-593`) with a check that returns a
   `*exit.HostKeyError`; the existing assertions already prove one dial and a closed session.
8. `internal/exit/ssh_test.go`: an in-process synthetic SSH server over `net.Pipe`, no
   sockets. The server side is `ssh.NewServerConn` with a generated host key,
   `PasswordCallback` and `PublicKeyCallback`, and a channel handler that accepts only
   `direct-tcpip` and asks a `DialTarget func(hostport string) (net.Conn, error)` hook for the
   far end, so a test target is a `net.Pipe` and the suite stays off the network. Cover:
   password success; key success; an encrypted key with the right and with a wrong passphrase;
   a wrong password as `IsLogin`; a wrong pinned fingerprint as `IsHostKeyMismatch` with
   exactly one handshake seen by the server; `Check` success and both refusals; two `Dial`s
   sharing one connection (one handshake, two channels); transport death followed by a
   reconnect on the next `Dial`; a refused channel open; concurrent `Dial`s under `-race`;
   `ParseFingerprint` accepting both forms and refusing MD5 and junk.
9. Checks: `gofmt`, `go vet ./...`, `go test -race ./internal/exit/ ./internal/supervisor/
   ./internal/app/`, `GOOS=windows go build ./...`, `git diff --check`. No live service and no
   network; nothing of the user's is read.

## Done when

- `internal/exit` has an SSH `Dialer` whose one connection per stream carries every proxied
  connection as a `direct-tcpip` channel, with password and key authentication, the key taken
  as a parsed `ssh.Signer` (so a passphrase is handled by `ParseKey`).
- A pinned fingerprint that does not match returns `*HostKeyError`, the error names
  `exit.host_key` and both fingerprints, the stream is closed, and the supervisor stops on it
  with one dial — proven by the new `TestExitCheck` row and by the protocol test.
- `Check` completes a handshake and an authentication through a fresh stream and classifies a
  refused login as `*LoginError` and a mismatch as `*HostKeyError`; every other failure is
  retryable.
- The tests above pass with `-race`, no test opens a socket, and `go vet`, the Windows
  cross-build and `git diff --check` are clean.
- No profile field, secret store access, wizard, import, menu or live run is touched, and
  `exit.type = "ssh"` still refuses to build in `internal/app/tunnel.go:145-146`.

## Risks and what is needed from the user

- The authentication failure is recognised by the library's error text only
  (`client_auth.go:154`); an upgrade that changes the text turns a wrong password into a
  retry with backoff. That is the safe direction, and the comment must say so.
- `ssh.Client.Wait` and a concurrent `Dial` race on the cached client: the generation counter
  and the mutex must be the only way to publish or clear it, or a `Dial` can take a client
  that is already dead and hand the app a confusing error.
- A stream deadline left over from the handshake kills a healthy connection later; a channel
  deadline set by the datapath belongs to the channel and is not the stream's.
- `Client.Dial` sends `close` on `Close` without waiting for the peer: a half-closed target
  sees a reset. That is the existing behaviour of the datapath for every exit and is not
  changed here.
- Nothing is needed from the user: everything is in-process and generated.

## Outcome

`internal/exit` has the SSH exit and the supervisor stops on a pinned fingerprint that does
not match; nothing else of the stage moved.

- New `internal/exit/ssh.go`: `SSH{Open, User, Password, Signer, HostKey}`, `ParseKey`,
  `ParseFingerprint`, `Dial`, `Check`. One connection per dialer is cached under a mutex with
  a generation counter and a single-flight open, so concurrent `Dial`s share one handshake and
  a failed open wakes the waiters instead of leaving them behind; one `client.Wait()`
  goroutine clears the cache, closes the stream and bumps the generation, and a late cleanup
  cannot clear a newer connection. `direct-tcpip` is `Client.DialContext`; the host key
  callback compares `ssh.FingerprintSHA256` with the normalized pin.
- `internal/exit/exit.go`: `HostKeyError{Got, Want}` and `IsHostKeyMismatch`.
  `internal/supervisor/tunnel.go`: the mismatch among the final causes, with the two comments
  that list them. `internal/supervisor/tunnel_test.go`: the `host key mismatch` row, and the
  table gained a `check` field so a row can supply its own check.
- What the library turned out to be, beyond the plan's assumptions:
  - it closes the stream itself on a failed handshake, and in v0.54.0 twice (its own error
    path and the key-exchange loop), so the mismatch test asserts at least one close rather
    than an exact count; the client adds no close of its own;
  - `direct-tcpip` channels have no deadlines at all (`SetDeadline` always fails), while the
    datapath unblocks a stuck read or write by setting a deadline in the past. `Dial`
    therefore returns a small wrapper: arming a deadline ends the connection (a background
    `Close`, because a close can wait on a stalled write) instead of producing a timeout
    error, which is what its consumers do. The alternative, a per-operation timeout that
    keeps the connection usable, would need a goroutine per read or write.
  - an unbuffered `net.Pipe` deadlocks on the SSH version exchange, both sides writing before
    reading; the test transport buffers writes. No test opens a socket.
- The single retry of `Dial` — the transport died between the cached-client lookup and the
  channel open — has no deterministic test: the race needs a hook in production code. The
  ordinary path, a dead connection cleared from the cache and a fresh one on the next `Dial`,
  is covered.
- Left for the next children: the profile fields, the secret store, the wizard, the import
  and the menu. `exit.type = "ssh"` still refuses to build.
