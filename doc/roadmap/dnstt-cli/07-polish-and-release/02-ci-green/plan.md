---
kind: task
status: done
detailed: true
depends: [01-small-debts]
---

# CI green on both platforms

## Task

Put the final tree on `origin/main` and get the two workflows green on it. The repository is
public and `main` is the branch a stranger clones: a red `linux` or `windows` run on the commit
that becomes the release is not a detail to note, it is a reason not to tag. A red run is
either fixed in the code or in the workflow, or reported as an environment problem with the
evidence; it is never left standing and never silenced.

`main` is 19 commits ahead of `origin/main` before this node, no workflow has run on any of
them, and the tree has never been pushed in this shape. The push is allowed by the user for
this stage; nothing beyond it is.

## Steps

1. **Read the range before it is published.** `git log --format='%h %an <%ae> %s'
   origin/main..main` — every commit must be by the project's author and carry no task key.
   Then scan what the range adds, `git log -p origin/main..main`, for the things `AGENTS.md`
   forbids in a public repository: home and profile paths, machine and OS details, dates of
   work in prose, non-example domains, IP literals outside `192.0.2.0/24`, `198.51.100.0/24`,
   `203.0.113.0/24` and `2001:db8::/32`, resolver addresses, server domain and public key,
   exit credentials, key material. `git diff --check` on the range. Nothing found — go on; a
   finding — stop and report it: history is not rewritten here, and the commit is not pushed.

2. **Push `main`, and only it.** `git push origin main`. No other branch, no `--force`, no
   tags in this node (the tag belongs to the next one).

3. **Watch both workflows on that commit.** `gh run list --branch main --limit 5`, then
   `gh run watch <id>` for each: `linux` must be green in all three jobs (`test`, `netns`,
   `core`) and `windows` in both (`test`, `core`). `netns` turns a skip into a failure
   (`.github/workflows/linux.yml:98-107`), so a green `netns` means the network checks ran.
   `release.yml` must not start: it listens to `v*` tags and `release/**` branches only.

4. **A red run is read, not re-run.** Take the failing step's log
   (`gh run view <id> --log-failed`), name the cause, and fix it:
   - a code or test failure — fix the code on `main`, commit, push, and go back to step 3;
   - a workflow failure — repair the step, and keep the check: no `continue-on-error`, no
     `if: false`, no removed job, no `|| true` on a command whose result matters. A step that
     cannot work in the runner is replaced by one that does, not disabled;
   - an environment failure — the netns container's `--device /dev/net/tun` or `NET_ADMIN`,
     a missing `dbus-daemon`, the core mirror at the commit of `core/CORE_VERSION` being
     unreachable, a runner without IPv6 (the `DNSTT_CLI_TEST_IPV6` line added by the previous
     node): record the exact message and decide in the open — install what is missing in the
     workflow, or report it as a blocker. Removing the check is the last resort and needs its
     reason in the Outcome.
   A flake is a finding too: a test that fails once and passes on the rerun is named in the
   Outcome with the test and the symptom, and is not counted as green by itself — the rerun
   that passes is, once the cause is known or recorded.

5. **Re-check the tree the runs were made on.** After the last push, `git status --short` is
   empty, `git rev-parse HEAD` equals `git rev-parse origin/main`, and the last `linux` and
   `windows` runs point at that commit.

6. **Record in the Outcome**: the commit hashes that were pushed, the run numbers and URLs of
   the green `linux` and `windows` runs, what was fixed on the way (with the failing step), and
   every flake or environment problem met, with its cause or its open reason.

## Done when

- `origin/main` carries the final tree of the previous node, and the working tree is clean.
- The `linux` run on that commit is green in `test`, `netns` and `core`, and the `windows` run
  in `test` and `core`; `netns` reported no skip and `vendorcore -check` passed.
- `release.yml` did not run for the push, and no branch other than `main` was pushed, no tag
  was created and no repository setting was changed.
- The Outcome names the commits, the run numbers and every fix, flake or environment problem
  with its cause or its recorded reason.

## Outcome

`main` is published and both workflows are green on the published tree. Nothing had to be
fixed: the tree the debts left behind passed the first run.

- Pushed `65b5149..e4f8114`, 23 commits: the 22 that were ahead of `origin/main` and this
  node's plan commit, `e4f8114` (`Start the CI green task`). Every commit is by the project's
  author with a midnight-UTC date and no task key.
- The range was read before it was published: `git diff --check` clean, and no home or profile
  path, machine or OS detail, date of work in prose, non-example domain, IP literal outside the
  documentation ranges, resolver of the developer's own, server domain or public key, exit
  credential or key material. Cloudflare's public DNS appears as the shipped `tun_dns` default,
  as a preset and as a live-test target; it is public infrastructure that names no ISP or
  region, and it predates this node.
- `linux` run [#43341165222](https://github.com/FreedomCats/dnstt-cli/actions/runs/43341165222)
  on `e4f8114`: `test`, `netns` and `core` all green.
- `windows` run [#43341165115](https://github.com/FreedomCats/dnstt-cli/actions/runs/43341165115)
  on `e4f8114`: `test` and `core` green.
- The IPv6 experiment of the previous node is answered by that run: the `test` job passed with
  `DNSTT_CLI_TEST_IPV6=1`, and under the variable `TestApplyReadsV6Only` fails instead of
  skipping when the kernel has no IPv6 socket or the socket comes out IPv4. A green job
  therefore means the runner has IPv6 and both subtests ran. The line stays in
  `.github/workflows/linux.yml`.
- `netns` reported no skip: the step fails on any `--- SKIP` and on a log without `--- PASS`, and
  it passed with both test binaries run under `-test.v` in the container with `NET_ADMIN`,
  `/dev/net/tun` and IPv6 switched on. `vendorcore -check` passed in `core` on both platforms
  against the mirror at the commit of `core/CORE_VERSION`.
- No fix, no flake and no environment problem was met. `release.yml` did not start: the push was
  `main` only, it listens to `v*` tags and `release/**`, and no tag was created. No repository
  setting was changed.
- After the runs the working tree was clean and `HEAD` equalled `origin/main`. The commit that
  carries this Outcome is documentation only; the workflows run on it as well, on the same code
  tree.
