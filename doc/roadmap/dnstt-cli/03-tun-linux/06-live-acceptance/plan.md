---
kind: stage
status: in-progress
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
