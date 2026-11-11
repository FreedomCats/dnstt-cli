---
kind: task
status: done
detailed: true
---

# Diagnose the TUN datapath

## Task

Establish, with evidence, why the TUN datapath is seconds slow and stalls under concurrency
while the same tunnel over SOCKS5 is fast, so that [the fix](../02-fix/plan.md) changes the
cause and not a guess. The parent stage holds the measured numbers, the contrast with SOCKS5
and the code facts already checked; this task produces the measurement that is missing and
names the cause. Deliverable: a reproducible container stand, a quiet live A/B, the numbers,
and the cause with its file and mechanism — no production code change.

Both parts run in isolation: the stand creates a real adapter and changes routes, so it runs
in a container with its own network namespace (`debian:trixie`, `NET_ADMIN`, `/dev/net/tun`,
image not pulled), and the live A/B runs there too, with the real profile and a file-backed
secret bind-mounted read-only from outside the repository. The host's routes and system DNS
are never touched, and no real value from a run goes into the repository or this node.
Architecture: §05, §05a, §12 (E2).

## Steps

1. **Build the local stand with a shaped, lossless exit.** Extend
   `internal/datapath/tun_netns_linux_test.go` under the existing `DNSTT_CLI_TEST_NETNS=1` +
   root gate, a real fdbased adapter, the destination a handler on loopback and no internet
   path:
   - an HTTP/1.1 handler on `127.0.0.1` with a fixed body and `Connection: close`, two sizes,
     2 KiB and 128 KiB;
   - a `shapedExit` (`exit.Dialer`) that records each destination and returns a stream to
     that handler wrapped in a lossless shaper: 50 ms one way, 64 KiB/s per stream (the
     stage's DNS times imply a ~50 ms tunnel RTT). The shape is per stream on purpose: this
     step isolates the local path, while the shared channel is step 3. A second shape,
     0 ms/∞, is the control;
   - one table test that drives the same client through (a) `NewTun`, (b) `NewSocks` on the
     same exit, (c) the exit directly, at 1 and 8 concurrent fetches; record TTFB, total and
     exit streams opened per case.
   Criterion: TUN total ≤ SOCKS total + one RTT and within 10% at both sizes and both
   concurrency levels. A gap means the local path (netstack, fdbased, drops, locks) is the
   cause and step 2 locates it; no gap means it is innocent and step 3 measures the tunnel.
   Run it in the container after `CGO_ENABLED=0 go test -c -o … ./internal/datapath`.

2. **Count the local path's losses and phases.** In the same harness:
   (a) a per-stream timeline — `handle` ran, `Dial` returned, first byte back, relay ended
   (wrap the dialer in the test for the middle two); (b) the stack's counters after each
   case: `stack.Stats().TCP.Retransmits`, `.TCP.Timeouts`, `.TCP.ForwardMaxInFlightDrop`, and
   the TX queue drops `stack.Stats().NICs.TxPacketsDroppedNoBufferSpace`
   (`pkg/tcpip/tcpip.go:2237`, `:2281`, `:2453-2458`, `:2488-2490`); (c) the adapter's TX/RX
   counts, through the same NIC statistics or a counting `LinkEndpoint` wrapper. Criterion:
   with the lossless shaper, zero retransmits and zero no-buffer drops at 8 × 128 KiB. A
   nonzero counter names the local loss source; a zero result rules the local path out with a
   number, not an impression.

3. **Re-measure the real tunnel in a quiet container, one test at a time.** A throwaway
   script kept outside the repository runs in the container described above, with the profile
   and the secret mounted read-only for the run. Same URL and body in both halves, pinned with
   `--resolve`, `Connection: close`: `connect -datapath tun`, then a fresh process with
   `-datapath socks`, and no other client in the container (`pgrep -a` shows only the shell
   and the client; the earlier failure was measured with a browser hammering the same tunnel,
   so nothing runs concurrently with another test):
   - for 1 fetch, 5 sequential, 4 parallel and a browser-equivalent fan-out (30 concurrent
     connections to one host, plus A and AAAA through the adapter), record `curl -w` for
     namelookup/connect/appconnect/starttransfer/total;
   - count open streams (`streams` in the status line) and the ↓/↑ bytes at the start and end
     of each phase; the per-fetch deltas are the amplification figure;
   - keep the two columns side by side and the raw outputs in `doc/runs/`, outside git.
   Criterion: a quiet TUN session either reproduces the seconds and the timeouts or shows it
   does not — the result is recorded, not assumed.

4. **Name the cause and separate it from the alternatives.** With the two tables, decide
   between the mechanisms the code points at, and write down the number that decides each:
   - local loss or serialization (`tun_linux.go`, `netstack.go`) — step 2's counters and the
     per-stream timeline;
   - DNS cost (`dns.go`/`netstack.go`) — streams or bytes per fetch dominated by queries;
   - connection setup and churn (`netstack.go`, `exit/socks5.go`) — streams per fetch far
     above the number of connections, or the tail growing with concurrency at a constant
     per-stream shape;
   - none of the above, the ceiling is the tunnel itself (payload size or MTU, the resolver).
   In the outcome, state the cause as file and line, the mechanism, why the SOCKS5 datapath on
   the same tunnel is unaffected, and what was ruled out with which measurement. If the last
   case is what the numbers show, stop and report to the lead: re-scoping the stage is the
   user's decision, and no code changes follow from this task.

5. **Leave the stand behind.** The new netns test runs in the CI netns job; `go test -race
   ./internal/datapath` and `go vet ./...` pass; the commands, the tables and the cause are in
   this node's report so that `02-fix` can start without repeating the runs. No private value,
   no date and no environment detail goes into the node or the test.

## Done when

- A test in `internal/datapath` (netns gate) drives the real adapter through a shaped,
  lossless exit, compares TUN with SOCKS and direct on the same body, prints TTFB, total and
  exit streams, and passes in the CI netns job.
- A quiet container A/B on the real tunnel produced one column for `tun` and one for `socks`
  at the same URL and body — 1 fetch, 5 sequential, 4 parallel, and the browser-equivalent
  fan-out — with open streams and ↓/↑ per phase; the run is recorded whether it reproduced the
  failure or not.
- The cause is named: the file and line, the mechanism, the evidence that distinguishes it
  from the alternatives, and why the SOCKS5 datapath on the same tunnel is unaffected.
- The measurements that ruled the other mechanisms out (local retransmits and queue drops,
  bytes and streams per fetch, the per-stream timeline) are in the report.
- Every live check ran in an isolated container with its own network namespace — image not
  pulled, `NET_ADMIN`, `/dev/net/tun`; the host's routes and system DNS were not touched, and
  no real value from the run is in the repository or in this node.

## Outcome

The TUN datapath has no defect to fix: in a quiet session it costs the same as the SOCKS5
datapath on the same tunnel, and the seconds and timeouts of the stage were measured on a
saturated, shared session. The Done-when line that asks for a cause and for why the SOCKS5
datapath is unaffected is therefore not met as written: there is no TUN-specific mechanism,
and at thirty connections the SOCKS5 datapath is bound by the same tunnel. By the user's
decision the task is closed without a fix, and [the fix](../02-fix/plan.md) is dropped.

**The local path, by the stand.** A new netns test (`tun_latency_netns_linux_test.go`)
drives the same client through the real adapter (`NewTun`), through `NewSocks` on the same
exit and through the exit alone, with the same `Connection: close` request and a byte-exact
body check, over a lossless shaper of 50 ms one way and 64 KiB/s per stream (control: 0 ms,
no limit), with bodies of 2 KiB and 128 KiB at 1 and 8 fetches at once. On the shaped exit
the TUN total exceeds the SOCKS5 total by 0.05–2.2 ms at totals of 101 ms–2.10 s; on the
control the difference is the same order at totals of 0.1–3.2 ms. The stack's counters are
zero at 8 × 128 KiB: no retransmits, no RTO expiries, no `ForwardMaxInFlightDrop`, no
`TxPacketsDroppedNoBufferSpace`; the exit opens exactly one stream per fetch, and the
adapter's TX/RX equal the body. The gVisor stack, the fdbased adapter, its queues and the
wiring are therefore ruled out with numbers, not impressions.

**The live tunnel, quiet.** Two `tun` runs and one `socks` run, each in its own throwaway
container with its own network namespace, one test at a time, the same page and body with
`--resolve` and `Connection: close`: one fetch 0.78–0.86 s, five sequential 0.69–0.82 s,
four parallel 0.65–0.83 s, thirty concurrent 3.75–3.80 s with all thirty answered — and the
same column over SOCKS5 within noise (0.78 s; 0.71–0.82 s; 0.46–0.79 s; 3.71 s). The bytes
agree: 6.6 KB down and 2.3 KB up per fetch, 263 KB / 88 KB for the thirty, and the TLS
handshake takes 0.39–0.72 s over either datapath. No fetch timed out, and the browser-like
fan-out completed instead of stalling. The one TUN-specific stream source, DNS over the
adapter (`dns.go:42-50`: a stream per query, no cache), was measured separately: twenty
concurrent A+AAAA lookups, forty queries, finished in 0.96–1.59 s for about 14 KB, not
dominating a page.

**What sets the numbers.** The shared ordered KCP channel of the tunnel: about 58 KB/s, the
same for both datapaths. Thirty fetches of 6.6 KB are about 200 KB and take about 3.5 s
with either datapath; a single fetch is latency-bound, a few tunnel round trips rather than
a bandwidth limit. That is the tunnel's capacity, not a property of the datapath, and the
stage's fixed boundaries (payload size and MTU, the resolver, the adapter's MTU and `/1`,
the sequential SOCKS5 handshake and `exit.one_write`, the gVisor version, the dependency
set) were not touched.

**Why the stage saw seconds.** The earlier numbers were measured in a session that held
about 160 open streams, peaking at 324 during the browser run, with a neighbouring tunnel
client (`qwdtt-cli`) running on the same machine; the stage's own evidence records 140–300
open streams during those fetches. That is a saturated, shared tunnel, not a quiet one, and
it also explains the browser's retry loop.

**Alternatives rejected, with the number that rejects each.** Local loss or serialization —
zero counters and 0.05–2.2 ms of added cost. DNS cost — a forty-query burst under two
seconds for about 14 KB. Connection setup and churn — one exit stream per fetch, and the
two-round-trip exit handshake is paid by both datapaths, which measured equal. A TUN-only
mechanism — none survived: the live columns agree within noise.

Checks: the new test passes in the CI netns job's shape (the already-present `debian:trixie`
image, no pull, `NET_ADMIN`, `/dev/net/tun`, `DNSTT_CLI_TEST_NETNS=1`) and is skipped
outside a netns; `gofmt`, the CI vet split, the full `-race` suite with the isolated D-Bus
tests and the four documented skips, and the Linux and Windows builds pass. One test-only
seam was added — `tunAdapter.stats func() tcpip.Stats` in `tun.go`, set in `tun_linux.go`
and read only by the test; no behaviour and no exported API changed. The independent review
accepted the stand and asked for a wider control allowance and a URL-parse tidy-up in the
out-of-tree run script, both done. No private value entered the repository or this node; the
live raw output stays in ignored `doc/runs/`.
