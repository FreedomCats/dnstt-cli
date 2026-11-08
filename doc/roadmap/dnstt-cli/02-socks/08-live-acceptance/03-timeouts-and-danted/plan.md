---
kind: stage
status: in-progress
base: 703fcad
depends: [02-live-run-script]
---

# Timeouts and the `danted` question

The two preliminary numbers measured on the live server and set in the code, and the
SOCKS5 `danted` question answered. `E1`, `S8`; `architecture.md` R2, R3, §04, §08, §13.

This stage stays focused on the SOCKS5 exchange in `internal/exit/socks5.go` and the
server's `danted` service. Its probes use the SOCKS5 baseline child of the live-run
tooling stage. HTTP CONNECT response timing and HTTP proxy behavior are outside this
question and are handled by the HTTP exit and HTTP-comparison children.

The dependency on `02-live-run-script` is retained as the preparation-stage gate before
measurements; it no longer includes user-run acceptance. It does not make an HTTP proxy
a valid substitute for `danted`, and no HTTP-specific setting is added here.

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
[Live-run tooling](../02-live-run-script/plan.md); the raw numbers stay in `doc/local/`.

Where the numbers live now: `client.Config.KeepAliveTimeout` (0 — the upstream
`idleTimeout`, `core/client/main.go`), never set by `internal/app/tunnel.go`; the smux
interval is the smaller of 10 s and half the timeout (`core/client/session.go`);
`DefaultHandshakeTimeout` and `DefaultDegradedAfter` (25 s) in
`internal/supervisor/tunnel.go`; a separate 20 s in `resolvers check`
(`internal/cli/resolvers.go`). The exit's exchange is `exit.SOCKS5.exchange` in
`internal/exit/socks5.go`; the exit keys of a profile are `type`, `user`, `password_ref`
(`internal/config/profiles.go`).

## Children

1. [Measurement method and evidence checklist](01-measurement-method/plan.md) — define the
   quantities, private evidence form, safe run sequence and user decision gates. Task;
   documentation only.
2. [Local timeout probes](02-timeout-probes/plan.md) — extend the existing ignored tool
   with fresh-session handshake timing and healthy-idle receive-gap observations. Stage;
   separate local instrumentation results, with no live acceptance or product defaults.
3. [Local one-write SOCKS5 probe](03-danted-probe/plan.md) — add a bounded experimental
   sequential/one-write comparison over raw tunnel streams, with synthetic fixtures. Task;
   local tooling only.
4. [Live one-write test against danted](04-danted-measurements/plan.md) — the user establishes
   an authenticated SOCKS5 baseline and measures the sequential/one-write exchange through
   the tunnel to their actual `danted`. Task; user-run evidence and documentation.
5. [Live timeout measurements and value selection](05-timeout-measurements/plan.md) — the
   user measures DoH, DoT and UDP; derive candidate values and obtain the user's timeout
   and recovery decision before product changes. Task; user-run evidence and documentation.
6. [Apply the measured timeout policy](06-apply-timeouts/plan.md) — wire the selected
   keepalive into application sessions, share the handshake deadline with resolver checks,
   and test the degraded-state relationship. Task; product code and tests.
7. [Resolve the measured danted result](07-danted-result/plan.md) — agree and implement the
   enduring exit setting if supported, or record the measured rejection in the code comment
   and R3. Stage; split after the live result determines the branch.

The method is accepted before either tool extension. Both extensions preserve the accepted
SOCKS5/HTTP comparison instead of recreating it. The timeout measurements depend only on
their probe, and applying their selected values does not depend on the `danted` result.
The `danted` probe and user-run test do not require a product setting to exist first.
The `danted` test is the first user-run child so the unconfirmed service prerequisite is
checked before product work. No child may close a live gate using a local fixture or
HTTP success.

## Execution boundaries and user gates

- A SOCKS5 service with a login on the user's server is a prerequisite to confirm, not an
  established fact. Preparation can finish without it. If it is unavailable, keep the
  affected live child open and ask the user about the prerequisite or scope; neither an
  HTTP service nor a failed setup proves that `danted` rejects one-write exchanges.
- The user performs all real runs from the local instructions. Agent implementation and
  checks use synthetic temporary profiles/stores, generated identities and loopback only.
  They must not read, stat or modify actual profile/config/secret data or metadata, dump
  inherited environment, or call live services or resolvers. Changes to the user's service
  or network require their own authorization.
- The user authorized one narrow exception to the timeout probes' no-core-change boundary:
  the separate [context-aware initial DoT prerequisite](02-timeout-probes/03-dot-context/plan.md)
  repairs caller-context propagation and the uTLS handshake, with maintained vendor
  rules/templates and regressions. Only that prerequisite may change core/vendor sources;
  both probe-command tasks remain tool-only. Preserve successful-session independent
  lifetime and later redial/close behavior. This authorizes no new public API, dependency,
  profile field, timeout value, weaker TLS verification or changed fingerprint behavior.
  It does not change the measurement method, observation-limit/interruption criteria or
  any live goal or gate; a newly necessary enduring interface/dependency requires a user
  question through lead before proceeding.
- Timeout selection includes its derivation rule and the visible stability/recovery
  tradeoff, with smux's timeout-check scheduling included. Obtain the user's decision before
  applying the values. R2's approximate 30 s is not measurement evidence or an approved
  replacement for the current 2 min.
- The new exit key and its default require the user's approval after a positive `danted`
  result. That contract also covers authentication-method handling and any early sending of
  credentials: the current sequential path can avoid sending a password when the server
  selects no authentication. Do not decide those product behaviors in a local experiment.
- Raw observations, resolver mappings, captures and the course of the runs stay in ignored
  `doc/local/` or `doc/runs/`. Published nodes carry sanitized decisions and results only;
  no work dates, environment paths, profile names, server identity or credentials.
- [Live runs](../04-live-runs/plan.md) still owns the browser/platform/restart/network-change
  matrix. [Recording the results](../05-record-results/plan.md) still owns the final R2,
  core/supervisor documentation and parent-stage reconciliation. This split does not waive
  either sibling's criteria; the negative `danted` branch updates R3 here as already required.

## Local validation for code children

Use cached dependencies only (`GOPROXY=off`, `GOSUMDB=off`, `GOTOOLCHAIN=local`); add no
dependency and perform no external fetch, netns check or network mutation. Keep build/test
artifacts outside tracked paths. For the relevant code children:

- Run focused race regressions and the explicit tool race suite
  (`go test -race -count=1 ./doc/runs/_live`) when tooling changes. Preserve the existing
  two-exit loopback selftest and add focused synthetic tests of the new probes. Linux
  selftest suppresses Secret Service discovery and uses temporary file-backed storage.
- Run the applicable project race suite with `DNSTT_CLI_TEST_DBUS=1` and
  `DNSTT_CLI_TEST_NETNS=0`, excluding exactly
  `^(TestDualStackListener|TestLoopbackIndexBlocks|TestPhysicalIndexPasses|TestFamilyWithoutRouteRefuses)$`.
  The mandatory Secret Service/keyring tests use isolated D-Bus and must pass without skips.
- Check the static product build and exact CI vet split on Linux and Windows: full vet
  outside `core/`, `-testinggoroutine=false` only for `core/`. The two known upstream
  noise-test warnings from plain full vet are not new regressions.
- Cross-build the Windows product and, when affected, tool; compile all applicable Windows
  project tests and the tool tests. Do not execute Windows binaries or claim Windows/DPAPI
  runtime acceptance. `TestDNSGuardPermitsPrograms` is compilation-only here.
- Check formatting and whitespace, the tool's explicit Linux/Windows vet, exclusion from
  `go list ./...`, Git ignore rules and privacy. Never force-add `doc/runs/` or `doc/local/`.

Documentation-only and user-evidence children require link, consistency and privacy checks,
not a new code build. The pre-existing signal-reader follow-up stays separate unless a
concrete regression from this work is demonstrated.

## Done when

- Over each transport — DoH, DoT, UDP — through the user's resolvers, the durations of a
  series of handshakes and the longest silence of the server in a healthy idle session of
  several minutes are measured and kept in `doc/local/`.
- The chosen keepalive timeout and handshake timeout, each with the rule it was derived
  by, are set in the code: `connect` sets the keepalive timeout of its sessions, the
  handshake timeout replaces the preliminary 20 s, `resolvers check` uses the same
  handshake timeout, and `DefaultDegradedAfter` is checked against both. Tests pass.
- A one-write SOCKS5 greeting, login and `CONNECT` sent through the tunnel to the user's
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
  `danted` with a login on their server. An HTTP proxy is not part of this measurement.
