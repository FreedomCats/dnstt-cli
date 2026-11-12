---
kind: stage
status: done
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

## Outcome

The stage is accepted as a whole: every child is in the range and its own Outcome holds, the
parts fit each other, and the local matrix is green on the final tree. The acceptance itself
changed no code; the one integration finding it made is recorded below as a known limit
instead of a fix.

Children, each with the commit that landed it:

- P6, plain DNS over TCP — `a4f7f67`, with the API note in `7c3b91d`: `tcp:host[:port]` is a
  resolver transport in the parser and in the core; the framing, the queues and the redial are
  the DoT ones, the TLS is not, and the dialer goes through `s.dial`, so the socket guard sees
  the socket. `core/` is generated from the recorded upstream commit.
- P7, the payload cap — `190ebae`: the profile key `payload_size` lowers the session's KCP MTU
  and reaches the first session and the redials; `profile show` prints it when set.
- The core track/close deadlock — `86f4aa7`: found by P7's matrix, fixed in `_files` with a
  deterministic test that times out on the old code.
- The `slipnet://` import — `9ce7e2f`, over the parser and the command in `9675387` and
  `af880ce` and the missing real link recorded in `3ed6813`: both tunnel types import, the
  refusals name the reason, and the imported profile carries the `tcp:` resolver and
  `payload_size`.
- The menu and the wizard — `ac2bf2c`, over `690c245`, `65b5149` and `46ee876`: the menu
  without arguments in a terminal, the command list without one, the seven items over the
  command bodies, the first-profile wizard with the link question, the connect-time resolver
  offer and the mid-session offer after a full failed round.
- Elevation — `5d766e4`: a `tun` profile in a non-root menu session runs one `connect` under
  `sudo` in the same terminal, and `socks` is offered for this time when that is impossible.
- The MIT license — `9c533bd`: `LICENSE`, `README.md`, `AGENTS.md`, `analysis.md` §6 and
  `architecture.md` §11 agree on MIT; the archives carry `LICENSE`, `COPYING` and the notices.
- The release — `7e73d32`: `release`, `notices`, `releasenotes`, `distcheck`, the `release.yml`
  workflow, one archive content list in `scripts/internal/dist`, the committed notices file
  and the README.

The stage's own notes: `d47c3fb` (the start of the stage and two stale references), `09d0dfd`
(`menu -connect` asks no resolver question) and `b7b6a14` (what is actually known about macOS).

What the parts were checked for together:

- One connection body: `connect`, `menu -connect` and the menu's connect item all go through
  `runConnection` with `connectRequest`, and the `connect` tests are unmodified. The one
  recorded change of `connect` is that the stop ladder is armed before the connection body, so
  a signal during the setup ends with `stopping...` instead of killing the process.
- One item table (`menuItems`: seven items and `0`), one stdin reader (`internal/lines`, shared
  by the menu, the wizard and the link question; `profile import` from stdin reads the whole
  stream and uses no prompter), one archive content list (`scripts/internal/dist`, used by both
  `release` and `distcheck`).
- `THIRD_PARTY_NOTICES.txt` is regenerated byte for byte by `go run ./scripts/notices` (35
  components), and the workflow fails on a stale copy.
- MIT is the same in `LICENSE`, `README.md`, `AGENTS.md`, `analysis.md` §6, `architecture.md`
  §11 and the archive contents; the core's CC0 is `COPYING`.
- `core/` matches `scripts/vendorcore/_files` file by file, and `vendorcore -check` passes.

Checks on the final tree: `go test ./scripts/...`; `gofmt` outside `core/`; `go build ./...`
and `CGO_ENABLED=0 go build ./...`; the CI vet split on Linux and with `GOOS=windows`, the
package list taken under the same `GOOS`; the full `go test -race -count=1 ./...` with
`DNSTT_CLI_TEST_DBUS=1`, `DNSTT_CLI_TEST_NETNS=0` and the four external `netguard` live tests
excluded by `-skip`, so no live call was made; the Windows cross-build and `go test -c` of all
23 packages that have tests and build for Windows; `git diff --check` on the tree and on the
range; `go run ./scripts/notices -dest <tmp>` compared with the committed file. A local
`scripts/release -version v0.0.0-accept` built both archives, `distcheck` exited 0 and ran the
Linux binary's `version`, `help` and command list, `sha256sums` wrote `SHA256SUMS.txt`,
`sha256sum -c` passed and `releasenotes` wrote the download block without a date. The archives
were built into a temporary directory and nothing of them is committed: `dist/` stays ignored.

Not seen, and not claimed:

- **The Windows half of the elevation criterion is not done.** `tun` without privileges runs
  elevated in the same terminal on Linux only. The elevated run in a new window on Windows is
  `04-tun-windows`, which is blocked, and on a Windows build `tun` is `Unsupported` before any
  elevation decision, so what a Windows user gets today is the reason and the `socks` offer.
  The `goos == "windows"` branch of `planElevation` is covered by a unit test with fakes that
  fail if called; no Windows result is claimed.
- The double-click pause of the menu, the UAC window and a run of the Windows archive are
  compiled or content-checked only; the Windows exe is never run, and no Windows CI run of this
  tree is claimed.
- The real terminal is a manual check that was not made: the keys and Enter in the menu, the
  hidden input and the masked defaults, the status line, `kill -TERM`/`-HUP`.
- The real elevated session — sudo's prompt, Ctrl+C, a tunnel through it — was not run.
- A real `slipnet://` link: the user has none, so the import rests on synthetic links.
- No live resolver, profile, secret store or tunnel was used.

Limits and debts recorded, not fixed here:

- While an elevated child runs, the menu takes no signal. A SIGTERM or SIGHUP sent to the menu
  alone in that window is dropped, not deferred: the parent neither stops nor exits, and after
  the child returns the menu keeps running. Ctrl+C from the terminal reaches the child through
  the process group, which is the usual case; an addressed `kill -TERM` is not. Deferring the
  signal until the resume was weighed and left out: the elevation task's rule is that the
  parent takes no signal of the terminal while the child runs, and the fix belongs to the
  session's suspend/resume rather than to the acceptance of the stage.
- The elevated connection has no mid-session resolver offer: the child reports nothing back,
  there is no IPC.
- `menu -connect` and `connect` do not elevate and ask no resolver question.
- The third Ctrl+C in the menu does nothing (no `os.Exit`); with profiles but no default an
  empty answer closes the menu.
- `sudo` resets the environment, so the elevated child reads the invoker's `~/.config`, not a
  custom `XDG_CONFIG_HOME`.
- The menu's buffered reader keeps a line pasted between the `socks` question and the child's
  start in the parent.
- `payload_size` is not asked by the wizard and is written only by the import; `resolvers
  check` and the core's unused `run` build `client.Config` without the cap.
- The redial failure log says `dial tls:` for a plain TCP resolver; the "no transport" hint of
  `internal/resolvers/presets.go` still lists `(doh:, dot:, udp:)` without `tcp:` while the
  message in `resolvers.go` names all four; `doc/architecture.md` §04's `Resolver` comment
  still lists `DoH | DoT | UDP`.
- An IPv6 literal in field 4 of a link is not expressible by the §10 grammar and is refused; a
  link broken by a hard newline is not joined; the 255-byte limit on a SOCKS5 login is applied
  by `connect`, not by the import.
- `TestConnectWithInProcessServer` logs from the core after the test returns and has flaked
  outside this stage's changes. `TestDefaultUTLSFingerprints` in the vendored core flaked once
  during the stage's runs (loopback HTTP/2) and passed in the final matrix; it flaked before
  this stage as well.
- The README's macOS row claims no CI build and no live check, while `doc/architecture.md` §09
  still says macOS "builds in CI".
