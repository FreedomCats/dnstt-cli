---
kind: task
status: todo
detailed: true
---

# One-write SOCKS5 exchange

## Task

The measured `danted` 1.4.4 takes the SOCKS5 greeting, the RFC 1929 login and the `CONNECT`
in one stream Write — a synthetic, environment-scoped result of
[synthetic danted](../../08-synthetic-danted/plan.md), accepted by the user as `exit.one_write`
in the [parent stage's contract](../plan.md#accepted-contract). This task adds the exchange
to `internal/exit/socks5.go`: a mode of the dialer that sends the batch in one Write and
reads the three replies in order from the same stream. It adds no profile key and no
application binding — [profile key and binding](../02-profile-key-and-binding/plan.md) does
that — and it leaves the sequential path, still the default, as it is.

The mode belongs to one connection's exchange, not to the exit as a whole: `Check` has no
target and stays sequential, so the setting can never put a hidden `CONNECT` at startup.

## Steps

1. Add `OneWrite bool` to `exit.SOCKS5` (`internal/exit/socks5.go:88`), documented as the
   measured mode: greeting, login and `CONNECT` in one write; it applies to `Dial` only and
   never to `Check`. Rewrite the package comment (`internal/exit/socks5.go:15`) that still
   says the live answer is to be seen: state the measured `danted` 1.4.4, the synthetic
   environment scope, the off default and what a server that reads one message per `recv`
   does to the batch.
2. Split the reply reading in `exchange` (`internal/exit/socks5.go:169`) into helpers —
   method choice, RFC 1929 status, `CONNECT` reply (version, code, address type, bound
   address) — with the exact `io.ReadFull` sizes and the current error classification kept.
   The sequential path must produce the same messages and errors as before: the existing
   cases in `internal/exit/socks5_test.go` stay unchanged and green.
3. Build the batch from the same pieces: with a login `[5,1,2]` + the RFC 1929 message
   (the body of `login`, `internal/exit/socks5.go:250`, becomes a byte builder) + `req`;
   without a login `[5,1,0]` + `req`. Write it with one `conn.Write`, continuing with the
   remainder on a short Write (`writeAll` loop) without turning that into a second logical
   message. Keep the deadline setup of `handshake` (`internal/exit/socks5.go:122`): the
   batch is covered by `handshakeTimeout` and by the context's `AfterFunc`.
4. Read the replies in order with `io.ReadFull` on the same conn — method choice, login
   status when a login was sent, then the `CONNECT` reply — and with no buffered reader:
   bytes read past a reply would be the target's. Return the stream with deadlines cleared
   and its first byte still the target's, as the sequential path does.
5. Apply the contract's failure behavior in the one-write path. A method other than the one
   offered (including `0x00` when only `0x02` was offered, `0xff`, or an unknown method), a
   truncated or malformed reply, and a stream closed or dropped before a reply are errors
   that name `exit.one_write` and say there is no fallback; keep `ErrNotSOCKS5` as the
   wrapped kind for a non-SOCKS5 answer. A refused login stays `*LoginError`; a refused
   `CONNECT` stays `*ConnectError{Address, Code}`. A failed batch must not open a second
   stream and must not retry the same request sequentially.
6. Keep `Check` (`internal/exit/socks5.go:112`) sequential whatever `OneWrite` says: enter
   the one-write path only when there is a request (`Dial`). Its greeting offers the methods
   it offers today (`0x00` alone, or `0x00` and `0x02` with a login) and it sends no
   `CONNECT`.
7. Extend the SOCKS5 fixture `socksServer` (`internal/exit/server_test.go:16`) rather than
   writing another one: log the greeting it received (the methods), add a mode that answers
   a chosen method regardless of what was offered, a mode that reads only the first message
   with one Read and closes (the server that reads one message per `recv`), a mode that
   writes the method choice, the login status and the `CONNECT` reply in one Write (with the
   `first` target bytes right after), and a mode that writes each reply one byte at a time.
   Over `pipe()` (`internal/exit/server_test.go:45`, a `net.Pipe`) the fixture's first Read
   returns exactly one client Write, so the batch is observable without timing.
8. Test the exchange in `internal/exit/socks5_test.go`, next to the existing cases:
   - one Write for the whole batch with a login (the greeting offers only `0x02`) and
     without one (only `0x00`), over `srv.pipe()`;
   - the sequential cases still pass, and its greeting log still shows both methods with a
     login;
   - coalesced replies in one Write and fragmented replies one byte at a time, each with
     the target's first bytes in the same segment, delivered intact;
   - full and partial I/O: a conn that takes only a few bytes per Write (the loop completes
     the batch) and one that returns one byte per Read (`io.ReadFull` reassembles);
   - refusals and malformed replies: `0x00` chosen with a login configured, `0xff`, an
     unknown method, a bad version at each stage, a truncated login status, an unknown
     address type in the reply, a short bound address; the `CONNECT` refusal keeps
     `*ConnectError` and does not wait for the rest;
   - a fixture that reads only the greeting and closes: the error names `exit.one_write`, is
     not `*LoginError`, and the opener ran once;
   - a refused login: `*LoginError`, names the key, opener ran once, no sequential retry;
   - `TestCheckOneWriteStaysSequential`: `SOCKS5{OneWrite: true, User, Password}.Check`
     succeeds, the fixture's first Read is the 4-byte greeting alone and no `CONNECT`
     follows;
   - cancellation during the batch read and a closed stream: the context cause (or the
     stream error), the stream closed once, no fallback, as in `TestDialCancel` and
     `TestDialStreamClosed`.
9. Extend the generated loopback tunnel test (`internal/exit/tunnel_test.go:56`) with a
   one-write case: the in-process `server.StartTest` with the reserved `t.example.com` and a
   generated key, a login, `Check` first, then a one-write `Dial` to the echo target, a
   50 KB echo, a closed port returning `*ConnectError` with the session alive, and an
   upstream that drops the batch failing with the key-named error without a retry. The
   fixture listens on loopback TCP there, so only behavior is asserted; the single-Write
   property is proved over `net.Pipe` in step 8.

## Done when

- `exit.SOCKS5` has `OneWrite`; with it set and a `Dial`, one Write carries the greeting,
  the RFC 1929 login when there is one, and the `CONNECT`; the greeting offers only `0x02`
  with a login and only `0x00` without; the sequential path is unchanged in bytes, messages
  and errors, and every existing `internal/exit` test passes without changed expectations.
- One-write `Dial` succeeds with the replies coalesced into one segment and with them
  fragmented byte by byte; the target's first bytes arrive intact; a 50 KB echo passes
  through the generated loopback tunnel with `t.example.com`, a generated key and a login.
- The contract's incompatibilities — a method not offered, a refused login, a batch the
  exit does not take — yield errors that name `exit.one_write`; the classification is kept
  (`*LoginError` for a refused login, `*ConnectError` for a refused target, `ErrNotSOCKS5`
  for an unoffered method, a stream error for a dropped batch), and each `Dial` opens
  exactly one stream with no sequential retry.
- A cancelled or closed exchange ends with the context cause (or the stream error) within
  the deadline, closes the stream once and does not fall back.
- `Check` with `OneWrite` set stays sequential: the greeting as today, no `CONNECT`, no
  hidden target; a refused login remains `*LoginError`.
- Full and partial I/O cases pass: a short Write is completed, one-byte reads are
  reassembled.
- `go build ./...` and `go test -race -count=1 ./internal/exit` pass, and the stage's
  [local validation matrix](../../plan.md#local-validation-for-code-children) for code
  children passes (vet split, Windows cross-build and test compilation, formatting,
  `git diff --check`); no dependency, network access or real value is involved.

## Risks and where it is easy to get it wrong

- A buffered reader over the stream is the easy way to break the "first target byte" rule:
  the reply and the target data can share a segment. Exact `io.ReadFull` reads on the conn,
  as in the sequential path, avoid it.
- The single Write is the point of the mode: a loop that writes each message separately, or
  gives up on a short Write, turns it into the sequential path with a worse error.
- Dropping the deadline/`AfterFunc` cleanup, or returning the stream after a failed batch,
  makes a stopped exchange hold the application's connection.
- Reusing the one-write path in `Check` would send a `CONNECT` with no target or a batch the
  server may not take, and would break the startup contract.
- The fixture must keep its sequential reading for the existing cases: a global change to
  its `serve` can silently change what the old tests prove.
