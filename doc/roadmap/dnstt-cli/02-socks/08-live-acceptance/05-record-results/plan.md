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
  §08 — the handshake timeout; R3 — the answer about `danted` and the setting if there is
  one; §13 — the row "A dead session is noticed late".
- The parent stage: its "Open points" lose the two timeout items, and the item about
  uTLS fingerprints left by S1 is closed or restated.
- This stage's `Outcome`: what was seen live, per item of [Live runs](../04-live-runs/plan.md),
  on each platform; what failed and on which side; what was left open and why.
- Code comments that still call a number preliminary or point at "the live acceptance
  task".

Detailed when its turn comes.

## Done when

- The documents above say what was measured and seen; no "preliminary" or "to be measured"
  is left about the keepalive, the handshake timeout or `danted`.
- No domain, key, address, resolver, login, link speed, date or detail of the environment
  is in the changed files; the course of the runs is in `doc/local/`.
