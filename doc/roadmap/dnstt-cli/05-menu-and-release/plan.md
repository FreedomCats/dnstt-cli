---
kind: stage
status: in-progress
base: 3d80d87
---

# Interactive mode and releases

Goal (`architecture.md` §12, E4): the program is usable without learning the commands, a
`slipnet://` link imports a profile, and there are signed-off release archives for Linux and
Windows. `architecture.md` R9, §10a; `analysis.md` §6 for the license.

Split into the children below. The order is the risk order: the two core patches first, because
they change the vendored core and its reproducible generation and make the `tcp` transport and
the payload-size field of a link usable; then the link import that uses them; then the menu;
then elevation, which is a menu behaviour; then the license record and the release that needs
it.

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

## Children

1. [Plain DNS over TCP (P6)](01-p6-tcp-transport/plan.md) — the `tcp:` resolver transport in
   the vendored core and in the resolver parser. Task; detailed.
2. [A cap on the payload per query (P7)](02-p7-payload-cap/plan.md) — a smaller KCP MTU
   carried by the profile key `payload_size`, for the `slipnet://` payload-size field. Task;
   detailed.
3. [Fix the core track/close deadlock](03-core-track-deadlock/plan.md) — close a socket
   outside the session lock so a session ending between a successful dial and its tracking
   cannot deadlock the redial or the teardown. Task; detailed.
4. [Import of `slipnet://` links](04-slipnet-import/plan.md) — the parser and
   `profile import` for `dnstt` and `dnstt_ssh` links, then the user's real link. Stage; two
   children.
5. [The menu and the wizard](05-menu/plan.md) — the interactive mode of §10a. Stage; three
   children.
6. [Elevation from the menu](06-elevation/plan.md) — `tun` without root re-runs one
   connection under `sudo` in the same terminal; `socks` when that is impossible. Task.
7. [The MIT license](07-license/plan.md) — the chosen license and its record in the
   repository. Task.
8. [Release archives and `distcheck`](08-release/plan.md) — `release`, `notices`,
   `releasenotes`, `distcheck`, the `release.yml` workflow, the archives with `SHA256SUMS.txt`
   and `wintun.dll`, the notices file and the README. Task; detailed.

The core work comes first: P6, P7 and the track/close deadlock fix are the riskiest (the
vendored-core rules and a reproducible generation), and the import can only write a `tcp:`
resolver and a payload cap after P6/P7. The deadlock fix comes before the product work so a
session teardown cannot hang the later live checks. The import follows, since the menu's
wizard offers a link among its first questions. The menu is the largest product change and
comes after the pieces it calls.
Elevation is one menu behaviour and follows the menu. The license is decided (MIT) and only
recorded before the release, which needs `LICENSE` and the notices. Windows is deferred with
`04-tun-windows`: the menu's double-click pause, the UAC window and a run of the Windows
archive are cross-built and content-checked, never run here.

## Risks and what is needed from the user

- The license is MIT, chosen by the user; `07-license` records it, and it blocks nothing else.
- `slipnet://` links for a live test are the user's; they carry an exit password, so they are
  given in chat and never written into the repository.
- `dnstt_ssh` password links create an `ssh` profile that does not connect until
  `06-ssh-exit`; that is the user's deliberate choice, the import says so, and `host_key`
  arrives with that stage. Key-based links are refused with a message naming that stage, and
  no key material is kept before its `key_ref`. Until then `connect` refuses the imported
  profile with the existing "SSH exit is not implemented yet".
- P7's key is `payload_size` (bytes of the DNS payload; `0` or absent — the domain's maximum;
  the wizard does not ask it).
- The archives are `linux/amd64` and `windows/amd64`; `wintun.dll` is fetched by the release
  build itself — the `release.yml` job runs `scripts/release`, which downloads it — so nobody
  downloads it by hand and puts it next to an unpacked release. The workflow run and the
  publication are the user's; the draft release is published by hand.
- The Windows halves — the UAC window of elevation, the double-click pause of the menu and a
  run of the Windows archive — stay with `04-tun-windows`, which is blocked. Here they are
  cross-built and content-checked only, and no Windows result is claimed.
