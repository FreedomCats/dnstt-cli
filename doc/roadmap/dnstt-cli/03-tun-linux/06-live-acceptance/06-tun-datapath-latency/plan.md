---
kind: stage
status: done
base: ebdd65e
---

# TUN datapath latency and stalls

The TUN datapath is not usable for real traffic: page fetches take seconds and concurrent
fetches stall, while the same tunnel over the SOCKS5 datapath answers in under a second. The
fault is in the TUN path — the gVisor netstack, the adapter, or their wiring in
`internal/app` and `internal/datapath`; the SOCKS5 comparison on the same profile and machine
rules out the tunnel, the exit and the resolver, and the browser loads the same pages without
the tunnel. The browser/QUIC branch and the live re-run of [the matrix](../02-user-run/plan.md)
wait for this stage. Architecture: §05, §05a, §12 (E2).

Measured on the real machine through the same tunnel, exit and network:

- SOCKS5 datapath: five sequential page fetches 0.73–0.79 s each; four parallel fetches
  0.74–0.81 s each.
- TUN datapath: the TLS handshake alone takes 1.1–4.7 s; one of five fetches times out at
  25 s; a plain-HTTP fetch needs 2.8 s to the first byte; four parallel fetches all time out
  at 30 s; a headless browser loads neither the HTTPS nor the plain-HTTP page.
- Direct network to the same sites: 0.13–0.2 s; DNS through the adapter's listener:
  0.15–0.2 s per query.
- The TUN runs stall the whole machine's traffic while the kill-switch is on, which is the
  expected consequence, not the cause.

The evidence is confounded and must be re-measured. The status `streams` figure is open
streams (`core/client/session.go:70-71`); the browser-retry session held 140–300 of them and
ran at the same time as the four-parallel fetches. Raw evidence with private values is in
ignored `doc/runs/tun-matrix/host/browser-retry/`.

Read against the code, these are checked rather than assumed:

- **gVisor's TCP defaults are not the fault.** The pinned version has SACK on, Reno as the
  default congestion control (CUBIC is available as `tcp.NewProtocolCUBIC`), 1 MiB default
  send and receive buffers (a forwarded connection advertises the forwarder's 64 KiB window,
  `netstack.go:29-32`), min RTO 200 ms and no delayed ACK (`protocol.go:538-540,589-601`,
  `snd.go:33` of `gvisor.dev/gvisor@v0.0.0-20250503011706-39ed1f5ac29c`).
- **The adapter's MTU 1500 never reaches the tunnel.** TCP ends in the netstack, so the
  tunnel carries the exit stream, not 1500-byte IP packets (`datapath.go:8-10`,
  `netstack.go:279-293`). Fragmentation and MTU mismatch are not the mechanism.
- **Every connection costs two tunnel round trips before its first byte:** the SOCKS5
  greeting and the CONNECT, each with its reply (`exit/socks5.go:188-234`). The smux stream
  opens without waiting for the server (smux v1.5.24 `OpenStream`), and the forwarder
  completes the client's own handshake first (`netstack.go:238-256`), so the app's clock
  starts while the exit is still being set up.
- **Every UDP query to the adapter's listener opens its own exit stream**, with no coalescing
  and no cache, and the 65th in-flight query gets SERVFAIL at once (`dns.go:42-50`,
  `netstack.go:185-217`).
- **Nothing bounds concurrent exit dials.** `forwardInFlight` caps only pending SYN handling
  and is released at `Complete` (`netstack.go:33-37`, gVisor `forwarder.go:88-99` and `:138`);
  the dials in `serve` are unbounded.
- **The tunnel is a single ordered KCP channel over ~100-byte DNS exchanges**, so the TUN
  path's extra packets compete with the page's own data.

The working hypothesis is that the TUN path multiplies what the tunnel must carry — a stream
per DNS query, a two-round-trip setup per connection, no admission control — the channel
saturates, and KCP retransmissions turn into seconds and timeouts. It is not established: the
diagnosis child measures it before any code changes, and the fix child addresses only the
mechanism the diagnosis names.

Fixed for the whole stage: live checks only in an isolated container with its own network
namespace (`debian:trixie` is available locally; the image is not pulled; `NET_ADMIN` and
`/dev/net/tun`); the host's routes and system DNS are never touched; the real server values,
the profile and the secret stay in the container and out of the repository. Not in scope
without the user's decision: the tunnel's payload size or MTU, the resolver, the adapter's
MTU and `/1` routes, the sequential SOCKS5 handshake and `exit.one_write`, a gVisor version
change or a new dependency. If the diagnosis shows the ceiling is the tunnel itself, this
stage's goal is re-scoped with the user; the diagnosis reports that instead of forcing a fix.

## Done when

- The diagnosis names the cause: the mechanism, the file and line, and why the SOCKS5 datapath
  on the same tunnel is unaffected.
- In a quiet container, same tunnel and same body, sequential and four-parallel page fetches
  through the TUN finish in the same order of magnitude as over SOCKS5, no fetch hits the
  25/30 s client timeout, and a browser-equivalent concurrent load (≥30 connections to one
  host plus A/AAAA lookups) completes instead of stalling.
- The SOCKS5 datapath, the kill-switch and the timeout policy show no regression: their tests
  pass and their live lines behave as before.
- Every live check ran in an isolated container with its own network namespace — image not
  pulled, `NET_ADMIN`, `/dev/net/tun`; the host's routes and system DNS were not touched, and
  no private value from the runs is in the repository.
- The evidence is recorded outside git; this node carries only the substance.

## Children

1. [Diagnose the TUN datapath](01-diagnose/plan.md) — a container stand that compares the
   datapaths locally and on the live tunnel, a quiet session and the named cause with its
   evidence. Task; detailed.
2. [Fix the TUN datapath](02-fix/plan.md) — dropped: the diagnosis found no datapath
   mechanism to change; the measured cost is the tunnel's own capacity, which is a separate
   user decision (see the Outcome). Task; depends on the diagnosis.

## Outcome

The stage is closed without a defect and without a fix. [The diagnosis](01-diagnose/plan.md)
measured the local path on a lossless shaped stand and the real tunnel in a quiet container
A/B: the TUN datapath costs the same as the SOCKS5 datapath on the same tunnel. On the stand
the difference is 0.05–2.2 ms at totals of 101 ms–2.10 s, with zero retransmits, RTO
expiries, refused connection requests and TX-queue drops, and one exit stream per fetch. On
the live tunnel the columns agree within noise: one fetch 0.78–0.86 s, five sequential
0.69–0.82 s, four parallel 0.65–0.83 s, thirty concurrent 3.75–3.80 s with all thirty
answered and no timeout; the bytes per fetch and per phase are identical, and the TLS
handshake takes 0.39–0.72 s over either datapath.

The seconds and timeouts that opened the stage did not reproduce. They were measured in a
session holding about 160 open streams, peaking at 324 during the browser run, with a
neighbouring tunnel client (`qwdtt-cli`) on the same machine — a saturated, shared tunnel.
The only measured ceiling is the tunnel's own shared channel, about 58 KB/s, which both
datapaths share and neither causes.

[The fix](02-fix/plan.md) is dropped: there is no mechanism to change, and the tunnel's
capacity — payload size and MTU, the resolver, KCP settings — is a separate decision the
user has not taken. The boundaries this stage fixed stay untouched.

Left open, as separate decisions: the browser/QUIC branch of
[the live matrix](../02-user-run/plan.md), which this stage deferred and which no datapath
measurement settles (the browser's IPv6/QUIC/DoH path is the remaining unknown), and any
work on the tunnel's capacity. The only product-code change is a test-only counters seam in
the datapath (an unexported field read only by the test); behaviour and the public surface
are unchanged. The new netns test runs in the CI netns job and is skipped outside a netns.
No private value from the live runs is in the repository or in these nodes.
