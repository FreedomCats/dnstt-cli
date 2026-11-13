---
kind: root
status: in-progress
lang: en
source: doc/architecture.md
branch: main
base: 5dae04e
---

# dnstt-cli

A desktop console client for dnstt: a local SOCKS5 proxy or a system-wide VPN through a
DNS tunnel, with reconnection, a list of resolvers, profiles and secret storage. One Go
binary for Linux and Windows; macOS in the proxy mode only. The dnstt client core is
vendored and built in, the shell is carried over from csqtt-cli.

Why it is built this way — [`doc/architecture.md`](../../architecture.md). That document
holds the decisions (R1–R9), the interfaces and the analysis behind them; this roadmap
holds what is done and in which order.

**State: finishing.** Every stage is accepted except
[the Windows VPN](04-tun-windows/plan.md), which is `blocked` on a Windows machine for
Wintun, WFP and the live run; the criteria below that name Windows cannot be accepted
without one. A last stage, [finishing](07-polish-and-release/plan.md), closes the small
debts recorded at the acceptances, gets the CI green on both platforms and publishes the
first release, `v0.9.0`; the map then returns to `blocked` until a machine is available.

## Done when

- `dnstt-cli connect` carries a browser through the server's SOCKS5 exit with a login, on
  Linux and Windows, and the session survives a server restart and a network change.
- The local SOCKS5 proxy also carries a browser through a configured HTTP CONNECT exit
  with Basic authentication on Linux and Windows, without removing the SOCKS5 or SSH exit
  choices. HTTP authentication starts on the first real application CONNECT; before it,
  `Connected` means tunnel readiness only, and HTTP 407 stops the supervisor.
- `tun` mode sends all system traffic through the tunnel on Linux and on Windows, with DNS
  protection, kill-switch and IPv6 block; what leaks and what does not is written in
  README.
- The menu and the wizard run without arguments, and a `slipnet://` link imports a profile.
- Release archives for Linux and Windows carry `SHA256SUMS.txt`, the CI is green on both.
- A license is chosen and recorded.

## Stages

1. [Foundation](01-foundation/plan.md) — the repository builds, the core is vendored, CI
   is green. Done.
2. [SOCKS5 mode with reconnection](02-socks/plan.md) — a browser goes through the tunnel;
   profiles, resolvers, the status line.
3. [System VPN on Linux](03-tun-linux/plan.md) — `tun` with netstack, routes, DNS, leak
   protection.
4. [System VPN on Windows](04-tun-windows/plan.md) — Wintun, WFP, one app ID.
5. [Interactive mode and releases](05-menu-and-release/plan.md) — menu, wizard,
   `slipnet://` import, release archives.
6. [SSH exit](06-ssh-exit/plan.md) — `direct-tcpip`, password and key, pinned host key.
7. [Finishing](07-polish-and-release/plan.md) — the small debts, CI green on both platforms
   and the first release, `v0.9.0`.

The old stage letters (`E0`–`E5`, task letters `F`, `S`) stay in commit messages and in
the architecture, and the nodes here list them, so history is readable. The detailed
plan for the foundation and for the first four SOCKS5 tasks is kept in
[`notes/former-plan.md`](notes/former-plan.md).

There is no Jira in this project and no issue numbers: commits describe the change
without a task key.

Work notes and the journal of decisions that must not be published live outside git —
see [`doc/local/`](../../../doc/local/) (`AGENTS.md`, "Documents in `doc/`").
