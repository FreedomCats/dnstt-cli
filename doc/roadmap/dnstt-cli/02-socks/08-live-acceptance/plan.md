---
kind: stage
status: done
base: 8302b38
---

# Live acceptance

The stage's goal checked against a real server, on both platforms, and the numbers the live
runs settled. `E1`, `S8`.

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

The timeouts are settled from the live measurements: keepalive 30 s, handshake 10 s,
`degraded` 25 s. The `danted` question is answered: `danted` 1.4.4 takes the SOCKS5 greeting,
login and `CONNECT` in one write, and `exit.one_write` ships off by default for `socks5`
only (R3).

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
  the presets have been seen live on Linux; the Windows run is deferred until a machine is
  available and is recorded as `not seen`, not counted as a pass.
- The measured keepalive and handshake timeouts are set in the code.
- The `danted` question is answered and, if it pays off, the exit setting exists.
- The `network` resolver works in `connect` and `resolvers check` on Linux, covered by unit
  and injected-DNS tests; the Windows path is cross-built and test-compiled only and is
  `not seen`; no live `network` run was made. Unpinned resolver host names use the R5
  bootstrap-server selection.
- The results are recorded in this stage and in `architecture.md` without the environment,
  addresses or resolvers; the course of the runs stays in `doc/local/` (`AGENTS.md`).

## Outcome

The live acceptance ran on Linux in disposable containers. The SOCKS5 part is synthetic: a
local `dnstt-server` with `t.example.com` and a generated key, a local `danted` 1.4.4 with one
login, a fixed-body local target and headless Chromium; it is not the user's exit, server or
network. The HTTP part ran against the user's real server: the HTTP CONNECT exit with Basic
authentication, one DoH, one DoT and one public UDP resolver (the user supplied no own UDP
resolver; the user's own UDP path is not proven). Windows is deferred until a machine is
available and is `not seen`, not a pass.

Synthetic SOCKS5 (S1–S4). The browser loaded the fixed target through the local SOCKS5
listener; after a restart of the local server the session recovered in 52.8 s (25 s
`degraded`, the 30 s keepalive, the stop/start and the redial) and new requests worked
without restarting `connect`; a change of the client container's address in its own network
namespace did not break the session (0 s, the SOCKS5 transport opens a per-query UDP socket),
and the next requests worked. A clean SIGINT left no process or container; the host matched
the baseline apart from other projects' Docker churn. No `dnstt-cli` defect was found.

Real server, HTTP exit (R0–R10). The temporary profile loaded from the read-only mount and
the control fetch outside the tunnel answered 200. Over DoH, DoT and UDP the browser loaded
the target through the authenticated exit. The DoT browser phase's first fetch failed (empty
DOM, no response headers) while the control through the same listener and `resolvers check`
passed; the planned one-time `--disable-quic` recheck passed, the first failure is kept, and
the side is the browser. Over DoH and the public UDP resolver the upstream `dnstt-client` and
the in-process product each passed all attempts in the independent `compare`, every attempt
200, with the first real application CONNECT authenticated; `connected` before it was
readiness only. With a wrong password the exit answered 407, the supervisor stopped and did
not retry it as a resolver failure. The dead-resolver check named the dead entry and exited
1 when none passed; seven of eight built-in presets completed a handshake, the failing DoH
preset is attributed to the resolver (both pinned 443 paths were open and the same resolver's
DoT preset passed); all five `DefaultUTLS` labels completed a handshake through at least one
DoH and one DoT preset. The real-server sessions were stopped with SIGINT, and the host check
matched the baseline; the temporary profiles and stores were deleted.

Recorded limits: the source-address change proof is indirect (the image has no `ip`/`ss`;
the change was made inside the network namespace and the idle socket list was empty), the
synthetic clean stop's SIGINT reached the `script` wrapper rather than the product (the
product exited 0 and no process remained, but its own stop line is missing), and the host
comparison is not literal because of other projects' Docker churn. The synthetic SOCKS5 run
used the local literal UDP resolver only; DoH and DoT were exercised through the HTTP exit,
so the SOCKS5 exit over DoH and DoT is not claimed. The `network` resolver is covered by
unit and injected-DNS tests; its Windows path was cross-built and test-compiled only, and no
live `network` run was made (the live profiles used pins or a literal UDP entry). A system
DNS on a loopback stub that is not the systemd-resolved stub is not used by the reader, a
recorded limitation; allowing it is a separate change. The HTTP result does not replace the
SOCKS5 acceptance, and the synthetic SOCKS5 result does not stand for the user's server,
exit or network.

The final timeouts are keepalive 30 s, handshake 10 s and `degraded` 25 s. The `danted`
question is answered by the measured one-write exchange and the `exit.one_write` setting,
off by default for `socks5` only. The results are recorded in `architecture.md`; the course
of the runs and the raw values stay in `doc/local/` and `doc/runs/`.
