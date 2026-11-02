---
kind: task
status: done
detailed: true
---

# Session API and socket hook

## Task

Core patch P1: the upstream client becomes a package with a `Dial` that returns one
session, and every socket the core creates passes through a caller-supplied `Control`, so
that later the tunnel's own queries can bypass the tunnel. A guard keeps new sockets from
appearing outside the hook. `E0`, `F3`; `architecture.md` R2, R4, §04, §06.

## Steps

1. `session.go` in `scripts/vendorcore/_files/client/`: `Config`, `Dial(ctx, Config)
   (*Session, error)`, `Session` with `OpenStream`, `Done`, `Err`, `Stats`, `Close`.
   `Dial` builds transport → `NewDNSPacketConn` → KCP → Noise → smux exactly as upstream
   `run` does, with the same KCP and smux settings, and returns after the Noise handshake.
   The client-side smux keepalive timeout is a `Config` field.
2. A transports file in the same place, built from the upstream constructors
   (`NewHTTPPacketConn`, `NewTLSPacketConn`, `NewUTLSRoundTripper`) with `Config.Control`
   on every dialer and `Config.LookupIP` for host names; a `nil` `LookupIP` keeps the
   system resolver for now.
3. A rule: `utlsDialContext` takes the `net.Dialer` from its caller instead of building
   one itself. Nothing else in upstream files.
4. `core/server/testing.go` (an added file): a helper that starts the upstream server `run`
   on a loopback UDP socket with a generated key and a given upstream TCP address.
5. The socket guard (`scripts/vendorcore/sockets.go`): scan `core/` for `net.Dial*`,
   `net.Listen*`, `net.Dialer{`, `tls.Dial*`, `tls.Dialer{`, `http.Transport`,
   `net.Resolve*`, `LookupHost`/`LookupIP`, and check every hit against a table of file,
   call, count and reason. A new hit or a changed count fails `vendorcore` and `-check`.

## Done when

A test in `core/client`:

1. starts the in-process server with an echo TCP upstream, dials it over UDP on loopback,
   opens two streams in parallel and gets the echo back;
2. counts `Control` calls — one per socket the client created;
3. stops the server and sees `Session.Done` closed and `Err` set within the (shortened in
   the test) keepalive timeout;
4. `vendorcore -check` passes, and the guard fails when a `net.Dial` is added to a core
   file.

## Outcome

The session API dials the in-process server and passes its tests under `-race`: parallel
streams, one `Control` call per socket, the end of a session when the server goes silent,
no goroutines left after `Close`, and DoH and DoT connecting through `Control`.

Deviations from the steps, each also in `architecture.md`:

- uTLS takes a dial *function* from its caller, not a `*net.Dialer`: `LookupIP` has to sit
  between the name and the socket, and a dialer would resolve the name itself.
- P1 also ends the upstream transport loops on close. Upstream never closes a session, and
  without the rule `Session.Close` leaves goroutines behind.
- The in-process server could panic when a test stopped it; a rule keeps its channel open.

Not covered: a full session over DoH or DoT — the in-process server speaks UDP only. That
is checked live in stage 2.
