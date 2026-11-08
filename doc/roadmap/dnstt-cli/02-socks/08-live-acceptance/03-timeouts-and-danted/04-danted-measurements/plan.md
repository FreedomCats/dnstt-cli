---
kind: task
status: todo
depends: [03-danted-probe]
---

# Live one-write test against danted

## Task

The user answers the SOCKS5 one-write question against their actual `danted` with a login,
using the accepted [experimental probe](../03-danted-probe/plan.md) and
[method](../01-measurement-method/plan.md). Keep the paired raw results private and
produce an attributable support/rejection result for
[Resolve the measured danted result](../07-danted-result/plan.md). No product setting or
format is implemented in this user-evidence task.

Use the local README and the `danted` section of `doc/local/plan-timeouts-and-danted.md`.
The user confirms service availability and performs the real runs; no agent reads/stats
actual profiles/stores or their metadata, calls a live resolver/service, or changes the
server. Any service setup or server reconfiguration needs the user's separate decision.

## Done when

- A login-protected SOCKS5 `danted` behind the tunnel is confirmed by the user and a real
  authenticated sequential CONNECT with target data succeeds. The private record retains
  enough service/test conditions to qualify the result without publishing server metadata.
- Paired fresh-stream sequential/one-write trials use the same authenticated exit, target
  and selected tunnel/resolver. The record shows that the treatment sent the whole batch
  before reading, the method/login/CONNECT status and complete-reply validation, verified
  target-data outcome and separate control/treatment durations, including all failures and
  observation limits. No credential or raw protocol/payload dump is required.
- The answer is either repeated successful one-write exchange with target data or a
  repeatable failure attributable to one-write handling while sequential controls pass.
  A missing SOCKS5 service, wrong credentials, target refusal, HTTP success, tunnel failure
  or a local fixture is not an answer; inconclusive runs keep this task open and trigger
  a prerequisite/diagnosis question instead of a negative `danted` claim.
- The handoff states the observed compatibility and latency benefit/limits, without a
  claim about TCP segmentation or the server's unobserved `recv` calls. A positive result
  requires the next stage's user-approved key/default and behavior contract; it does not
  itself enable a product option.
- Evidence/captures stay ignored. The published result has no actual server/key/resolver,
  credentials, profile name, work date or environment. Link, consistency and privacy
  checks pass. The existing broader SOCKS5 live matrix is still required separately.

## Risks and what is needed from the user

Actual `danted` availability is unknown. The user needs a working authenticated SOCKS5
exit, a target it permits and a resolver carrying that tunnel. If these cannot be supplied,
report the unmet gate and ask about the prerequisite or stage scope; do not replace it
with the selected HTTP service or interpret absence as one-write rejection.
