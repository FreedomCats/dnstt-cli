---
kind: task
status: todo
detailed: true
depends: [02-timeout-probes, 09-http-idle-control]
---

# Live timeout measurements and value selection

## Task

The live handshake series and healthy-idle silence evidence are collected over DoH, DoT and
UDP by the agent, from values the user supplies privately; the user does not run the
commands. The accepted [timeout probes](../02-timeout-probes/plan.md),
[HTTP CONNECT control](../09-http-idle-control/plan.md) and
[measurement method](../01-measurement-method/plan.md) are used unchanged. From that
evidence the agent derives candidate keepalive and handshake values with their rules,
checks the degraded threshold against them and asks the user for the stability/recovery
decision. The accepted output is a private evidence record and an approved sanitized policy
handoff to [Apply the measured timeout policy](../06-apply-timeouts/plan.md); no product
code changes. The independent `compare` command over the same temporary profile may
attribute a failure, but its verdict is not a timing sample and HTTP success or HTTP
response timing never substitutes for the handshake series or the idle checks.

The run is the narrow agent-run exception in the parent stage's execution boundaries: the
user supplies the tunnel server's domain and public key, the resolver entries for the three
transports, the HTTP CONNECT exit's Basic login and password, and one allowed target with
the SHA-256 of its entire expected body. The agent reads only the temporary profile and
file-backed store it creates in an ignored path, makes only the resolver, tunnel and target
requests these commands need, and changes nothing on the server or in the network. No real
profile, store or metadata is read or statted. Real values and raw records live only in
ignored paths, and the temporary configuration and stored secret are deleted after the
runs. `timeout-dial` needs no exit; `timeout-idle` uses the HTTP CONNECT control because
that is the user's real exit.

## Steps

1. Receive the private values and record them only in the ignored private form: the tunnel
   domain, the server public key, the intended resolver entries for DoH, DoT and UDP, the
   exit's Basic user and password, the allowed target URL and the SHA-256 of its entire
   expected body, and any non-default uTLS or bootstrap conditions. Copy none of them into
   tracked files, commit messages or the journal. If a value is missing, stop and report
   which one; do not guess, substitute a public resolver or fall back to `network`.

2. Build an isolated temporary configuration: a `0700` directory inside the ignored
   `doc/runs/` tree as `XDG_CONFIG_HOME`, with Secret Service discovery suppressed on Linux
   (`DBUS_SESSION_BUS_ADDRESS` and `XDG_RUNTIME_DIR` unset), so the store is file-backed and
   lives in that directory. Write one reserved profile with the supplied domain, public key,
   `exit.type = "http"`, the supplied exit user and the intended resolver entries. Run
   `secret -profile <temp>` with the password on stdin from a `0600` file in that directory,
   paste the printed reference into `exit.password_ref`, then delete the input file. Before
   the first run verify that only this temporary configuration is in use and that the real
   configuration path, store and their metadata are not read or statted.

3. Collect the fresh handshake series per transport and per intended resolver with the
   accepted command:

   ```sh
   go run ./doc/runs/_live timeout-dial -profile <temp> -resolver '<entry>' \
     -n 10 -dial-limit 90s -keepalive-limit 7m
   ```

   These limits are run-only observation controls; if working samples are clipped, lengthen
   them explicitly and keep both series. One resolver per invocation, no substitution of a
   failed resolver, and every failure, limit and censoring kept. Logs go to
   `doc/runs/logs/`. `timeout-dial` output is never exit evidence.

4. Collect the healthy-idle windows per transport with the accepted command and the HTTP
   control:

   ```sh
   go run ./doc/runs/_live timeout-idle -profile <temp> -resolver '<entry>' \
     -dial-limit 90s -keepalive-limit 7m -window 5m -poll 1s -verify-limit 30s \
     -url '<target>' -expect-sha256 <digest>
   ```

   The pre-check and post-check must pass through the HTTP CONNECT control. A 407, a
   refused CONNECT, a missing target or a failed digest is a failed usability check and
   keeps the run incomplete: diagnose, keep it and report; never reclassify it as a timeout
   or replace it with a cleaner rerun. One session per run, no hidden redial, no application
   traffic inside the window.

5. Map every run into the ignored private form `doc/local/plan-timeouts-and-danted.md`: the
   Fresh Handshake Series rows and per-sample records, the Healthy Idle Windows rows and the
   complete trace reference, conditions, sample counts, all limits, actual offsets and
   counters, interval bounds, polling lateness, end reasons and the failed, censored or
   incomplete classification. Record the control mode (HTTP CONNECT with Basic) and the
   temporary configuration's identity and removal. Update the form's execution note to this
   agent-run boundary; keep the form ignored.

6. Derive the candidates from the evidence: the slowest transport intended to work
   constrains the handshake and keepalive maxima, including the sampling uncertainty,
   smux's detection range (one to two configured timeouts plus scheduling, architecture
   §04), the redial, backoff and exit-readiness recovery cost, and the degraded threshold's
   polling cadence. State whether the current 25 s `DefaultDegradedAfter` is retained or
   changed, and why. A missing transport, an unavailable control or a failed window keeps
   the result open and is escalated; it is never silently dropped or replaced by an
   assumption such as R2's approximate 30 s.

7. Ask the user for the decision on the exact values, the derivation rules and the visible
   stability/recovery tradeoff before any product change, and record the approval in the
   private form and in a sanitized handoff. A large departure from R2's approximate 30 s is
   handed to [Recording the results](../../05-record-results/plan.md); no network tracking
   and no new profile field is selected here.

8. Clean up and check: delete the temporary configuration directory with the stored secret,
   confirm the deletion, and verify that no real value remains in a tracked file, in a
   non-private log, or in temporary files; keep only the private evidence the decision
   needs. Do not force-add `doc/runs/` or `doc/local/`, do not edit `doc/local/journal.md`,
   and run link, English, privacy and `git diff --check` checks over the tracked changes.

## Done when

- For every intended resolver and each transport, private records contain a series of fresh
  handshakes with all outcomes and observation bounds, and the longest healthy-idle silence
  over several minutes with actual receive observations, uncertainty and boundary/end
  conditions; the HTTP CONNECT pre-check and post-check passed on the real exit. The
  resolver, uTLS/bootstrap conditions and sample counts are attributable in the private
  form.
- The runs were performed by the agent from privately supplied values; only the temporary
  profile and file-backed store in an ignored path were read or written, only the resolver,
  tunnel and target requests the commands need were made, and no real profile, store or
  metadata was read or statted. The temporary configuration and secret were deleted and the
  deletion is recorded. No real value appears in a tracked file or in published text.
- Timeouts, missing transports and interrupted or failed idle runs are not relabeled as
  measured healthy maxima. An incomplete measurement is repeated as needed; an unavailable
  prerequisite or failed control keeps this child open and is escalated to the user, not
  waived.
- Candidate keepalive and handshake timeouts each have a reproducible derivation from the
  slowest intended working transport, with margins and sampling uncertainty. The text
  explains smux's actual detection range and the additional redial/backoff/exit-readiness
  time; a configured timeout is not equated with the full recovery time in `socks` mode.
- `DefaultDegradedAfter` is checked against both selected values and its polling cadence;
  the record states whether its current 25 s is retained or a change is proposed, with the
  reason. The user approves the values and rules and the visible stability/recovery
  tradeoff before the product task starts. A large departure from R2's approximate 30 s is
  handed to [Recording the results](../../05-record-results/plan.md), without selecting
  network tracking or a new profile field here.
- Raw measurements, captures and the decision record stay in ignored private files.
  Published task results contain only the approved policy, derivation and limitations,
  without resolver identities, raw timings, dates, personal values or environment details.
  Documentation consistency, links, English and privacy checks pass.

## Risks and what is needed from the user

The user supplies the private values, not their time: the tunnel domain and public key, the
intended working resolvers for all three transports, the HTTP exit's Basic login, an
allowed target with its expected body digest, and the final decision. Their availability is
not established, and the local probe acceptance supplies no live evidence. The exit may not
be plain Basic (another scheme or a portal login), in which case the control ends in 407 and
the idle windows cannot qualify; that is reported, not worked around. A single failed run
closes nothing: the affected series or window is kept, diagnosed and repeated explicitly.
Neither loopback, HTTP success nor an assumed 30 s closes the policy gate.
