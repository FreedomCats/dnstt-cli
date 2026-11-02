---
kind: stage
status: todo
---

# Interactive mode and releases

Goal (`architecture.md` §12, E4): the program is usable without learning the commands, a
`slipnet://` link imports a profile, and there are signed-off release archives for Linux and
Windows. `architecture.md` R9, §10a; `analysis.md` §6 for the license.

Detailed when its turn comes; below is the outline from `architecture.md`.

## Done when

- Without arguments in a terminal the menu opens; without a terminal it prints the command
  list.
- With no profile yet, the wizard runs: a `slipnet://` link (hidden input) or questions;
  the defaults are `home`, `network` and `tun` where supported.
- The menu calls the same code as the commands; it offers to check and edit resolvers when
  they run out mid-session.
- `tun` without privileges re-runs one connection elevated in the same terminal (Linux) or
  in a new window (Windows); if that is impossible it offers `socks`.
- `slipnet://` import creates a `dnstt` or `dnstt_ssh` profile; `slipnet-enc://` and
  NoizDNS (`sayedns`) links are refused with a clear message.
- P6 (plain DNS over TCP) and P7 (payload cap per query) are done.
- Release archives for Linux and Windows with `SHA256SUMS.txt`; `distcheck` runs.
- A license is chosen (`analysis.md` §6) and recorded.

## Outline

1. The menu: items, signals, the status line at the bottom.
2. The wizard and the profile import.
3. P6 — the transport `tcp:`.
4. P7 — a cap on the payload per query (KCP MTU), for the `slipnet://` payload-size field
   and for resolvers that cut large answers.
5. Privilege elevation from the menu.
6. Release scripts: `release`, `notices`, `releasenotes`, `distcheck`.
7. The license decision.
8. `slipnet://` import live, on a real link.

## Risks and what is needed from the user

- The license is the user's decision and blocks the first release.
- `slipnet://` links for a live test are the user's; they carry an exit password, so they
  are given in chat and never written into the repository.
