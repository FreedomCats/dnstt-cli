---
kind: task
status: todo
detailed: true
depends: [04-slipnet-import, 05-menu, 06-elevation, 07-license]
---

# Release archives and `distcheck`

## Task

The release machinery: `scripts/release`, `scripts/notices`, `scripts/releasenotes`,
`scripts/distcheck` and the `.github/workflows/release.yml` job that runs them. The scripts and
the workflow are carried from csqtt-cli (MIT) and adapted: one Go binary instead of a shell
plus a Rust core, two platforms, `LICENSE` and the core's CC0 as `COPYING`. qwdtt-cli is read
as behaviour only; its GPL-3 code is not copied (`AGENTS.md`).

The release is built on Linux: `linux/amd64` natively and `windows/amd64` by cross-build, both
with `CGO_ENABLED=0`, so no Windows machine is involved and no Windows run is claimed. The
Windows archive carries `wintun.dll`: `scripts/release` fetches wintun 0.14.1 itself through
`scripts/internal/wintun` — the pinned archive, its SHA-256 checked before unpacking — so the
build that makes the archive downloads it, and nobody puts a zip next to an unpacked release.
`-wintun-archive` gives the same archive from an already downloaded zip without network.

Tag, push and publication are the user's actions; nothing in this task pushes, tags or
publishes.

## Steps

1. **The archive layout** — new `scripts/internal/dist/dist.go`:
   - `Docs` — the files in every archive: `README.md`, `LICENSE`, `COPYING` (the core's
     `core/COPYING`, flattened), `THIRD_PARTY_NOTICES.txt`, `dnstt-cli.example.toml`;
   - `Wintun` — `wintun.dll`, `wintun-LICENSE.txt`;
   - `Executables(goos)` — `dnstt-cli.exe` for Windows, `dnstt-cli` otherwise;
   - `Files(goos)` — the executables, for Windows the Wintun pair, and the docs. One list for
     `release` and `distcheck`, so they cannot drift; the package is the sibling's shape,
     reduced to one binary.
2. **`scripts/notices`** — carried from csqtt-cli and reduced (no Rust part):
   - `-dest` (required). The module list is what really links into the binary — the union of
     `go list -deps -json ./cmd/dnstt-cli` for `linux/amd64` and `windows/amd64` with
     `CGO_ENABLED=0`, so a Windows-only dependency is not missed — not `go list -m all`, which
     includes test-only modules;
   - the Go runtime and standard library first, their `LICENSE` and `PATENTS` taken from
     `GOROOT`; then each module's license text, sorted stably; then the shell's `LICENSE`
     (MIT) and the core's CC0, whose text the archive carries as `COPYING` (the repository's
     `core/COPYING`);
   - writes `THIRD_PARTY_NOTICES.txt`, which is committed; the script regenerates it byte for
     byte. Tests: a fixture module list gives the expected order and text, and the committed
     file matches a regeneration.
3. **`scripts/release`** — carried and reduced (no `-core` flag, no `buildcore`):
   - flags: `-version` (default `git describe --tags --always --dirty`), `-out` (default
     `dist` in the repository root, made absolute at once), `-platforms` (default
     `windows/amd64,linux/amd64`), `-nozip`, `-wintun-archive`;
   - per platform: `CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> go build -trimpath -ldflags "-s -w
     -X main.version=<version> -X main.commit=<short commit>"` into
     `dist/dnstt-cli_<version>_<os>_<arch>/`, then the docs from `scripts/internal/dist` copied
     beside it;
   - Windows: `wintun.Fetch(wintun.Options{Arch: "amd64", Dest: dir, Archive: o.WintunArchive})`
     from `scripts/internal/wintun` (pinned 0.14.1, archive SHA-256 checked; the DLL's
     Authenticode signature is checked where the fetch runs on Windows, and a Linux cross-build
     relies on the archive hash);
   - archives: `dnstt-cli_<version>_linux_amd64.tar.gz` and
     `dnstt-cli_<version>_windows_amd64.zip`, every entry inside a directory named after the
     archive, `/` separators in the zip, executable bits 0755 for the Linux binary and 0644 for
     everything else; the archives are written in Go, not by a system `tar`/`zip`;
   - at the end the binary of the host platform runs `version`, and the script fails if the
     version and commit did not arrive through `-ldflags`; `-nozip` skips the archives and
     `wintun` and leaves the directories only. The `THIRD_PARTY_NOTICES.txt` in the archives
     is the committed file; the workflow checks it is current (step 7).
4. **`scripts/distcheck`** — carried and reduced: `-dir` (default `dist`), at least one archive
   or an error; for each archive the entry names must sit inside the archive's directory and
   use `/`, the files must be exactly `dist.Files(goos)` (no missing, no extra), the tar.gz
   executable bits must be right, and the Windows archive must hold `wintun.dll` and
   `wintun-LICENSE.txt`; the Linux binary is unpacked and run — `version`, `help`, and no
   arguments with redirected stdin and stdout printing the command list and exiting 0; the
   Windows exe is checked by its PE magic and file list, never run; problems print as
   `::error::` annotations and the exit code is 1.
5. **`scripts/releasenotes`** — carried and reduced: `-dir`, `-repo`, `-out`; writes the
   "Download" block of the release description — the Windows archive, the Linux archive, the
   sources and `SHA256SUMS.txt`, in that order, linked as
   `https://github.com/<repo>/releases/download/<tag>/<name>` (the sources as
   `.../archive/refs/tags/<tag>.tar.gz` and `.zip`) — because GitHub sorts assets
   alphabetically and puts the sources last; the tag comes from the archive names, and mixed
   versions, no archives or a missing `SHA256SUMS.txt` are errors. The "what changed" part is
   added by `gh release create --generate-notes`, so nothing dated or environmental is
   committed.
6. **`scripts/sha256sums`** — already carried, takes `-dir` (default `dist`), writes
   `SHA256SUMS.txt` with LF and `sha256sum`-compatible lines; its comment names the
   `SHA256SUMS.txt` step of `release.yml`, which becomes true with step 7. No change is
   needed.
7. **`.github/workflows/release.yml`** — new, the sibling's shape (MIT):
   - triggers: `push: tags: ['v*']`, `push: branches: ['release/**']`, `workflow_dispatch`;
     top-level `permissions: contents: read`; actions pinned by commit hash, as in
     `linux.yml`/`windows.yml` (the upload and download actions take the sibling's pinned
     commit for their major version);
   - jobs `test` (`uses: ./.github/workflows/windows.yml`) and `test-linux`
     (`uses: ./.github/workflows/linux.yml`) — the existing workflows through their
     `workflow_call`, no duplicated test steps;
   - job `build` (needs both, `ubuntu-latest`): checkout with `fetch-depth: 0` so `git
     describe` sees the tags; setup-go from `go.mod`; `go run ./scripts/notices -dest .` then
     `git diff --exit-code -- THIRD_PARTY_NOTICES.txt`, so a stale notices file fails the
     build; on a tag `go run ./scripts/release -version "$GITHUB_REF_NAME"`, otherwise
     `go run ./scripts/release`; then
     `go run ./scripts/distcheck -dir dist`, `go run ./scripts/sha256sums -dir dist`,
     `go run ./scripts/releasenotes -dir dist -repo "$GITHUB_REPOSITORY" -out
     release-notes.md`; upload the `dist` archives with `SHA256SUMS.txt` and the notes file as
     artifacts; the wintun download is inside `scripts/release`, no separate step;
   - job `release` (needs `build`, only on a tag push, `permissions: contents: write`):
     download the two artifacts and run `gh release create "$TAG" dist/* --draft
     --verify-tag --title "$TAG" --notes-file notes/release-notes.md --generate-notes`. It
     runs no repository code. The draft is published by hand; this task neither tags, pushes
     nor publishes.
8. **README** — drop "In development. Nothing works yet"; add what the program does now (the
   menu and the commands, `profile import`), the platform matrix as it stands (Linux
   live-accepted; Windows built and CI-tested, `tun` on Windows gated by the blocked stage;
   macOS `socks` only), the archives, `SHA256SUMS.txt` and `wintun.dll` next to the Windows exe,
   and the license and notices. The leak matrix stays.
9. **Checks** — script tests (`go test ./scripts/...`), `go build ./...`, the CI vet split, the
   full race matrix with the stated skips, the Windows cross-build and `git diff --check`; a
   local `scripts/release` run followed by `distcheck` and `sha256sums` (the build downloads
   wintun, or takes `-wintun-archive`); the workflow file is parsed as YAML and read against
   the sibling's, and is not run from this tree.

Easy to get wrong:

- the zip entry names must carry the archive's directory and `/`, or a Linux unzip writes files
  with backslashes in their names;
- the version and commit must reach `cmd/dnstt-cli` through `-ldflags`; `distcheck` fails a
  binary whose `version` still says `dev` when a version was given;
- `-trimpath`, or the build machine's path is inside the binary;
- `scripts/release` must run the docs to the directory it builds, not leave them to the
  checkout, and use one list with `distcheck` (`scripts/internal/dist`);
- the release job must keep `contents: write` to itself and must not run repository code; the
  test and build jobs stay read-only;
- a `workflow_dispatch` run on a tag builds artifacts only: `gh release create` runs only on
  `push` of a tag, as in the sibling.

## Done when

- `scripts/notices` regenerates the committed `THIRD_PARTY_NOTICES.txt` byte for byte: the Go
  standard library, every module that links into the binary, the shell's MIT and the core's
  CC0 (`COPYING`).
- `scripts/release -version vX.Y.Z` builds both archives into `dist/` with the documented
  contents, the version and commit in the binary, and `wintun.dll` with its license in the
  Windows one, fetched by the build through `scripts/internal/wintun` with the pinned archive
  hash checked; `-wintun-archive` builds the same archive offline; `-nozip` leaves the
  directories only.
- `scripts/sha256sums` writes `dist/SHA256SUMS.txt` and `sha256sum -c` passes.
- `distcheck` exits 0 on that output and fails on a missing file, an extra file, a missing
  `wintun.dll` or its license, a forbidden path, a wrong executable bit, or a binary whose
  `version` still says `dev`; it runs the Linux binary's `version`, `help` and the no-terminal
  command list, and does not run the Windows exe.
- `scripts/releasenotes` writes the download block for the archives and `SHA256SUMS.txt` with
  no dates; a mixed version set and an empty directory are refused.
- `.github/workflows/release.yml` exists with the two `workflow_call` test jobs, the build job
  that runs the scripts and uploads artifacts, and a draft-release job limited to a tag push
  with `contents: write`; actions are pinned by commit hash; no tag, push or publication is
  made here, and the file is not run from this tree.
- The README states the current behaviour, the archives and the platform matrix, and no
  "in development" line remains.
- The local matrix of step 9 passes; `dist/` stays ignored.

What is needed from the user:

- the first workflow run is theirs: a `v*` tag or a `release/**` branch push, or a manual
  dispatch, and publishing the draft is manual;
- the architectures are `linux/amd64` and `windows/amd64`; more are a later decision.
