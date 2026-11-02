---
kind: task
status: done
detailed: true
---

# Close the stage

## Task

Record what the foundation established and make the next stage readable from the
repository alone. `E0`, `F6`.

## Steps

1. `architecture.md` §12: the Go version, the dependency versions, anything the core turned
   out to need.
2. The stage's result, deviations and open points.
3. The next stage (SOCKS5 mode) with its tasks in the order they will be taken.

## Done when

The documents are written and committed, and the next session can start the SOCKS5 stage
from them alone.

## Outcome

The result is in the parent stage's `## Outcome`, the open points in the root. The
architecture carries the deviations of the foundation and the versions the core settled on
(`flynn/noise` 1.0.0, `utls` 1.6.6 at the time, `kcp-go` 5.6.8, `smux` 1.5.24; uTLS was
raised to `v1.7.3` in stage 2, patch P5).
