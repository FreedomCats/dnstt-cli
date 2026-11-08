---
kind: task
status: todo
depends: [01-measurement-method, 02-timeout-probes]
---

# Local one-write SOCKS5 probe

## Task

Add the experimental SOCKS5 control/treatment probe to the existing ignored tool, using
the raw-session lifecycle prepared by [Local timeout probes](../02-timeout-probes/plan.md).
It supplies evidence for [the user's danted test](../04-danted-measurements/plan.md)
without needing a product exit setting first. Follow the
[measurement method](../01-measurement-method/plan.md) and architecture R3.

Keep the change under `doc/runs/_live/`: `main.go`, focused SOCKS5 probe/tests and an
appropriate synthetic fixture, plus `README.md`. Preserve `compare`, its independent
SOCKS5/HTTP helpers and its verdicts. Use an independent experimental wire exchange on raw
tunnel streams, not `internal/exit.SOCKS5.exchange` as both implementation and oracle.
Reuse safe profile/secret lookup, resolver selection and private-log filtering; reject a
non-SOCKS5 selection before fetching credentials or sending traffic.

## Done when

- With explicit run-only options and target, the tool runs authenticated sequential and
  one-write trials on fresh streams through the same selected tunnel/resolver. One-write
  emits the complete greeting/login/CONNECT batch in one stream `Write` before reading
  replies; full-write completion is checked. It does not claim a TCP packet or `recv`
  boundary and does not silently fall back to sequential on failure.
- Both paths validate method selection, RFC 1929 status and the entire CONNECT response,
  preserve first target bytes, verify target data and record separate durations/outcomes.
  Interrupted, refused, malformed and unsupported trials are distinguished from success.
  No readiness-only, listener-only or successful `Write` result counts as support.
- Independent synthetic fixtures exercise accepted batching and deterministic rejection
  or stalling while sequential controls still work. Tests cover partial I/O, coalesced
  and fragmented replies, bad credentials, cancellation/deadline and closure. Fixture
  buffering is checked explicitly so it cannot discard already-read batch bytes.
- Experiments require explicit user-run intent; authentication-method selection and early
  credential transmission are documented as experimental conditions, not adopted product
  policy. No real payload or credential is emitted in logs, args, environment or captures.
- The README supplies the actual paired-run commands, safe credential handling and result
  interpretation. Only the later user's actual `danted` run can answer compatibility.
  The [parent's local validation matrix](../plan.md#local-validation-for-code-children),
  existing two-exit selftest, tool race/vet/Windows compilation and privacy checks pass.
  Tooling remains ignored; no product/config format or dependency changes.

## Risks and what is needed from the user

Implementation uses synthetic credentials, generated identity and loopback only; it needs
no real service access. Success of a buffered local fixture does not establish `danted`
behavior. A live failure without a successful paired control remains inconclusive.
