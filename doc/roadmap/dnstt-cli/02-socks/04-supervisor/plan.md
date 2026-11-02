---
kind: task
status: done
detailed: true
---

# Supervisor

## Task

`internal/supervisor` — the component that owns the one session: dials through the resolver
list, checks the exit, notices a dead session, reconnects with a backoff, restarts on a
network change and hands streams to the datapath. Everything above it (the datapath) and
below it (resolvers, exit) is decided; this is where the states of `architecture.md` §08
come alive. `E1`, `S4`; `architecture.md` R2, §08.

## Steps

1. `Tunnel` over `client.Dial`, a `resolvers.Pool` and an optional `resolvers.Bootstrap`.
2. Dialing: a handshake timeout; a failed dial moves to the next resolver at once and
   strikes it in the pool; a full round without a session — backoff 1·2^n s up to 64 s, or,
   if no handshake has completed since `connect` started, the end.
3. `*NeverConnectedError` names every resolver with its error and the four reasons (wrong
   domain, wrong key, server down, every resolver blocked).
4. A new session is checked by `Options.CheckExit` (`exit.SOCKS5.Check`): a login refusal
   and an exit that is not SOCKS5 stop the tunnel; any other error is a retry through the
   same resolver.
5. Session end: a session that died by itself goes to the pool; one that lived a minute
   clears the backoff and is redialed at once, a shorter one waits the next delay; the same
   resolver first, unless it has just been put aside.
6. No data from the server for 25 s — `StateDegraded`, data again — `StateConnected`.
7. `Restart(reason)` — ends the session or the attempt, clears the backoff, the pool and the
   bootstrap cache, and dials from the top; it cuts a delay short.
8. `OpenStream(ctx)` — opens a stream through the session that is up, or waits for the next
   one until `ctx` ends (`ErrNoSession`); refuses once the tunnel stops (`ErrStopped`).
9. A stop calls `Options.Drain` with the session still up, for 5 s at most; the second
   Ctrl+C cuts it short (`Interrupt`, `WithAbort`, `OnAbort`).
10. `Status` — the snapshot for the status line.
11. Tests with a fake dialer for each state, and a test with the in-process server where the
    session dies of silence while the server is down and comes back by itself.

## Done when

State tests with a fake dialer pass, and a test with the in-process server sees a session
recreated after the server restarts.

## Outcome

Done as planned; the states of §08 are covered by tests with a fake dialer. The test with
the in-process server uses `TestServer.Reopen`: the session dies of silence while the
server is down and comes back by itself.

Still preliminary, to be measured live: the handshake timeout of 20 s
(`DefaultHandshakeTimeout`) and the client keepalive timeout, which stays at the upstream
2 min. `OpenStream` refuses "no session" at once for an already ended `ctx` — that is what
the datapath needs until it sets its own deadlines.
