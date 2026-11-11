---
kind: task
status: dropped
depends: [01-diagnose]
---

# Fix the TUN datapath

## Task

Historical scope, not performed — see Dropped below.

Fix, in one reviewable change, the mechanism that [the diagnosis](../01-diagnose/plan.md) names,
so that the TUN datapath carries real traffic at the same order of magnitude as the SOCKS5
datapath on the same tunnel. The parent stage holds the measured numbers, the contrast with
SOCKS5 and the code facts; the diagnosis holds the cause and the number that proves it. Read
its Outcome first: the change must address that mechanism and must not touch the boundaries
the stage fixed — the tunnel's payload size or MTU, the resolver, the adapter's MTU and `/1`
routes, the sequential SOCKS5 handshake and `exit.one_write`, the gVisor version and the
dependency set — without the user's decision.

One change: the files the diagnosis names, plus the regression test that pins the mechanism;
no unrelated cleanup. The steps are written by the planner after the diagnosis, when the
cause is known.

## Done when

- The change addresses the cause named in `01-diagnose` and nothing else; the report says
  which files and why.
- In a quiet container, same tunnel and same body, 5 sequential and 4 parallel page fetches
  through the TUN finish in the same order of magnitude as over SOCKS5, no fetch hits the
  25/30 s client timeout, and the browser-equivalent fan-out (≥30 connections to one host plus
  A/AAAA lookups) completes instead of stalling.
- A datapath-level regression test reproduces the cause's shape on the code before the change
  and passes after it; `go test -race ./internal/datapath ./internal/app ./internal/exit
  ./internal/supervisor`, `go vet ./...` and the CI netns job pass.
- The SOCKS5 datapath, the kill-switch (`-no-kill-switch`, `kill_switch = false`,
  `-allow-leak`) and the timeout policy show no regression: their tests pass and their live
  lines behave as before.
- Every live check ran in an isolated container with its own network namespace — image not
  pulled, `NET_ADMIN`, `/dev/net/tun`; the host's routes and system DNS were not touched, and
  no private value from the run is in the repository or in this node.

## Dropped

There is no cause to fix. [The diagnosis](../01-diagnose/plan.md) measured the local path on
a lossless stand and the real tunnel in a quiet container A/B: the TUN datapath is the same
as the SOCKS5 datapath on the same tunnel within noise, with zero local losses and one exit
stream per fetch. The seconds and timeouts this stage was opened for came from a session
holding about 160 open streams, peaking at 324 during the browser run, with a neighbouring
tunnel client on the same machine — not from the datapath.

The only measured ceiling is the tunnel's shared channel, about 58 KB/s, shared by both
datapaths. Changing it would touch the payload size or MTU, the resolver or the KCP settings
— boundaries the stage fixed and the user has not opened. The node is dropped after the
outcome of [the diagnosis](../01-diagnose/plan.md) rather than repurposed: its criteria name
a datapath mechanism, and no mechanism was found.

What is preserved: the diagnosis's stand
(`internal/datapath/tun_latency_netns_linux_test.go`) stays as the regression guard, and this
node can be revived with a real cause if a datapath mechanism is ever measured.
