---
kind: task
status: done
detailed: true
depends: [01-runbook, 04-recover-system-dns, 05-secret-service-helper, 06-tun-datapath-latency]
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

The matrix has been run. The agent ran P0–P4, P6 and P7 on the real Linux machine; P3c–P3f
were covered in an isolated container and P3e is not runnable on this host. Two real defects
were found and fixed inside this stage: the `kill -9` system-DNS recovery order
([04](../04-recover-system-dns/plan.md)) and the undispatched secret-service helper
([05](../05-secret-service-helper/plan.md)); P2 and the keyring path were re-run green after
them.

Two items are not seen, and the node waits for the user's decision on them:

- **P5, network change.** No second network is available to the agent, and changing the link
  would cut the session. The stage's `Done when` says "a network change is noticed and the
  session restarts", so this line is unproven without it.
- **The browser/QUIC branch of P4.** Retried by the agent with the session kept up. It was
  recorded as a reproducible failure of the TUN datapath, not a browser limitation: through
  the same tunnel and exit, five page fetches over SOCKS take 0.7–0.8 s each, while through
  the TUN the TLS handshake alone takes 1.1–4.7 s, one of five fetches times out at 25 s, a
  plain-HTTP fetch needs 2.8 s to the first byte, four parallel fetches all time out at 30 s,
  and a headless browser loads neither the HTTPS nor the plain-HTTP page. Direct network to
  the same sites is 0.13–0.2 s; DNS through the listener answers in 0.15–0.2 s. A clean
  reproduction in an isolated network namespace was needed.

The TUN datapath was then measured, and the record above did not survive it:
[the latency stage](../06-tun-datapath-latency/plan.md) closed without a defect. On a local
shaped stand the TUN path adds milliseconds to SOCKS5 with zero local loss, and in a
quiet container on the real tunnel both datapaths give the same columns with identical bytes
and no timeout; the seconds and timeouts above came from a session that was already saturated
(about 160 open streams, peaking at 324 during the browser run, with a neighbouring tunnel
client), not from the datapath. The only measured ceiling is the tunnel's own shared channel,
about 58 KB/s, which both datapaths share. No fix follows.

The browser path itself was then measured in a quiet throwaway container
([the container task](../07-browser-container/plan.md)): headless Chromium loaded all four
phases — the control page by name and with a pinned address, a heavy page of about 821 KiB,
and the control page with QUIC disabled — in 1.0–8.1 s at 15–102 KiB/s. The browser's DNS
fan-out peaked at four queries in flight against the listener's 64, the exit answered 65 of
65 concurrent CONNECTs, and every phase drained its streams back to zero. The browser path is
therefore not the limit either; what remains is the real-machine part — IPv6/Happy Eyeballs
and QUIC on the live network — which the container cannot exercise.

The TUN runs take over the machine's routes and system DNS, as `tun` mode does by design. A
retry's cleanup killed the `script` wrapper before `connect` could restore the system DNS,
leaving the tunnel's dead address first in `/etc/resolv.conf`; connectivity stalled and an
unrelated `qwdtt-cli` process had to be killed. The leftover was removed with
`resolvconf -d tun.dnstt-cli`. No agent-run `tun` session on the host again without an explicit
fresh request from the user; further live TUN work goes into a container with its own network
namespace, with the host untouched.

The choice is the user's: how to settle P5, and whether the real-machine browser/QUIC check is
still required before the matrix is accepted. The filled checklist is
`doc/local/runbook-tun-linux.md`, the raw evidence is in `doc/runs/tun-matrix/`; both stay
outside git and carry no real value.

## Outcome

The matrix has been run. Every check has a result; two are not seen, and the user accepted
that gap for this acceptance.

- P0, P1, P2, P3, P4 (partly), P6 and P7 are seen on the real Linux machine. The `kill -9`
  run first left a dead resolver in the system DNS and the next start could not resolve the
  DoH name; [04](../04-recover-system-dns/plan.md) fixed the recovery order and the re-run was
  green. The keyring branch under `sudo` first failed with `unknown command`;
  [05](../05-secret-service-helper/plan.md) wired the helper, and the re-run read the secret
  and carried traffic.
- P4: names resolve through the adapter, other UDP is refused at once, outward IPv6 is
  blocked, and the browser path was measured in a quiet throwaway container
  ([07](../07-browser-container/plan.md)): all four Chromium phases loaded, the browser's DNS
  fan-out peaked at four queries, the exit answered 65 of 65 CONNECTs, and the streams drained
  after every phase.
- **P5, network change: not seen.** No second network was available to the agent, and changing
  the link would cut the session. The code path is covered by unit tests only; the stage's
  `Done when` line about a network change stays unproven by a live run. The user accepted this
  gap.
- **The live IPv6/Happy Eyeballs part of the browser branch: not seen.** The container is IPv4
  only; the IPv4 QUIC fallback is covered by the container phases.
- No failure is left pointing at the server, the exit or the network; the failures found were
  fixed inside this stage. The raw evidence is in `doc/runs/tun-matrix/` and
  `doc/runs/browser-container/`, the filled checklist in `doc/local/runbook-tun-linux.md`;
  all outside git, and no value is in a tracked file.
