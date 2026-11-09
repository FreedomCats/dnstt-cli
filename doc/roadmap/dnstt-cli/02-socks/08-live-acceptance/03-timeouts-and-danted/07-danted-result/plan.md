---
kind: stage
status: todo
depends: [08-synthetic-danted]
---

# Resolve the measured danted result

Resolve the branch established by the
[synthetic danted one-write check](../08-synthetic-danted/plan.md), preserving the parent
stage's complete criterion. The user's server has no `danted` (its exit is an HTTP proxy), so
the evidence is environmental and scoped: it speaks about the measured version in the
measured conditions, not about the user's exit. A supported one-write exchange becomes an
enduring exit setting with validation and tests after the user approves its contract. A
measured rejection is recorded in `internal/exit/socks5.go` and architecture R3 as an
environment-scoped negative result, without adding an unused key. An inconclusive series, an
unqualified environment or an unavailable service is neither branch and keeps this node open.

This is a stage because the positive branch includes an enduring user format/behavior
decision and subsequent product implementation that can be accepted separately. Split it
when the evidence exists. Do not preselect a key name, type or default, or write code behind
a temporary format. The user contract must address authentication methods and early
credential transmission, behavior for no-login profiles and incompatible servers, and any
fallback/retry policy. The current sequential path offers no authentication too and can
avoid sending the password; preserve it until an explicit decision authorizes a change.

Expected positive-branch product scope: `internal/exit/socks5.go`, its protocol/fixture and
tunnel tests, `internal/config/profiles.go` and round-trip/validation tests, and the binding
in `internal/app/tunnel.go` with its tests. The key, default and limits are documented with
reserved examples in the existing example/architecture documentation. A profile field and
its sole consumer are one atomic product result, not independently usable file-only tasks.
The experimental ignored probe remains an independent control rather than product code.

## Done when

- A supported result has the user's explicit enduring key/default and behavior decision
  recorded before implementation. Profile load/save/unknown-key and semantic validation
  enforce that contract, including applicability to exit types and credential conditions.
  Application construction passes it to the SOCKS5 dialer; no HTTP-specific setting or
  change to the settled HTTP behavior is introduced.
- For that branch, tests cover sequential and one-write operation, authenticated and
  applicable no-login cases, full/partial I/O, fragmented/coalesced responses, refusal and
  malformed replies, cancellation/closure and preserved first target bytes. Synthetic
  fixtures prove the selected mode and error types; generated loopback tunnel integration
  reaches the target. SOCKS5 readiness remains a login-only check with no hidden CONNECT
  target, and its fatal-login behavior retains coverage.
- The positive product change passes the
  [parent's local validation matrix](../plan.md#local-validation-for-code-children) and
  documents the approved setting in the example and R3/§10 as needed. Its handoff identifies
  the approved exchange modes for the later product acceptance runs, using the independent
  tool for attribution. The accepted paired probe run in the synthetic environment supplies
  the compatibility evidence, and the published result states that scope; the synthetic
  result does not authorize the setting by itself, because the user's decision above comes
  first and because it says nothing about the user's HTTP exit. The product/platform/live
  matrix stays with [Live runs](../../04-live-runs/plan.md).
- For a measured rejection, the comment in `internal/exit/socks5.go` and architecture R3
  record the negative compatibility result and its measured scope accurately: the `danted`
  version, the synthetic environment and conditions, and the absence of a real authenticated
  SOCKS5 exit on the user's server. Sequential operation remains; no profile key is added.
  Applicable exit regressions/build/vet and documentation checks pass, and the negative
  statement is supported by paired synthetic controls with a recorded version and conditions
  rather than by a fixture rejection, an unavailable service or a single failed attempt. It
  claims nothing about the user's HTTP exit or about other `danted` deployments, and it does
  not claim to know the server's `recv` boundaries.
- Only the evidence-supported branch is required, but every criterion of that branch is
  met. Published results and documentation are English and sanitized; raw evidence and
  the user's private server conditions remain outside Git. The later
  [results-recording task](../../05-record-results/plan.md) has a consistent R3 handoff.
