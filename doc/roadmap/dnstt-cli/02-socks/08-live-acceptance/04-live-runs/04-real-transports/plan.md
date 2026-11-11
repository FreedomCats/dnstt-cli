---
kind: task
status: todo
depends: [01-runbook, 03-real-compare]
---

# Real-server transports

## Task

A headless browser through the local SOCKS5 listener and the user's real HTTP CONNECT exit
with Basic authentication, once with only a DoH resolver, once with only DoT, once with
only UDP. The browser and the product share a disposable client container; the product
listens on `127.0.0.1:<local-port>`; Chromium starts with
`--proxy-server="socks5://127.0.0.1:<local-port>"`, and the target `<target>` (default
`https://example.com/`) is resolved at the exit. Each transport is a temporary profile
built from the values supplied in chat and stored in the ignored profile; the user's real
profile and store are not read.

The first real application CONNECT performs Basic authentication; `Connected` before it is
tunnel readiness only. With a wrong password the exit answers 407, the supervisor stops
and does not retry it as a resolver failure; this is recorded as the final exit-login
result and is not read as a resolver problem. A transport that cannot be tested (no
working resolver supplied, the container cannot reach it) is recorded as
`not seen — <reason>`, not silently skipped. The run records into the
[runbook](../01-runbook/plan.md) and follows the [stage design](../plan.md#run-design).

## Done when

- Over each of DoH, DoT and UDP, a headless Chromium through the local proxy loaded the
  target page through the HTTP exit with Basic authentication; per phase the DOM and
  title, the netlog status, the wall time, streams and bytes are recorded.
- The session evidence shows that `Connected` before the first real application CONNECT is
  readiness only and that the CONNECT authenticates; a wrong password produced 407,
  stopped the supervisor and was not retried as a resolver failure.
- A failing transport is attributed to the resolver, the network, the exit or the product
  with a control, not called a product defect without it; a product defect becomes a child
  of the stage with a test.
- The clean stop and the host check pass; the temporary profile and store are deleted; no
  domain, key, address, resolver, login or date is in a tracked file.
