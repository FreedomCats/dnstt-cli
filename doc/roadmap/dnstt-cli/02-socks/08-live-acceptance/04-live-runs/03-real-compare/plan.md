---
kind: task
status: todo
depends: [01-runbook]
---

# Authenticated comparison

## Task

The attribution gate on the user's real server, with the HTTP CONNECT exit and Basic
authentication. The ignored `doc/runs/_live/` tool's `compare` runs the upstream
`dnstt-client` through the tunnel with its independent HTTP CONNECT probe, and the
in-process product through its local SOCKS5 listener; over one working DoH resolver and
one working UDP resolver, every attempt on both sides must reach the target through the
authenticated exit. The result is what later browser failures are attributed against, and
it is not replaced by the browser runs.

`Connected` before the first real application CONNECT is tunnel readiness only; the
product verdict must record a successful first real application CONNECT, not readiness
alone. A 407 or another refusal fails the check and is not counted as a pass. An upstream
listener-ready or Noise-only result, a local selftest, or an attributed upstream failure
does not close this gate.

The user supplies the domain, public key, HTTP login and password, and the DoH and UDP
resolver entries in chat when the work starts; they live only in an ignored temporary
profile and file-backed store under `doc/runs/live-socks/`, mounted read-only into a
disposable client container, and are deleted after the real-server runs. The user's real
profile and secret store are not read. The upstream client is built from the sibling
checkout with cached dependencies (or a ready binary is used); the target is `<target>`
(default `https://example.com/`), resolved at the exit. The run records into the
[runbook](../01-runbook/plan.md) and follows the [stage design](../plan.md#run-design).

## Done when

- `compare` ran in a disposable container with the temporary profile, over a working DoH
  and a working UDP resolver; every attempt on the upstream side and on the in-process
  product side passed and fetched the target through the authenticated HTTP exit.
- The product verdict records a successful first real application CONNECT with Basic
  authentication, not `Connected` or readiness alone; a refusal or a 407 would be a
  failure, not a pass.
- A failure, if any, is attributed to the server, the exit, the resolver, the network or
  the product with the tool's output and a control; the side is named; the first failure
  is kept, not rerun until it passes.
- The temporary profile and store are deleted; the user's real profile and store were not
  read; no domain, key, address, resolver, login or date is in a tracked file; the clean
  stop and the host check pass.
