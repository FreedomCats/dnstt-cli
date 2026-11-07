---
kind: stage
status: todo
depends: [02-live-run-script, 03-timeouts-and-danted]
---

# Live runs

The acceptance runs of the SOCKS5 datapath against the user's server, with the timeouts of
[Timeouts and the `danted` question](../03-timeouts-and-danted/plan.md) in place. They
cover the existing SOCKS5 server-side exit and the selected HTTP CONNECT exit. The agent
cannot carry them out — a browser, a server restart, a network change, a Windows machine:
the user runs them from a checklist, and every failure goes through `compare` of the
[live-run tooling stage](../02-live-run-script/plan.md) before it is called a bug.

The checklist is written with placeholders (`<profile>`, `t.example.com`, "the ISP
resolver"); its filled copy, the outputs and the course of the runs are in `doc/local/`.
A bug found here becomes a task of this stage, with its own test against the in-process
server where one can be made.

What the runs have to show:

- **Transports.** A browser set to the local proxy opens sites through the server's
  SOCKS5 exit with a login, once with only a DoH resolver in the profile, once with only
  DoT, once with only UDP.
- **HTTP exit.** With `exit.type = "http"`, the local SOCKS5 proxy opens sites through
  the server-side HTTP CONNECT service with Basic authentication. `compare` passes on the
  upstream side through its independent HTTP CONNECT probe and gives a result for the
  in-process `dnstt-cli` side over at least one working DoH resolver and one working UDP
  resolver. For the in-process side, `Connected` before the first request means tunnel
  readiness only; the first real application CONNECT must authenticate and carry the target.
  A 407 stops the supervisor and is not retried as a resolver failure. The upstream result
  is evidence for the tunnel plus helper probe, not a claim that the upstream binary
  implements the HTTP exit protocol.
- **Server restart.** `dnstt-server` restarted in the middle of a session: the session
  comes back by itself, new connections work without restarting `connect`.
- **Network change.** Wi-Fi to cable or another network in the middle of a session: the
  same. In `socks` mode nothing tracks the network yet (`architecture.md` §12, E2), so the
  old session ends by the keepalive timeout; the time to recover is noted against it.
- **Dead resolver.** `resolvers check` on a list with a resolver that does not answer:
  it is named with its error, the others pass; on a list of dead resolvers only, the exit
  code is 1.
- **Presets.** `resolvers check` on a profile with all eight presets: each completes a
  handshake or its failure is attributed. Each fingerprint of `client.DefaultUTLS`
  (through the profile's `utls`) completes a handshake through at least one DoH and one
  DoT preset — left open by the core patches (S1, P5).
- **Windows.** The same SOCKS5 datapath checks on Windows, plus the selected HTTP exit
  scenario with the exit password stored there by the tool's `secret`.

## Done when

- Every item above has been seen on Linux and on Windows, or its failure is attributed by
  `compare` and either fixed in a task of this stage or written down as the server's,
  exit's or network's.
- Over a working DoH resolver and a working UDP resolver, the upstream tunnel plus the
  independent HTTP CONNECT probe reaches the target using Basic authentication, and the
  in-process `dnstt-cli` verdict records a successful first real application CONNECT rather
  than tunnel readiness alone. Repeat this selected HTTP scenario on both platforms. A
  wrong-password 407 stops `connect` without a resolver retry and is recorded as the final
  exit-login result. An upstream listener-ready or Noise-only result, a local selftest, or
  an attributed upstream failure does not close this authenticated-comparison gate.
- Existing SOCKS5 runs remain required; their absence is not an HTTP success and is recorded
  as an unmet prerequisite. HTTP success does not answer the `danted` question.
- Bugs of `dnstt-cli` found in the runs are fixed and committed, each with a test.
- The filled checklist, outputs and course are in `doc/local/`; nothing of them is in a
  file of the repository.

## Risks and what is needed from the user

- Needed from the user: the server and the right to restart it, two networks for the
  change, a Windows machine, a profile selecting the HTTP exit with Basic credentials,
  and the resolvers that pass in their network. An available SOCKS5 exit is needed for
  the existing SOCKS5 matrix; the HTTP scenario does not require relabeling it as SOCKS5.
- The network-resolver task, the HTTP exit integration and the live-run tooling must be
  complete before these runs; include `network` and an unpinned resolver hostname in the
  resolver checks. Do not write the user's profile or credentials into the checklist or
  roadmap.
- A network that lets none of the transports through makes it a design problem, not an
  acceptance run: name it when it happens.
- If recovery after a network change takes too long for the keepalive alone, the remedy is
  network tracking in `socks` mode, which the architecture puts into E2: a question to the
  user, not a quiet addition.
