---
kind: stage
status: todo
depends: [02-live-run-script]
---

# Timeouts and the `danted` question

The two preliminary numbers measured on the live server and set in the code, and the
`danted` question answered. `E1`, `S8`; `architecture.md` R2, R3, §04, §08, §13.

- **Keepalive.** The client's smux keepalive timeout: how long a session may hear nothing
  from the server before it counts as dead. It stands at the upstream 2 min; R2 expects
  about 30 s against the server's 10 s keepalives. Too short — healthy sessions through a
  slow resolver are torn down; too long — a dead session (a server restart, a network
  change in `socks` mode, where nothing tracks the network) is noticed late.
- **Handshake.** `supervisor.DefaultHandshakeTimeout`, 20 s: how long a dial waits for the
  Noise handshake before it moves to the next resolver. It has to cover a slow but working
  resolver, and no more.
- **`danted`.** Whether it takes the SOCKS5 greeting, login and `CONNECT` in one write.
  If it does, the exit gets that as a setting (R3), saving two round trips per connection
  with a login.

Measuring needs the user's server and resolvers. The probes extend the tool of
[Live-run tool](../02-live-run-script/plan.md); the raw numbers stay in `doc/local/`.

Where the numbers live now: `client.Config.KeepAliveTimeout` (0 — the upstream
`idleTimeout`, `core/client/main.go`), never set by `internal/app/tunnel.go`; the smux
interval is the smaller of 10 s and half the timeout (`core/client/session.go`);
`DefaultHandshakeTimeout` and `DefaultDegradedAfter` (25 s) in
`internal/supervisor/tunnel.go`; a separate 20 s in `resolvers check`
(`internal/cli/resolvers.go`). The exit's exchange is `exit.SOCKS5.exchange` in
`internal/exit/socks5.go`; the exit keys of a profile are `type`, `user`, `password_ref`
(`internal/config/profiles.go`).

## Done when

- Over each transport — DoH, DoT, UDP — through the user's resolvers, the durations of a
  series of handshakes and the longest silence of the server in a healthy idle session of
  several minutes are measured and kept in `doc/local/`.
- The chosen keepalive timeout and handshake timeout, each with the rule it was derived
  by, are set in the code: `connect` sets the keepalive timeout of its sessions, the
  handshake timeout replaces the preliminary 20 s, `resolvers check` uses the same
  handshake timeout, and `DefaultDegradedAfter` is checked against both. Tests pass.
- A one-write greeting, login and `CONNECT` sent through the tunnel to the user's
  `danted` has a recorded answer: taken or not.
- If it is taken: the SOCKS5 exit has the setting, the profile has a key for it with
  validation, and tests cover both ways of the exchange. If not: the comment in
  `internal/exit/socks5.go` and R3 say that `danted` does not take it.

## Risks and what is needed from the user

- A new key in the exit section of the profile is a format that stays: its name and
  default are agreed with the user before the code.
- The silence a healthy session sees depends on the resolver, not only on the server's
  10 s: a resolver that holds or drops answers stretches it. The timeout is derived from
  the slowest transport that is meant to work, not from R2's 30 s; if that is far from
  30 s, R2 is corrected in [Recording the results](../05-record-results/plan.md).
- Needed from the user: idle sessions of several minutes over each transport, and a
  `danted` with a login on their server.
