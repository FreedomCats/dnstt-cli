---
kind: task
status: done
detailed: true
depends: [03-danted-probe]
---

# Synthetic `danted` one-write check

## Task

Answer the SOCKS5 one-write question for `danted` in a disposable synthetic environment,
because the user's server has no `danted`: its exit is an HTTP proxy, and the live node
[Live one-write test against danted](../04-danted-measurements/plan.md) is dropped for that
reason. On the user's decision this bounded result replaces the marked-inapplicable answer,
another real SOCKS5 exit and deferral.

The result is about one `danted` version under recorded conditions. It is not about the
user's exit and not about every `danted` deployment, and it must say so. The accepted
[method](../01-measurement-method/plan.md) and [probe](../03-danted-probe/plan.md) are used
unchanged: neither is reworked, no product setting is chosen and no profile key, default,
authentication policy or fallback is decided here.

The environment is entirely local and disposable: a `dnstt-server` built from the vendored
`core/server` (`core/server/main.go:857` has `-gen-key`, `-udp` and `-privkey-file`) with the
reserved domain `t.example.com` and a generated key; a container with the source-built
`danted` and one local login; the ignored `danted-probe`; a synthetic HTTP target. The
user's server, profile, secret store, resolvers and network do not participate, and no real
value is read or written.

The user granted the fetch permission for this run. Debian trixie ships no `dante-server`
package at all, so the user directed that the service be built from source instead: the
source archive, its build dependencies and the local account tooling are fetched and
installed only inside the disposable container, which uses the `debian:trixie` image already
present locally. The exact `danted` version and the source digest are recorded and published
with the result. No other image, package or download is authorized.

The agent performs the run. Everything is loopback or container-internal with generated
values, the tool is already exercised by agents on generated fixtures, and a user run would
add no evidence while bringing their environment into the record. The one thing that is not
assumed is the network fetch: fetching the `danted` source archive and its build dependencies
into the disposable container needs the user's explicit permission as its own step, recorded
before anything is fetched. If that permission or a container runtime is unavailable, the
node stops and reports — it does not silently switch to a user run, to the user's HTTP exit,
or to a local fixture outcome.

## Steps

1. Obtain and record the user's explicit permission for the single network fetch (the
   container image and the `danted` source archive with its build dependencies) before any
   of it happens, with them identified in advance. Without it, stop and report: no pull, no
   fetch, no substitute service. The fetch goes only into the disposable container.
2. Build the tunnel server from the vendored `core/server` with cached dependencies, generate
   a keypair with `-gen-key` into an ignored private directory, and start
   `dnstt-server -udp 127.0.0.1:<port> -privkey-file <key> t.example.com <danted address>`.
   Use a free high loopback port. The client side then reaches the tunnel directly with the
   resolver `udp:127.0.0.1:<port>`, which `internal/resolvers/resolvers.go:130` accepts; no
   external resolver or name lookup is involved.
3. Start the exit and the target in the container: `danted` with one local account and
   username/password authentication enabled (the probe offers only method `0x02`), its
   permitted destination limited to the synthetic target, logging to its own stderr. The
   tunnel's upstream address is danted's SOCKS port. The target is a plain `http://` listener
   with a fixed body file, reachable from danted under the name the probe's URL uses (a
   container-network alias or a pinned entry in the container's hosts file). The probe adds
   no trust flags for a local CA, so the target stays `http://`; after the build nothing
   needs egress. Record the `danted` version and the image identifier privately.
4. Prepare the isolated client side: a temporary `XDG_CONFIG_HOME` with Secret Service
   discovery suppressed, so the store is file-backed — the same isolation the selftest uses
   (`doc/runs/_live/selftest.go:252`). Write a synthetic profile with the reserved domain, the
   generated public key, `exit.type = "socks5"`, a synthetic user and `password_ref`, the
   loopback resolver above and a loopback local SOCKS listener; put the synthetic exit
   password into that store with the tool's `secret` command. Compute the target body's
   SHA-256. Confirm the profile loads and that no real profile, config or store is read,
   statted or written.
5. Qualify the environment before measuring: an authenticated sequential `CONNECT` through
   the tunnel returning the verified target body — the sequential control of the first pair
   of the accepted probe is enough. A failure here is an environment or configuration fault,
   not a one-write answer: diagnose it, and if it is not repaired the node ends inconclusive.
6. Run the paired measurement with the accepted probe: an explicit pair count fixed before
   the run (at least five), positive `-dial-limit`, `-keepalive-limit` and `-trial-limit`,
   `-allow-early-credentials`, the target URL and its `-expect-sha256`. Keep every attempted
   path, ordinary failure, limit, censoring and closure result in the ignored private record,
   mapped into the unchanged
   [Paired Danted Experiment form](../../../../../../local/plan-timeouts-and-danted.md#paired-danted-experiment).
   A bad control is kept, not replaced by rerunning until it passes.
7. Classify the series as exactly one of three outcomes, from the recorded evidence rather
   than from an impression: repeated one-write success with verified target data and passing
   sequential controls; repeatable one-write failure with passing sequential controls in the
   same environment and conditions; or inconclusive, which covers every other case including
   a setup or diagnosis failure, a tunnel or target failure, an interrupting limit, uncertain
   closure and a treatment without a qualified control. Record the first failure, whether the
   treatment made one complete Write (`n == len(batch)`, no error), and the observation
   boundary instead of a configured limit.
8. Prepare the sanitized published result for the coordinator to place in this node's
   `## Outcome`: the classification, the `danted` version, the conditions that qualify
   it (method `0x02`-only greeting, a single stream Write of greeting, login and `CONNECT`,
   target class, disposable local environment), and the explicit scope limit — this `danted`
   in this environment only, no claim about the user's HTTP exit, other versions or
   deployments, TCP segmentation or the server's unobserved `recv`. Refer to the target and
   digest through the private form rather than repeating either, and hand nothing with real
   values, addresses or credentials.
9. Checks and handoff: links and anchors resolve, frontmatter stays `kind: task`,
   `status: todo` until the run and `detailed: true`, English, and a privacy pass over every
   changed file (`git diff --check` included). Raw records and the environment stay in
   ignored `doc/local/` or `doc/runs/`. No product code, config or default is changed. State
   for [Resolve the measured danted result](../07-danted-result/plan.md) which branch the
   evidence supports and that the evidence is synthetic; leave the live SOCKS5 matrix with
   [Live runs](../../04-live-runs/plan.md).

## Done when

- The user's permission for the container image and the `danted` source archive with its
  build dependencies is recorded before the fetch; nothing was fetched or installed without
  it, and a refused permission leaves the node unfinished rather than substituted.
- The environment is reproducible from the record and fully synthetic: `dnstt-server` from
  the vendored `core/server` with a generated key and the reserved domain, `danted` with one
  local login, the accepted probe, a fixed-body HTTP target. The run used no internet after
  the install, and no server, profile, store, resolver or network of the user; the only
  config and store read or written were the temporary synthetic ones, verified isolated.
- At least one authenticated sequential `CONNECT` through the tunnel returned the verified
  target body before the paired series, which qualifies the environment; otherwise the
  result is inconclusive.
- Paired fresh-stream sequential/one-write trials used the accepted method and probe with the
  fixed pair count, target and limits; every attempted path, ordinary failure, limit,
  interruption, censoring and closure result is in the private record; the treatment's single
  complete Write is recorded; no replacement session, hidden retry or silent re-run appears.
- The classification is one of: repeated one-write success with verified target data and
  passing controls; repeatable one-write failure with passing sequential controls under the
  same conditions; inconclusive. A fixture outcome, an HTTP result, a missing or failed
  control or a single failed attempt cannot produce the negative branch, and an inconclusive
  series leaves the node open with no compatibility claim.
- The published text names the measured `danted` version and the conditions that qualify it,
  states the classification, and states explicitly that it answers for this `danted` in this
  environment only — not for the user's HTTP exit, other versions or deployments — and makes
  no claim about TCP segmentation or the server's unobserved `recv` calls.
- Links, anchors, frontmatter, English and privacy pass, including `git diff --check`;
  published files carry no real domain, key, address, resolver, login, profile name, work
  date or environment path, and raw evidence stays ignored. No product code, profile field,
  default or authentication policy was changed, and the
  [live matrix](../../04-live-runs/plan.md) criteria are untouched by this result.

## Risks and what is needed from the user

- The container image and the `danted` source archive are fixed by the permission step; a
  version drift between the run and the published result would weaken the scope statement,
  so the version is recorded at the run and published with it.
- A negative result is version- and condition-scoped. It must not be written as "`danted`
  does not take one-write exchanges" in general, and R3 must carry the same qualification;
  a positive result still needs the user's enduring key/default/behavior decision before any
  code in [Resolve the measured danted result](../07-danted-result/plan.md).
- The likely failure modes are environmental, not evidence: danted refusing loopback
  internal/external configuration, a username method not actually selected, a target the
  container cannot resolve or reach, and a tunnel that cannot be dialed over the loopback
  resolver. All of them end inconclusive after diagnosis, never as a rejection.
- The probe offers only method `0x02` and sends credentials early in the treatment; this is
  the accepted experimental condition, and it is a product-independent choice — neither the
  probe nor this run changes the product's method selection or no-auth behavior.
- Needed from the user: permission for the one fetch. Nothing else: no server access, no
  profile, no resolver and no network of theirs is used, and no real value is read. If the
  machine that runs the agent has no container runtime, report it — do not work around it
  with a different environment.
- The run builds `danted` from the official upstream archive inside the disposable container
  and uses a minimal configuration based on its shipped example. Its source is not copied
  into the probe, the product or the result, and nothing from it is carried there
  (`AGENTS.md`, third-party code).

## Outcome

The synthetic check answers the one-write question for the measured service: `danted` from
Dante 1.4.4 accepts the SOCKS5 greeting, RFC 1929 login and `CONNECT` sent in one complete
stream Write, and the tunneled HTTP target data is verified. The classification is
**positive**; it is synthetic and environment-scoped.

- The measured service is `danted` 1.4.4, built from the official upstream archive
  `dante-1.4.4.tar.gz` with SHA-256
  `1973c7732f1f9f0a4c0ccf2c1ce462c7c25060b25643ea90f9b98f53a813faec` and with PAM
  (`--with-pam`, `pam_unix`) username/password authentication. Debian trixie ships no
  `dante-server` package, so the user directed the source build; the archive, its build
  dependencies and the local account tooling were fetched only inside the disposable
  container under the recorded permission, and the `debian:trixie` image was already
  local. The image identifier stays in the private record.
- Conditions: one local login; the tunnel from the vendored `core/server` run through a
  local harness, with a generated in-memory key and the reserved domain; a loopback UDP
  resolver; one explicit HTTP target with danted's destination rule limited to it; only
  method `0x02` offered, with early credentials as the accepted experimental condition.
  Everything was loopback or container-internal; no user service, profile, store, resolver
  or network participated, and nothing external was reached after the source fetch. The target and its digest are
  referenced through the private
  [Paired Danted Experiment form](../../../../../../local/plan-timeouts-and-danted.md#paired-danted-experiment),
  not repeated here.
- Measurement: a separate qualification pair, then one fixed ten-pair series with
  `-dial-limit 90s`, `-keepalive-limit 7m` and `-trial-limit 30s`. All 10 sequential
  controls authenticated, completed `CONNECT` and returned the verified body; all 10
  treatments made exactly one complete 79-byte Write (requested = returned, nil error,
  first Read after it), completed method, login and `CONNECT`, and returned the verified
  HTTP 200 body. No failure, short write, censoring or uncertain closure appeared; closure
  was confirmed, zero local streams remained, and the evidence was saved. The largest
  observed boundary was about 12 ms against the 30 s trial limit. It is one noisy local
  series; no latency saving is inferred.
- Scope: this `danted` 1.4.4 in this build and configuration, in this disposable
  environment only. It makes no claim about the user's HTTP exit, other versions or
  deployments, TCP segmentation, or the server's unobserved `recv` calls, and it closes no
  live gate.
- The positive branch is what [Resolve the measured danted result](../07-danted-result/plan.md)
  may now take up; any enduring exit key, default, authentication/no-login behavior or
  fallback still needs the user's decision before code. No product setting, profile field
  or authentication policy is selected here, and the live SOCKS5 matrix stays with
  [Live runs](../../04-live-runs/plan.md).
- The accepted probe was used with unchanged logic; the ignored tool needed one mechanical
  call-site adaptation to the current resolver-bootstrap constructor (a nil netguard
  control for this loopback run). Raw records, the target digest and the image identifier
  stay in ignored `doc/local/` and `doc/runs/`; nothing was force-added.
