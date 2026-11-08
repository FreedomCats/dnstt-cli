---
kind: task
status: todo
depends: [01-fresh-dial]
---

# Healthy-idle observation command

## Task

Add a usable `timeout-idle` command to the existing ignored `doc/runs/_live/` tool,
reusing the raw configuration and bounded session ownership from
[Fresh Noise dial command](../01-fresh-dial/plan.md). Its single result is a qualified
receive-progress observation on one session: independent sequential SOCKS5/target checks
outside the window, the trace, conservative gap estimation and synthetic validation.
Follow the [parent stage](../plan.md) and accepted
[measurement method](../../01-measurement-method/plan.md); the later
[user-run measurements](../../05-timeout-measurements/plan.md) own live evidence and policy.

The existing dependency on fresh-dial also gates this task on the separately accepted
[initial DoT core prerequisite](../03-dot-context/plan.md). Reuse its restored dial-context
contract, not an additional core change or weaker observation/cancellation behavior here.
The command's scope and Done when below remain unchanged.

Expected change: `main.go`, focused new `timeout_idle.go`, `idle_trace.go` and tests,
an independent raw-stream SOCKS5 verification adapter if needed, `README.md` and a small
extension of `raw_session.go` if required. Keep this within roughly ten tool files.
Reuse existing independent SOCKS5/target helpers and synthetic fixtures where suitable;
do not build a second comparison tool or use product exit code as the verification oracle.
The estimator, lifecycle and command are accepted together as one usable observation.

## Done when

- The command records one selected resolver's conditions, explicit positive run-only
  dial/keepalive limits, requested window, polling interval and bounded verification
  controls. Instructions start with a window of at least five minutes and a keepalive
  observation limit that permits it. Tests can use short or injected windows. One fresh
  raw session is closed on every path; there is no supervisor, hidden redial, resolver
  switch or splicing of traces from different sessions.
- Non-SOCKS5 exit selections are refused before secret lookup or traffic. Independent
  sequential SOCKS5 authentication, complete CONNECT and explicit target-data verification
  succeed before a qualified window begins and again after its final snapshot, on the
  same session and with the same explicit target/expected-data criterion. Verification
  streams/transports are closed outside the window. Missing credentials/service or a
  failed check cannot qualify idle evidence. HTTP proxy response timing is never used;
  an HTTP(S) target-data check remains a separate usability check.
- The baseline snapshots `Session.Stats().BytesReceived` after setup and stream closure.
  Every counter value and actual monotonic observation offset, including the final
  boundary, is retained with requested/actual lengths and the largest polling delay.
  The window opens no application stream and sends no application request; normal tunnel
  polls and smux keepalives continue. Counter semantics are documented as Noise-channel
  bytes, including encryption but excluding KCP/DNS overhead
  (`core/client/session.go:68`), not packet-arrival or server-send timestamps or RTT.
- The estimator follows the accepted progress-interval method: for `(a, b]` and `(c, d]`,
  retain connecting-gap bounds `max(0, c - b)` to `d - a`; include unresolved arrivals
  within individual polling intervals and the leading/trailing boundaries when reporting
  a conservative maximum upper bound. Uneven actual polling widens uncertainty. A window
  with no progress supplies a boundary/censored gap, not a healthy inter-arrival maximum.
- Cancellation, session termination/replacement, a counter decrease or invalid time trace,
  observation-limit expiry, failed post-check and cleanup/evidence-write failures retain
  their trace and explicit end reason. They cannot produce an unqualified healthy maximum.
  Requested-window completion remains distinct from early termination and from the later
  same-session post-check result; the exposed session error is not stronger causal evidence
  than the API provides.
- Deterministic trace tests cover changes and repeated counters, multiple unresolved
  arrivals, uneven polling, both boundary gaps, no progress, counter/time anomalies,
  cancellation and session end without waiting several minutes. Generated-key loopback
  sessions with synthetic authenticated SOCKS5/known targets verify pre/post usability,
  baseline/cleanup ordering, no application traffic in the window, no replacement dial
  and closure on success/failure/interruption. Credentials and generated identity remain
  filtered from output/logs; expected target data is verified without a payload dump.
- README help and executable reserved-example commands map trace output and qualification
  to the existing private evidence form. They preserve the accepted method, independent
  comparison, both exit protocols and selftest. No method/form rewrite, live measurement,
  candidate timeout selection or product/core/API/profile/dependency change is required.
- Focused synthetic races, explicit Linux tool race, Linux/Windows tool vet, both-exit
  loopback selftest/redaction and the
  [parent validation matrix](../../plan.md#local-validation-for-code-children) pass,
  including applicable project races with mandatory isolated D-Bus and the exact external
  TCP/DNS exclusions, static product build, exact CI vet split, and Windows product/tool
  builds and test compilation. Retain passing broad evidence only for unchanged sources
  and build inputs; rerun affected tool checks and broaden for new failures or concerns.
  Formatting, links, English, ignore/exclusion and privacy checks pass. Only authorized
  roadmap metadata is committed later; never force-add implementation or private records.

## Risks and what is needed from the user

Implementation/checks use synthetic temporary profiles/stores, generated identities and
certificates, loopback and cached dependencies only (`GOPROXY=off`, `GOSUMDB=off`,
`GOTOOLCHAIN=local`). Do not read/stat/modify actual configuration, secret data or metadata,
dump environment, call live services/resolvers, mutate networking, run netns checks or fetch
vendor code. Follow `AGENTS.md` licensing restrictions. Linux selftest suppresses Secret
Service discovery; mandatory keyring regressions use isolated D-Bus. Windows binaries and
`TestDNSGuardPermitsPrograms` are compiled without execution, establishing no runtime/DPAPI
result. The main risks are measuring setup/verification traffic inside the window and
understating uncertainty or qualifying a failed session. No user access is needed for
local acceptance; actual SOCKS5 availability, several-minute transport evidence and the
timeout/recovery decision remain later gates.
