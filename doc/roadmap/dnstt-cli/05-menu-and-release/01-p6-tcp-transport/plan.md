---
kind: task
status: done
detailed: true
---

# Plain DNS over TCP (P6)

## Task

P6 of `architecture.md` §07: a resolver entry `tcp:host[:port]` carries the tunnel's DNS
messages over a plain TCP connection (RFC 7766), each message prefixed with its two-octet
length exactly as DNS over TLS does, without TLS. Two things need it: `slipnet://` links name
`tcp` as the transport (field 22 of `architecture.md` §10), and some networks pass TCP :53
where UDP is blocked.

The core knows DoH, DoT and UDP (`core/client/transport.go:32-36`, `:197-300`), and the shell
refuses the transport by name (`internal/resolvers/resolvers.go:139-140`, "transport tcp is
not supported yet"). The framing, queueing and redial of a length-prefixed stream transport
already exist in `newTLSPacketConn` (`core/client/tls.go`); it takes the dialer as a
parameter, so P6 is a plain dialer on the same code, not a second transport.

The core is vendored (R1): the edits go into `scripts/vendorcore/_files/client/` — the files
the generator adds, `transport.go:1` marks itself as added — and `core/` is regenerated from
the recorded upstream commit, as in P2–P5. `core/` is never edited by hand
(`doc/architecture.md` R1, §02).

Out of scope: the `slipnet://` import that will write such a resolver (child 3), the payload
cap (child 2), a profile key for the transport (none is needed: the resolver prefix carries
it), and any change to the DoH, DoT or UDP paths.

## Steps

1. **The core transport** — `scripts/vendorcore/_files/client/transport.go`:
   - add `TCP Transport = "tcp"` to the constant block at `:32-36`, with a comment: plain DNS
     over TCP, the two-octet length prefix as in DoT, no TLS and no uTLS fingerprint;
   - extend the `Transport` comment: the dialer decides whether the framed stream runs over
     TLS or over plain TCP;
   - add `case TCP:` to `openTransport` (`:197`): `net.SplitHostPort(r.Address)` with a clear
     "TCP address" error, `s.canLookup(host)` for a host name (the DoT rule, `:127-135`: a
     name needs `Config.LookupIP`), then a dial function that (a) keeps the session's
     cancellation active until the TCP connection is returned, with the same
     `context.AfterFunc(s.ctx, cancel)` bridge as DoT's `dialTLS` (`:244-269`), and (b) calls
     `s.dial(ctx, network, addr)` (`:139-168`), so every socket goes through
     `Config.Control` and `s.track`;
   - pass that dialer to `newTLSPacketConn(ctx, r.Address, dialPlain, cfg.Logf)` — it gives
     the length prefix, the queue and the redial with backoff — then `s.track(pconn)` it and
     return `turbotunnel.DummyAddr{}` and the conn, as the DoT case does (`:272-279`);
   - keep `newTLSPacketConn`'s contract: its own `dialTimeout` (30 s) for redials, the
     caller's ctx for the initial dial only. Do not rename the symbol: DoT and its rules use
     it.
2. **The core test** — new `scripts/vendorcore/_files/client/tcp_test.go`:
   - a loopback TCP stub resolver: read the two-octet length and the message, forward the
     message over UDP to `server.StartTest(domain, upstream)` (`core/server/testing.go:28-30`),
     read the answer and write it back with the length. The in-process server speaks UDP
     only, so the stub is the resolver the session talks to over plain TCP; it belongs in the
     test file, not in `core/` proper. The upstream is a loopback TCP listener that echoes,
     as in the other session tests;
   - one test builds `client.Dial` with `Config.Resolver = Resolver{Transport: TCP, Address:
     <stub>}`, opens a stream or sends a message through the session, and asserts the answer;
   - a second test asserts the wire: the first bytes the stub reads are a length prefix and a
     DNS message, not a TLS record, and a TLS ClientHello would fail the test;
   - a third test closes the session and asserts the stub's read returns (the connection is
     closed with the session), and a fourth drops the connection in the stub after the first
     message and asserts the next message arrives through the P2 redial, with a short backoff
     in the test;
   - keep the tests hermetic: loopback only, no public resolver, no live call.
3. **The shell's parser** — `internal/resolvers/resolvers.go`,
   `internal/resolvers/resolvers_test.go`:
   - accept `client.TCP` in the switch at `:136-143`, with the default port 53 (the
     `portUDP` default of `:157-163`); the unknown-transport message becomes "doh, dot, udp
     or tcp"; the "not supported yet" branch goes away;
   - pins (`@address`), the host-name rules and the `String`/`Parse` round-trip need no
     change;
   - tests: `tcp:192.0.2.53` gets port 53; `tcp:ns.example:5353` keeps its port; a name with
     pins parses; a bad port, an empty address and an unknown transport are refused; `String`
     then `Parse` gives the same resolver.
   - `internal/resolvers/presets.go` is not touched: no tcp preset is added.
4. **User-facing text** — `internal/cli/resolvers.go`: the `resolvers` help and the `check`
   description that list transports say "doh, dot, udp or tcp". `dnstt-cli.example.toml` is
   left alone: its entries are examples, not a list of every transport.
5. **Regeneration** — from the verified clone at the recorded commit (`core/CORE_VERSION`,
   `0c5c52a`): `go run ./scripts/vendorcore -upstream ../dnstt`; then
   `go run ./scripts/vendorcore -check -upstream ../dnstt` must pass; run the generation a
   second time and compare `core/` byte for byte. `core/CORE_VERSION` gets new `files` and
   `added` counts with the new test file, written by the tool, never by hand. No version,
   dependency or upstream-metadata change.
6. **Checks** — `gofmt` outside `core/` (it is never run through it, `doc/architecture.md`
   §02), `go build ./...` and `CGO_ENABLED=0 go build ./...`, the CI vet split on Linux and
   Windows, `go test -race ./core/client ./internal/resolvers`, the full
   `go test -race -count=1 ./...` with `DNSTT_CLI_TEST_DBUS=1`,
   `DNSTT_CLI_TEST_NETNS=0` and the four external `netguard` live tests excluded by `-skip`
   (no live call), the Windows cross-build with `go test -c` of every package with tests, and
   `git diff --check`.

Easy to get wrong:

- reusing `newTLSPacketConn` for plain TCP: its name and comments speak of TLS. Say in the
  core's comment that the dialer decides TLS or plain, and pin the bytes in the test — a
  plain resolver would otherwise "work" through a TLS handshake attempt that never answers;
- the DoT ctx bridge is what cancels a dial stuck in TCP connect; drop it and `closeAll`
  waits for the dial timeout;
- the initial dial uses the caller's ctx, redials use `dialTimeout`; keep that split;
- the vendorcore metadata counts are easy to hand-edit wrongly; let the tool write them;
- the socket guard (`scripts/vendorcore/sockets.go`) must see the new socket through
  `s.dial`, not a raw `net.Dialer`.

## Done when

- `tcp:host[:port]`, with and without `@pins`, parses, normalizes and round-trips; a `tcp:`
  entry is accepted by `connect` and by `resolvers check`; the "not supported yet" refusal is
  gone.
- The core dials a plain TCP resolver, frames every message with the two-octet length, sends
  no TLS, closes the connection with the session and redials after a dropped connection; the
  four new loopback tests prove it.
- `core/` is generated from the recorded upstream commit with the new file;
  `vendorcore -check` passes, a second generation is byte-identical, and `core/CORE_VERSION`
  is written by the tool.
- The full local matrix of step 6 passes; no live run, no profile, no new dependency.
- No link import, no payload cap and no profile key is part of this task.

## Outcome

Done: `tcp:host[:port]` is a resolver transport both in the parser and in the core.

- Core (`scripts/vendorcore/_files/client/transport.go`, generated into `core/client/`):
  `TCP Transport = "tcp"`; `openTransport` splits the address, requires `Config.LookupIP`
  for a host name, and passes a plain dialer to `newTLSPacketConn` — the two-octet length
  prefix, the queues and the P2 redial are DoT's, the TLS is not. The dialer keeps the
  session cancellation active through the dial with DoT's `context.AfterFunc` bridge and
  calls `s.dial`, so `Config.Control` and `s.track` see the socket. `newTLSPacketConn` was
  not renamed. The session log prints no uTLS fingerprint for TCP.
- Tests (`_files/client/tcp_test.go`, four loopback tests): a session through a TCP stub
  that forwards to `server.StartTest`; the wire is a length prefix and a parsed DNS query,
  not a TLS record; closing the session closes the TCP connection; a connection dropped
  after its first message is redialed and carries the session. The stub leaves P2's backoff
  globals at their defaults: `shortRedial`'s cleanup races with a transport entering
  `redial` at session close under `-race`, and the default redial's first attempt is
  immediate anyway.
- Parser (`internal/resolvers/resolvers.go`): `tcp` accepted, port 53 by default; pins,
  host-name rules and the `String`/`Parse` round-trip are unchanged; the "not supported
  yet" refusal is gone and the messages name all four transports. No preset and no
  bootstrap transport; `internal/resolvers/presets.go` is untouched.
- Help (`internal/cli/resolvers.go`): the resolvers description names the four entry forms.
  Step 4 expected such a list to exist already; it did not, so one sentence was added
  instead of a list being edited.
- Generation: `core/` comes from `0c5c52a` with the new file; `core/CORE_VERSION` is
  written by the tool (`files = 34`, `added = 13`); `vendorcore -check` passes and a second
  generation is byte-identical.

Checks on the final tree: `gofmt` outside `core/` clean; `go build ./...` and
`CGO_ENABLED=0 go build ./...`; the CI vet split on Linux and with `GOOS=windows`;
`go test -race ./core/client ./internal/resolvers`; the full
`go test -race -count=1 ./...` with `DNSTT_CLI_TEST_DBUS=1`, `DNSTT_CLI_TEST_NETNS=0` and
the four external `netguard` live tests skipped; the Windows cross-build and `go test -c`
of all 19 packages with tests; `git diff --check`.

Out of the plan and not done: no `slipnet://` import, no payload cap, no profile key, no
new dependency, no live run. Not touched, worth a later look: the redial failure log still
says `dial tls:` for plain TCP (shared DoT path); `internal/resolvers/presets.go` still
lists `(doh:, dot:, udp:)` in its "no transport" hint; `doc/architecture.md` §04 still
lists `DoH | DoT | UDP` in the `Resolver` comment.
