---
kind: task
status: done
detailed: true
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
[measurement method](../../01-measurement-method/plan.md), with
[architecture](../../../../../../../architecture.md) R5 and §04; the later
[user-run measurements](../../05-timeout-measurements/plan.md) own live evidence and policy.

The existing dependency on fresh-dial also gates this task on the separately accepted
[initial DoT core prerequisite](../03-dot-context/plan.md). Reuse its restored dial-context
contract, not an additional core change or weaker observation/cancellation behavior here.
This detail preserves the command's existing scope and acceptance criteria.

Expected change, all under `doc/runs/_live/`: `main.go`, new `timeout_idle.go`,
`idle_trace.go`, `idle_verify.go` and their three test files, `README.md`, and a narrow
extension of `probe.go` for explicit target-data verification. This is nine files, with
`raw_session.go` a tenth only if a small shared-adapter adjustment is needed. Its existing
`rawSession` deliberately omits `OpenStream`; prefer an idle-only capability interface so
Noise-only fakes and `timeout-dial` keep their stream-free contract. Reuse existing
independent SOCKS5/target helpers and synthetic fixtures; do not recreate the comparison
or use product exit code as the verification oracle. The estimator and verifier have no
separate acceptance result here: one review and one validation set accept the usable command.

## Steps

1. Add `timeout-idle` dispatch, help and fixed safe argument diagnostics in `main.go`,
   with focused option validation in `timeout_idle.go`. Reuse `-profile`, `-resolver`,
   `-utls`, filtered `-v` and required positive `-dial-limit`/`-keepalive-limit`; add
   required positive `-window`, `-poll` and `-verify-limit`, an explicit HTTP(S) `-url`
   without URL credentials, and a required valid `-expect-sha256` for the complete expected
   response body. Use the same parsed target and criterion for both checks; supply no
   hidden target, credential flag/environment input, attempt series or upstream options.
   Reject invalid flags, durations, URL/digest and positional arguments before profile
   access, without echoing their values. Short windows are valid for synthetic checks;
   the five-minute initial guidance belongs in instructions, not a test-time minimum.
2. In `timeout_idle.go`, use the read-only `loadProfile` and refuse every non-SOCKS5
   selection before secret lookup, network-DNS selection or traffic; an empty exit type
   retains the existing SOCKS5 meaning (`profile.go:114`). Read only the selected password
   reference through the existing `config.GetSecret` path, using a small injected lookup
   in tests. Preserve the existing SOCKS5 credential length and empty-password semantics
   (`profile.go:99`, `internal/exit/socks5.go:159`); add no credential format or product
   rule. A missing authenticated baseline cannot start a qualified window. Reuse
   `prepareRawSession` (`raw_session.go:81`) unchanged in substance: exactly one effective
   resolver, fixed first DNS for `network`, lazy standard bootstrap cache, explicit
   bootstrap precedence, pins/literals, original TLS hostname and profile/override uTLS.
   Invoke the existing raw dial/runtime once with the explicit dial deadline, retaining
   its returned result before cleanup. Canceling a successful dial's context must leave
   its session alive. No supervisor, `app.BuildTunnel`, upstream preparation, second raw
   dial, resolver switch or wrapper retry participates.
3. Implement independent pre/post verification in `idle_verify.go`. Use an idle-only
   `rawSession` plus `OpenStream() (net.Conn, error)` interface backed by the already
   exported method (`core/client/session.go:225`), and the existing cached
   `golang.org/x/net/proxy.SOCKS5` with a context-aware forward adapter to that same
   session. The forward adapter never dials a proxy socket or resolves the target locally.
   Retain the actual method/login outcome with a small reply observer: supplying
   `proxy.Auth` also offers no-auth, so CONNECT success alone cannot establish RFC 1929
   authentication. Validate the sequential method/login and complete CONNECT before
   accepting target data; no-auth cannot qualify this authenticated experiment. Do not
   log wire buffers or introduce a one-write treatment. Reuse `probeWithClient`
   (`probe.go:53`) and its streaming body/error handling, extending private probe options
   narrowly for SHA-256 verification and idle-only redirect refusal. Require a complete
   2xx HTTP response with the expected digest, preserve the requested hostname for
   SOCKS/HTTP/TLS, and never follow a redirect to a different target. Keep environment
   proxies disabled and normal TLS verification; generated trust is test-local only.
   Comparison/selftest retain their current body and redirect behavior.
4. Bound each verification, including stream acquisition, greeting/login/CONNECT, target
   request/body and stream/transport closure, with `-verify-limit` and the existing
   separately recorded cleanup safety bound. Implement this ownership in `idle_verify.go`
   and `timeout_idle.go`: `OpenStream` has no context argument, and smux stream open/close
   can wait. Install deadlines before protocol writes and cancellation handling for
   pending reads/writes; do not assume a later write-deadline update interrupts a write
   already in progress. On cancellation/expiry, tear down the sole session if necessary,
   collect the outstanding operation's result and close any late stream. Join owned
   workers/callbacks; never detach an acquisition, close or eventual result as a workaround.
   Before baseline, confirm the pre-check body, every verification connection/transport
   closure and zero local streams. Apply the same ownership after the post-check. Keep
   session cleanup through `closeRawSession` (`raw_session.go:174`), confirming both Close
   return and Done on every returned-session path; uncertain closure is an explicit failure.
5. Implement a pure estimator in `idle_trace.go` over actual monotonic offsets and uint64
   receive counters. Preserve every observation, detect counter decreases before unsigned
   subtraction, and reject negative, regressing or non-increasing sample times rather
   than sorting, dropping or repairing the trace. Each increase locates activity in
   `(a, b]`; its byte delta is not an arrival count. For consecutive progress intervals
   `(a, b]`, `(c, d]`, retain connecting bounds `[max(0, c - b), d - a]`. Also retain each
   progress interval's width for unresolved arrivals. With window `[0, T]`, the leading
   bounds are `[a_first, b_first]` and trailing bounds are
   `[T - b_last, T - a_last]`. The conservative maximum upper bound is the maximum of
   all connecting upper bounds, interval widths and both boundary upper bounds. A
   no-progress window retains its entire boundary span as censored evidence and has no
   qualified inter-arrival maximum. Use actual final time T, including polling overshoot;
   do not substitute scheduled ticks, requested duration, KCP RTT or target latency.
6. Implement the one-session observation lifecycle in `timeout_idle.go`, reusing
   `rawRuntime` for clock/dial/deadline injection and adding only a private cancellable
   timer/wait seam for deterministic sampling. After successful pre-check closure, take
   the receive baseline and adjacent actual monotonic time. During the window, only
   sample `Stats().BytesReceived`, process timers/context/session Done and save evidence;
   never open an application stream or request. Normal tunnel polls, keepalives and the
   existing core transport behavior continue. Stop at requested-window completion or the
   first interruption, session end, invalid trace or evidence failure, taking an actual
   final snapshot when available. Record maximum actual sample spacing and polling
   lateness separately from the requested cadence. Then run the same bounded post-check
   outside the trace when the original session/context still permits it; otherwise record
   why it was not attempted. Freeze the window trace before any post-check traffic.
   Preserve window completion/end reason, post-check and final cleanup as separate results:
   reaching the requested boundary is normal completion, not proof of usability, and an
   earlier dial/verification/keepalive or controlling-context limit is not that completion.
   Treat races conservatively. Record the API's session error category without inferring
   resolver death or a stronger cause from `ErrSilent` (`core/client/session.go:193`).
7. Add bounded structured private records and filtered output in `timeout_idle.go`, using
   `log.go`, `publicRedactor` and the accepted `timeout-dial` record/output safeguards as
   references. Retain conditions/private resolver mapping, all limits, pre/post method,
   login/CONNECT/data and closure outcomes, baseline and every time/counter, requested and
   actual window, cadence/delays, estimator bounds, end reason and qualification. Reference
   the target/expected-data criterion through the existing private form; emit no payload.
   Keep actual nanosecond precision and uint64 counters. Write samples/bounds incrementally
   rather than one unbounded trace line; validate mandatory encoded records against the
   logger's 64 KiB bound and reject oversized setup evidence before dialing. Filter string
   inputs before encoding, preserving numeric fields and schema even for short identities.
   Both private logs and public/verbose output hide generated identity, profile/reference,
   raw/Basic-encoded credentials and target URL/path/query or echoed errors. Public
   filtering additionally hides original/canonical resolver and bootstrap spellings,
   including bare IPv6, while the permitted resolver mapping remains private. Preserve
   partial trace/primary end reason when cleanup or writes fail. Short writes, output or
   log-close errors prohibit a saved-evidence/healthy completion claim; publish final saved
   status only after all records and closure succeed. Explain qualified, censored and
   incomplete outcomes and their command exit codes without changing comparison verdicts.
8. Add `idle_trace_test.go`, `idle_verify_test.go` and `timeout_idle_test.go`. Deterministic
   traces cover repeated/changed counters, single and multiple unresolved arrivals, uneven
   polling/overshoot, leading/trailing gaps, no progress, counter/time anomalies and exact
   conservative bounds. Inject clocks/waits to test long requested windows/limits without
   real multi-minute waits. Exercise success, pre/post failures, missing/bad credentials,
   non-SOCKS5 refusal before lookup/traffic, dial errors/limits, session end, cancellation
   at each phase, deadline/completion races, delayed acquisition/close and evidence failures.
   Check one dial, same-session pre/post checks, closure before baseline, no open/request
   inside the window, an actual final boundary before post traffic and final Close/Done.
   Reuse fixtures at `selftest_exit.go:35` and `selftest_exit.go:253`, plus `server.StartTest`,
   with generated identities, authenticated SOCKS5 and known HTTP/HTTPS targets.
   Check strict sequential replies,
   fragmented/coalesced replies, full CONNECT and preserved first target bytes, no-auth
   selection, auth/target refusal, wrong/truncated body, redirects, silent peers and TLS
   failures. Add test-local recording wrappers/fixtures where needed instead of rewriting
   the comparison or using product exit/application code as oracle. Establish synthetic
   config/store isolation before any path/loader/store call, suppress Secret Service
   discovery, and assert unchanged profile/store bytes and no unintended metadata writes.
   Exercise real logger/mirror bounds, private/public redaction and numeric integrity.
   Join all fixtures/workers on failing paths; do not parallelize tests changing process
   environment or global logging. Include actual dispatch/help and generated-loopback
   command success/interruption; estimator qualification need not wait for real keepalives.
9. Extend the accepted measurement section of `README.md` with actual `timeout-idle` help,
   executable reserved examples for DoH/DoT/UDP and the explicit target/digest and limits.
   Initial instructions use at least five minutes with a keepalive observation limit that
   permits the window, not a selected product value. Explain setup/verification closure,
   exact trace boundaries, Noise-channel byte semantics (`core/client/session.go:68`),
   all interval/boundary uncertainty, no-progress/censored runs and finite conditional
   qualification. Map complete records and target/criterion references to the existing
   Healthy Idle Windows form; preserve failed runs when repeating with different controls.
   Update only idle-command availability text as needed; preserve the accepted method,
   unfilled form, `timeout-dial`, comparison, both exits, secret-input ceiling and selftest.
   Run focused synthetic races, explicit full tool race/vet, both-exit selftest/redaction
   and the validation matrix below. Record source/build-input identity for retained broad
   evidence and rerun affected checks; expand only for changed inputs, failures or concerns.

### Validation matrix

The [parent matrix](../../plan.md#local-validation-for-code-children) applies. With cached
Go matching `go.mod` and `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`:

- Run focused estimator/verifier/command races, then
  `go test -race -count=1 ./doc/runs/_live`, explicit Linux and Windows tool vet, and
  the existing both-exit isolated selftest/redaction. Its coverage is HTTP/HTTPS while
  the server is up and the existing HTTPS-only down loop; do not claim more.
- Cover applicable project races with `DNSTT_CLI_TEST_DBUS=1`, `DNSTT_CLI_TEST_NETNS=0`
  and exactly
  `-skip '^(TestDualStackListener|TestLoopbackIndexBlocks|TestPhysicalIndexPasses|TestFamilyWithoutRouteRefuses)$'`.
  All twenty mandatory isolated D-Bus/keyring cases must pass without skips.
- Cover `CGO_ENABLED=0 go build ./...`, the static product build and the exact
  Linux/Windows CI vet split: full vet outside `core/`, `-testinggoroutine=false` only
  for `core/`. Cross-build product/tool and compile all applicable Windows project test
  packages plus tool with `GOOS=windows CGO_ENABLED=0`; keep artifacts outside tracked
  paths. `TestDNSGuardPermitsPrograms` is compilation-only; execute no Windows binary.
- Existing passing broad product/core checks and Windows artifacts may be retained only
  after verifying their source/build-input identity against accepted fresh-dial/core
  evidence. New tool inputs require fresh affected tool checks, not a repeated unchanged
  product matrix or new vendor generation/fetch. Record precisely what ran or was retained.
- Check formatting/whitespace, relative links/anchors, English, ignore rules, exclusion
  from `go list ./...` and privacy. Never force-add tool implementation or private records.

## Done when

- The command records one selected resolver's conditions, explicit positive run-only
  dial/keepalive limits, requested window, polling interval, explicit verification limit
  and the same target/expected-body SHA-256 criterion for both checks. Help and invalid-input
  checks are safe before profile access. Instructions start with at least five minutes
  and a keepalive observation limit that permits it. Tests can use short or injected windows.
  One fresh raw session is closed on every path; there is no supervisor, hidden redial, resolver
  switch or splicing of traces from different sessions.
- Non-SOCKS5 exit selections are refused before secret lookup or traffic. Independent
  sequential SOCKS5 authentication, complete CONNECT and explicit target-data verification
  succeed before a qualified window begins and again after its final snapshot, on the
  same session and with the same explicit target/expected-data criterion. Verification
  streams/transports and their workers are confirmed closed outside the window. Merely
  configuring credentials or accepting no-auth does not establish login. Missing
  credentials/service, redirects, unverified/incomplete data or a failed check cannot
  qualify idle evidence. Target names are sent through SOCKS without local lookup and
  HTTPS keeps normal verification. HTTP proxy response timing is never used;
  an HTTP(S) target-data check remains a separate usability check.
- The baseline snapshots `Session.Stats().BytesReceived` after setup and stream closure.
  Every counter value and actual monotonic observation offset, including the final
  boundary, is retained at full precision with requested/actual lengths, maximum actual
  sample spacing and polling lateness. The final snapshot precedes every post-check action.
  The window opens no application stream and sends no application request; normal tunnel
  polls and smux keepalives continue. Counter semantics are documented as Noise-channel
  bytes, including encryption but excluding KCP/DNS overhead
  (`core/client/session.go:68`), not packet-arrival or server-send timestamps or RTT.
- The estimator follows the accepted progress-interval method: for `(a, b]` and `(c, d]`,
  retain connecting-gap bounds `max(0, c - b)` to `d - a`; include unresolved arrivals
  within individual polling intervals and the leading/trailing boundaries when reporting
  a conservative maximum upper bound, with the explicit formulas from Step 5. Uneven
  actual polling widens uncertainty; byte deltas are not arrival counts. A window
  with no progress supplies a boundary/censored gap, not a healthy inter-arrival maximum.
- Cancellation, session termination/replacement, a counter decrease or invalid time trace,
  observation-limit expiry, failed post-check and cleanup/evidence-write failures retain
  their trace and explicit end reason. They cannot produce an unqualified healthy maximum.
  Requested-window completion remains distinct from early termination and from the later
  same-session post-check result; the exposed session error is not stronger causal evidence
  than the API provides. Pending stream acquisition/protocol/closure results are owned,
  late streams are closed and workers joined; no detached-operation workaround is used.
  Complete saved evidence requires successful bounded-record writes, output and log closure;
  the real logger cannot silently omit a mandatory oversized record or corrupt trace numbers.
- Deterministic trace tests cover changes and repeated counters, multiple unresolved
  arrivals, uneven polling, both boundary gaps, no progress, counter/time anomalies,
  cancellation and session end without waiting several minutes. Generated-key loopback
  sessions with synthetic authenticated SOCKS5/known targets verify pre/post usability,
  baseline/cleanup ordering, no application traffic in the window, no replacement dial
  and closure on success/failure/interruption, including delayed acquisition and close.
  Real-logger tests cover partial/oversized evidence and short-write/close failures.
  Credentials, Basic encodings, references, generated identity and target-bearing errors
  remain filtered from output/logs; public diagnostics also hide original/normalized
  bootstrap/resolver IPv6. Expected target data is verified without a payload dump.
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
understating uncertainty or qualifying a failed session. Stream open/close has no caller
context API; ownership and session teardown must bound verification without leaving work
behind. The API's `ErrSilent` also covers smux termination without proving its root cause.
Keep the existing signal-reader and source-confirmed socket-tracking lock follow-ups
separate unless this command demonstrates a concrete blocking impact; report such evidence
to lead rather than expanding into core work or weakening acceptance.

The inspected interfaces and cached independent helpers support one coherent result within
the expected nine-to-ten-file surface. If implementing the required ownership exposes a
new core/API/dependency prerequisite or independently acceptable extra result, stop and
return it to lead; do not force it into this tool task. No user access is needed for local
acceptance; actual SOCKS5 availability, several-minute transport evidence and the
timeout/recovery decision remain later gates. Command-option plumbing and the digest
criterion implement the accepted method and select no product authentication or timeout policy.

## Outcome

The existing ignored tool now provides `timeout-idle`, with safe help and early validation
of explicit positive dial, keepalive, window, polling and verification controls, an HTTP(S)
target without credentials, and its complete expected-body SHA-256. The README supplies
reserved DoH, DoT and UDP examples, initial five-minute guidance, result codes and complete
record mapping to the unchanged Healthy Idle Windows form. No live run or product value
was selected.

The command refuses non-SOCKS5 exits before secret lookup, network-DNS selection or traffic
and reads only the selected password reference. Existing raw preparation preserves one
effective resolver, R5 bootstrap/cache behavior, pins, literals, original TLS hostname,
fixed first network DNS and uTLS. Exactly one raw dial supplies the original session for
both checks and the observation; no supervisor, retry, resolver switch or replacement
session participates. The accepted initial DoT prerequisite remains unchanged.

Independent sequential SOCKS5 checks observe actual username/password method selection
and RFC 1929 login, complete CONNECT, and verify the entire expected 2xx response body.
No-auth success cannot qualify the experiment. Target names stay remote, redirects are
refused, and HTTPS retains normal certificate verification. A synchronous private target
transport reuses standard TLS/HTTP parsing and the existing streaming probe while keeping
acquisition, protocol, body, closure and eventual results under one joined worker. Deadline
or interruption tears down the sole session when necessary. Every pre-check stream is
closed and zero local streams confirmed before baseline; post-check starts only after the
actual final snapshot is frozen. Final cleanup confirms both Close return and Done.

The window sends no application traffic. It retains every actual monotonic observation
and full-width receive counter, polling spacing/lateness and final overshoot. The estimator
applies the accepted progress-width, connecting-gap and leading/trailing formulas without
sorting or repairing anomalies. No-progress runs retain censored boundary evidence.
Window completion, primary end reason, post-check and cleanup remain separate, and failures
cannot qualify a healthy maximum. The API's silent category is not stronger causal evidence.

Incremental private records preserve nanoseconds and uint64 values while filtering string
inputs before encoding. Mandatory encoded records are bounded before the logger; oversized
setup stops before dial. Private resolver mapping remains available, while public output
also hides original and canonical resolver/bootstrap spellings, including bare IPv6.
Identity, references, credentials and target-bearing diagnostics stay filtered. Short writes,
output errors or log-close failures prevent complete saved-evidence acceptance and retain
the available partial trace and primary reason.

Two accepted review findings were fully corrected in one correction round. Informational
HTTP responses, including Early Hints, now advance to the final response with buffered body
bytes preserved; protocol switching is refused. Response status lines and interim/final
headers share a fixed private byte and metadata budget before standard parsing, with safe
overflow failure; response bodies remain streamed. Exact-boundary, oversized, cumulative,
fragmented and generated-session regressions pass. No finding was rejected or left partly
fixed because of cost. Final independent review explicitly approves the complete task and
finds no new defect or regression.

Fresh final checks pass for nineteen focused idle test groups with race and no skips, the
full explicit tool race suite, Linux/Windows tool vet and builds, and Windows tool test
compilation. The built-command generated-loopback run verifies both same-session checks,
receive progress, the frozen boundary and confirmed cleanup; actual interruption retains
incomplete evidence and joins its child and fixtures. Both-exit selftest/redaction passes
HTTP/HTTPS while the server is up and its existing HTTPS-only down loop. Final review
independently reran affected races, platform tool checks and the built-command scenarios,
and verified the matching selftest evidence. Synthetic profile/store bytes and metadata
remain unchanged.

Broad product/core evidence was retained only after matching all thirteen reviewed
prerequisite files, the complete core manifest and all tracked build/test/CI inputs to the
accepted core. It covers applicable project races, all twenty mandatory isolated D-Bus/
keyring cases without skips, static builds, the exact Linux/Windows CI vet split, and the
Windows product plus all nineteen applicable project test-package compilations. Exactly
the four specified external TCP/DNS tests remain excluded, netns checks are disabled and
TestDNSGuardPermitsPrograms is compilation-only. No Windows binary was executed.

Formatting, whitespace, relative links, English, privacy, ignore rules and package-list
exclusion pass. The task changes ten ignored tool files; sixteen other original files are
unchanged. Only this completed roadmap node is committed. No product/core/API/profile/
dependency change, actual configuration/store access, external resolver/service, fetch,
network/trust mutation, live measurement, danted result, selected timeout policy or Windows
runtime/DPAPI acceptance belongs to this task. The existing CLI signal-reader and
source-confirmed socket-tracking lock follow-ups remain unchanged and separate. Lead owns
parent-stage acceptance and the later one-write, service, live-measurement and policy gates.
