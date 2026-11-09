---
kind: task
status: done
detailed: true
depends: [01-measurement-method, 02-timeout-probes]
---

# Local one-write SOCKS5 probe

## Task

Add one usable experimental `danted-probe` command to the existing ignored tool: paired
authenticated sequential/one-write exchanges on fresh streams in one original raw tunnel
session, with complete replies, verified target data and attributable private evidence.
Reuse the accepted [raw-session lifecycle](../02-timeout-probes/plan.md) and independent
[target verifier](../02-timeout-probes/02-idle-observation/plan.md). The command prepares
evidence for [the user's actual danted test](../04-danted-measurements/plan.md); local
fixtures establish the instrument, not service availability or danted compatibility.
Follow the [measurement method](../01-measurement-method/plan.md),
[parent scope](../plan.md) and [architecture](../../../../../../architecture.md) R3.

Expected surface, all under `doc/runs/_live/`: `main.go`, `README.md`, new
`danted_probe.go`, `danted_exchange.go`, `danted_trial.go`, `danted_evidence.go`, their
four test files and `danted_fixture_test.go` (eleven files). At most a narrow private
target-verification seam and regression in `idle_verify.go`/`idle_verify_test.go` may add
two files if direct reuse needs it. The exchange, ownership, fixture, evidence and command
are parts of one result with one review and validation set, not separate infrastructure
deliverables. Preserve accepted comparison, idle/fresh-dial commands and selftest behavior.
No product/core/API/profile/dependency, vendoring or timeout-policy change belongs here.

## Steps

1. Add `danted-probe` dispatch/help and fixed safe diagnostics in `main.go`, with private
   options/validation in `danted_probe.go`. Reuse `-profile`, exactly one effective
   `-resolver`, optional `-utls` and filtered `-v`. Require explicit positive `-n` (pairs),
   `-dial-limit`, `-keepalive-limit` and `-trial-limit`; the last bounds each entire trial
   from before stream acquisition through stream closure. Require an explicit HTTP(S)
   `-url` without userinfo and a valid full-body `-expect-sha256`, as in `timeout-idle`.
   Add a required affirmative `-allow-early-credentials` acknowledgement, default false,
   with help and a fixed warning explaining transmission before method/login replies.
   Reject invalid flags, positional arguments, missing acknowledgement, unsafe count
   arithmetic, durations and target/digest before configuration-path or profile access,
   without echoing values. No credential input through flags/environment, hidden target,
   upstream binary, mode-only run or automatic fallback is needed. These options and
   record names are tool-private experiment controls, not an enduring product interface.
2. In `danted_probe.go`, use the existing read-only `loadProfile`; reject every non-SOCKS5
   exit before secret lookup, network-DNS selection or traffic. An empty exit type retains
   the SOCKS5 meaning. Fetch only the selected `exit.password_ref` through the existing
   lookup, with dependency injection for tests. Require a nonempty username and preserve
   existing byte-length/empty-password semantics (`profile.go:99`,
   `internal/exit/socks5.go:159`); actual RFC 1929 acceptance, not configured credentials,
   qualifies a control. Reuse `prepareRawSession` (`raw_session.go:81`) and the existing
   bounded raw dial/runtime: one effective resolver, fixed first DNS for `network`, lazy
   R5 bootstrap/cache, exclusive explicit bootstrap, pins/literals, original TLS hostname
   and profile/override uTLS. Dial exactly once, record its actual result separately, and
   retain the returned session after canceling the successful dial context. No supervisor,
   product exit/application builder, resolver switch, redial or session replacement.
3. Implement an independent RFC 1928/1929 exchange in `danted_exchange.go`; do not import,
   call or copy product `internal/exit` encoders, parsers or error types. Both paths offer
   only method `0x02` with greeting `05 01 02`, using identical credentials and CONNECT
   bytes. This deliberate experimental condition avoids x/net's additional no-auth offer
   (`idle_verify.go:377`); neither its behavior nor the product's method policy is changed.
   Sequential control completes greeting, validates method, completes login, validates
   status, then sends CONNECT. Its message writer handles partial progress and errors.
   Treatment constructs greeting + RFC 1929 login + CONNECT and invokes the underlying
   owned tunnel stream's `Write` exactly once before any `Read` or buffered prefetch.
   Require `n == len(batch)` and `err == nil`; a short/failed Write ends that treatment
   without another Write, reply read or sequential retry. Record actual call/count/order
   evidence without retaining or dumping the credential-bearing batch. Encode domain
   targets without local lookup, support literal IPv4/IPv6 with appropriate ATYP, validate
   host/port/length before traffic and keep the same target authority on both paths.
4. In `danted_exchange.go`, read exact framed replies or retain one buffered reader through
   every phase and the returned connection. Validate SOCKS version and the actually chosen
   method; distinguish `0xff`, unoffered no-auth/other methods and malformed selection.
   Require RFC 1929 version/status success. Parse the entire CONNECT response: version,
   REP, reserved byte, ATYP, bounded domain/address and port for IPv4/domain/IPv6. Retain
   an observed refusal code separately from full-reply validation if a refusal is truncated;
   neither is complete CONNECT success. Reject unsupported/malformed framing and stop
   after the first terminal phase. Preserve target bytes coalesced with any replies; do
   not discard a reader or its prefix. Never send the HTTP request after a failed or
   unqualified SOCKS exchange, and do not log bound addresses, wire data or echoed errors.
5. Implement the full trial worker and ownership in `danted_trial.go`. Reuse the private
   stream capability, deadline-clamping connection, single-owner Close and joined teardown
   patterns in `idle_verify.go:20`, `idle_verify.go:159`, `idle_verify.go:450`, plus
   `rawRuntime` and `closeRawSession(..., true)` (`raw_session.go:174`). Keep Noise-only
   `rawSession` and its fakes stream-free. `OpenStream` has no context
   (`core/client/session.go:225`), and smux open/write/close may wait: install deadlines
   before writes, watch cancellation,
   limit and session end, and tear down the sole session when necessary. A later deadline
   update is not assumed to interrupt an already blocked Write. Collect eventual results,
   close late streams and join every worker/callback/Close; exceeding the existing recorded
   `5s` cleanup safety bound fails qualification and does not permit detached work.
   After successful CONNECT, give the already-owned connection to `idleHTTPTransport`
   (`idle_verify.go:326`) and `probeWithClient` (`probe.go:58`) with the same immutable
   target/digest and redirect refusal as idle verification. Preserve standard TLS/HTTP
   parsing, Host/SNI, disabled environment proxies, complete streamed 2xx-body SHA-256,
   informational responses except 101 and cumulative `10 MiB` header/metadata budgets.
   Generated trust is test-local only. Confirm body/transport/stream closure and zero local
   streams before the next trial; final cleanup confirms both session Close return and Done.
6. In `danted_probe.go` and `danted_trial.go`, run each numbered pair as sequential control
   then one-write treatment, each on a fresh stream of the same session with the same
   target and limits. A safely closed ordinary protocol/target failure, including a bad
   control, stays in the record and does not silently remove that pair or later attempts.
   Its treatment is not interpretable as compatibility evidence without a passing control.
   Interruption, observation expiry, session end, deadline/completion race, uncertain
   closure or evidence/output failure stops further work; record attempted/unstarted
   paths/pairs and the first end reason, without a replacement dial. Keep failure and
   censoring separate from cleanup and evidence status. Define monotonic timers in the
   trial result: acquisition call/return; each SOCKS write call/return; reply validation
   completions; complete SOCKS exchange from first greeting/batch Write through complete
   CONNECT; target check from before TLS/GET through body verification/closure; and separate
   stream closure/session teardown. Keep actual nanoseconds on failed/late returns too;
   unreached phases stay explicitly not started/observed, without invented successful timings.
   For sequential phases, time each request-to-complete-reply interval. For treatment,
   the batch's emission boundary is its single Write start; record batch Write duration,
   cumulative method/login/CONNECT completion offsets from that origin and incremental
   reply-read/validation times. The latter are not separate login/CONNECT round trips or
   per-frame send timestamps. Record the actual observed limit/termination boundary and
   eventual worker duration without replacing either with the configured limit. Only
   like-for-like complete exchange/target timings from qualified pairs can support a later
   latency comparison; no fixed saving is inferred from local/noisy samples.
7. Add incremental structured records and output in `danted_evidence.go`, following the
   accepted idle evidence/output safeguards (`timeout_idle.go:109`, `timeout_idle.go:287`,
   `timeout_idle.go:466`) and `log.go`. Include conditions/private resolver mapping,
   planned counts/order, explicit method/early-credential condition, all limits, separate
   dial result, pair/path ordinals, actual Write completion/order evidence,
   phase validation/status/code/timings, target
   status/body count, closure/session result and attempted/unstarted/control-qualified
   counts. Reference the target/digest in the existing private form instead of logging
   either. Preserve every attempted path, first failure and incomplete/censored outcome;
   no readiness-only, successful Write or successful unauthenticated path can pass.
   Reuse `idleRedactors`/public filtering in substance: hide profile/reference, identity,
   raw and Basic-encoded credentials, URL/host/path/query variants and echoed diagnostics
   in private/public records. Only private conditions retain resolver mapping; public and
   verbose output also hide original/normalized resolver/bootstrap/pin spellings, including
   bare IPv6. Filter input strings before encoding so short identities cannot corrupt
   schema, categories, counts, status bytes or nanoseconds. Enforce the mandatory encoded
   `64 KiB` record bound including prefix/newline; reject oversized setup before dial and
   never substitute the logger's omission marker for evidence. Detect short writes,
   mirror/output and log-close failures. Claim `evidence=saved` only after joined session
   cleanup, all mandatory writes, prior output and log closure have succeeded; retain
   partial evidence otherwise. Define command codes: 0 only for all planned paths passing
   with confirmed closure/evidence; 1 for a fully recorded/closed series with ordinary
   failures; 2 for setup failure or an incomplete series. A code or local fixture outcome
   is not an automatic danted support/rejection verdict.
8. Add `danted_exchange_test.go`, `danted_trial_test.go` and an independent
   `danted_fixture_test.go`. The fixture parses RFC fields itself, without the new exchange
   as oracle, and retains one persistent bounded reader across greeting/login/CONNECT and
   relay. Do not use the sequential selftest reader reset (`selftest_exit.go:120`) as a
   pipeline oracle or refactor that accepted fixture. Exercise an accepting buffered exit
   and deterministic treatment rejection/stalling with working sequential controls. Use
   scripted stream roles or explicit operation barriers for negative cases, never TCP
   receive sizes, sleeps or a claim to emulate danted's recv behavior. Fixtures must keep
   consuming a batch while replies are pending, with joined reply work where needed;
   zero-capacity test pipes must not fabricate an acceptance deadlock. Recording client
   connection wrappers prove one complete treatment Write before its first Read and
   sequential waits before subsequent messages; no one-packet assumption participates.
   Cover short/zero/error writes, fragmented/coalesced replies, all CONNECT ATYPs, first target bytes,
   no-auth/unoffered/refused methods, bad credentials, malformed/truncated replies and
   CONNECT refusal. Exercise cancellation/limits/session end at acquisition, blocked Write,
   reply, TLS/body and Close, including late acquisition/Close and completion races; assert
   ownership and no next trial after teardown. Reuse the existing target verifier's
   regressions and add command-path HTTP/HTTPS checks for known data, wrong/truncated data,
   redirect/non-2xx/TLS failure, 103 followed by final success, 101 and bounded headers.
9. Add `danted_probe_test.go` and `danted_evidence_test.go` for help/dispatch, early safe
   validation, acknowledgement, unsupported exit before lookup/traffic, selected-only
   secrets, one raw dial, fresh streams, fixed ordering, retained bad controls/failures,
   exact timer boundaries/counts and stop/unstarted accounting. Inject clocks/runtime where
   practical, avoiding long real waits. Test actual logger limits/short writes/close and
   output failures, string redaction/IPv6/path variants and numeric/schema integrity.
   Use generated `server.StartTest` sessions and known loopback targets to exercise the
   actual built command with passing pairs, controlled rejection/stall and real SIGINT;
   check command codes, full records, no fallback/replacement, joined child/fixtures and
   unchanged synthetic profile/store bytes and metadata. Establish isolated temporary
   config/store state before every path/loader/store call; suppress Linux Secret Service
   discovery and use its temporary file store. Join fixtures even on failing paths, and
   do not parallelize tests that change environment or global logging. This adds no
   credential environment input, external fixture process/dependency or trust mutation.
10. Extend `README.md` with actual `danted-probe -h`, reserved-example paired commands,
    explicit acknowledgement/method/limits/target/digest, read-only selected secret setup,
    timer boundaries, evidence fields/codes and mapping into the unchanged Paired Danted
    Experiment form. Update command availability text only where necessary; preserve the
    accepted method and unfilled form. Explain one stream Write versus unobserved TCP/
    downstream/recv boundaries, bad controls and censored/incomplete cases, and preserve
    failed runs when explicitly repeating with changed limits. Actual login-protected
    danted availability is confirmed by the user in the next child; HTTP success or a
    local fixture cannot answer it. A later positive live result still needs the user's
    enduring key/default/authentication/no-login/fallback contract in
    [Resolve the measured result](../07-danted-result/plan.md). Run the matrix below and
    record exact new execution versus input-bound retained evidence privately.

### Validation matrix

Apply the [parent matrix](../plan.md#local-validation-for-code-children) with cached Go
matching `go.mod` and `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`:

- Fresh focused exchange/trial/command/evidence races, full explicit
  `go test -race -count=1 ./doc/runs/_live`, explicit Linux/Windows tool vet, static tool
  build, Windows tool build/test compilation and the actual built-command scenarios above.
  Preserve fresh-dial/idle/shared-consumer regressions and the existing both-exit isolated
  selftest/redaction: HTTP/HTTPS up, existing HTTPS-only down. Claim no additional HTTP-down
  or Windows runtime coverage.
- Account for the applicable project race with `DNSTT_CLI_TEST_DBUS=1`,
  `DNSTT_CLI_TEST_NETNS=0` and exactly
  `-skip '^(TestDualStackListener|TestLoopbackIndexBlocks|TestPhysicalIndexPasses|TestFamilyWithoutRouteRefuses)$'`.
  All twenty mandatory isolated D-Bus/keyring cases must pass without skips. Cover
  `CGO_ENABLED=0 go build ./...`, static product build, exact Linux/Windows CI vet split
  (full outside `core/`, `-testinggoroutine=false` only in `core/`), Windows product build
  and all nineteen applicable project test-package compilations. Keep artifacts outside
  tracked paths; `TestDNSGuardPermitsPrograms` is compilation-only.
- Broad product/core/build/vet/race/Windows evidence and artifacts from the accepted timeout
  stage may be retained only after exact source/build-input/toolchain binding, described in
  `doc/local/plan-timeout-probes-acceptance.md`. New tool inputs require fresh affected
  checks; changed inputs, failures or unresolved concerns require corresponding new runs.
  Retained checks are not relabeled as fresh. Do not repeat unchanged core generation,
  download or fetch upstream/dependencies.
- Check formatting/whitespace, relative links/anchors, English, privacy, Git ignore and
  exclusion from `go list ./...`; never force-add tooling or private records. No real
  configuration/store/metadata, inherited-environment dump, live service/resolver, netns,
  network/trust mutation or Windows execution is part of local acceptance.

## Done when

- Help/invalid-input/acknowledgement checks precede configuration access and use fixed safe
  diagnostics. The command requires explicit pair count, target/full-body digest and
  positive run-only limits; rejects non-SOCKS5 before secret/DNS/traffic; and reads only
  the selected password. One raw dial supplies every fresh control/treatment stream, with
  unchanged resolver/pin/bootstrap/uTLS semantics and no redial or hidden retry.
- Both paths offer only `0x02` and actually validate RFC 1929 authentication. Instrumented
  tests prove sequential waits and exactly one complete treatment stream Write before
  any Read; short/failed batches cannot pass, retry or fall back. Complete method/login/
  CONNECT validation, all address forms, remote target names, first-byte preservation and
  full expected 2xx-body verification are exercised independently of product exit code.
- Every attempted path, including failed controls, has an ordinal, actual phase/whole
  exchange/target/closure timings and explicit outcome. Treatment batch-relative completion
  offsets and incremental reply-read durations are named accurately, not presented as
  independent phase RTTs. Refusal, malformed/unsupported/truncated replies, failed/short
  Write, data failure, censoring, interruption and session/cleanup failure remain distinct.
  Missing/bad controls and observation limits cannot produce a compatibility verdict.
- Acquisition, protocol, target and closure obey the trial observation/ownership contract.
  Late results/streams and every worker/callback are collected/joined; streams close before
  the next attempt, and every returned session confirms Close and Done. Stop races retain
  the first reason and unstarted counts. Cleanup uncertainty prohibits successful completion;
  evidence/output/close failure prohibits a saved-evidence claim. No detached operation or
  replacement session participates.
- Independent buffered fixtures pass accepted batching and deterministic rejection/stall
  with working sequential controls, without relying on packets/recv or shared protocol
  helpers. Focused tests cover partial I/O, coalescing/fragmentation, authentication,
  malformed/refused replies, data/TLS failures, cancellation at every blocking phase,
  late closure, exact timers and bounded privacy-safe records. Built-command success,
  negative scenarios and SIGINT confirm outcomes, ownership and synthetic-store immutability.
- Every mandatory encoded record fits the 64 KiB logger boundary; oversized setup stops
  before dial, with no silent omission. Logs/console hide credentials including Basic,
  identity/reference and target-bearing strings; public/verbose output also hides original
  and normalized resolver/bootstrap IPv6. Counts, schema and nanoseconds stay exact after
  filtering. Saved evidence follows all writes, joined cleanup, output and log closure.
- README commands, warning, timer/result interpretation and form mapping are usable and
  consistent with the accepted method. The form and completed nodes remain untouched.
  Local results answer no actual danted/live gate, infer no server read boundary or fixed
  latency saving and select no enduring exit/authentication/default or timeout policy.
- Fresh affected checks and input-bound broad evidence satisfy the validation matrix,
  including mandatory isolated D-Bus, exact external exclusions, static build/CI vet split
  and Windows compilation. Formatting, links, English, privacy and ignore/exclusion pass.
  The implementation stays ignored, with no product/core/API/config/dependency change;
  comparison, both exits, secret-input ceiling, selftest and accepted timeout probes retain
  their behavior. Later live/platform/service and user-decision gates remain open.

## Risks and what is needed from the user

No actual service access or new user decision is needed to prepare and accept this local
instrument. Use isolated synthetic profiles/stores before any configuration lookup,
generated keys/certificates, loopback and cached dependencies only. Follow `AGENTS.md`'s
incompatible-license restrictions; do not open GPL/AGPL/PolyForm implementations.

The main risks are a fixture discarding buffered pipeline bytes, deadlock from assuming
stream Write/recv boundaries, unjoined context-free smux operations, incorrect batch-phase
latency labels and treating bad controls or clipped data as rejection. The accepted helpers
support one coherent command within eleven-to-thirteen tool files. A concrete new core/API/
dependency prerequisite or independently acceptable extra result must be reported to lead
for scope/splitting rather than forced into this task or hidden by weaker criteria. Keep
the existing CLI signal-reader and source-confirmed socket-tracking lock follow-ups
separate unless new evidence demonstrates blocking impact; neither is repaired here.

Only the later user-run child confirms actual login-protected danted availability and
collects paired live evidence. Functional early-credential testing is already part of the
explicit experiment; product method selection, no-auth behavior and any key/default or
fallback still require the later user contract. A working local fixture or HTTP exit does
not settle those gates, and a live failure without working paired controls is inconclusive.

## Outcome

The ignored tool now provides one usable `danted-probe` command. It runs authenticated
sequential controls and one-write treatments on fresh streams of one original raw session,
with identical credentials and target authority. Both paths offer only method `0x02`;
early credential transmission requires explicit acknowledgement and changes no product
authentication policy. Invalid inputs are refused safely before configuration access.

- An independent RFC 1928/1929 implementation validates every method, login and complete
  CONNECT response. Treatment makes exactly one complete owned-stream Write before any
  Read, with no retry or fallback after a short or failed batch. Verified HTTP(S) data uses
  the accepted streamed full-body digest, standard TLS, redirect refusal and bounded
  response parsing; first target bytes are preserved.
- Trial ownership covers acquisition, protocol, target and closure. Late results, workers,
  streams and teardown are joined without replacing the session. Actual phase, exchange,
  target and cleanup timings remain distinct; treatment completion offsets are explicitly
  batch-relative. Failed controls, ordinary failures, limits, interruptions and unstarted
  paths remain attributable rather than becoming a compatibility verdict.
- Bounded incremental evidence preserves numeric/schema integrity and filters identities,
  credentials, target strings and public resolver variants. Sink I/O is outside measured
  protocol work. A stalled post-trial sink triggers joined session cleanup and cannot
  start another path; an arbitrary permanently blocked synchronous writer can still hold
  the CLI until it returns. This limitation is explicit and no writer is detached.
- Independent buffered fixtures, generated loopback sessions, HTTP/HTTPS targets and real
  process interruption establish the instrument. Five accepted review findings were fully
  corrected: measured-path evidence I/O, IDNA TLS naming, DNS hostname length, preservation
  of the primary stop reason, and cleanup of unused synthetic HTTP targets. Re-review
  approves all ten Steps and eight Done-when criteria; no finding was rejected or deferred.
- Fresh final-input focused and complete tool races, actual command success/rejection/
  stall/SIGINT, platform tool checks and both-exit selftest pass. The final independent
  full tool run has 93 top-level tests and 460 passing test events. The applicable project
  race, twenty mandatory isolated D-Bus/keyring passes, static builds, exact Linux/Windows
  CI vet split, Windows product and nineteen project test compilations are covered by
  verified matching-input evidence, with execution versus retention recorded privately.
  Exactly four external TCP/DNS tests are excluded, netns tests remain disabled and Windows
  DNSGuard is compilation-only. No Windows runtime or additional HTTP-down coverage is
  claimed.
- README syntax, warning, timer interpretation and mapping into the unchanged private form
  are usable. Formatting, links, English, privacy, ignore rules and package exclusion pass.
  The complete task surface is thirteen ignored tool files; twenty-two unrelated existing
  tool files are unchanged. No product/core/API/profile/dependency, vendor or policy change
  was needed. Comparison, both exits, secret-input ceiling and timeout probes are preserved.

Original temporary baseline and earlier captures became unavailable during the work, with
no established cause. They were not presented as retained evidence: broad checks were
rerun and their current bindings, later corrections and independent acceptance were saved
privately. No accepted correction remains partly fixed because of cost. The separate
pre-existing signal-reader and socket-tracking-lock follow-ups remain unchanged.

This local acceptance establishes neither authenticated danted availability nor live
compatibility, server read boundaries, fixed latency savings, timeout values or an enduring
key/default/authentication/fallback contract. Those user-run and decision gates stay open.
