---
kind: task
status: done
detailed: true
---

# SOCKS5 exit

## Task

`internal/exit` — a `Dialer` that opens a stream through the session and speaks SOCKS5 to
whatever the server hands the stream to. This is the only place that knows the exit
protocol; the datapath above it sees a plain dialer. `E1`, `S2`; `architecture.md` R3.

## Steps

1. `exit.Dialer` (`Dial(ctx, "host:port")`) and its SOCKS5 implementation over a function
   that opens a stream (`exit.OpenStream`).
2. A host that parses as an address goes as IPv4 or IPv6; anything else as a name resolved
   on the server.
3. The greeting always offers "none", and "username/password" (RFC 1929) too when the
   profile has a login — so an exit open to anyone never gets the password.
4. Messages go one at a time, each after the answer to the previous one: a server that
   reads one message per `recv` drops the rest and hangs.
5. Errors as types: `*LoginError` (wrong login, a login required and the profile has none,
   no offered method accepted) — the supervisor's reason to stop; `*ConnectError` with the
   RFC 1928 reply code for the datapath to pass on; `ErrNotSOCKS5` for an exit that answers
   as something else; anything else belongs to the stream or the session.
6. `Check(ctx)` — log in without a request, for the supervisor on a new session.
7. The exchange is bounded by `ctx` and by 60 s of its own; the stream comes back without
   deadlines, and the reply is read to its end, so the target's first bytes stay in the
   stream.

## Done when

Tests against a SOCKS5 server in the test — with and without a login, a wrong password, a
refused `CONNECT` — pass, and a test through the in-process dnstt server reaches an echo
server behind a SOCKS5 server.

## Outcome

Done as planned, with the exchange bounded by `ctx` and 60 s, and the reply read to its
end. Tests run against a SOCKS5 server in the test, in memory and through the in-process
dnstt server (a name to an echo server, a refused port, a wrong password), all under
`-race`.

Left open until a live server: whether `danted` takes the greeting, login and request in
one write. If it does, two round trips per connection are saved and it becomes an option of
the exit; that is checked in live acceptance.
