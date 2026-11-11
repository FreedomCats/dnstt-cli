---
kind: stage
status: in-progress
base: 8302b38
---

# Live acceptance

The stage's goal checked against a real server, on both platforms, and the numbers that
were left preliminary until then. `E1`, `S8`.

What is to be checked, from the parent stage and the additional HTTP-exit decision:

- a browser through the local proxy reaches a site through the server's SOCKS5 exit with a
  login, over DoH, DoT and UDP resolvers, on Linux and on Windows;
- the same local proxy reaches a site through the selected HTTP CONNECT exit with Basic
  authentication, over the working DoH and UDP resolvers;
- the session survives a server restart and a network change, and new connections work
  without restarting the client;
- `resolvers check` names the dead resolvers and exits non-zero when none passes;
- every built-in preset completes a handshake against the real service;
- every failure is attributable: either the server or `dnstt-cli`.

Two numbers are still preliminary and are settled here: the client keepalive timeout
stands at the upstream 2 min, and `DefaultHandshakeTimeout` is 20 s. The exit has one open
question of its own: whether `danted` takes the SOCKS5 greeting, login and request in one
write, which saves two round trips per connection (R3).

Everything live — the server's domain and key, the exit login, the resolvers, addresses,
logs and the course of the runs — stays in `doc/local/` and `doc/runs/`, outside git
(`AGENTS.md`). The nodes below carry decisions and results only.

## Children

1. [Fix network resolver support](01-network-resolver/plan.md) — wire system DNS into
   `connect` and `resolvers check`, including bootstrap resolution of resolver host names.
   Task.
2. [Live-run tooling](02-live-run-script/plan.md) — the local-only comparison tool, the
   HTTP exit implementation needed by the selected service, and the protocol-specific
   comparison checks. Stage.
3. [Timeouts and the `danted` question](03-timeouts-and-danted/plan.md) — the keepalive and
   handshake timeouts measured live and set in the code; whether `danted` takes the SOCKS5
   messages in one write, and the exit setting if it does. Stage.
4. [Live runs](04-live-runs/plan.md) — the acceptance runs from a checklist in disposable
   containers: a synthetic local SOCKS5 setup, the user's real server for the HTTP exit,
   and the same on Windows when a machine is available. Stage.
5. [Recording the results](05-record-results/plan.md) — the results in this stage and in
   `architecture.md`, without the environment, addresses or resolvers. Task.

The network-resolver fix comes first because the default resolver and unpinned resolver
names must work before they can be accepted. The live-run tooling stage preserves the
working SOCKS5 comparison, adds the HTTP exit to the product, and then teaches the
comparison to exercise both protocols. The timeouts are set before the acceptance runs,
so those runs see the final values.

The HTTP profile-integration task follows the selected lazy readiness and
authentication-failure behavior: HTTP authentication starts on the first real application
`CONNECT`, `Connected` before that request means tunnel readiness only, and a final 407 stops
the supervisor without a resolver retry. Its protocol-only dialer and the existing SOCKS5
baseline can be accepted independently. No HTTP startup probe target is selected by this
replan. A successful HTTP run does not replace the SOCKS5 matrix or answer the `danted`
one-write question.

## What the code does not do yet

Facts from the code at `base`, which the children take into account:

- `connect` never sets `client.Config.KeepAliveTimeout` (`internal/app/tunnel.go`), so the
  session gets the upstream 2 min (`core/client/session.go`); `resolvers check` has its own
  20 s, not `supervisor.DefaultHandshakeTimeout` (`internal/cli/resolvers.go`).
- The entry `network` does not work in `connect`: `NetworkDNS` is a stub
  (`internal/app/tunnel.go`), and `resolvers check` skips the entry. A resolver host name
  without a pin has no bootstrap resolver unless the profile has its own `bootstrap`. The
  first child fixes these integrations using the existing Linux and Windows system-DNS
  readers and the R5 bootstrap-server order.
- Nothing calls `supervisor.Restart` on a network change: network tracking
  (`netguard.Watch`) is wired in the `tun` stages (`architecture.md` §12, E2). In `socks`
  mode a network change is survived through the keepalive: the old session dies of silence
  and the next one is dialed.
- There is no command that puts the exit password into the secret store: `profile set` is
  not interactive yet (the wizard is E4). On Windows the store is DPAPI, so the value
  cannot be written by hand.
- The SOCKS5 messages to the exit go one at a time (`internal/exit/socks5.go`).
- `exit.type = "http"` is not implemented: profile validation accepts only `socks5` and
  `ssh`, `internal/app/tunnel.go` builds only the SOCKS5 exit, and the live tool speaks
  SOCKS5 to the upstream side and to the local probe. The HTTP stage adds HTTP/1.1 CONNECT
  with Basic authentication while keeping the local datapath SOCKS5.

## Done when

- A live-run script starts the upstream `dnstt-client` against the same server, so that a
  failure can be attributed to the server or to `dnstt-cli`.
- `exit.type = "http"` is accepted by the product, uses Basic authentication for the
  server-side HTTP proxy, and is covered by in-process protocol tests without changing the
  existing SOCKS5 path.
- For HTTP, `Connected` before the first real application CONNECT is tunnel readiness only;
  that CONNECT performs Basic authentication, and HTTP 407 stops the supervisor without a
  resolver retry.
- The live-run tool's upstream comparison exercises an HTTP exit with an independent
  CONNECT probe and the in-process `dnstt-cli` path through its local SOCKS5 listener; it
  does not claim that the upstream binary implements the exit protocol itself.
- The three transports, the server restart, the network change, the dead-resolver check and
  the presets have been seen live; the Windows run repeats what the Linux run showed.
- The measured keepalive and handshake timeouts are set in the code.
- The `danted` question is answered and, if it pays off, the exit setting exists.
- The `network` resolver works in `connect` and `resolvers check` on Linux and Windows;
  unpinned resolver host names use the R5 bootstrap-server selection.
- The results are recorded in this stage and in `architecture.md` without the environment,
  addresses or resolvers; the course of the runs stays in `doc/local/` (`AGENTS.md`).
