---
kind: stage
status: done
base: 9e7a9f8
depends: [05-protection]
---

# Live acceptance on Linux

The parent stage's Done when checked on a real machine by the user (`architecture.md` §12,
E2). Agents do not run these checks: they change the machine's real network and need the
user's server, profile and network. The code children end with local tests; this node is
the user-run matrix and the record of its results.

## What the user runs

- `sudo dnstt-cli connect` with the real profile in `tun` mode; a browser and a
  command-line client reach a site through the server's exit. The settings file, the
  secrets and the keyring under `sudo` are the calling user's.
- `checktunnel` before, during and after a run: the adapter, the `/1` routes, the system
  DNS, the `inet dnstt-cli` table, and no leftovers after Ctrl+C.
- A `kill -9` run: the adapter, routes and table disappear with the process; a DNS
  record or an overwritten `resolv.conf` left behind is removed by the next start with a
  line.
- The kill-switch: at the first dial, during a reconnection, and while the server is
  down; the §05a exceptions (dnstt-cli itself, loopback, DHCP, ND/RA, the local network
  without port 53); `-no-kill-switch`, `kill_switch = false`, `-allow-leak`.
- DNS: the system resolver is the adapter's address, names resolve through `tun_dns`;
  other UDP is refused at once and QUIC falls back to TCP; IPv6 outward is closed while
  the local IPv6 network works.
- A network change (Wi-Fi ↔ Ethernet, cable out and back): the session restarts and new
  connections work without restarting dnstt-cli.
- A machine with a strict IPv4 `rp_filter`: the refusal with the sysctl hint, or the
  documented loose setting. A second instance is refused by the lock.
- The results and the course of the runs go to `doc/local/` and `doc/runs/`, outside git;
  `README.md` gets the leak matrix, and `architecture.md` gets the settled values, without
  addresses, resolvers, environment or dates (`AGENTS.md`).

## Children

1. [Runbook and evidence form](01-runbook/plan.md) — the user-run checklist with placeholders,
   the exact commands, the expected observations and the private evidence layout. Task; docs
   only.
2. [The Linux matrix](02-user-run/plan.md) — the agent runs the steps that need no browser or
   physical network change (P0–P4, P6, P7), the user the rest (P5, the browser/QUIC checks,
   the keyring path); the checklist is filled from the evidence. Task; mixed execution,
   recorded in the node.
3. [Recording the results](03-record/plan.md) — the sanitized leak matrix in `README.md`, the
   settled values in `architecture.md` and this stage's `Outcome`. Task.
4. [Recovering the system DNS before the first dial](04-recover-system-dns/plan.md) — the
   `kill -9` leftover in the system DNS is removed right after the TUN reservation and before
   the first session, so a run whose resolver is given by host name can start; P2 of the
   matrix found the gap. Task.
5. [Dispatching the secret-service helper](05-secret-service-helper/plan.md) — the hidden
   bridge that reads the invoking user's keyring under `sudo` is wired into the CLI; without
   it a `secretservice:` reference cannot be read under `sudo`. P0 of the matrix found it.
   Task.
6. [TUN datapath latency and stalls](06-tun-datapath-latency/plan.md) — the TUN path was
   reported as seconds slow and stalling under concurrency while the same tunnel over SOCKS5
   was fast; the diagnosis measured it on a local stand and in a quiet container A/B and
   closed without a defect — the cost is the tunnel's own capacity — so the fix was dropped.
   The retried browser/QUIC branch raised it. Stage; two children.
7. [Browser through the TUN in a container](07-browser-container/plan.md) — the browser/QUIC
   branch re-run in a quiet throwaway container with a headless browser, to tell a browser
   path problem from the host's saturated session. The user authorized the Chromium fetch
   inside the disposable container. Task.

The runbook is prepared and reviewed before the user starts, so that a mistake in a command
costs a line and not a wasted session. The matrix is then run in the user's time; the record
task writes only what the runs settled and keeps every value that identifies the user out of
the repository. The matrix waits for the fix of the `kill -9` leftover (child 4): P2 cannot
pass without it.

## Done when

- Every line of the parent stage's `## Done when` has been seen on the user's Linux
  machine; the exceptions and the fail-open flags behave as written.
- Ctrl+C and `kill -9` leave no adapter, routes or nftables table; DNS leftovers are
  recovered by the next start.
- A network change restarts the session without a manual restart.
- `README.md` says what leaks in `tun` and what does not; the architecture holds the final
  values if any changed.
- The evidence is recorded outside git; this node carries only the substance.

## Outcome

The matrix was accepted with two lines not exercised, and the user accepted that gap. P0–P4,
P6 and P7 are seen on the real Linux machine; P5 (network change) and the live
IPv6/Happy-Eyeballs part of the browser branch are not.

- **P0.** The build, the real profile, the baseline, the non-root refusal and the
  `sudo`/keyring path are seen. The system DNS method is `resolvconf`; the keyring path first
  failed and works after [the helper fix](05-secret-service-helper/plan.md).
- **P1.** A session came up with the adapter, the two `/1` routes, the `inet dnstt-cli` table
  with a netlink owner and the adapter as system DNS; `curl` through the exit answered 200,
  the resolver section showed the exit's answer, the lock was held, and a clean stop restored
  the system DNS.
- **P2.** After `kill -9` the adapter, routes, table and lock disappeared, but the system-DNS
  record remained and the next start first could not resolve the DoH name.
  [The recovery fix](04-recover-system-dns/plan.md) moves the removal right after the
  adapter reservation, before the first session; the re-run with `-v` printed the removal
  line, established a session and answered 200, and the clean stop left no record.
- **P3.** The locked state, `-no-kill-switch` and `kill_switch = false` behave as §05a says;
  loopback and the local network without port 53 were seen in the container, and the
  IPv6/DHCPv6/ND exceptions together with the outward block were proven in the netns suite,
  on real nftables with IPv6 on. `-allow-leak` was covered in the container — the refusal,
  the warning and the empty rule set — because the machine has no `nft`; a working tunnel
  under `-allow-leak` was not exercised.
- **P4.** Names resolve through the adapter, and a connected public UDP send is refused at
  once. The outward IPv6 block and the local IPv6/ND/DHCPv6 exceptions are proven by the
  netns suite with IPv6 switched on; the machine's network has no global IPv6, so the live
  effect was not observed. The browser branch was measured in a quiet throwaway container
  ([the container task](07-browser-container/plan.md)): all four headless Chromium phases
  loaded, the browser's DNS fan-out peaked at four queries, the exit answered 65 of 65
  CONNECTs, and streams drained after every phase. The earlier host failure was a saturated
  session with a neighbouring tunnel client, not a defect in the browser path.
- **P5 — not exercised.** No second network was available to the agent, and changing the link
  would cut the session; the code path is covered by unit tests only. The user accepted the
  gap.
- **The live IPv6/Happy-Eyeballs part — not exercised.** The container is IPv4 only; the IPv4
  QUIC fallback is covered.
- **P6.** The effective `rp_filter` was 2; forcing 1 made `connect` refuse with the sysctl
  hint, exit 1 and leave no adapter or table; the value was restored.
- **P7.** A second instance was refused by the lock while the first ran, and the lock was
  free after it stopped.

The failures found point at `dnstt-cli` alone and were fixed inside this stage (children 4 and
5); none is left open. The DNS listener's gate of 64 exchanges in flight stays recorded as a
limit, not a defect: the 65th gets an immediate SERVFAIL, and the browser's fan-out peaked at
four, far below it. The tunnel's shared channel is a reference rather than a fixed ceiling —
about 58 KB/s in the diagnosis, about 52 KiB/s under 65 concurrent fetches and about 100 KiB/s
for the quiet heavy page; changing its payload size, MTU, the resolver or KCP remains a user
decision. The evidence is in `doc/runs/tun-matrix/` and `doc/runs/browser-container/`, the
filled checklist in `doc/local/runbook-tun-linux.md`; all outside git, and no value is in a
tracked file.

The stage was accepted on the final tree: `gofmt` outside `core/`, the CI vet split on Linux
and Windows, the Linux build, the Windows cross-build with the compilation of every test
package, `git diff --check`, the full `go test -race -count=1 ./...` with the keyring/D-Bus
tests enabled, `DNSTT_CLI_TEST_NETNS=0` and the four stated netguard exclusions applied by
`-skip`, and the netns suites in the already-present `debian:trixie` container
(`linuxsys.test` 72 passed, `datapath.test` 92 passed, no skips). The accepted run made no
live call, pulled no image and changed no host route or DNS.
