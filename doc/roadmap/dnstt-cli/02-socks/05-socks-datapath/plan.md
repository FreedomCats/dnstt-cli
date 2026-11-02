---
kind: task
status: done
---

# SOCKS5 datapath

## Task

The local SOCKS5 listener that turns a program's proxy setting into a stream through the
exit. `internal/datapath`. This is the last piece that makes `connect` usable by a browser
in `socks` mode. `E1`, `S5`; `architecture.md` R3, R6, §05.

## Steps

1. A SOCKS5 server on loopback, `127.0.0.1:1080` by default.
2. `CONNECT` with a domain or an address → `exit.Dialer`; the reply is the RFC 1928 code
   the exit returned, or success, and the connection then runs as a plain relay both ways.
3. `BIND` and `UDP ASSOCIATE` are refused at once with the right reply code — the tunnel
   carries streams only, and a fast refusal lets the program fall back instead of waiting.
4. "No session" — the tunnel has not come up yet — is refused rather than queued.
5. The listener opens before the first session and lives as long as `connect`, so a program
   can be pointed at it while the tunnel is still dialing.
6. Carry over the server half of csqtt-cli's `socks5.go` where it fits; the client half is
   `internal/exit` and is already done.
7. Tests: a real SOCKS5 client in the test, through the in-process dnstt server and an echo
   server behind the exit.

## Done when

Tests with a SOCKS5 client in the test pass through the in-process server, on Linux and
Windows; `go vet ./...` and `go test -race ./...` are clean.

## Depends on

[Supervisor](../04-supervisor/plan.md) and [SOCKS5 exit](../02-exit/plan.md) — both done.

## Outcome

Done as planned: `datapath.NewSocks` with `SocksOptions{Listen, Dialer, Up, Logf}`.
`internal/datapath` imports `internal/exit` only; the "no session" check is the `Up func()
bool` hook, called before `Dial` — `false` is refused with `0x03` without asking the exit,
`nil` is no check. The caller (S7) passes its own view of the tunnel, e.g. connected or
degraded. The interfaces of `datapath.go` are unchanged.

Replies: success `0x00`, a `*exit.ConnectError` its own code, anything else (a login
refusal, an exit that is not SOCKS5, the session ended) `0x01`; `BIND` and `UDP ASSOCIATE`
`0x07`, an unknown address type `0x08`, an empty host name `0x04`. Only the method "none"
is offered; a client without it gets `0xFF`. Names go to the exit as names, IPv6
addresses as addresses — the exit decides whether it can reach them. The greeting and the
request are bounded by 10 s, the dial by 60 s (the exit's own limit), the reply by 10 s;
the relay has no deadlines. `smux.Stream` has no `CloseWrite`, so a half-close of either
side ends the whole connection.

Tests run a real SOCKS5 client (`golang.org/x/net/proxy`) through the in-process dnstt
server to an echo server behind a SOCKS5 exit, plus a fake dialer for reply codes, the
`Up` hook, `Close` with idle, half-done and relaying clients, and no goroutines left
behind. The Windows tests compile; they are run by the CI.

Left for [Commands](../07-commands/plan.md): `smux.Stream.Close` waits up to 30 s of its
own for its frame to go out, so `Socks.Close` over a stalled session that is not yet
closed can take that long. The stop of `connect` should close the session (stop the
supervisor) before or together with `Socks.Close`.
