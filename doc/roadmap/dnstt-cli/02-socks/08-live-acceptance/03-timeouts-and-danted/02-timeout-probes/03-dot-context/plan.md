---
kind: task
status: blocked
detailed: true
---

# Context-aware initial DoT setup and uTLS handshakes

## Task

Restore the existing `client.Dial(ctx, cfg)` cancellation/deadline contract through
initial DoT bootstrap, TCP connection and TLS handshake, for standard TLS and uTLS.
The shared uTLS helper must finish its handshake under the context it receives. This is
the user's narrowly authorized core/vendor prerequisite to
[Fresh Noise dial command](../01-fresh-dial/plan.md), not part of that tool's implementation
or acceptance. Weakening its observation-limit/interruption criteria was rejected.

Follow the [parent stage](../plan.md#approved-core-prerequisite), the
[measurement-stage execution boundaries](../../plan.md#execution-boundaries-and-user-gates)
and [architecture](../../../../../../../architecture.md) R1, R2, R4, R5 and §§04, 06.
Successful sessions keep their independent lifetime; canceling a completed dial's context
must not end the session or its later transport redials. Preserve P2 backoff and ownership
on close, R5 lookup/pins/SNI, certificate validation, uTLS fingerprints and ALPN behavior.
No new public API, configuration field, dependency, upstream version or timeout policy is
approved. If one proves necessary, stop and return a user question through lead.

The defect is before the context-aware Noise wait: `core/client/session.go:133` calls
`openTransport` synchronously; `core/client/tls.go:43-51` creates an independent initial
30-second timeout; `core/client/utls.go:82-93` then uses context-free `Handshake()`.
The `sockets.dial` lifetime bridge (`core/client/transport.go:139-165`) ends at TCP return,
not after TLS. Finite silent-loopback reproductions confirmed missed caller deadlines and
interruption for standard TLS and `Firefox_120`; an unbounded uTLS wait is a source-path
inference, not an observed infinite run. Existing cancellation and `HandshakeContext`
facilities suffice; no detached `Dial` goroutine is an ownership-safe workaround.

Expected review surface: `scripts/vendorcore/rules.go`, `main_test.go`,
`_files/client/transport.go`, a new `_files/client/tls_context_test.go`, and focused
extensions of `_files/client/redial_test.go` and `_files/client/utls_test.go`; their
generated `core/client/` counterparts, generated `tls.go`/`utls.go` and `core/CORE_VERSION`
give roughly thirteen files. This is one context/ownership repair with one regression
matrix, not separate helper-only results. Keep unrelated core, shell and signal-reader
changes out. DoH is a regression consumer of the shared helper, not a new transport feature
or a general HTTP lifecycle rewrite.

An independent core coordinator accepts this result. Preserve all saved ignored-tool
source, README, inventories and evidence; do not edit them or the private measurement
form/journal. Passing a tool smoke check here does not accept fresh-dial or start idle
observation. Lead resumes the saved command coordinator only after this prerequisite.

## Steps

1. Establish the initial-call/redial boundary using `session.go:127-177`,
   `transport.go:67-102,139-168,236-269`, `tls.go:37-94`, `redial.go:21-45` and
   `utls.go:69-98,189-254` under `core/client/`, with their maintained templates/rules.
   Add bounded failing regressions in `scripts/vendorcore/_files/client/tls_context_test.go`
   for `Dial` against a TCP peer that accepts but never replies to TLS: standard TLS
   (`UTLS = "none"`) and `Firefox_120`, each with a short deadline and explicit cancellation
   after accept. Check the actual return and context error, not only that a timer fired.
   Own the test call/result: on every failure path, close the fixture and join its goroutines
   and the outstanding dial. Never leave an eventual session or test subprocess abandoned.
2. In `scripts/vendorcore/rules.go`, preserve the exported `NewTLSPacketConn` signature
   and its legacy initial timeout while factoring its body into a private context-taking
   constructor. The existing exported entry can create its historical background timeout
   and delegate; `openTransport` in `_files/client/transport.go` calls the private entry
   with the caller's context for the initial dial. The initial callback must use that
   context directly, not the old independent timeout or `min(caller limit, 30 seconds)`.
   Keep later redials on the existing per-attempt `dialTimeout` and P2 backoff, independent
   of the initial context. Add/update explanatory comments through rules/templates and
   respect the existing rule order, mandatory match counts and `transportLogf` conversion.
   Existing upstream `main` and constructor callers continue to build without API changes.
3. In `_files/client/transport.go`, keep session-lifetime cancellation active throughout
   each DoT setup attempt, including TLS after `sockets.dial` returns. Combine the attempt
   context with `sockets.ctx` using existing cancellation facilities; remove temporary
   callbacks/cancel functions on every exit. Do not parent `newSockets` to the initial dial
   context or retain it in the redial closure. Failure/cancellation closes and forgets any
   acquired raw/TLS connection; a successful handshake leaves no persistent deadline or
   cancellation hook from the dial. Keep the original hostname for verification/SNI and
   route all connections and lookups through the current `Control`/`LookupIP` ownership.
   Update the stale `canLookup`/constructor comments about DoT's initial timing. Do not
   introduce raw sockets, a system resolver or a new public testing hook.
4. Add a mandatory-count rule in `rules.go` changing the explicit uTLS handshake in
   `utlsDialContext` to the cached dependency's `HandshakeContext(ctx)`. Keep the eager
   handshake before returning, since it enforces the selected ClientHello fingerprint;
   preserve config cloning, certificate checks and error-path closure. Check both DoT
   callers and DoH's `makeRoundTripper` bootstrap and subsequent dial callbacks. Do not
   alter the fingerprint table/distribution, disable verification or replace the HTTP/ALPN
   machinery to solve this narrow defect. `go.mod`/`go.sum` stay unchanged.
5. Complete maintained regressions in `_files/client/tls_context_test.go`,
   `_files/client/redial_test.go` and `_files/client/utls_test.go`:
   - Cover pre-canceled/expired contexts, cancellation while `Config.LookupIP` waits,
     and cancellation/deadline through the initial dial callback and post-TCP handshake.
     Use injected callbacks or loopback, not external TCP black holes. Check wrapped
     `context.Canceled`/`context.DeadlineExceeded`, closed acquired connections and eventual
     empty ownership/terminated operations. Check that a caller deadline beyond 30 seconds
     reaches the initial callback unchanged without waiting that long; separately retain
     the legacy constructor's and redial callback's existing per-attempt timeout.
   - Use generated certificates/keys and a loopback TLS DNS forwarder to the in-process
     dnstt test server for successful DoT/Noise sessions in both TLS variants. After
     `Dial` succeeds, cancel its context and verify continued stream data. Drop a transport
     connection and verify a later redial still works; then close the session and confirm
     `Done`, sockets and owned loops finish. Cover teardown during a pending lookup/dial
     or silent redial handshake, with no further redials after closure. Reuse the existing
     short-backoff tests; do not change production timeout/backoff constants for testing.
   - Establish generated trust only inside test-local TLS configs/root pools or an isolated
     test process for the full `Dial` fixture. Never change system trust, use real server
     material, weaken production verification or add a public CA/config field for tests.
     Check hostname/SNI and untrusted/wrong-name failures as well as the successful path.
   - Check the shared uTLS helper's silent-peer deadline/cancel and successful connection
     after handshake-context cancellation. Retain the existing all-default-fingerprint
     HTTP/2 test and exercise HTTP/1.1 plus the shared DoH bootstrap/subsequent-dial path
     with generated trust and the caller's dial hook. Preserve negotiated protocol, hook
     use and proxy bypass; scope assertions to the existing context/lifecycle contract,
     not a new general DoH feature. Include affected DoH failed-dial/close regressions.
   - Use explicit fixture readiness and finite guards tolerant of race instrumentation.
     Clean up and join operations even when assertions fail. Test failed, canceled and
     successfully closed paths for owned socket/goroutine leaks; no test waits for the
     production 30-second timeout, several idle minutes or an inferred infinite wait.
6. In `scripts/vendorcore/main_test.go`, add focused tests for the constructor/initial-call
   and uTLS handshake transformations, including mandatory-count failure on source drift.
   Run the rule/template and socket-guard tests. Generate `core/` only through
   `scripts/vendorcore` from cached source matching the recorded upstream; do not hand-edit
   generated files or format the whole vendored tree. Check template mirrors, expected
   `CORE_VERSION` count changes, `vendorcore -check` and a second no-op generation. Review
   the generated diff for unrelated churn. No upstream fetch or version change is allowed.
   If cached source lacks verifiable revision metadata, report that input limitation to
   lead rather than inventing provenance, rewriting the recorded revision to force PASS
   or claiming an unchecked regeneration is reproducible.
7. Run the affected validation matrix below with the final generated sources. Preserve
   the ignored tool byte-for-byte while checking its existing races and both-exit selftest.
   The existing actual-DoT `timeout-dial` deadline/interruption reproduction may also be
   rerun against silent fixtures as a smoke check, with every process waited and no tool
   edits. Hand off the core result and affected integration evidence separately; fresh-dial
   remains blocked until lead independently accepts this task and resumes that command's
   own unchanged acceptance.

### Validation matrix

Use a cached toolchain satisfying `go.mod` and cached dependencies only, with
`GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`. The
[parent matrix](../../plan.md#local-validation-for-code-children) applies in full to this
core/vendor/product change, not only to the ignored tool:

- Run focused new regressions, then `go test -race -count=1 ./core/client ./scripts/vendorcore`
  and the applicable full project race suite with `DNSTT_CLI_TEST_DBUS=1`,
  `DNSTT_CLI_TEST_NETNS=0` and exactly
  `-skip '^(TestDualStackListener|TestLoopbackIndexBlocks|TestPhysicalIndexPasses|TestFamilyWithoutRouteRefuses)$'`.
  Mandatory Secret Service/keyring cases must pass using isolated D-Bus, without skips.
- Check vendor rules, template mirrors, socket coverage and generated reproducibility;
  `go run ./scripts/vendorcore -check -upstream <cached-source>` must be checked against
  the recorded source, with any unavailable provenance reported explicitly, not hidden.
- Run `CGO_ENABLED=0 go build ./...` and a static product build outside tracked paths.
  Use the exact Linux/Windows CI vet split from `.github/workflows/linux.yml` and
  `windows.yml`: full vet outside `core/`, `go vet -testinggoroutine=false ./core/...`
  only for core. The two known upstream noise-test warnings from plain full vet are
  not permission to disable other checks.
- Cross-build the Windows product and compile all applicable Windows project tests,
  including the new core and vendor tests, with `GOOS=windows CGO_ENABLED=0` and artifacts
  outside tracked paths. Compile `TestDNSGuardPermitsPrograms`; do not execute Windows
  binaries, external DNS tests or DPAPI. No Windows runtime acceptance is claimed.
- Run the saved tool's explicit `go test -race -count=1 ./doc/runs/_live`, Linux/Windows
  tool vet, product/tool integration checks and both-exit loopback selftest using only
  cached upstream inputs, temporary synthetic configuration/stores and filtered logs.
  Suppress Linux Secret Service discovery in selftest; the separate mandatory keyring
  suite still uses isolated D-Bus. Cross-build the Windows tool and compile its tests.
  Preserve the existing selftest's coverage and limitations without declaring fresh-dial
  accepted or editing the tool. New core inputs invalidate prior broad core/product evidence.
- Check formatting of maintained additions, `git diff --check`, links, English and privacy.
  Keep the tool excluded from `go list ./...`; verify `doc/runs/` and `doc/local/` remain
  ignored and unchanged. Do not stage private evidence or force-add ignored files.

## Done when

- For standard TLS and `Firefox_120`, initial DoT setup ends on the caller's deadline or
  explicit cancellation through lookup/TCP/TLS, including an accepted silent TLS peer.
  Tests check returned context errors, actual completion and owned cleanup, not just timer
  expiry. The initial callback sees the caller's longer deadline without the old hidden
  30-second cap; no long wall-clock wait is needed to establish that boundary.
- A successful generated DoT/Noise session remains usable after its dial context is
  canceled and after a subsequent transport redial. Existing per-redial timeout/P2 backoff
  remain independent of that context. Session close/teardown ends acquired sockets and
  pending setup/redial work; bounded checks find no abandoned result, goroutine or session.
- uTLS performs its eager fingerprint-preserving handshake under the supplied context;
  failures close acquired connections and successful handshakes are not later canceled
  by that context. Standard/uTLS certificate verification, original SNI/pins/lookup/hook
  semantics and shared DoH HTTP/1.1/HTTP/2 regressions pass without a behavior/scope rewrite.
- The fix and tests are maintained in rules/templates, generated mirrors match, mandatory
  rule counts/socket guard pass and regeneration is reproducible against verified cached
  upstream input. The second generation has no additional diff; no vendored-only edits,
  upstream upgrade, new API, profile field, dependency or product timeout policy is included.
  Missing verification input is reported to lead and cannot be marked as a passed gate.
- Focused/full applicable races, isolated mandatory D-Bus, static build, exact Linux/Windows
  CI vet split, vendor checks, affected tool integration and Windows cross-build/test
  compilation pass under the stated exclusions/isolation. Formatting, links, English,
  whitespace, ignore/exclusion and privacy checks pass; ignored work is preserved.
- This prerequisite is accepted separately from the saved fresh-dial implementation. No
  observation/cancellation criterion, measurement method, live goal, HTTP/SOCKS5 decision
  or timeout selection changed, and no live or Windows runtime/DPAPI evidence is claimed.

## Risks and what is needed from the user

The main risks are accidentally retaining the initial context for redial/session lifetime,
leaving a cancellation callback/deadline on a successful connection, closing a connection
concurrently with ownership transfer, and overlooking DoH consumers of the shared helper.
Test both sides of the initial/success/close boundary; cancellation is not permission to
abandon eventual results. Trust setup for generated successful DoT fixtures must remain
test-only and must not motivate a public API or weaker certificate verification.

No real configuration, store, metadata, service or resolver is needed. Use synthetic
temporary state, generated identities/certificates and loopback; do not dump inherited
environment, perform external downloads, run netns checks or mutate networking/trust.
Follow `AGENTS.md` license restrictions; the permitted dnstt CC0 source is sufficient.
Cached toolchain/source availability and provenance are execution prerequisites to verify,
not authorization to fetch or invent metadata. Return a newly necessary public-interface,
dependency or other enduring/security choice through lead; do not reopen the already
authorized repair. Live service availability, several-minute evidence and timeout/recovery
choices remain with the later user-run nodes.

## Missing verified generation input

The initial preflight stopped before implementation. The available cached source has no
Git commit/tree objects or source-byte manifest verifying the recorded upstream revision.
Cached version metadata corroborates the version/hash mapping but cannot establish the
source bytes. The read-only baseline vendor check fails on unknown revision metadata and
two additional source entries; the remaining generated files match existing transformations.
That consistency is useful evidence, not verified clean-source provenance.

This task is blocked pending a verified local checkout or Git bundle/object source for
the recorded commit, or the user's explicit authorization for a bounded public-upstream
fetch into a fresh temporary directory. No fetch, generation, source edit or code review
has occurred. Do not invent metadata, change the recorded revision or weaken acceptance.
The repair is already authorized; only its required verification input is unresolved.
