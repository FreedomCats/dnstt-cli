---
kind: task
status: done
detailed: true
depends: [05-timeout-measurements]
---

# Apply the measured timeout policy

## Task

Apply the values and derivation rules approved in
[Live timeout measurements](../05-timeout-measurements/plan.md) as one consistent product
policy. `connect` must set its sessions' keepalive timeout; supervisor dial attempts and
`resolvers check` must share the measured handshake timeout; the degraded-state threshold
must agree with the approved policy. Architecture R2 and §§04/08 define the behavior.

Expected scope: `internal/app/tunnel.go` and its tests, `internal/supervisor/tunnel.go`
and its tests, and `internal/cli/resolvers.go` and its tests, with a small shared policy
definition only if needed. Use the existing `client.Config.KeepAliveTimeout`; a core
API, vendoring change, network watcher or new timeout profile key is not required.
Do not change the separate SOCKS5 exit exchange's 60 s limit as a side effect.

Approved by the user after the measurements: keepalive **30 s**, handshake **10 s**
(replacing the preliminary 20 s and used by `resolvers check` too), degraded
**25 s** unchanged. The three numbers are one policy and live in one place; the
derivations are sanitized aggregates, never the raw records (`doc/local/`).

## Steps

1. Put the approved values in the policy block of `internal/supervisor/tunnel.go`
   (the const block at lines 19-30) and keep them the only numbers in the diff:
   - Add `DefaultKeepAliveTimeout = 30 * time.Second`. Comment: `30 s is 2.5x the worst
     measured healthy-silence bound (~12 s across the measured transports); the server's
     progress arrives about every 10 s and smux checks every min(10 s, timeout/2) = 10 s,
     so a dead session is noticed after about 30 s.` Do not name resolvers, transports'
     identities, a domain or raw samples.
   - Change `DefaultHandshakeTimeout` to `10 * time.Second` and replace the "Preliminary
     ... to be measured" wording: `10 s is about 5.5x the slowest measured fresh
     handshake (~1.8 s), which leaves room for a cold bootstrap and cache misses; the
     supervisor's dials and "resolvers check" share this value.`
   - Keep `DefaultDegradedAfter = 25 * time.Second`. Comment: `Retained: above the worst
     measured healthy silence (~12 s) and below the 30 s keepalive, so the status shows
     "degraded" before the session is declared dead — nominally 5 s before the earliest
     detection, less the poll step.` (`reconnecting` comes later, after the session ends.)
     Note in the comment that `serve` polls every fifth of it
     (`internal/supervisor/tunnel.go:674`) and that data restores `Connected`.
   - Keep the `Options.Session` doc (`internal/supervisor/tunnel.go:52-55`) true: the
     caller sets the keepalive, and every dial copies `Options.Session`
     (`internal/supervisor/tunnel.go:568`), so a redial inherits it. Do not add a
     supervisor default for it: `app` is the only caller that builds a session config,
     and a second assignment of the same number would be a second place to keep in sync.

2. Set the keepalive where `connect` builds its session config:
   `internal/app/tunnel.go:98-105`. Move the `client.Config` literal into
   `func sessionConfig(prof *config.Profile, domain dns.Name, pubkey []byte, opts
   TunnelOptions) client.Config`, add `KeepAliveTimeout: supervisor.DefaultKeepAliveTimeout`,
   and call it from `buildTunnel`. This works for the first connection and for every
   redial because the supervisor reuses `Options.Session`. Add a comment that the value is
   the approved policy keepalive of every session and that no profile key is added for it.
   Do not set a keepalive for the `resolvers check` config: that session is closed right
   after the handshake and its 10 s deadline bounds it.

3. Share one handshake deadline with `resolvers check`: in
   `internal/cli/resolvers.go:132` replace `20*time.Second` with
   `supervisor.DefaultHandshakeTimeout` and import `internal/supervisor` (already used by
   `internal/cli/connect.go`, so there is no cycle). Keep the per-resolver context inside
   the loop, the elapsed output and the raw deadline error text: only the number changes.
   A shorter caller context keeps working, because `context.WithTimeout` takes the earlier
   of the two deadlines.

4. Supervisor tests (`internal/supervisor/tunnel_test.go`, or a `timeouts_test.go` in the
   same package; the harness in `fake_test.go:148` injects options and `h.tun.dial` at
   line 172 is replaceable):
   - `TestApprovedTimeoutPolicy`: the three constants equal the approved values and
     `DefaultDegradedAfter < DefaultKeepAliveTimeout`, with a comment that these are the
     user-approved numbers and changing one is a new decision.
   - `TestDefaultTimeoutsFromNew`: `New` without overrides yields those values in its
     options (guards the defaults, does not wait for them).
   - `TestKeepAliveTimeoutReachesEveryDial`: inject `Session.KeepAliveTimeout = 250ms`,
     wrap `h.tun.dial` to record each `client.Config`, run, wait for `Connected`,
     `h.session(0).die(client.ErrSilent)`, wait for the second `Connected`, then require
     both recorded configs to carry exactly the injected value. This is the propagation
     proof for the first dial and the redial.
   - `TestHandshakeTimeoutBoundsDial`: inject `HandshakeTimeout = 300ms`, capture
     `ctx.Deadline()` in the dial, require the remaining budget to be at most 300 ms and
     above half of it. `TestNeverConnected` (`tunnel_test.go:456-485`) already pins the
     timeout text from this value.
   - Leave `TestDegraded` (`tunnel_test.go:755-765`, 50 ms injected) as the
     silence-to-`Degraded` and data-back-to-`Connected` test. Do not test the defaults
     with wall-clock waits and do not add live-sized sleeps.

5. App test for the wiring: `internal/app/tunnel_test.go` (or a `timeouts_test.go` beside
   it) — `TestSessionConfigCarriesPolicyKeepAlive` calls `sessionConfig` with the
   `testProfile` helper (`internal/app/http_test.go:82`) and requires
   `KeepAliveTimeout == supervisor.DefaultKeepAliveTimeout` and non-zero, with `Domain`,
   `PubKey`, `UTLS`, `Control` and `Logf` still taken from the profile and the options
   (table over a uTLS spec and a control hook). This is the only seam that observes the
   value without a new public API; a full-loopback keepalive test would have to wait a
   live-sized timeout. The existing `TestBuildHTTPExitCredentials` and
   `TestBuildOtherExitContracts` continue to prove that `buildTunnel` still builds.

6. CLI tests in `internal/cli/resolvers_test.go`, using the injected dial of
   `TestRunResolverChecksDialsEveryNetworkResolver`:
   - `TestResolverCheckDeadlineMatchesSharedHandshake`: the dial captures
     `ctx.Deadline()`; the remaining budget is at most
     `supervisor.DefaultHandshakeTimeout` and above it minus one second. This is the
     equal-deadlines test for supervisor and check.
   - `TestResolverCheckShorterCallerContextBoundsDial`: the parent context has a 30 ms
     deadline, the dial waits for `ctx.Done()` and returns its error; the run ends with
     the exit-code error and the `FAILED` line well inside the 10 s policy (assert it
     under 2 s), and the deadline the dial saw is the caller's.
   - Keep `TestRunResolverChecksReportsNetworkDNSError`, `...DialsEveryNetworkResolver`
     and `...NetworkDNSError` unchanged.

7. Do not touch neighboring behavior: `internal/exit/socks5.go` keeps its 60 s exchange
   limit, the startup exit check and the HTTP exit contract are unchanged, and no profile
   key, core API, vendored source, network watcher or dependency is added. Confirm by the
   unchanged loopback and session tests: `TestServerRestart` (silent session death,
   failed dials, recovery, `internal/supervisor/real_test.go:76`), `TestOpenStream`
   (cancellation), `TestSOCKS5StartupCheckRemainsSessionLocal`,
   `TestHTTPApplicationThroughTunnel` and the config/secret tests of `internal/app`.

8. Run the parent's [local matrix](../plan.md#local-validation-for-code-children) with
   cached dependencies only (`GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`):
   focused `go test -race -count=1 ./internal/supervisor ./internal/app ./internal/cli`;
   the project race suite
   `DNSTT_CLI_TEST_DBUS=1 DNSTT_CLI_TEST_NETNS=0 go test -race -count=1 ./...` with exactly
   `-skip '^(TestDualStackListener|TestLoopbackIndexBlocks|TestPhysicalIndexPasses|TestFamilyWithoutRouteRefuses)$'`
   and the Secret Service tests passing on isolated D-Bus without skips; `CGO_ENABLED=0 go
   build ./...`; the exact CI vet split (full vet outside `core/`, `go vet
   -testinggoroutine=false ./core/...`); `GOOS=windows CGO_ENABLED=0 go build ./...` and
   `go test -c` compilation of the changed packages, without executing Windows binaries;
   `gofmt -l` on the changed files; `git diff --check`. Also check that `go.mod` and
   `go.sum` are unchanged, that `go list ./...` still excludes `doc/runs/_live`, and that
   `git diff --stat` shows no change under `internal/exit/` or `core/`.

9. Do not edit `doc/architecture.md` here. The section below is the handoff that
   [Recording the results](../../05-record-results/plan.md) executes, together with the
   check that no "preliminary" or "to be measured" wording is left about these numbers.

## Done when

- Sessions built for `connect` carry the approved 30 s keepalive on the first connection
  and after every redial: `sessionConfig` sets `client.Config.KeepAliveTimeout` from the
  shared constant, `TestSessionConfigCarriesPolicyKeepAlive` pins that wiring and
  `TestKeepAliveTimeoutReachesEveryDial` (injected short value) proves both dials receive
  the configured value.
- The handshake timeout is 10 s with a single definition: the supervisor's default and
  `resolvers check` both use `supervisor.DefaultHandshakeTimeout`, no local 20 s remains,
  `TestResolverCheckDeadlineMatchesSharedHandshake` shows the check's deadline equals that
  constant, `TestHandshakeTimeoutBoundsDial` shows the supervisor's, and
  `TestResolverCheckShorterCallerContextBoundsDial` shows a shorter caller context still
  bounds the attempt.
- `DefaultDegradedAfter` stays 25 s: `TestApprovedTimeoutPolicy` and
  `TestDefaultTimeoutsFromNew` pin the approved numbers and
  `DefaultDegradedAfter < DefaultKeepAliveTimeout`, and `TestDegraded` with a short
  injected duration still shows silence to `Degraded` and data back to `Connected`. No
  test waits for a live-sized default.
- The existing loopback, cancellation and startup-check coverage still passes unchanged:
  `TestServerRestart`, `TestOpenStream`, `TestNeverConnected`,
  `TestSOCKS5StartupCheckRemainsSessionLocal`, `TestHTTPApplicationThroughTunnel` and the
  `internal/app` config/secret tests; `TestDNSGuardPermitsPrograms` stays
  compilation-only.
- Every selected value has a sanitized derivation comment and the changed files contain no
  resolver entry, domain, key, raw sample, date or environment detail; no changed comment
  calls these numbers preliminary or to be measured.
- `internal/exit/socks5.go` (the 60 s exchange limit) is unchanged, and no profile key,
  core API, vendored source, network watcher or dependency appeared: `go.mod`, `go.sum`
  and `core/` are untouched in the diff.
- The [parent's local matrix](../plan.md#local-validation-for-code-children) passes:
  focused and project race with mandatory isolated D-Bus and exactly the four stated test
  exclusions, static build, exact CI vet split, Windows cross-build and test compilation,
  `gofmt`, `git diff --check`, the ignored tool still excluded from `go list ./...`.
- The handoff below names the lines of R2, §04, §08 and §13 to update and what stays out
  of this task.

## Handoff to the results task

[Recording the results](../../05-record-results/plan.md) writes these; this task leaves
`doc/architecture.md` alone.

- R2 (`doc/architecture.md:73-74`): the client keepalive is 30 s, derived as 2.5x the worst
  measured healthy silence (~12 s over the measured transports) against the server's ~10 s
  progress; smux declares silence dead one to two keepalive periods (30–60 s) after the
  last data, and recovery adds redial, backoff and the exit check. Remove "about 30 s ...
  the number is set by a live run, E1".
- §04 `client.Config` (`doc/architecture.md:271`): the core's `KeepAliveTimeout` still
  means upstream's 2 min when zero, and the shell sets 30 s in the session config of
  `connect`; at line 303 note that smux sends its keepalive every 10 s and checks the
  timeout on its own 30 s period, so silence is declared dead one to two periods after the
  last data.
- §08 "Dialing" (`doc/architecture.md:487-488`): the handshake timeout is 10 s, derived as
  about 5.5x the slowest measured fresh handshake (~1.8 s) to cover a cold bootstrap, and
  `resolvers check` uses the same value; replace "(20 s for now, to be measured on the
  live server)".
- §08 "Degraded" (`doc/architecture.md:504-505`): 25 s is retained because it is above the
  worst measured healthy silence and below the keepalive, so the `degraded` status appears
  before the core ends the session at its keepalive timeout — nominally 5 s before the
  earliest detection, less the 5 s poll step; `reconnecting` comes later, after the session
  ends.
- §13 (`doc/architecture.md:727`): the row "A dead session is noticed late" states the
  measured 30 s keepalive and the one-to-two-period detection window instead of a plan.
- Out of scope here: the parent stage's `Outcome` and open points, R3 and `danted`, §10
  profile keys, the HTTP semantics of `Connected`, and every live, browser, platform,
  restart and network-change result. Those belong to the sibling tasks of the recording
  node and [Live runs](../../04-live-runs/plan.md).

## Risks and what is needed from the user

Do not start implementation until the evidence and user decision are recorded — they now
are, in the measurements task's outcome. The SOCKS5/browser/platform restart and
network-change acceptance remains in [Live runs](../../04-live-runs/plan.md) with these
final values. This task does not claim that synthetic silence tests establish those live
results or Windows runtime behavior.

- The 10 s handshake shortens what `resolvers check` and every dial tolerate from a slow
  but working resolver. The measured maximum was 1.83 s and the user approved the 5.5x
  margin; a working resolver slower than that on a cold bootstrap is now reported as a
  failed check, and that is the approved tradeoff, not a bug to hide by lengthening it
  locally.
- The app-level value has no end-to-end behavioral test: a loopback session would need a
  live-sized wait to show the keepalive. It is covered directly through `sessionConfig` and
  through the supervisor's injected propagation test; the reviewer should read those two
  rather than expect a 30 s test.
- Say nothing about Windows or live recovery: the matrix compiles Windows and the
  measurement came from one set of transports. Nothing here changes that boundary.
- No new user input is needed; the values and the stability/recovery decision are recorded.

## Outcome

The approved policy is applied with its values unchanged, and one wrong mechanism claim,
inherited from the selection text, was corrected in review:

- `supervisor.DefaultKeepAliveTimeout` is 30 s and is set only in `connect`'s session config
  (`sessionConfig` in `internal/app/tunnel.go`), so the first dial and every redial carry
  it; there is no profile key and no supervisor-side default.
  `supervisor.DefaultHandshakeTimeout` is 10 s and `internal/cli/resolvers.go` uses that same
  constant instead of its own 20 s. `DefaultDegradedAfter` stays 25 s. The comments carry
  the approved derivations and are sanitized.
- Correction: smux sends its keepalive every `min(10 s, timeout/2)` = 10 s, but checks the
  timeout on its own `KeepAliveTimeout` ticker, so with a 30 s keepalive silence is declared
  dead one to two periods (30–60 s) after the last data. The earlier "about 30 s" is the
  earliest detection, not the typical one. The comment says so, the approved values are
  unchanged (the 2.5x margin over the worst measured healthy silence still holds at the
  earliest detection), and the handoff above for R2, §04, §08 and §13 states the window
  instead of a 10 s check interval.
- Review also corrected the status shown at the threshold: 25 s of silence sets `degraded`,
  not `reconnecting`, which appears only after the session ends. The comment and the §08
  handoff now say so. Full `socks`-mode recovery adds the redial, backoff and exit check on
  top of the detection window, so the status returns to `connected` later than the session
  death.
- Tests added, all with injected short durations and none waiting for a live-sized default:
  `TestApprovedTimeoutPolicy`, `TestDefaultTimeoutsFromNew`,
  `TestKeepAliveTimeoutReachesEveryDial`, `TestHandshakeTimeoutBoundsDial` in a new
  `internal/supervisor/timeouts_test.go`; `TestSessionConfigCarriesPolicyKeepAlive` in a new
  `internal/app/timeouts_test.go`; and the shared-deadline and shorter-caller-context tests
  in `internal/cli/resolvers_test.go`. `TestDegraded`, `TestNeverConnected`,
  `TestServerRestart`, `TestOpenStream`, `TestSOCKS5StartupCheckRemainsSessionLocal`, the
  app config/secret tests and the loopback streaming tests pass unchanged.
- The parent's local matrix passes on the final tree: the focused race run on supervisor,
  app and CLI; the project race suite with the isolated D-Bus and exactly the four stated
  test exclusions, Secret Service tests passing without skips; the static build; the CI vet
  split; the Windows cross-build and test compilation; `gofmt`; and `git diff --check`.
  `go.mod`, `go.sum`, `core/` and `internal/exit/` are untouched, and `doc/runs/_live` is
  still excluded from `go list ./...`. No live run, network access, real profile or secret
  was used.
- Left to [Recording the results](../../05-record-results/plan.md): `doc/architecture.md`,
  with the corrected handoff above. The detection window makes recovery longer than the
  "about 30 s" the selection text used; the values remain the approved ones, and the
  results task should describe the window as one to two keepalive periods. No Windows or
  live recovery behavior is claimed here.
