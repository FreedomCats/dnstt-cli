---
kind: stage
status: done
base: ba4b001
---

# Foundation

Goal: the repository builds, the core is vendored and tested, the packages carried over
from csqtt-cli pass their tests, CI is green on Linux and Windows. No server and no access
to a live tunnel is needed. `doc/architecture.md` §12, E0.

## Done when

- `go build ./...` and `go vet ./...` pass.
- `core/` is filled by `scripts/vendorcore` from the mirror, and `-check` compares it with
  the upstream plus the rules.
- `go test ./...` passes on Linux and on Windows, `-race` too.
- Both CI workflows are green on `main`.

## Children

1. [Workspace](01-workspace/plan.md) — no commit: the clones, Go, git identity.
2. [Module and skeleton](02-module-skeleton/plan.md) — F1.
3. [Vendoring the core](03-vendoring-the-core/plan.md) — F2.
4. [Session API and socket hook](04-session-api/plan.md) — F3, core patch P1.
5. [Packages carried over](05-carried-packages/plan.md) — F4.
6. [CI](06-ci/plan.md) — F5.
7. [Close the stage](07-close-the-stage/plan.md) — F6.

## Outcome

Go 1.26.2; `core/` is the mirror at `v1.20260501.0` with P1 (15 rules, 5 own files in
`scripts/vendorcore/_files/`), checked in CI against the mirror. The session API dials the
upstream server run in-process and passes its tests under `-race`: parallel streams, one
`Control` call per socket, the end of a session when the server goes silent, no goroutines
left after `Close`, DoH and DoT connecting through `Control`. The socket guard parses
`core/` and fails on any socket outside its table. The csqtt-cli packages are carried with
new GUIDs and `SO_MARK` (0x1076) and translated into English. CI is green on `main`
(`linux`: test, netns, core; `windows`: test, core).

Deviations, each recorded in `architecture.md`: own core files live in `_files/` (a plain
`files/` was compiled by `go build ./...`); uTLS takes a dial *function* from its caller,
not a `*net.Dialer`, because `LookupIP` has to sit between the name and the socket; P1 also
ends the upstream transport loops on close (upstream never closes a session); `distcheck`
moved to the release stage.

Not covered: a full session over DoH or DoT — the in-process server speaks UDP only; that
is checked live later in stage 2.
