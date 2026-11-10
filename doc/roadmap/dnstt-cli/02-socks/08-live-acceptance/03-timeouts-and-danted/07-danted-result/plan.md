---
kind: stage
status: done
base: 938b7fc
depends: [08-synthetic-danted]
---

# Resolve the measured danted result

Resolve the branch established by the
[synthetic danted one-write check](../08-synthetic-danted/plan.md), preserving the parent
stage's complete criterion. The user's server has no `danted` (its exit is an HTTP proxy), so
the evidence is environmental and scoped: it speaks about the measured version in the
measured conditions, not about the user's exit. A supported one-write exchange becomes an
enduring exit setting with validation and tests after the user approves its contract; the
approval is recorded in `## Accepted contract` below. A measured rejection is recorded in
`internal/exit/socks5.go` and architecture R3 as an environment-scoped negative result,
without adding an unused key. An inconclusive series, an unqualified environment or an
unavailable service is neither branch and keeps this node open.

This is a stage because the positive branch includes an enduring user format/behavior
decision and subsequent product implementation that can be accepted separately. The
evidence exists and the user accepted the contract, so the branch is split into the children
below; the key name, type and default are no longer open. The sequential path remains the
default (`one_write = false`) and still offers authentication "none" with a login, so it
does not send the password unless the exit asks for it; the accepted contract keeps that for
the default and pays the early password only in the one-write mode.

Expected positive-branch product scope: `internal/exit/socks5.go`, its protocol/fixture and
tunnel tests, `internal/config/profiles.go` and round-trip/validation tests, and the binding
in `internal/app/tunnel.go` with its tests. The children divide that scope by result: the
exchange mechanism first, then the key together with its sole consumer, then the
documentation. The key, default and limits are documented with reserved examples in the
existing example/architecture documentation. A profile field and its sole consumer are one
atomic product result, not independently usable file-only tasks. The experimental ignored
probe remains an independent control rather than product code.

## Children

1. [One-write SOCKS5 exchange](01-one-write-exchange/plan.md) — the exit sends the greeting,
   the RFC 1929 login and the `CONNECT` in one Write and reads the three replies in order,
   with the sequential path unchanged and the contract's failure behavior. Task;
   `internal/exit/socks5.go`, its fixtures and the generated loopback tunnel test.
2. [Profile key and binding](02-profile-key-and-binding/plan.md) — `exit.one_write` in
   `internal/config/profiles.go` with its validation, and its sole consumer, the SOCKS5
   dialer built by `internal/app/tunnel.go`, while the startup `Check` stays sequential.
   Task; the field and its consumer are one product result. Depends on child 1.
3. [Documentation and handoff](03-documentation-and-handoff/plan.md) — R3, §10 and the
   reserved example carry the setting, its default and the measured scope, with the handoff
   for [Recording the results](../../05-record-results/plan.md). Task; documentation only.
   Depends on child 2.

The mechanism comes first: the key must not exist before there is something to turn on. The
key and its binding follow together, because a field with no consumer and a consumer with no
field are each unusable. The documentation comes last so that it describes the shipped
behavior and the error the user sees. The positive branch is the evidence-supported one, so
the children implement it; the rejection criteria stay in `## Done when` untouched, for the
branch the evidence does not support, and no child takes that branch.

## Accepted contract

The user accepted the positive branch on the synthetic evidence with this enduring contract:

- a boolean `one_write` in the exit section of the profile (`[profile.<name>.exit]
  one_write = true`), default `false`;
- it applies to `exit.type = "socks5"` only; an empty type counts as socks5, and the key
  with `http` or `ssh` is a validation error;
- with `one_write = true` the greeting, the RFC 1929 login and the `CONNECT` request go in
  one Write: with a login the greeting offers only method `0x02`, so the password is sent
  before the method is confirmed — the documented price of the saved round trips; without a
  login the greeting offers only `0x00` and the `CONNECT` follows;
- the startup `Check` stays sequential: it has no target and must work against any server;
- an exit that chooses another method, refuses the login or does not take the batch yields a
  clear error that names the key, with no automatic fallback and no second stream; the user
  turns the key off;
- R3 and the reserved example carry the measurement (`danted` 1.4.4) and its limits.

Rejected: default `true` (a server that reads one message per `recv` would hang); no key at
all (the measured result would stay unused); a second flag for early credentials.

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

## Outcome

The positive branch of the measured one-write result is implemented, tested and documented
over `938b7fc..fa7d041`, and the user's enduring contract is what shipped:

- `exit.one_write` is a bool in the exit section, absent or `false` by default, accepted for
  `socks5` and for an empty type, rejected with `http` or `ssh`, and a misspelt key stays an
  unknown exit key. The datapath SOCKS5 dialer is its only consumer; the startup readiness
  check is not affected.
- With it set, `Dial` sends the greeting, the RFC 1929 login and the `CONNECT` in one Write
  and reads the three replies in order; with a login the greeting offers only `0x02`, so the
  password goes before the method is confirmed; without one it offers only `0x00`. The
  sequential path keeps its bytes, messages and errors, and `Check` stays sequential.
- Failures keep `*LoginError`, `*ConnectError` and `ErrNotSOCKS5` identity; a batch the exit
  does not take — including a silent exit at the deadline or a cancellation — names
  `exit.one_write` and says there is no fallback, with no second stream and no sequential
  retry. The acceptance review found and fixed the deadline case, which had lost the marker.
- R3 states the setting, the early-credential price, the failure behavior and the measured
  `danted` 1.4.4 source build in a disposable synthetic environment, with the limits: not
  the user's HTTP exit, other versions or deployments, TCP segmentation, or the server's
  `recv` calls. §08 says the readiness check stays sequential; §10 and
  `dnstt-cli.example.toml` carry the reserved key.

Children: `d4b63c3` (exchange), `4bcc30f` (key and binding), `1618e02` (documentation), plus
the acceptance fixes `21786e4` (keep the marker on timeout and cancellation) and `fa7d041`
(R3 states the failure and the scope). The whole range passed the stage's local matrix: the
full race suite with isolated D-Bus and exactly the four external tests excluded, the CI vet
split on Linux and Windows, a static build, the Windows cross-build and test compilation,
`gofmt`, `git diff --check`, and the focused exit, app, supervisor and config packages,
including a hundred-run repeat of the new silent-exit test. No dependency, live service,
real profile or real value was involved. The tool race suite was not rerun because the
ignored tooling was untouched in the range.

Open and not part of this branch: the live SOCKS5 matrix and the user's HTTP exit stay with
[Live runs](../../04-live-runs/plan.md), and the timeout values with
[Live timeout measurements](../05-timeout-measurements/plan.md); no live gate is closed by
this stage.
