---
kind: task
status: todo
depends: [02-timeout-probes]
---

# Live timeout measurements and value selection

## Task

The user collects the handshake series and healthy-idle silence evidence over DoH, DoT and
UDP using the accepted [timeout probes](../02-timeout-probes/plan.md) and
[method](../01-measurement-method/plan.md). Derive candidate keepalive/handshake values and
their rules from that evidence, then obtain the user's stability/recovery decision. The
accepted output is a private evidence record and an approved sanitized policy handoff to
[Apply the measured timeout policy](../06-apply-timeouts/plan.md); no product code changes.

Use the unfilled form in `doc/local/plan-timeouts-and-danted.md` and the existing local
README commands. The user executes them; the agent does not inspect real configuration
or its metadata, call services/resolvers, or operate the user's network. An authenticated
SOCKS5 baseline and independent comparison remain controls for the relevant runs; HTTP
success or HTTP response timing cannot substitute for them.

## Done when

- For every transport, private records contain a series of fresh handshakes, all outcomes
  and observation bounds, and the longest healthy-idle silence over several minutes with
  actual receive observations, uncertainty and boundary/end conditions. The resolver,
  uTLS/bootstrap conditions and sample counts are attributable in the private form.
- Timeouts, missing transports and interrupted/failed idle runs are not relabeled as
  measured healthy maxima. Repeat incomplete measurements as needed; an unavailable
  prerequisite keeps this child open and is escalated to the user, not waived.
- Candidate keepalive and handshake timeouts each have a reproducible derivation from
  the slowest intended working transport, with margins and sampling uncertainty. Explain
  smux's actual detection range and the additional redial/backoff/exit-readiness time;
  do not equate a configured timeout with the full recovery time in `socks` mode.
- Check `DefaultDegradedAfter` against both selected values and its polling cadence;
  state whether its current 25 s is retained or a change is proposed, with the reason.
  The user approves the values/rules and visible stability/recovery tradeoff before the
  product task starts. A large departure from R2's approximate 30 s is handed to
  [Recording the results](../../05-record-results/plan.md), without selecting network
  tracking or a new profile field here.
- Raw measurements/captures and the decision record stay in ignored private files.
  Published task results contain only the approved policy, derivation and limitations,
  without resolver identities, raw timings, dates, personal values or environment details.
  Documentation consistency, links and privacy checks pass.

## Risks and what is needed from the user

The user needs the server, the intended working resolvers for all three transports and
time for several-minute idle runs. Their availability is not established. The local probe
acceptance supplies no live evidence. There is no approved numeric policy until these
measurements and the user's decision exist; neither loopback nor an assumed 30 s closes it.
