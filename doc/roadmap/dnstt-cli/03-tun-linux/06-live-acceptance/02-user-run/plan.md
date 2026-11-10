---
kind: task
status: blocked
detailed: true
depends: [01-runbook, 04-recover-system-dns, 05-secret-service-helper]
---

# The Linux matrix by the user

## Task

The matrix is run on the real Linux machine, following [the runbook](../01-runbook/plan.md).
On the user's request the agent runs the steps that need no browser and no physical network
change — P0–P4, P6 and P7, which change the machine's routes and system DNS and need root —
while P5, the browser/QUIC checks and the keyring path of a graphical session remain the
user's. The agent uses only the real profile and the values the user supplied, and publishes
no real value. The lead keeps this node blocked while waiting for the user; a result is the filled
checklist and the evidence, not a code change.

Input: `doc/local/runbook-tun-linux.md`. Output: the same file filled in (verdicts only) and the
raw outputs in `doc/runs/tun-matrix/`. The user decides where a failure lies — server, exit,
network or `dnstt-cli` — and leaves a bug of `dnstt-cli` for a new task of this stage rather than
working around it silently.

## Steps

1. **P0 — build, profile, baseline.** Follow the runbook's P0. Build into `build/`; create the
   profile as the user with `./build/dnstt-cli profile add <profile>` (exit `http`, mode `tun`);
   confirm `profile show` and the ownership of `~/.config/dnstt-cli/`; take the baseline
   `sudo ./build/checktunnel -label "P0 baseline"`; record the system-DNS method, the IPv6
   availability and the `rp_filter` values; confirm the non-root refusal.
2. **P1 — first session and traffic.** Run `script -q -e -c "sudo ./build/dnstt-cli connect -v
   <profile>" doc/runs/tun-matrix/01-connect.log`; open `<site>` in the browser and fetch
   `https://example.com` with `curl`; take the "up" `checktunnel`; stop with one Ctrl+C and take
   the "after" one. Expected: the kill-switch line, the `[TUN]` lines, traffic through the
   exit, the adapter and `/1` routes, `inet dnstt-cli`, the adapter as system DNS, the exit's
   external address and resolver answer, lock held; after the stop everything is as in P0.
3. **P2 — `kill -9`.** With the session up, kill the `dnstt-cli` process; record what
   disappears at once and what DNS leftover remains; start again with `-v`, record the recovery
   line and the clean state.
4. **P3 — kill-switch and fail-open flags.** With `<server>` stopped: the blocked line, traffic
   failing at once for UDP (`EPERM`) and by timeout for TCP, the table present without an
   adapter; start the server and see the up line. With the server stopped and a network change:
   the reconnecting line and blocked traffic. Then `-no-kill-switch`, `kill_switch = false` in
   the profile (restored afterwards), `-allow-leak` with a foreign `inet dnstt-cli` table
   (deleted afterwards), and the §05a exceptions: loopback, the local network, port 53 closed
   even locally.
5. **P4 — DNS, UDP, QUIC, IPv6.** With the session up: the system resolver is `10.66.67.1`
   (by the method's tool); names resolve; other UDP gets the netstack's immediate
   `ConnectionRefused`, port 53 elsewhere is `EPERM`; the browser loads a site over TCP with
   QUIC refused, and shows the protocol it used where it can; outward IPv6 is closed while
   local IPv6 answers, or the check is marked unexercised because the network has no IPv6.
6. **P5 — network change.** Switch Wi-Fi/Ethernet or unplug and replug the cable with the
   session up; record the reconnecting and up lines, the new interface for the core's sockets,
   and that new connections work without restarting `connect`.
7. **P6 — `rp_filter`.** Record the values; force the effective value to 1 if it is not
   already; expect the refusal with the sysctl hint and `-datapath socks`, and no adapter or
   table; restore the original values and confirm with `checktunnel`.
8. **P7 — second instance.** With a session up, a second `connect` must be refused with
   `dnstt-cli connect: tun: the adapter is already busy: another dnstt-cli with the tun datapath
   is running on this machine; stop it or connect with -datapath socks`; the lock is `held` and
   then `free` after the first stops.
9. **P8 — final state and summary.** The final `checktunnel` equals the baseline; `git status
   --short` is clean; every check has a `Result`; the summary lists each P id, its verdict, the
   system-DNS method, the `rp_filter` values, what was not exercised and why, and the failures
   with the side they point at. The raw files stay in `doc/runs/tun-matrix/`; nothing is
   force-added; the report to the lead carries no real values.

## Done when

- Every check P0–P8 has a `Result`: seen, not seen with the reason, or failed with the observed
  behavior; no field is blank.
- The evidence files named in the runbook exist in `doc/runs/tun-matrix/`, and the filled
  checklist is in `doc/local/`; both are ignored by git and nothing was force-added.
- The report to the lead covers every line of this stage's `What the user runs` and of the parent
  stage's `## Done when`, names the failures and the side they point at, and leaves a
  `dnstt-cli` bug for a new task of this stage instead of hiding it.
- No domain, key, resolver, address, profile name, date or detail of the environment is in a
  tracked file; `git status --short` is clean.

## What is needed from the user

- Root through plain `sudo` from the user's graphical session, `/dev/net/tun`, and the real
  profile with the exit credentials in the user's keyring or `secrets.json`.
- The server reachable, and the right to stop and start `dnstt-server` during P3.
- A second network or a cable to replug for P5; a LAN that answers IPv6 for the local part of P4,
  or the decision to record that check as not exercised.
- `nft` for the P3 `-allow-leak` setup only; `python3` for the UDP probes; `resolvectl` or
  `resolvconf` for the DNS check, depending on the machine's method.
- Consent to change `rp_filter` temporarily in P6 and `kill_switch` in the profile in P3d, both
  restored in the same scenario.

## Blocked

Waiting for the user's live run of P0–P8 on the real Linux machine. The runbook is
`doc/local/runbook-tun-linux.md`; the raw outputs go to `doc/runs/tun-matrix/`. No agent
runs any step or reads the raw evidence; the user reports verdicts and the side of any
failure, without real values. The node stays blocked until the user's results are in. It
also waits for [the `kill -9` leftover fix](../04-recover-system-dns/plan.md): with the record
still in the system DNS the next run cannot resolve a resolver's host name, so P2 cannot pass
before that fix is in.
