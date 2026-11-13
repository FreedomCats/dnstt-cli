---
kind: task
status: done
detailed: true
depends: [02-ci-green]
---

# The `v0.9.0` release

## Task

Tag the green commit, let `release.yml` build the two archives, check what was published, and
publish it with notes that say honestly what the release is. `v0.9.0` is the user's choice of
version: the project is almost complete, but there is no Windows support and no manual run on
Windows, so `1.0` would be a claim the project cannot back.

The tag is lightweight: an annotated tag stores the name, the email and the date of whoever
made it, and none of that may get into the repository (`AGENTS.md`). The build runs in CI from
the tagged commit — `scripts/release` with `-version v0.9.0`, `scripts/distcheck`,
`scripts/sha256sums`, `scripts/releasenotes` — and the draft release is made by the `release`
job of `.github/workflows/release.yml:103-134`; publishing the draft is done here.

## Steps

1. **Preconditions.** The previous node's runs are green on `origin/main`; `git status
   --short` is empty; `git rev-parse HEAD` equals `git rev-parse origin/main`; `git tag -l
   'v*'` prints nothing (there is no release yet, and a tag is never moved or deleted here).

2. **The tag.** `git tag v0.9.0` — no `-a`, no `-s`, no `-m`. Check it: `git cat-file -t
   v0.9.0` prints `commit`, `git tag -l v0.9.0` prints the name, and `git rev-parse v0.9.0`
   is the commit from step 1.

3. **Push the tag alone.** `git push origin v0.9.0`. No branch push, no `--force`.

4. **Watch the `release` run.** `gh run list --workflow release.yml --limit 3`, then
   `gh run watch <id>`. The jobs: `test` (calls `windows.yml`), `test-linux` (calls
   `linux.yml`), `build`, `release`. `build` fails if `THIRD_PARTY_NOTICES.txt` is stale
   (`git diff --exit-code` after `scripts/notices`), if `scripts/release` cannot fetch the
   pinned `wintun` archive, if `distcheck` finds a missing or extra file, or if
   `releasenotes` sees archives of different versions. A red job here is read and fixed the
   way the previous node fixes one — but a fix now means a new commit, a new tag on it and
   deleting the old tag is **not** allowed: if the tagged commit is wrong, stop and report;
   the user decides whether the tag is moved.

5. **Check what was published.** `gh release view v0.9.0 --json tagName,isDraft,assets` — the
   draft must carry `dnstt-cli_v0.9.0_linux_amd64.tar.gz`,
   `dnstt-cli_v0.9.0_windows_amd64.zip` and `SHA256SUMS.txt`. Download them to a directory
   outside the repository (`/tmp/opencode/release-v0.9.0`), then: `sha256sum -c
   SHA256SUMS.txt`, and `go run ./scripts/distcheck -dir <that directory>` — the check of the
   assets as downloaded, not of the build directory. Read the archives too: the tar.gz holds
   `dnstt-cli` (0755) with `README.md`, `LICENSE`, `COPYING`, `THIRD_PARTY_NOTICES.txt` and
   `dnstt-cli.example.toml`; the zip holds `dnstt-cli.exe`, `wintun.dll`, `wintun-LICENSE.txt`
   and the same documents; every entry is inside the directory named after the archive; the
   Linux binary prints `v0.9.0` (not `dev`) for `version`.

6. **The notes state the scope.** The draft body is the "Download" block from
   `scripts/releasenotes` plus the generated commit notes. Add a short section above it, in
   English, with no dates, names, machines or addresses:
   - Linux is live-accepted: `socks` and `tun`, the SOCKS5 exit, the HTTP CONNECT exit with
     Basic authentication and the SSH exit;
   - Windows is built and its tests are compiled and run in CI, but nothing was run by hand on
     Windows, and `tun` is not available there — the Windows archive carries `wintun.dll` for
     the stage that will use it;
   - the SSH exit was accepted against a synthetic `sshd`, not a real one;
   - the real server was exercised through its HTTP CONNECT exit (its exit is an HTTP proxy
     and it has no `sshd`);
   - macOS builds in the proxy mode only and is not checked live;
   - `v0.9.0` rather than `1.0` because the Windows `tun` mode is not done.
   Compose it with `gh release view v0.9.0 --json body -q .body > /tmp/opencode/release-body.md`,
   edit that file, then `gh release edit v0.9.0 --notes-file /tmp/opencode/release-body.md` —
   the file stays outside the repository and is not committed.

7. **Publish.** `gh release edit v0.9.0 --draft=false`, then `gh release view v0.9.0 --json
   isDraft,url,assets` — not a draft, the three assets present, the Download links in the body
   pointing at the tag. Open the page and read it once as a stranger would: the scope section,
   the links, no personal data, no date of work, no server domain or key, no resolver address.

8. **Nothing beyond.** No push to a branch, no tag deletion or moving, no change to the
   repository's settings, description or topics, no edit of another release, no upload of an
   asset that CI did not build.

9. **Record in the Outcome**: the tag, the commit it points at, the run number of the `release`
   workflow, the asset names with their SHA-256 from `SHA256SUMS.txt`, and the final URL.

## Done when

- `v0.9.0` is a lightweight tag on the commit whose `linux` and `windows` runs are green, and
  it was pushed without moving or deleting anything.
- The `release` workflow succeeded on the tag and the release is published, not a draft, with
  the Linux archive, the Windows archive and `SHA256SUMS.txt`.
- `sha256sum -c` and `scripts/distcheck` pass on the downloaded assets; the Windows archive
  holds `wintun.dll` and `wintun-LICENSE.txt`; the Linux binary prints `v0.9.0`.
- The published notes state the scope honestly — Linux live-accepted, Windows built and tested
  in CI only, the SSH exit against a synthetic `sshd`, the real server through its HTTP CONNECT
  exit — and carry no date, name, environment, address or key.
- The Outcome names the tag, the commit, the run number and the assets with their checksums.

## Outcome

`v0.9.0` is published. The tag is lightweight and sits on the commit whose `linux` and `windows`
runs were green; the `release` workflow built the archives from that commit and the draft it made
was published here.

- Tag `v0.9.0` → `610ffd1502d7f7eafd3e7a93ceed1ab3c86da5ef`: `git cat-file -t v0.9.0` prints
  `commit`, `git rev-parse v0.9.0` was `HEAD` and `origin/main` at the time of tagging. It was
  pushed alone (`git push origin v0.9.0`); nothing was moved or deleted.
- `release` run
  [#43342483073](https://github.com/FreedomCats/dnstt-cli/actions/runs/43342483073) on the tag:
  `test`, `test-linux`, `build` and `release` green on the first attempt, nothing had to be
  fixed.
- Published at <https://github.com/FreedomCats/dnstt-cli/releases/tag/v0.9.0>: not a draft, three
  assets.
- Nothing beyond: no branch push, no tag deletion or move, no change to the repository's
  settings, description or topics, no edit of another release, no asset uploaded by hand.

The assets were checked as downloaded from the release, not from `dist/`:

| Asset | SHA-256 |
| --- | --- |
| `dnstt-cli_v0.9.0_linux_amd64.tar.gz` | `d27d4a7b77723093d2d5bda0bd2870508daa7c03d4b0121154e66fa251540a00` |
| `dnstt-cli_v0.9.0_windows_amd64.zip` | `aa6c8394f124cac594a834d87fe8247a81020932aae6a4dcf2e0323c97e4b5cb` |

- `sha256sum -c SHA256SUMS.txt` passes on both, and so does
  `go run ./scripts/distcheck -dir <the download directory>`; `SHA256SUMS.txt` lists exactly the
  two archives. The digests match the ones the release API reports for the uploaded assets.
- The tar.gz holds `dnstt-cli` (0755), `README.md`, `LICENSE`, `COPYING`,
  `THIRD_PARTY_NOTICES.txt` and `dnstt-cli.example.toml`; the zip the same documents plus
  `dnstt-cli.exe`, `wintun.dll` and `wintun-LICENSE.txt`. Every entry is inside the directory
  named after the archive, and there is nothing extra.
- The extracted Linux binary prints `dnstt-cli v0.9.0 (610ffd1)` for `version`; `go version -m`
  on both binaries reports module version `v0.9.0`, the tagged revision and an unmodified build.

The notes are the `Download` block from `scripts/releasenotes` and the generated changelog, with a
`## Scope of this release` section above them: Linux accepted live with `socks` and `tun` and all
three exits; Windows built and tested in CI only, never run by hand, and without `tun`; the SSH
exit accepted against an `sshd` in a disposable container; the real server exercised through its
HTTP CONNECT exit; macOS in proxy mode only; and why the version is `0.9.0`. The page was read
after publishing: every link answers with `200`, and the body carries no date of work, name,
environment, address, server domain or key. The only date on the page is GitHub's own "released
this" line.

What was learned on the way:

- A draft release made by the `release` job has a placeholder URL (`/releases/tag/untagged-…`);
  it becomes `/releases/tag/v0.9.0` when the draft is published.
- `gh release download` outside a git repository needs `-R <owner>/<repo>`.
- The workflow needed no fix: `THIRD_PARTY_NOTICES.txt` was current, the pinned `wintun` archive
  was fetched, `distcheck` and `releasenotes` passed, and the cross-built Windows archive went
  through the same `distcheck` as the Linux one.

Left for later, and why: a manual run on Windows and the Windows `tun` mode — the stage that will
use the `wintun.dll` this archive already carries. The scope section of the notes says so instead
of claiming a Windows acceptance the project does not have, and that is why the version is
`0.9.0` rather than `1.0`.

This Outcome is documentation only and is committed to `main` after publishing; the tag stays on
`610ffd1`.
