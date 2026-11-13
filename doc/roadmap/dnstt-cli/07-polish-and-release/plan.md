---
kind: stage
status: done
base: 74e330d
---

# Finishing: debts, CI and the first release

The code is accepted; what is left before a first public release is not a feature. Three
things: close the small debts recorded at the stage acceptances, get the CI green on both
platforms for the released tree, and publish the first release, `v0.9.0`, built by CI from
the tagged commit. The Windows VPN stays out: `04-tun-windows` is `blocked` on a Windows
machine, so the release says Windows is cross-built and test-compiled only, never run.

The version is the user's choice: **`v0.9.0`** — the project is almost complete, but there is
no Windows support and no manual run on Windows.

## Done when

- Every debt listed in the first child is closed, or is re-recorded in the roadmap Outcome
  with the reason it stays open; no debt disappears silently.
- `main` is pushed and both the `linux` and the `windows` workflow are green on the final
  tree; a red run is either fixed or recorded as an environment problem, never ignored.
- The `v0.9.0` tag runs `release.yml`, which builds the two archives, passes `distcheck`,
  writes `SHA256SUMS.txt` and the release notes, and the release is published with those
  assets.
- The published notes state the scope honestly: Linux live-accepted, Windows cross-built and
  test-compiled only, the SSH exit accepted against a synthetic `sshd`, the user's real
  server exercised through its HTTP CONNECT exit.
- The published text and the repository carry no personal data, dates or environment.

## Children

1. [Small debts](01-small-debts/plan.md) — the resolver hint, the delete confirmation, `0` at
   the profile question, a `dnstt_ssh` link without a credential, the `profile set` message,
   one definition of the secret suffixes, and the two tests that are not honest about what
   they check; the rest of the recorded debts stay open with a reason. Task, detailed.
2. [CI green on both platforms](02-ci-green/plan.md) — the push of `main` and green `linux` and
   `windows` runs on the final tree, a red run fixed or reported with its evidence. Task.
3. [The `v0.9.0` release](03-release/plan.md) — the lightweight tag, the CI build, the assets
   checked as downloaded, the notes with the scope stated, the publication. Task.

The order is the dependency: the runs and the tag must be on the tree the debts left behind,
so each node starts only when the previous one is accepted.

## Outcome

The stage is accepted. The debts are closed or re-recorded below, `main` is published with both
workflows green, and `v0.9.0` is published from the tagged commit with the archives CI built.

- **Debts.** Every item of the first child is closed in the code: the resolver hint names the four
  transports, the delete question says "exit key" for a key profile, `0` cancels at the `Profile:`
  question while a profile named `0` stays selectable, a `dnstt_ssh` link with no credential is
  refused by the parser before any question, `profile set` says plainly that it is not interactive
  and names no file, the secret suffixes are built in `exitSecret.secretSuffix` alone,
  `TestDialOneWriteCancel` no longer races a timer against the deadline, and `DNSTT_CLI_TEST_IPV6=1`
  turns the IPv6 skip into a failure, which the Linux `test` job sets. The list that stays open is
  repeated below with its reason.
- **CI.** `main` is published and both workflows are green on it: `linux` in `test`, `netns` and
  `core`, `windows` in `test` and `core`. `netns` reported no skip, so the network checks ran, and
  `vendorcore -check` passed on both platforms. `release.yml` did not start on the branch push. No
  fix, no flake and no environment problem was met; the IPv6 variable stayed. The run numbers are
  in the children's Outcomes; the commit that carries this Outcome is documentation only, and the
  workflows run on it as well, on the same code tree.
- **Release.** `v0.9.0` is a lightweight tag on the commit whose runs were green. `release.yml` on
  the tag built both archives, passed `distcheck` and `releasenotes`, and the draft its `release`
  job made was published. The release is not a draft and carries
  `dnstt-cli_v0.9.0_linux_amd64.tar.gz`, `dnstt-cli_v0.9.0_windows_amd64.zip` and
  `SHA256SUMS.txt`; the digests the release API reports match `SHA256SUMS.txt`. Nothing was moved,
  deleted or changed beyond the release itself.
- **Integration checks on the final tree**, run once for the whole stage: `gofmt` outside `core/`,
  both builds with and without cgo, the Linux and Windows `go vet` (the package list under the same
  `GOOS`) with `./core/...` as the workflows call it, the Windows cross-build and the four Windows
  test binaries, the whole race suite with `DNSTT_CLI_TEST_DBUS=1`, `DNSTT_CLI_TEST_NETNS=0` and
  the exact `-skip`, and `git diff --check` on the tree and on the stage's range — all clean.
  `go.mod`, `go.sum` and `core/` are untouched.
- **Published assets, checked as downloaded** from the release rather than from `dist/`:
  `sha256sum -c SHA256SUMS.txt` and `scripts/distcheck` pass; the tar.gz holds `dnstt-cli` (0755)
  with the documents, the zip the same plus `dnstt-cli.exe`, `wintun.dll` and its license, every
  entry inside the directory named after the archive; the extracted Linux binary prints `v0.9.0`
  for `version` and the module version of both binaries is `v0.9.0`.
- **Publication.** The range, the release page and the repository carry no personal data, dates of
  work, environment details, server domain or key, or resolver address; the commits of the range
  are by the project's author with a midnight-UTC date and no task key in the first line. The notes
  state the scope: Linux accepted live, Windows built and tested in CI but never run by hand and
  without `tun`, the SSH exit against an `sshd` in a disposable container, the real server through
  its HTTP CONNECT exit, macOS in proxy mode only.

Open, with the reason — recorded at an earlier acceptance, none of them closed or worsened here:

- The pause after a double click on Windows: no Windows machine to check it on
  (`05-menu-and-release/05-menu/01-menu-and-commands/plan.md`).
- The menu's buffered reader keeps a line pasted before the `sudo` child starts; the fix belongs to
  the elevation session (`05-menu-and-release/plan.md`).
- An addressed `SIGTERM` during the `sudo` child is dropped, not deferred: the parent takes no
  terminal signal while the child runs, by design (`05-menu-and-release/plan.md`).
- A deterministic test of `exit.SSH.Dial`'s single retry needs a hook in production code
  (`06-ssh-exit/plan.md`).
- `scripts/notices` reads a module's license kind from the first license text and stops on an
  unknown kind instead of guessing further, which is the intended limit (`scripts/notices/main.go`).
- A `slipnet://` link broken by a hard newline is not joined, an IPv6 literal in field 4 is not
  expressible and is refused, and the 255-byte login limit is applied by `connect` rather than by
  the import (`05-menu-and-release/plan.md`).
- `TestDefaultUTLSFingerprints` in the vendored `core/` flaked once (loopback HTTP/2) and passed on
  the rerun; `core/` stays verbatim (`05-menu-and-release/plan.md`).
- `TestConnectWithInProcessServer` logs from the core after the test returns: outside the shell,
  and the core is not edited (`05-menu-and-release/plan.md`).
- The README's macOS row recorded as disagreeing with `doc/architecture.md` §09 is closed: the
  current text agrees, so nothing was left to fix (`05-menu-and-release/plan.md`).

Recorded at the same acceptances but not in the first child's list, and still open:

- The redial log in `core/client/redial.go` says `dial tls:` for a plain TCP resolver, though the
  transport is not TLS (`05-menu-and-release/plan.md`). `core/` is vendored and stays verbatim.
- Switching the authentication method of an existing profile from the menu stays out of scope
  (`06-ssh-exit/plan.md`): the menu's secret item follows the profile's current shape, and the
  method is changed by hand in `profiles.toml`.
- `payload_size` is not asked by the wizard and is written only by the import, and `resolvers
  check` builds the core config without the cap (`05-menu-and-release/plan.md`).
- The elevated connection has no mid-session resolver offer and `menu -connect` and `connect` do
  not elevate or ask a resolver question: the child reports nothing back, there is no IPC
  (`05-menu-and-release/plan.md`).
- The third Ctrl+C in the menu does nothing, and `sudo` resets the environment, so the elevated
  child reads the invoker's `~/.config` rather than a custom `XDG_CONFIG_HOME`
  (`05-menu-and-release/plan.md`).

Acceptance limits carried over and stated in the release notes where they matter: the `tun`
datapath with an SSH exit was never exercised live, the SSH acceptance ran through the local SOCKS5
listener (`06-ssh-exit/plan.md`); SSH channel types other than `direct-tcpip`, long-lived sessions,
a reconnect after the SSH connection died, the wrong-password and wrong-key paths and any `sshd`
other than the container's were not exercised; nothing was run by hand on Windows, and the Windows
`tun` mode does not exist yet.

Found at acceptance and left to the lead, whose documents this stage does not edit:

- `README.md` says the Windows tests are compiled but that nothing was run on Windows. The `test`
  job of `windows.yml` runs the suite on a Windows runner, so the tests do run there; the sentence
  contradicts the platform row above it and the release notes, which say so. It came in with the
  README of this stage and sits inside the `v0.9.0` archive, where a correction cannot reach it
  without moving the tag. This node's `## Done when` carries the same wording — "Windows
  cross-built and test-compiled only" — and needs the same correction.
- `README.md` and the new `profile set` message name the menu and the wizard as the ways to change
  a profile. The menu changes the resolvers, the secret, the pinned fingerprint and the domain or
  key; it does not change the datapath, the exit type or login, or the authentication method, and
  `profile add` refuses a name that already exists. A person who wants to switch a profile from
  `socks` to `tun` is sent in a circle, and the file — the only path left — is named nowhere now
  that `profile set` stopped naming it. A sentence in the README and in the command's message, or a
  menu item, closes it; `internal/cli/wizard.go`'s "use 'profile set' to edit it" belongs with it,
  and alone it would only shorten the circle.
- `doc/roadmap/README.md` lists the roadmap as `blocked` while the root plan is `in-progress` with
  "State: finishing". The root's own text says it returns to `blocked` when this stage is done, and
  then the table is right again; until the root is changed the two disagree.
