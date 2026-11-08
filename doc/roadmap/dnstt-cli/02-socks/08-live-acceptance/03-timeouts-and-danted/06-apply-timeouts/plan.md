---
kind: task
status: todo
depends: [05-timeout-measurements]
---

# Apply the measured timeout policy

## Task

Apply the values and derivation rules approved in
[Live timeout measurements](../05-timeout-measurements/plan.md) as one consistent product
policy. `connect` must set its sessions' keepalive timeout; supervisor dial attempts and
`resolvers check` must share the measured handshake timeout; the degraded-state threshold
must agree with the approved policy. Architecture R2 and §§04/08 define the behavior.

Expected scope: `internal/app/tunnel.go` and its tests, `internal/supervisor/tunnel.go`
and its tests, and `internal/cli/resolvers.go` and its tests, with a small shared policy
definition only if needed. Use the existing `client.Config.KeepAliveTimeout`; a core
API, vendoring change, network watcher or new timeout profile key is not required.
Do not change the separate SOCKS5 exit exchange's 60 s limit as a side effect.

## Done when

- Application-built sessions receive the approved explicit keepalive timeout on initial
  connection and redial. The supervisor's default handshake timeout and resolver checks
  use one source of the approved handshake value, with no independent preliminary 20 s.
- The degraded threshold and polling behavior are tested against the selected silence
  policy. Keep or change `DefaultDegradedAfter` according to the recorded user decision,
  not an implementation guess. Shorter caller contexts still bound operations.
- Focused tests verify config propagation across reconnects, the dial/check deadline
  equality, timeout/cancellation, silence-to-degraded transitions and recovery on data.
  Existing loopback session/supervisor tests continue to cover silent-session death and
  reconnect. Tests use injected short durations rather than wait for live-sized defaults.
- Each selected value has a sanitized derivation comment; code does not present raw
  user timings or resolver details. The later documentation task has a clear handoff for
  the final R2/§04/§08 update, including any departure from the preliminary expectation.
- The [parent's applicable local matrix](../plan.md#local-validation-for-code-children)
  passes: focused/project race with mandatory isolated D-Bus and the stated external-test
  exclusions, static build, exact CI vet split and Windows compilation. The ignored tool
  remains compatible. SOCKS5 startup authentication and the settled HTTP readiness/fatal
  407 behavior retain their regression coverage.

## Risks and what is needed from the user

Do not start implementation until the evidence and user decision are recorded. The
SOCKS5/browser/platform restart and network-change acceptance remains in
[Live runs](../../04-live-runs/plan.md) with these final values. This task does not claim
that synthetic silence tests establish those live results or Windows runtime behavior.
