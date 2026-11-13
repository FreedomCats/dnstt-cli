---
kind: stage
status: in-progress
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
