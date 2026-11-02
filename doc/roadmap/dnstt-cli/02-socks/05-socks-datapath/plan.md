---
kind: task
status: todo
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
