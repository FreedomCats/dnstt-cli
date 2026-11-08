---
kind: task
status: done
detailed: true
depends: [03-dot-context]
---

# Fresh Noise dial command

## Task

Add a usable `timeout-dial` command to the existing ignored `doc/runs/_live/` tool.
It measures a numbered series of fresh raw Noise dials through one selected resolver,
preserving every attempted sample and its observation limits. Configuration, session
ownership, records, command help and synthetic tests belong to this single result.

Follow the [local timeout stage](../plan.md), the accepted
[measurement method](../../01-measurement-method/plan.md) and
[architecture](../../../../../../../architecture.md) R5 and §04. This command supplies
the raw-session preparation reused by [idle observation](../02-idle-observation/plan.md)
and the later one-write probe. It does not authenticate an exit, contact a target or
prepare an upstream binary. Exit credentials and a working SOCKS5 service are not needed
to implement or locally accept Noise-only timing.

Expected change: `main.go`, new `raw_session.go`, `timeout_dial.go` and their focused
test files, and `README.md`, all under `doc/runs/_live/`; a small adjustment to
`profile.go` is allowed only if needed to share existing safe selection/validation.
Reuse the accepted tool rather than refactor its comparison or selftest.

## Steps

1. Add `timeout-dial` dispatch, safe help and option validation in `main.go`, keeping
   measurement options separate from `compare` startup/HTTP options. Support the existing
   `-profile`, `-resolver`, `-utls` and filtered `-v` conventions, a positive `-n` with
   an initial default of ten, and required positive `-dial-limit` and `-keepalive-limit` values.
   These are run-only observation controls, not proposed product values. Reject invalid
   arguments with fixed diagnostics before profile access. Resolve exactly one concrete
   resolver per invocation using `profile.go:131`; when expansion yields several, require
   an explicit `-resolver` instead of selecting an undocumented subset. Retain the tool's
   stated first-system-DNS behavior for `network`, fixed for that series and recorded.
2. Implement raw configuration in `raw_session.go`, using read-only `loadProfile` for
   user-run setup, `dns.ParseName`, `hex.DecodeString` and existing resolver APIs. Use
   `internal/app/tunnel.go:61` and `internal/app/tunnel.go:193` as the
   profile-parsing/bootstrap references, and
   `internal/supervisor/tunnel.go:481` for `Resolver.Core()` and
   `Resolver.LookupIP(bootstrap.LookupIP)`. Parse bootstrap entries with
   `resolvers.ParseBootstrap` and construct `resolvers.NewProfileBootstrap` from the
   effective selected resolver list and the existing network-DNS reader. Honor explicit
   bootstrap servers exclusively; otherwise preserve the R5 network-DNS/address-based-UDP
   order. Pins and literal addresses must avoid bootstrap lookup, and the original resolver
   hostname must remain available for TLS verification/SNI. Apply the profile or explicit
   uTLS distribution, filtered `Logf` and the explicit `KeepAliveTimeout` to
   `client.Config`; add no socket/network policy. Do not call `app.BuildTunnel`,
   `config.GetSecret`, a secret-store constructor, `prepareUpstream` or exit validation
   requiring credentials. The existing loader's profile validation still applies.
3. Implement the series in `timeout_dial.go` through raw `client.Dial`
   (`core/client/session.go:97`), with a small tool-private adapter/dial and clock seam
   for deterministic testing. Build configuration before timing; perform no eager
   bootstrap lookup or transport warm-up. Start monotonic timing immediately before each
   `client.Dial` invocation and stop immediately on its return, including bootstrap and
   transport work performed inside that call. No supervisor deadline, resolver-health
   selection, exit readiness or target timer participates. Keep the standard bootstrap
   cache behavior and document possible warm lookups later in the series; a fresh Noise
   session does not imply a cold external DNS cache.
4. Own each attempt's deadline, cancellation and cleanup explicitly. Record the returned
   elapsed duration and outcome before cleanup, then close every returned session and
   confirm completion through the existing `Close`/`Done` API before starting another.
   A successful dial's context is not its session lifetime
   (`core/client/transport.go:67`, `core/client/session.go:258`). Ordinary failed attempts
   remain numbered samples; the next planned attempt is not a replacement retry.
   Interruption stops the series and preserves partial counts, the in-flight result and
   unstarted count. Unconfirmed closure or a cleanup failure makes the series incomplete
   and stops further dials. Do not hide session-end/deadline races as clean completions.
5. Reuse `log.go` and the public diagnostic filtering from `compare.go` for records and
   output. Keep the private resolver mapping, uTLS/bootstrap/pin conditions, requested
   count and both observation limits with the series. For every attempted dial retain
   its ordinal, actual elapsed duration, safe outcome category, deadline/interruption
   or censoring condition, and separate session-end/closure outcome. A timeout is not a
   completed handshake at the configured limit; never replace its actual elapsed value
   with that limit. Retain raw duration precision in evidence rather than only the
   existing rounded `duration()` presentation. Report planned/attempted/completed and
   failure/censoring counts without silently dropping slow or failed observations.
   Record/log write or close errors must prevent a claim of complete saved evidence.
6. Add `raw_session_test.go` and `timeout_dial_test.go`. Use injected time/dial/session
   behavior for timer boundaries, limits beyond the current 20 s/2 min defaults, all
   success/failure/cancellation categories, exact sample counts and close-before-next
   ordering, including cleanup and output failures. Test configuration for DoH, DoT and
   UDP, uTLS override, pins/literals, bootstrap-source order and fixed `network` selection
   with stub readers and loopback DNS fixtures only. Use generated-key loopback dnstt
   sessions to verify real fresh dials, closure and bounded cancellation against a silent
   loopback endpoint. No target/exit stream may open for this command. An unavailable but
   syntactically valid synthetic password reference must not block Noise-only sampling.
   Cover dispatch/help, invalid flags, redaction and unchanged synthetic profile contents.
   Establish temporary configuration isolation before any loader/path/store call; never
   inspect actual configuration or metadata. Avoid parallel tests that mutate process
   environment or global logging, and avoid real long-duration waits.
7. Extend the existing measurement section of `README.md` with executable `timeout-dial`
   help and reserved-example commands for one selected DoH, DoT or UDP resolver, ten
   initial attempts and explicit run-only limits. Explain how to retain the complete
   private records in the existing evidence form, repeat with a longer limit without
   deleting the first series, and interpret completion/status codes. Retain the method's
   timer boundaries, cache/uTLS qualifications and later user gates. Do not recreate the
   method, evidence form, comparison verdicts or secret command. State that local command
   acceptance produces no live samples or selected timeout policy.
8. Run focused synthetic races, then `go test -race -count=1 ./doc/runs/_live`, explicit
   tool vet on Linux and Windows, and the existing both-exit loopback selftest with its
   filtering assertions. Follow the
   [parent validation matrix](../../plan.md#local-validation-for-code-children): cached
   dependencies only (`GOPROXY=off`, `GOSUMDB=off`, `GOTOOLCHAIN=local`); the applicable
   project race suite with `DNSTT_CLI_TEST_DBUS=1`, `DNSTT_CLI_TEST_NETNS=0` and exactly
   `-skip '^(TestDualStackListener|TestLoopbackIndexBlocks|TestPhysicalIndexPasses|TestFamilyWithoutRouteRefuses)$'`;
   mandatory isolated D-Bus/keyring cases passing without skips; static product build;
   exact Linux/Windows CI vet split (full outside `core/`, `-testinggoroutine=false`
   only for `core/`); Windows product/tool builds and compilation of applicable project
   and tool tests. Use `GOOS=windows CGO_ENABLED=0` for cross-build/compilation, without
   executing Windows binaries; `TestDNSGuardPermitsPrograms` is compilation-only here.
   Keep artifacts outside tracked paths. Check formatting/whitespace, tool exclusion
   from `go list ./...`, Git ignore rules, links, English and privacy. Preserve the
   comparison's existing verdicts/exit codes, both exit helpers, pin/system-DNS/uTLS
   caveats and local secret-input ceiling. Record checks once; repeat or broaden them
   only for new changes, failures or unresolved concerns.

## Done when

- `timeout-dial` help and README commands describe one usable raw-dial series with ten
  initial samples and explicit positive run-only dial/keepalive limits. The implemented
  command works on generated loopback fixtures without an exit service, target, upstream
  executable or secret lookup; setup does not modify the synthetic profile.
- Each attempted sample times exactly one fresh `client.Dial` call. All completed,
  failed, timed-out and interrupted attempts, actual durations, recorded limits and
  closure results survive in private evidence. Partial/unstarted counts and cleanup or
  evidence-write failures are visible; no failure becomes a successful duration or an
  unreported retry. Every session closes before another attempt begins.
- Config tests establish the existing pin/bootstrap/uTLS semantics for the selected
  resolver. Synthetic tests show that explicit observation controls can exceed product
  defaults without a hidden supervisor ceiling or a long wall-clock test. No product,
  core, API, profile format, resolver-health behavior or dependency changes.
- The focused tests, Linux tool race, Linux/Windows tool vet, both-exit selftest/redaction
  and applicable parent build/race/vet/Windows compilation matrix pass under the stated
  isolation/exclusions.
  New diagnostics and records do not expose generated identity, profile/secret reference
  or credentials; private resolver mappings remain in ignored logs only.
- Links, English, formatting, whitespace, ignore/exclusion and privacy checks pass.
  Implementation and captures remain ignored; only authorized roadmap metadata belongs
  in the eventual task commit. No live or Windows runtime/DPAPI acceptance is claimed.

## Risks and what is needed from the user

No real configuration, store, service or resolver is needed for implementation or checks.
Use only isolated synthetic profiles/stores, generated identities/certificates and
loopback, without environment dumps, netns/network mutation or external vendor fetches.
Respect the third-party source restrictions in `AGENTS.md`. The main risks are losing
pins/SNI while converting a resolver, moving bootstrap outside the timer, confusing
deadline cancellation with session closure, and reporting only rounded or successful
samples. Actual transport evidence and timeout/recovery decisions remain in
[Live timeout measurements](../../05-timeout-measurements/plan.md); the known signal-reader
follow-up stays separate unless this work demonstrates a new regression.

## Accepted prerequisite and resumed acceptance

The local command was implemented and its scoped review corrections passed, but full
acceptance was withheld because initial DoT setup did not honor the caller's context:
`core/client/tls.go` created an independent background timeout before returning the
transport, and `core/client/utls.go` performed a context-free TLS handshake. A silent
TLS peer could outlive the requested observation limit or interruption before a session
was returned for cleanup. Synthetic loopback reproductions confirmed both paths.

This is a pre-existing core dependency defect, not an accepted exception to the command's
deadline/lifecycle criteria. The user authorized the separate
[context-aware initial DoT prerequisite](../03-dot-context/plan.md), including the uTLS
handshake, maintained vendor rules/templates and regression checks. The sibling dependency
selects that task first without renaming this active node. This command task remains
tool-only; its deadline and interruption criteria are unchanged. The prerequisite preserved
successful-session lifetime and later redial/close behavior. Weakening the
measurement contract was rejected. A detached goroutine around
`client.Dial` would lose ownership and is not a safe tool-only workaround.

The prerequisite is independently accepted and committed as `c11a40c`. This command's saved
implementation and review resumed under unchanged criteria, with all 20 tool files kept
byte-identical. Final command-specific DoT dial-limit and interruption checks pass for
standard TLS and `Firefox_120` against silent loopback fixtures, retaining and joining
every attempt. The prerequisite's reviewed final matrix was reused only where sources and
build inputs match; affected command checks were independently executed before this task's
acceptance. No live evidence is established by the prerequisite or these local checks.

## Outcome

The existing ignored tool now provides `timeout-dial`: a numbered series of fresh raw
Noise dials through exactly one selected resolver, initially ten attempts, with required
positive run-only dial and keepalive observation limits. Help and the existing README
measurement section provide executable reserved DoH, DoT and UDP examples, result codes,
complete private-record instructions and explicit repetition without discarding a clipped
series. No timeout policy or new profile field was selected.

Raw preparation uses the read-only profile loader and existing resolver APIs, preserving
the effective selection's pins, literal-address behavior, original TLS hostname, profile
or override uTLS, R5 bootstrap precedence and standard cache. The first system DNS server
for `network` is fixed for the series. No eager lookup, transport warm-up, supervisor,
secret lookup/store, exit stream, target or upstream executable participates in the command.
An unavailable but valid synthetic password reference does not block Noise-only sampling.

Each attempted sample times exactly one `client.Dial` from immediately before invocation
through return, including its bootstrap and transport work. Actual nanosecond durations,
all observation limits, private conditions, outcome/censoring flags and separate session-end
and closure results survive in the evidence. Returned results are recorded before cleanup;
every returned session requires both `Close` return and `Done` before another attempt.
Failed attempts retain their ordinals. Interruption, uncertain cleanup and deadline/session
races stop conservatively with attempted, failed, censored, interrupted and unstarted counts.
Write, short-write, output and log-close errors prevent a saved-evidence completion claim.

Two accepted review findings were fully corrected: command-local filtering now hides bare
and normalized IPv6 bootstrap addresses from verbose output while retaining private mapping;
effective bootstrap deduplication and an encoded record-size guard prevent mandatory evidence
from silently disappearing at the logger's line bound. Oversized conditions stop before any
dial, preserve partial counts and cannot claim saved evidence. Real-logger boundary/privacy
regressions pass. No finding was rejected and no correction was deferred because of cost.

The initial DoT limitation was resolved in the separate user-approved prerequisite rather
than weakening this command's criteria or abandoning in-flight results. Final independent
review approves all eight Steps and Done when. Fresh focused race checks pass for every
raw-session/timeout test group, including generated-key loopback sessions, distinct fresh
dials, stream-free operation, read-only profiles, close-before-next ordering, deterministic
longer observation controls, races and cleanup/evidence failures. Actual-command silent DoT
series pass deadlines and real interruption in both TLS variants; complete records, partial
counts, client-closed connections, joined fixtures and fully waited processes were verified.

The final accepted core and unchanged tool match the reviewed matrix inputs. Verified current
evidence covers the complete explicit tool and applicable project race suites, all twenty
mandatory isolated D-Bus/keyring cases without skips, static builds, the exact Linux/Windows
CI vet split, explicit tool vet and vendor checks. Windows product/tool builds and all
nineteen applicable project test-package compilations plus the tool pass; retained artifacts
were verified without claiming repeated independent cross-builds or Windows execution.
The both-exit selftest passes HTTP/HTTPS with the server up, its existing HTTPS-only down
loop and filtering assertions. Its former unavailable-upstream-revision warning is absent
with the accepted verified source. Matching broad checks were retained rather than repeated
without changed inputs or an unresolved concern.

Exactly the four specified external TCP/DNS tests are excluded, with netns checks disabled;
`TestDNSGuardPermitsPrograms` is compilation-only. Formatting, whitespace, relative links,
English, privacy, Git ignore and package-list exclusion checks pass. Implementation, README,
snapshots and captures remain ignored; only this task's roadmap record is committed.
No product/core/API/dependency change belongs to this command task. No real configuration
or store access, external resolver/service, network/trust mutation, live measurement,
healthy-idle qualification, danted result or Windows runtime/DPAPI acceptance is claimed.

The private raw preparation, dial/runtime adapter and explicit close/Done helper are ready
for the separately scoped idle observer. The pre-existing CLI signal-reader follow-up and
the source-confirmed, not runtime-reproduced `sockets.track`/`trackedConn.Close` ownership-lock
path remain unchanged and separate. Neither is a correction deferred because of cost.
Lead retains parent-stage reconciliation and all later idle, service and policy gates.
