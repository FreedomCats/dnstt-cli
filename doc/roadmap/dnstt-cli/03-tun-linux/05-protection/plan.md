---
kind: task
status: todo
depends: [04-connect-wiring]
---

# DNS protection and kill-switch

## Task

The leak protection of `architecture.md` §05a in the `tun` connect flow: the nftables table
`inet dnstt-cli` with the `owner` flag is opened before connecting; while a session is up,
port 53 is open only to the adapter's DNS address, loopback and the marked sockets; while
no session is up, the kill-switch's "closed" set blocks system traffic with the §05a
exceptions; IPv6 is closed outward; `-no-kill-switch`, `-allow-leak` and the profile's
`kill_switch` choose the behaviour for one run or for good. The nftables implementation,
the mark and the netns tests are already in `internal/linuxsys` and `internal/netguard`;
this child wires them into the datapath and the command.

The wrapper that turns the guard's `Disable` into `Lockdown` can be carried from
csqtt-cli (MIT, the `killSwitchGuard` with its tests), without the captcha branches: there
is no captcha, so the kill-switch is never lifted mid-session. The core's sockets are
recognised by `netguard.SocketMark` (`SO_MARK`), not by uid: under `sudo` every root
program would otherwise pass.

## Done when

- With the kill-switch on, before the first session the guard is already in "closed": a
  public UDP send gets `EPERM`, loopback, DHCP, ND/RA and the local network pass, and DNS
  to the router is dropped even on the local network. The startup line says system traffic
  is blocked.
- When a session comes up the rules become DNS protection: DNS passes only to the
  adapter's DNS address and to the marked sockets; other traffic passes; IPv6 outward is
  closed and the local IPv6 network stays open. The "tunnel up again" line is printed.
- On every reconnection the guard returns to "closed" before the routes are removed, and
  the "tunnel reconnecting" line is printed. On `Close` the table is gone; after `kill -9`
  it is gone too (the `owner` flag).
- `-no-kill-switch` for one run and `kill_switch = false` in the profile remove the
  "closed" set and let traffic pass between sessions, with a line saying so. A profile
  with no `kill_switch` key is on, not off: the loader's default and the profile writer
  agree (the current plain `bool` would turn a missing key into off).
  `-allow-leak` connects when the table cannot be opened or a rule cannot be set, with a
  loud line, and is a flag only: the profile has no such key.
- The guard and the session use the same `netguard.SocketMark`; the mark is put only on
  the core's sockets. Opening the table without `CAP_NET_ADMIN` fails before connecting
  unless `-allow-leak` was given.
- A failed protection setup does not leave routes or system DNS behind: the datapath's
  suspend path runs, like any other `Start` failure.
- Tests: the wrapper with a fake guard session (no root); the existing netns test of the
  real table; `go vet ./...`, `go test -race ./...`, the Linux build and the Windows
  cross-build (the Linux-specific opening stays in a `_linux.go` file).

## Depends on

[Connect wiring, core bypass and network changes](../04-connect-wiring/plan.md).
