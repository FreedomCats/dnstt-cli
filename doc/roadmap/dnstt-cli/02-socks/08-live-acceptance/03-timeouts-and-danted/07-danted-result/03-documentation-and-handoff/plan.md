---
kind: task
status: done
depends: [02-profile-key-and-binding]
---

# Documentation and handoff

## Task

The accepted contract of the [parent stage](../plan.md#accepted-contract) requires R3 and
the reserved example to carry the measurement (`danted` 1.4.4) and its limits. This task
writes the shipped setting into the architecture and the example after
[profile key and binding](../02-profile-key-and-binding/plan.md) lands, so the text states
what exists and what a user sees. It is documentation only: no code, no key default and no
product behavior changes here, and the code comment in `internal/exit/socks5.go` belongs to
[one-write exchange](../01-one-write-exchange/plan.md).

What changes:

- `doc/architecture.md` R3 (`doc/architecture.md:101`): the mode is no longer "turned on
  only if `danted` is seen to take it (live acceptance)", because the measurement is done
  and the user accepted the setting. State `exit.one_write` (socks5 only, default `false`):
  greeting, RFC 1929 login and `CONNECT` in one write; with a login the greeting offers only
  `0x02` and the password is sent before the method is confirmed — the documented price;
  without a login `0x00` and the `CONNECT`. State the measurement and its scope: `danted`
  1.4.4 built from the upstream archive with PAM, one login, a loopback environment, a
  verified target; this version and these conditions only, not the user's HTTP exit, other
  versions or deployments, TCP segmentation or the server's unobserved `recv`; no live gate
  is closed. State the failure behavior: an exit that chooses another method, refuses the
  login or does not take the batch gives an error naming the key, with no automatic
  fallback and no second stream; the startup `Check` stays sequential because it has no
  target.
- `doc/architecture.md` §10 (`doc/architecture.md:549`): `one_write = false` in the example
  profile's exit section with a short comment, matching name, type and default exactly, and
  a bullet next to the existing exit notes that names the applicability and the error rule.
- `dnstt-cli.example.toml` (`dnstt-cli.example.toml:22`): the same key with a short comment;
  the example is the reserved one of this repository, with no real value.
- Handoff for [Recording the results](../../../05-record-results/plan.md): R3 now states the
  two exchange modes that later product acceptance runs see (the sequential default and the
  one-write mode) and says the result is synthetic and environment-scoped, with attribution
  to the independent tool left where it is; the live SOCKS5 matrix stays with
  [Live runs](../../../04-live-runs/plan.md). This task names the paragraphs and lines for
  that later step but does not change R2, the core documentation or any other node.

## Done when

- R3 no longer awaits a live acceptance; it states the key, its default, socks5-only
  applicability, the early-credential price, the failure behavior and the measured service
  with the limits of the result, so a reader can tell which `danted` was measured and what
  the result does not cover.
- The §10 example and `dnstt-cli.example.toml` carry `one_write` with its default and
  applicability, and the key name, type, default and validation rule match
  `internal/config/profiles.go` exactly.
- No work date, environment, address, real domain, key, resolver, login or profile name
  appears; only reserved and documentation values are used, the text is English, links and
  anchors resolve, and `git diff --check` passes.
- The handoff text names which R3 paragraph and example lines
  [Recording the results](../../../05-record-results/plan.md) uses, leaves R2 and the core
  documentation to that node, and keeps the live SOCKS5 matrix with
  [Live runs](../../../04-live-runs/plan.md).
- No code, test or configuration behavior changes; the documentation-only checks of the
  stage's [local validation](../../plan.md#local-validation-for-code-children) pass (links,
  consistency, privacy), with no build required.

## Outcome

R3 now describes the shipped setting instead of a future check: `one_write = true` in the
exit section, off by default and `socks5` only, the one write of greeting, login and
`CONNECT`, the early-credential price of the login mode, the unchanged error classes, no
sequential retry, and the measured `danted` 1.4.4 built from source in a disposable
synthetic environment with the explicit limits (no other versions or deployments, no TCP
segmentation, no claim about the server's `recv` calls). The `socks5` readiness bullet in
§08 says the readiness check stays sequential and sends no `CONNECT`, so the optional mode
cannot hide a startup target. §10's example and `dnstt-cli.example.toml` carry
`one_write = false` in `[profile.home.exit]` with the reserved value and a one-line meaning.
The exit-key list in the parent stage
([`03-timeouts-and-danted`](../../plan.md)) now names `one_write` as well.

Handoff for [Recording the results](../../../05-record-results/plan.md): it uses R3's
`socks5` bullet and the paragraph on the one-write setting and its measured scope, the
`one_write` line of `[profile.home.exit]` in both the architecture example and
`dnstt-cli.example.toml`, and the §08 readiness sentence. R2, the core documentation, the
timeout values and the live SOCKS5 matrix stay with that node and with
[Live runs](../../../04-live-runs/plan.md); nothing here settles them.

Documentation-only: no code, test, configuration or default changed. Links and anchors
resolve, the text is English, no work date, environment, address, real domain, key,
resolver, login or profile name appears, and `git diff --check` passes.
