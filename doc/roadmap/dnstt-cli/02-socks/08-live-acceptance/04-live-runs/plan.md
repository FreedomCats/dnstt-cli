---
kind: stage
status: todo
depends: [02-live-run-script, 03-timeouts-and-danted]
---

# Live runs

The acceptance runs of the SOCKS5 stage against the user's server, with the timeouts of
[Timeouts and the `danted` question](../03-timeouts-and-danted/plan.md) in place. The
agent cannot carry them out — a browser, a server restart, a network change, a Windows
machine: the user runs them from a checklist, and every failure goes through `compare` of
the [Live-run tool](../02-live-run-script/plan.md) before it is called a bug.

The checklist is written with placeholders (`<profile>`, `t.example.com`, "the ISP
resolver"); its filled copy, the outputs and the course of the runs are in `doc/local/`.
A bug found here becomes a task of this stage, with its own test against the in-process
server where one can be made.

What the runs have to show:

- **Transports.** A browser set to the local proxy opens sites through the server's
  SOCKS5 exit with a login, once with only a DoH resolver in the profile, once with only
  DoT, once with only UDP.
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
- **Windows.** The same on Windows, the exit password stored there by the tool's `secret`.

## Done when

- Every item above has been seen on Linux and on Windows, or its failure is attributed by
  `compare` and either fixed in a task of this stage or written down as the server's or
  the network's.
- Bugs of `dnstt-cli` found in the runs are fixed and committed, each with a test.
- The filled checklist, outputs and course are in `doc/local/`; nothing of them is in a
  file of the repository.

## Risks and what is needed from the user

- Needed from the user: the server and the right to restart it, two networks for the
  change, a Windows machine, the resolvers that pass in their network.
- The network-resolver task and the preceding live-run tool must be complete before these
  runs; include `network` and an unpinned resolver hostname in the resolver checks.
- A network that lets none of the transports through makes it a design problem, not an
  acceptance run: name it when it happens.
- If recovery after a network change takes too long for the keepalive alone, the remedy is
  network tracking in `socks` mode, which the architecture puts into E2: a question to the
  user, not a quiet addition.
