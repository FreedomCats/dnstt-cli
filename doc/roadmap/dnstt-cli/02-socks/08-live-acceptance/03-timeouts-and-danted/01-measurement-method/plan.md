---
kind: task
status: done
detailed: true
---

# Measurement method and evidence checklist

## Task

Write the method and unfilled evidence checklist for this stage before implementing its
probes. The result is documentation the lead can accept independently: it defines what is
measured, which observations qualify, what remains a user gate and how private evidence
reaches the later decisions. Do not execute the checklist or invent measured values.

Use the [parent stage](../plan.md), [architecture](../../../../../../architecture.md)
R2, R3, §§04 and 08, and the accepted
[SOCKS5 tooling baseline](../../02-live-run-script/01-socks5-baseline/plan.md).
The existing local README and tool remain the starting point. No product profile key,
timeout default, authentication policy or replacement live-acceptance scope is chosen here.

## Steps

1. Read `doc/runs/_live/README.md` and the relevant entry points in `main.go`, `compare.go`,
   `cli.go` and `probe.go` under that directory. Reconcile the method with the actual
   measurement sites: `internal/supervisor/tunnel.go:489` times `client.Dial` separately
   from the subsequent exit check, `internal/cli/resolvers.go:132` has its own 20 s,
   `internal/app/tunnel.go:95` leaves keepalive unset, and `core/client/session.go:248`
   exposes the receive counter. Use source inspection only; opening real configuration,
   stores or their metadata is not part of this task.
2. Add a stage-specific method section to the ignored `doc/runs/_live/README.md` and an
   unfilled evidence form in ignored `doc/local/plan-timeouts-and-danted.md`, both in
   English with reserved examples only. State that the two probe implementations are
   forthcoming; their tasks add the actual executable command syntax. Reuse existing
   profile/secret setup and independent `compare` instructions by reference. Do not
   rewrite their accepted behavior, duplicate the tool or write a new script.
3. Define the timeout experiment and its evidence fields. For each selected DoH, DoT and
   UDP resolver, record a series of fresh `client.Dial` attempts from invocation through
   Noise completion, including bootstrap/transport setup, every failure and its observation
   limit. Separate that timer from SOCKS5 readiness and target-response timing. Record
   sample counts and the uTLS/bootstrap conditions; do not count repeated application
   requests in one session as repeated handshakes. Specify a several-minute idle window
   with no application probes during it, a receive-counter baseline after setup, actual
   observation times, the initial/final boundary gaps and session end reason. Check usable
   session/exit operation before and after, outside the idle window, and record any end or
   replacement of the measured session instead of hiding it by reconnecting. Explain how
   interval bounds and actual polling delays give a conservative silence estimate rather
   than an exact packet-arrival timestamp. Do not treat RTT or HTTP latency as server silence.
4. Define how the forthcoming local timeout probe can observe beyond the preliminary
   product deadlines without changing them: explicit run-only dial/keepalive observation
   limits, recorded with the samples, and an incomplete/censored result when a limit or
   session end prevents observation. A short measurement timeout cannot establish that
   a slower resolver is dead. Explain the separate margins for the slowest intended
   working resolver, the smux timeout-check period (architecture §04), and the degraded
   threshold's polling period. List the candidate values, their derivation rules and their
   stability/recovery consequences as fields to fill after measurement, for the user's
   decision in [Live timeout measurements](../05-timeout-measurements/plan.md).
5. Define the `danted` experiment separately. Require user confirmation that the actual
   SOCKS5 exit with a login is available, then a successful authenticated sequential
   control and paired fresh-stream trials to the same explicit target through the same
   tunnel/resolver. The experimental treatment sends greeting, RFC 1929 login and CONNECT
   in one stream write before reading replies; record each reply's status/validation,
   full CONNECT success, verified target-data outcome and durations separately from the
   control, without a raw protocol or payload dump. Document that stream/TCP
   segmentation is not a guaranteed server `recv` boundary. Record compatibility evidence,
   not a claim about unobserved server reads. Distinguish repeatable one-write failure
   with working controls from unavailable service, refused credentials/target, malformed
   replies or tunnel failure. No setup failure, HTTP result or loopback fixture answers
   the live question. Describe early credential transmission as part of this explicit
   user-run experiment; product authentication behavior remains a later approval gate.
6. Finish the safe run/checkpoint instructions: the user alone runs real profiles and
   services; no credential arguments, environment dump, raw protocol payload or profile
   dump is requested. Raw samples and outputs stay under `doc/local/` or `doc/runs/`;
   give lead a sanitized verdict/derivation handoff format. Name the prerequisite, timeout
   tradeoff and enduring key/default decisions that stop dependent work. Check links,
   consistency with parent criteria, English and privacy; ensure both new/changed local
   documents are ignored. Do not edit `doc/local/journal.md` or any completed roadmap node.

## Done when

- The local README and unfilled evidence form define all three transport rows, fresh
  handshake samples including failures/limits, the several-minute idle observation and
  its uncertainty/boundary fields, and paired sequential/one-write SOCKS5 evidence.
- Every timer has a stated start/end boundary. Noise completion, SOCKS5 authentication,
  CONNECT success and target data are distinct; no current HTTP probe timing is relabeled
  as a handshake, idle or `danted` measurement.
- Missing SOCKS5/danted availability and incomplete/censored measurements keep their live
  gates open. Neither 30 s nor any new exit key/default is presented as approved.
- The user-run instructions and decision handoff can be followed without giving an agent
  access to actual configuration or secrets. Real values/captures are requested only in
  the ignored private records; published results have an explicit sanitization boundary.
- Relative links resolve and `git diff --check` passes for the roadmap change. Both local
  documents are verified ignored; the tracked diff contains only authorized roadmap/docs.
  No code/test/build change or execution is needed for this documentation-only task.

## Risks and what is needed from the user

No live access is needed to write the method. Actual resolver coverage and authenticated
`danted` availability are unknown until the user-run children. Local probe implementation
may refine sampling resolution and command syntax; it must preserve these measurement
boundaries and must not silently turn an unresolved user decision into a default.

## Outcome

The ignored tool README now defines the stage-specific method, and the ignored local
evidence form is prepared with unfilled transport, sample, idle-window, paired-experiment
and user-decision fields. No live command, product change or configuration access was
needed for this documentation result.

- Fresh Noise dial, SOCKS5 authentication, complete CONNECT and verified target data have
  separate boundaries. All dial failures and observation limits remain in the series.
  Initial sample/window guidance is explicit without presenting it as measured evidence.
- The idle method records actual receive-counter observation times, interval uncertainty,
  leading/trailing boundaries, session termination and separate pre/post usability checks.
  It prohibits application probes during the window and hidden reconnection. Run-only
  observation limits do not select product defaults; incomplete or censored results stay open.
- Paired authenticated sequential/one-write trials require the actual service and verified
  target data. The method distinguishes compatibility from an unobserved server read
  boundary and experimental early credentials from later product authentication policy.
- The handoff retains raw evidence privately and identifies service availability, timeout
  stability/recovery and enduring key/default/behavior decisions that need the user.
  No new setting, numeric policy or replacement for the broader live matrix was selected.
- Markdown consistency, English, whitespace and 39 relative links/anchors across the
  current stage and local method documents passed. Both local documents are confirmed
  ignored; no source, test, build or configuration file changed. Measurement command syntax
  remains explicitly forthcoming in the probe tasks. No code checks or live runs were run.

The later probes may refine sampling resolution while preserving these boundaries.
No real problem was left incompletely fixed because of cost.
