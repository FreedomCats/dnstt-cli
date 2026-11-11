---
kind: task
status: done
detailed: true
depends: [04-live-runs]
---

# Recording the results

## Task

The results of the live acceptance are written where they are published, without anything
that identifies the user, and the preliminary notes removed. `E1`, `S8`; `AGENTS.md`,
"Documents in `doc/`".

- `architecture.md`: R2 — the keepalive timeout and the rule it was derived by; §04 and
  §08 — the handshake timeout and the selected HTTP semantics: `Connected` before the first
  real application CONNECT means tunnel readiness only, that CONNECT performs Basic
  authentication, and HTTP 407 stops the supervisor; R3 — the answer about `danted` and the
  setting if there is one. Confirm that R3 and §10 describe the HTTP CONNECT/Basic exit added
  by the product task; §13 — the row "A dead session is noticed late".
- The `02-socks` stage's `## Open points` (the live-acceptance stage's parent): it loses
  the two timeout items, and the item about uTLS fingerprints left by S1 is closed or
  restated.
- This stage's `Outcome`: what was seen live, per item of [Live runs](../04-live-runs/plan.md),
  separately for the synthetic SOCKS5 setup and the real-server HTTP exit; the platform
  that ran, with the deferred Windows part stated as not seen; what failed and on which
  side; what was left open and why. Distinguish the upstream tunnel-plus-independent-helper
  result from the in-process product result. The synthetic SOCKS5 result does not stand
  for the user's server, exit or network, and HTTP success does not replace the SOCKS5
  acceptance.
- Code comments that still call a number preliminary or point at "the live acceptance
  task".

## Steps

1. **Settle the timeouts in `doc/architecture.md`.** The values and their derivation are
   already in `internal/supervisor/tunnel.go:19-41`; the document must say the same.
   - R2 (`doc/architecture.md:73-74`): replace "about 30 s ... the number is set by a live
     run, E1" with the final 30 s and its rule: 2.5× the worst measured healthy-silence
     upper bound (~12 s across the measured transports), against the server's ~10 s
     progress; smux declares a session dead one to two keepalive periods (30–60 s) after
     the last data, and the full `socks` recovery adds redial, backoff and the exit check.
     Keep the following sentence about connections in a dead session and the next session.
   - §04 `Config` (`doc/architecture.md:271`): keep "0 — upstream's 2 min" and add that
     `connect` sets 30 s in `sessionConfig` (`internal/app/tunnel.go:177-189`); a profile
     key never sets it.
   - §04 smux paragraph (`doc/architecture.md:303-305`): state the keepalive interval is
     10 s (or half the timeout if shorter) and that smux checks the timeout on its own
     30 s period, so silence is declared dead one to two periods after the last data. Do
     not state more than `internal/supervisor/tunnel.go:25-31` does.
   - §08 "Dialing" (`doc/architecture.md:490-493`): replace "(20 s for now, to be measured
     on the live server)" with 10 s, derived as about 5.5× the slowest measured fresh
     handshake (~1.8 s), which leaves room for a cold bootstrap and cache misses;
     `resolvers check` uses the same `supervisor.DefaultHandshakeTimeout`
     (`internal/cli/resolvers.go:132-134`).
   - §08 "Degraded" (`doc/architecture.md:507-508`): keep 25 s and state the rule from the
     [apply-timeouts handoff](../03-timeouts-and-danted/06-apply-timeouts/plan.md#handoff-to-the-results-task):
     above the worst measured healthy silence (~12 s) and below the 30 s keepalive, so the
     `degraded` status shows before the core ends the session — nominally 5 s before the
     earliest detection, less the 5 s poll step; `reconnecting` comes after the session
     ends, and data restores `Connected`.
   - §13 (`doc/architecture.md:732`): the row "A dead session is noticed late" states the
     measured 30 s keepalive and the one-to-two-period detection window instead of
     "measured in E1".
   - Keep the exact measurement samples out of the architecture; only the sanitized bounds
     (~12 s, ~1.8 s) and the rules go there. No date, no transport-specific value beyond
     those bounds.

2. **Confirm the exit text; do not rewrite what is already final.** R3 and §10 were
   updated by the product task and the `danted` result; this step checks agreement and
   fixes only a contradiction.
   - R3 (`doc/architecture.md:87-88`): the `http` bullet already states HTTP/1.1
     `CONNECT` with an authority and optional Basic authentication, with target resolution
     at the exit. Leave it.
   - R3 one-write paragraph (`doc/architecture.md:101-115`): it already states the measured
     `danted` 1.4.4 answer, `one_write` off by default and `socks5` only, the early
     credential price, the failures that name `exit.one_write`, no fallback, and the
     synthetic scope. Verify it against `internal/exit/socks5.go:20,103` and
     `internal/config/profiles.go:215-225`; edit only if the text and the code disagree.
   - §10 (`doc/architecture.md:565-569`): `exit.type` accepts `socks5 | http | ssh`,
     `user` and `password_ref` are shared, and `one_write = false` is present. Confirm the
     HTTP exit uses the same `user`/`password_ref` (Basic, per R3) and needs no new key; if
     a line implies another key or another authentication, correct that one line.
   - §08 "Connected" (`doc/architecture.md:494-503`): it already carries the selected HTTP
     semantics — `Connected` before the first real application `CONNECT` is tunnel
     readiness only, that `CONNECT` performs Basic authentication, a final 407 moves the
     supervisor to `Stopping` without resolver penalty or redial, and other HTTP errors
     stay per-stream. Leave it, unless it contradicts `internal/app/tunnel.go:191-199`
     and `internal/exit/http.go`.

3. **Remove the preliminary notes from the live-acceptance stage.** In
   `doc/roadmap/dnstt-cli/02-socks/08-live-acceptance/plan.md:24-27`, replace the "Two
   numbers are still preliminary ..." paragraph with the settled state: keepalive 30 s,
   handshake 10 s and degraded 25 s are set in `internal/supervisor/tunnel.go` (R2, §08),
   and the `danted` one-write question is answered by the measured 1.4.4 with
   `exit.one_write` off by default (R3). Keep the privacy paragraph that follows. Also
   correct the one stale Windows line in the stage's `## Done when` (`the Windows run
   repeats what the Linux run showed`) to say the Windows run is deferred and recorded as
   `not seen`; this is the user's recorded deferral, not a new criterion. Do not change the
   stage goal, `## Risks`, `base` or `status: in-progress`.

4. **Write the stage `## Outcome`.** Append it after `## Risks and what is needed from the
   user` in `08-live-acceptance/plan.md`, from the [Live runs outcome](../04-live-runs/plan.md#outcome)
   and the filled checklist; do not invent results. Cover, in this order:
   1. **Scope.** The results of the runs on the final tree. Linux only; the Windows child
      is deferred (`blocked`), W1 is `not seen`, and no Windows item is counted as seen.
      Name the two setups: the synthetic local SOCKS5 setup and the user's real server
      with its HTTP `CONNECT` exit with Basic authentication.
   2. **Synthetic SOCKS5 (S1–S4).** A headless Chromium through the local SOCKS5 listener
      loaded the target page; the local `dnstt-server` restart and the client container's
      address change did not require restarting `connect`, and new fetches worked. Give
      the restart recovery (52.8 s: the 25 s degraded state, the unanswered 30 s
      keepalive, the restart and the redial) and the address-change recovery (0 s: the
      SOCKS5 transport opens a UDP socket per query, so the session is not tied to the
      source address). The clean stop left no product process, listener, container,
      profile or secret; the host matched its pre-run record apart from external Docker
      churn. State that this is not the user's server, exit or network.
   3. **Real server, HTTP exit (R0–R10).** The profile from the read-only mount and the
      control fetch passed; the browser loaded the target over DoH and UDP, and over DoT
      after the first fetch failed on the browser side (empty DOM, no response headers;
      the same-listener control and the resolver check passed), with the first failure
      kept and the planned `--disable-quic` recheck passing. The wrong password gave 407:
      the login error, the stopped supervisor, no resolver retry; the correct password
      returned 200. The comparison over DoH and UDP passed for the upstream tunnel plus
      the independent HTTP `CONNECT` probe and for the in-process product, both `PASS
      3/3` and verdict `OK`, with the product verdict recording the first real application
      `CONNECT`, not readiness. The dead resolver was named with its error in a mixed
      list, and a dead-only list exited 1. Seven of the eight presets completed a real
      handshake, the failure attributed to the resolver. All five `DefaultUTLS` labels
      completed a handshake through a DoH and a DoT preset. R10: SIGINT, exit 0, no
      process or listener, container, profiles and store removed.
   4. **Failures and sides.** The kept failures are the DoT browser fetch (browser side)
      and the preset (resolver side); no `dnstt-cli` defect was found and no defect child
      was opened. Keep the recorded limits: the indirect source-address proof (the image
      has no `ip`/`ss`), the SIGINT reaching the `script` wrapper rather than the product
      (exit 0, stop line missing), and the external Docker churn.
   5. **Open.** The user's own UDP path was not checked: the user has no UDP resolver,
      a public resolver was the allowed substitute for the transport check, and DoH and
      DoT ran over the user's entries. Windows remains deferred. Say plainly that the
      synthetic result does not stand for the user's server, exit or network and that the
      HTTP result does not replace the SOCKS5 acceptance.
   6. **Privacy.** The filled checklist and raw logs are only in ignored `doc/local/` and
      `doc/runs/`; the temporary profiles and the stored secret were deleted; no domain,
      key, address, resolver, login, target, date or environment detail is in a tracked
      file.
   - No commit ids, dates or environment details are needed; if a commit is named, take
     it from the live-runs Outcome, do not invent one. Add a short `## Outcome` to this
     task's own `plan.md` recording what was written and that the code-comment scan found no
     hit.

5. **Close the `02-socks` stage's open points.** In
   `doc/roadmap/dnstt-cli/02-socks/plan.md:54-60`:
   - remove the bullet "The client keepalive timeout stays at the upstream 2 min until it
     is measured on a live server; `DefaultHandshakeTimeout` (20 s) is preliminary for the
     same reason." The values are settled and now recorded in the live-acceptance stage and
     in `architecture.md`.
   - restate the uTLS bullet: the `v1.7.x` pin stays because of the v1.8 randomized-hello
     bug (P5, `architecture.md` §04), and the five `DefaultUTLS` labels have now completed
     handshakes through DoH and DoT live (R9 of
     [the resolver matrix](../04-live-runs/05-resolver-matrix/plan.md)), so S1's "not
     checked live" item is closed.
   - leave the `TestApplyReadsV6Only` bullet and `## Outcome` ("Filled in when every child
     is done.") unchanged. The done node `01-core-patches/plan.md` is not edited.

6. **Scan the code comments.** Run
   `rg -n -i 'preliminary|to be measured|live acceptance|plan\.md S|\bS0\b|\bS8\b' internal cmd`.
   At this tree it has no hits: the only nearby phrase is
   `internal/winsys/dnsguard_windows_test.go:168` ("found in a live run"), which is a TUN
   live check and unrelated — leave it. Also read the authoritative comments
   `internal/supervisor/tunnel.go:19-41`, `internal/app/tunnel.go:177-189`,
   `internal/cli/resolvers.go:132-134` and `internal/exit/socks5.go:20,103`; they already
   state final values. If the scan finds a comment that calls a number preliminary or
   points at this roadmap, change the comment only — no constant, behavior, test or
   profile key. If a Go file changes, `gofmt -l` on it and `git diff` must show comment-only
   lines.

7. **Check consistency and privacy.** `git diff --name-only` should list only
   `doc/architecture.md`, `doc/roadmap/dnstt-cli/02-socks/plan.md`,
   `doc/roadmap/dnstt-cli/02-socks/08-live-acceptance/plan.md`,
   `doc/roadmap/dnstt-cli/02-socks/08-live-acceptance/05-record-results/plan.md`, and a
   comment-only Go file if step 6 found a hit. Walk the diff line by line: no real domain,
   key, address, resolver, login, link speed, date or environment detail; only reserved
   placeholders (`t.example.com`, `192.0.2.0/24`, `doh.example`, `dot.example`, `user`).
   Do not publish a list of working public resolvers; the UDP text says only that the
   user's own path was not checked. The run course and evidence stay in ignored
   `doc/local/` and `doc/runs/`; nothing there is force-added. Links and anchors resolve,
   `git diff --check` passes. If a Go file changed, `GOPROXY=off go build ./...`; otherwise
   no build is required. A repository-wide search also finds "preliminary" in done nodes
   and earlier measurement or apply plans (for example
   `02-socks/04-supervisor/plan.md:53`); those describe the state before this task and are
   not edited. The verifiable check here is the changed documents and the code comments.

## Done when

- `doc/architecture.md` R2 states 30 s and the 2.5× worst measured healthy-silence rule;
  §04 states that zero means upstream's 2 min and that `connect` sets 30 s, plus the 10 s
  smux interval and the 30 s check period with one-to-two-period detection; §08 states the
  10 s handshake with the ~5.5× rule, and the HTTP semantics (readiness before the first
  real `CONNECT`, Basic on that `CONNECT`, final 407 stops the supervisor) are present;
  §13 states the measured 30 s and the detection window. No "preliminary", "to be
  measured", "for now" or "measured in E1" about the keepalive, the handshake timeout or
  `danted` remains in the changed documents.
- R3 states the measured `danted` 1.4.4 answer, `one_write` off by default and `socks5`
  only, the early-credential price, the failure behavior and the synthetic scope; §10
  accepts `socks5 | http | ssh` with the shared `user`/`password_ref` and `one_write`;
  the architecture text agrees with `internal/exit` and `internal/config`.
- `02-socks/plan.md` `## Open points` has no timeout bullet; the uTLS item is restated
  with the `v1.7.x` pin and the live five-label result; the IPv6-test bullet is unchanged.
- `08-live-acceptance/plan.md` has an `## Outcome` that records, per item of
  [Live runs](../04-live-runs/plan.md): synthetic SOCKS5 separately from the real-server
  HTTP exit; Linux seen, Windows `not seen` by deferral; what failed and on which side;
  upstream tunnel plus independent helper separately from the in-process product; what
  stays open (the user's own UDP path, Windows) and why. It does not present the synthetic
  setup as the user's server, exit or network, and does not present HTTP success as the
  SOCKS5 acceptance. The stage goal, `base` and `status: in-progress` are unchanged; the
  stage's stale Windows `## Done when` line is corrected to record the user's deferral, and
  this task's own `plan.md` has an `## Outcome`.
- The code-comment scan names no hit that calls a number preliminary or points at this
  roadmap; any edit found is comment-only; `git diff` shows no behavior, test, constant or
  profile-key change.
- No domain, key, address, resolver, login, link speed, date or environment detail is in
  the changed tracked files; only reserved placeholders are used; the course of the runs
  and the raw evidence are in ignored `doc/local/` and `doc/runs/`; links and anchors
  resolve and `git diff --check` passes.
- If a Go file changed, `gofmt -l` is clean and `GOPROXY=off go build ./...` passes;
  otherwise no build is required.

## Risks and what is needed from the user

- **Overclaiming the scope.** The synthetic SOCKS5 result and the real-server HTTP result
  are different evidence. A sentence that lets the synthetic setup stand for the user's
  server, exit or network, or that lets HTTP success stand for the SOCKS5 acceptance,
  reopens the stage's main risk. Check every claim against the live-runs Outcome and the
  filled checklist.
- **Numbers drifting from the code.** The architecture must match
  `internal/supervisor/tunnel.go:19-41`; a second source of truth, a new decimal, or a
  per-transport value in the architecture makes the text stale again. The exact samples
  stay where they are; only the sanitized bounds and the rules are published.
- **The `danted` sentence.** It is scoped to the measured 1.4.4 in a synthetic environment;
  it must not become a statement about the user's HTTP exit, other versions or
  deployments.
- **Two open-point locations.** `02-socks/plan.md` has the `## Open points` section; the
  live-acceptance stage has the stale "Two numbers are still preliminary" paragraph. Both
  are updated, and the stage's goal/`## Done when` and the done S1 node are not touched.
- **Privacy.** The filled checklist and the raw logs stay ignored; no value, resolver,
  address, login or date is copied into a tracked file. The UDP wording says "not checked",
  not which public resolver was used.
- **No user input is needed.** This task publishes only results that are already recorded;
  no live run, container or network access is part of it.

## Outcome

The live results are recorded where they are published. `doc/architecture.md` now states the
final keepalive 30 s and the 2.5× worst measured healthy-silence rule, the smux 10 s interval
and 30 s check period with one-to-two-period detection, the 10 s handshake and its ~5.5×
rule, the 25 s degraded rule, and the measured 30 s row in §13. R3 and §10 already described
the measured `danted` one-write result and the HTTP CONNECT/Basic exit; no contradiction was
found, so they were left unchanged. The live-acceptance stage's preliminary paragraph was
replaced by the settled timeouts and the `danted`/`one_write` answer, its stale Windows
`## Done when` line now records the deferral, and its `## Outcome` records the synthetic
SOCKS5 and real-server HTTP results, the sides of the kept failures, the unproven user UDP
path and the deferred Windows child. The `02-socks` open points lost the timeout bullet and
restated the uTLS item with the live five-label result. The code-comment scan found no hit
that names a preliminary number or this roadmap; no Go file changed. Links and anchors
resolve, `git diff --check` passes, and the changed files carry no domain, key, address,
resolver, login, link speed, date or environment detail. Documentation only; no code, test,
build file or configuration changed.
