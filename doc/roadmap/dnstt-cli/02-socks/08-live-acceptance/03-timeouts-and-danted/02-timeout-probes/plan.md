---
kind: stage
status: done
base: 643e9e4
depends: [01-measurement-method]
---

# Local timeout probes

Extend the accepted ignored tool with bounded fresh-session handshake measurements and
healthy-idle receive-gap observations for the [measurement method](../01-measurement-method/plan.md).
These are independently testable local instrumentation results, not live measurements or
changes to product timeout policy. Their user-run output feeds
[Live timeout measurements](../05-timeout-measurements/plan.md).

Keep probe-command changes under `doc/runs/_live/`: the command entry in `main.go`, focused
new timing and session-helper files/tests, and `README.md`. Reuse existing safe
profile/resolver selection and logging. Construct raw `client.Config`/`client.Dial` sessions
with the same R5 pin/bootstrap rules; do not funnel measurements through the supervisor's preliminary
20 s or treat `StateConnected` as Noise timing. No new public core API is assumed:
`Session.Stats().BytesReceived` already exposes receive progress.

## Approved core prerequisite

The user authorized a separate [context-aware initial DoT task](03-dot-context/plan.md)
after silent-loopback checks exposed a pre-existing core cancellation defect. It is the
sole exception to this stage's previous no-core/vendor-change restriction: the command
children and their criteria remain tool-only. The prerequisite restores the existing
`client.Dial(ctx, cfg)` contract through initial bootstrap/TCP/TLS and the shared uTLS
handshake, without changing successful-session lifetime or subsequent redial/close
behavior. No new public API or dependency is approved or assumed necessary.

The instrumentation goals and Done when below are unchanged. They do not permit weaker
observation limits, abandonment of an eventual `Dial` result, new product timeout policy
or substitute live evidence. The prerequisite is reviewed and accepted independently,
then the preserved fresh-dial implementation is resumed under its original criteria.

## Children

1. [Fresh Noise dial command](01-fresh-dial/plan.md) — an executable fresh-session series,
   including raw configuration, explicit observation limits, complete sample records,
   closure and synthetic regressions. Task; independently usable without exit credentials.
2. [Healthy-idle observation command](02-idle-observation/plan.md) — one-session receive
   tracing and conservative gap estimation, with independent sequential SOCKS5/target
   checks outside the window and synthetic regressions. Task; depends on the first child.
3. [Context-aware initial DoT setup and uTLS handshakes](03-dot-context/plan.md) — the
   separately authorized core/vendor prerequisite, including maintained regressions.
   Task; must be accepted before fresh-dial can finish.

The next free number preserves the active command's path. Execution order is
`03-dot-context` → `01-fresh-dial` → `02-idle-observation`, enforced by sibling dependencies;
the core task does not depend on acceptance of the tool it unblocks.

Each command child includes its command integration, focused tests and README instructions
as one usable instrumentation result. The second reuses the first's raw-session setup and
lifecycle; neither child redefines the accepted method or fills the private evidence
form with results. The later one-write probe can reuse the same raw-session helper.

## Done when

- The local command records every fresh-session dial duration and outcome, including
  failures and observation limits, without silently retrying, discarding slow samples or
  reusing a session as another handshake sample. Every session is closed before the next.
- The idle observer produces a receive-progress trace and conservative gap bounds over
  the requested window, with actual polling times, leading/trailing gaps and end reason.
  It snapshots the post-setup counter, checks usability outside the window and sends no
  application traffic during it. Sampling delay and censored or incomplete runs are
  explicit; no false healthy-idle maximum is reported after a failure or hidden redial.
- Explicit run-only dial/keepalive observation limits permit measurement past the current
  product defaults and are recorded. They introduce no profile field and change no
  product default, core source, vendoring rule or resolver-health behavior. HTTP response
  timing is not used as SOCKS5, Noise or idle evidence.
- Synthetic trace tests cover receive changes, repeated samples, uneven polling, boundary
  gaps, cancellation and session end. Generated loopback sessions cover lifecycle and
  closure; tests never wait several real minutes to test the estimator.
- The README adds usable commands and private evidence instructions. Existing comparison,
  secret handling, both exit protocols and selftest remain accepted. The
  [parent's local validation matrix](../plan.md#local-validation-for-code-children),
  explicit tool checks, ignore/exclusion and privacy checks pass.

## Risks and what is needed from the user

No actual profile, store, metadata, live service or resolver is needed for implementation.
Receive-counter sampling observes arrival intervals rather than exact packet timestamps;
use conservative bounds and record the uncertainty. User-run several-minute evidence
and the timeout/recovery choice remain separate gates.

## Outcome

All children and whole-stage integration acceptance are complete: the context prerequisite
is recorded by `c11a40c`, fresh dial by `59064d3` and idle observation by `4c21334`.
Independent review covered `643e9e4..4c21334`, all eighteen changed tracked files and the
complete twenty-six-file ignored tool, including shared helpers and existing consumers.
It found no new integration defect or regression and required no further implementation
change. All five stage criteria and the original child criteria are accepted.

- `timeout-dial` records one selected resolver's complete fresh-session series, every
  attempted outcome, precise elapsed duration and explicit run-only limits. Session
  Close and Done complete before the next sample; no exit, target or secret lookup is
  needed. `timeout-idle` uses one original session, actual receive observations and
  conservative progress/connecting/boundary intervals, with independent authenticated
  SOCKS5 and complete expected-body checks outside the window. Incomplete, canceled or
  censored runs cannot become healthy maxima. Evidence and ownership failures stay explicit.
- The authorized core repair restores initial DoT caller-context and shared uTLS
  handshake cancellation while preserving successful-session independence, subsequent
  redial/close behavior, TLS verification, fingerprints and the public API. It is
  maintained through vendor rules/templates and generated mirrors, not manual core edits.
- The accepted method, executable local README commands and private unfilled evidence
  form agree. Shared configuration, cache/pins/SNI, cleanup, record bounds and privacy
  safeguards remain consistent. Existing comparison, secret handling, both exits and
  selftest are preserved. Child Outcomes retain the fixed review findings and regressions.
- The integration reviewer freshly ran focused and complete tool/core/vendor races and
  the full applicable project race suite. All twenty mandatory isolated D-Bus/keyring
  cases passed without skips. Static Linux builds, explicit tool vet, exact Linux/Windows
  CI vet split, Windows product/tool builds and all nineteen project test-package plus
  tool compilations passed. Formatting, whitespace, links, English, privacy, ignore and
  package-list exclusion passed. No earlier child matrix was merely relabeled as new.
- Built-command generated-loopback checks passed fresh-session success, standard/Firefox
  DoT deadline and SIGINT, idle success and SIGINT, complete records and joined cleanup.
  Fresh both-exit selftest passed HTTP/HTTPS server-up and its existing HTTPS-only down
  phase, including redaction. This does not establish an additional HTTP-down loop.
- Verified clean upstream revision and Git integrity, mandatory rule counts, socket guard,
  twelve mirror pairs, normal vendor check and two identical generations in a temporary
  complete snapshot passed. The generated core matched all thirty-four current files;
  recorded counts remain 33 files, 41 rules and 12 additions with unchanged upstream.
- Project races excluded exactly `TestDualStackListener`, `TestLoopbackIndexBlocks`,
  `TestPhysicalIndexPasses` and `TestFamilyWithoutRouteRefuses`. Only the three opt-in
  netns tests were skipped; `TestDNSGuardPermitsPrograms` was compiled without execution.
  No actual configuration/store access, live service/resolver, download, network/trust
  mutation or Windows runtime/DPAPI result belongs to this local acceptance.

No integration finding was accepted or rejected, and no real problem was left partly
fixed because of cost. The pre-existing CLI signal-reader and source-confirmed socket
tracking/forget lock-recursion observations remain unchanged separate follow-ups; the
latter has not been runtime-reproduced. All tool files and private evidence stay ignored.
Actual authenticated service availability, multi-minute transport measurements, the
one-write experiment and user-selected timeout/recovery policy remain later gates.
