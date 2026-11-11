---
kind: task
status: todo
depends: [04-live-runs]
---

# Recording the results

## Task

The results of the live acceptance written where they are published, without anything
that identifies the user, and the preliminary notes removed. `E1`, `S8`; `AGENTS.md`,
"Documents in `doc/`".

- `architecture.md`: R2 — the keepalive timeout and the rule it was derived by; §04 and
  §08 — the handshake timeout and the selected HTTP semantics: `Connected` before the first
  real application CONNECT means tunnel readiness only, that CONNECT performs Basic
  authentication, and HTTP 407 stops the supervisor; R3 — the answer about `danted` and the
  setting if there is one. Confirm that R3 and §10 describe the HTTP CONNECT/Basic exit added
  by the product task; §13 — the row "A dead session is noticed late".
- The parent stage: its "Open points" lose the two timeout items, and the item about
  uTLS fingerprints left by S1 is closed or restated.
- This stage's `Outcome`: what was seen live, per item of [Live runs](../04-live-runs/plan.md),
  separately for the synthetic SOCKS5 setup and the real-server HTTP exit; the platform
  that ran, with the deferred Windows part stated as not seen; what failed and on which
  side; what was left open and why. Distinguish the upstream tunnel-plus-independent-helper
  result from the in-process product result. The synthetic SOCKS5 result does not stand
  for the user's server, exit or network, and HTTP success does not replace the SOCKS5
  acceptance.
- Code comments that still call a number preliminary or point at "the live acceptance
  task".

Detailed when its turn comes.

## Done when

- The documents above say what was measured and seen; no "preliminary" or "to be measured"
  is left about the keepalive, the handshake timeout or `danted`.
- No domain, key, address, resolver, login, link speed, date or detail of the environment
  is in the changed files; the course of the runs is in `doc/local/`.
