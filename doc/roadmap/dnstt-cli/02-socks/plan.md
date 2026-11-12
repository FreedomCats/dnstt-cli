---
kind: stage
status: done
base: ba4b001
---

# SOCKS5 mode with reconnection

Goal (`architecture.md` §12, E1): a browser goes through `dnstt-cli connect` in `socks`
mode to the server's `danted` with a login, on Linux and Windows, and the session survives
a server restart and a network change. No privileges: programs that have a proxy setting
point it at the local SOCKS5 listener.

The additional user-selected HTTP CONNECT/Basic exit uses the same local SOCKS5 listener;
it is added during live-acceptance preparation, not substituted for the SOCKS5 result. Its
supervisor reports tunnel readiness as `Connected` until the first real application CONNECT;
that request performs Basic authentication, and a final HTTP 407 stops the supervisor.

Chain: local SOCKS5 listener → `exit.Dialer` (SOCKS5 or HTTP CONNECT over a tunnel stream) →
`supervisor.Tunnel` (one session at a time, a resolver list, reconnection) →
`core.Dial` (transport → KCP → Noise → smux).

## Done when

- A browser through the local proxy reaches a site over DoH, DoT and UDP resolvers: seen on
  Linux (the local proxy over all three transports; the SOCKS5 exit itself only in the
  synthetic setup over its literal UDP resolver); the Windows run is deferred by the user
  and is `not seen`, not a pass.
- The configured HTTP CONNECT exit with Basic authentication is also exercised through the
  local SOCKS5 proxy over working DoH and UDP resolvers: seen on Linux against the real
  server; the Windows run is deferred and is `not seen`.
- The session survives a server restart and a network change, and streams opened in the
  new session work without restarting the client.
- `resolvers check` names the dead resolvers and exits non-zero when none passes.
- Profiles and secrets are read, validated and saved, and the exit password never appears
  in the profile file.

## Children

1. [Core patches P2–P5](01-core-patches/plan.md) — S1. Done.
2. [SOCKS5 exit](02-exit/plan.md) — S2. Done.
3. [Resolvers](03-resolvers/plan.md) — S3. Done.
4. [Supervisor](04-supervisor/plan.md) — S4. Done.
5. [SOCKS5 datapath](05-socks-datapath/plan.md) — S5. Done.
6. [Profiles and secrets](06-profiles-secrets/plan.md) — S6. Done.
7. [Commands](07-commands/plan.md) — S7. Done.
8. [Live acceptance](08-live-acceptance/plan.md) — S8. A stage of its own.
9. [Interactive profile wizard](09-profile-wizard/plan.md) — the deferred `profile add`
   wizard, shared with the later menu; a prerequisite of the Linux live acceptance. Task.
10. [Delete a profile's secret with the profile](10-profile-rm-secret/plan.md) — remove the
    referenced secret in `profile rm`, closing the orphan left by the wizard's secrets. Task.

S1–S3 do not depend on each other; the order above is the one they were taken in. A test
server of the user's own (the old `S0`) is not a node: it is a live prerequisite of
[Live acceptance](08-live-acceptance/plan.md) and of the interactive stage.

## Open points

- `TestApplyReadsV6Only` (netguard) skips without IPv6 in the kernel; the CI `test` job
  runs without `-v`, so it is not yet confirmed that it runs there rather than skips.
- uTLS stays at `v1.7.x` until the randomized-hello bug of `v1.8` is fixed; the five
  `DefaultUTLS` labels were seen live through DoH and DoT (R9).

## Outcome

The stage is accepted on the final tree: the code, the tests and the recorded live evidence.
All children are done and accepted; the live-acceptance stage carries the runs and their
record.

**Built and accepted.** A local SOCKS5 listener (loopback, `127.0.0.1:1080` by default) hands
a program's `CONNECT` to `exit.Dialer`: the SOCKS5 exit over a tunnel stream (greeting, the
RFC 1929 login when the profile has one, `CONNECT`, the exchange bounded by the caller and
60 s), or the HTTP/1.1 `CONNECT` exit with Basic authentication (R3). `supervisor.Tunnel` owns
the one session over the resolver list — a handshake per resolver, the exit readiness check,
resolver health, backoff, `degraded` and `reconnecting`, a restart, `OpenStream` across a
session boundary, and the stop sequence. `core.Dial` carries transport → KCP → Noise → smux
with P2–P5 applied. Profiles and secrets follow §10: unknown keys refused, the exit password
only in the secret store behind `password_ref`, `profile rm` removing the referenced secret,
`profile add` as the interactive wizard. The commands `connect`, `profile` and
`resolvers check` exist with the status line; the `network` entry and the R5 bootstrap order
work in `connect` and `resolvers check`. The final timeouts are keepalive 30 s, handshake
10 s and `degraded` 25 s; `exit.one_write` is off by default and applies to `socks5` only.

**Found and fixed at acceptance.** `connect` could stall the supervisor when its output was
redirected: transitions were sent into a channel that only the terminal status line reads, so
the buffer filled and the supervisor blocked. A non-terminal run now installs no callback and
the callback never blocks on a full buffer. The first Ctrl+C did not use the supervisor's
drain: the socks datapath now stops accepting and lets the established relays finish for up
to 5 s with the session still up, and the second Ctrl+C cuts it short. A `socks5` profile
with a `password_ref` and no `user` is refused when the tunnel is built instead of retrying
forever, and `profile show` rejects an unknown flag in any position.

**Live evidence (Linux).** The SOCKS5 part is synthetic: a local `dnstt-server`, `danted`
1.4.4 built from source, a fixed-body target and headless Chromium in disposable containers;
the browser loaded the target through the local SOCKS5 listener, a local server restart
recovered in 52.8 s (25 s `degraded`, the 30 s keepalive, stop/start and redial) and a change
of the client's address did not break the session (0 s), with new requests working without a
restart. The HTTP part ran against the real server: the browser loaded the target through the
authenticated exit over DoH, DoT and a public UDP resolver, a wrong password gave 407 and the
supervisor stopped without a resolver retry, the dead resolver was named with exit 1 when none
passed, seven of eight presets completed a handshake, all five `DefaultUTLS` labels completed
one through a DoH and a DoT preset, and the upstream-plus-helper and the in-process product
comparisons passed 3/3 over DoH and UDP. A clean SIGINT left no process, listener, container,
profile or store.

**Platform and scope.** Linux is seen. The Windows child is deferred by the user and is
`not seen`, not a pass: it has cross-build, vet and test-compilation evidence only. The
synthetic SOCKS5 run used its literal UDP resolver only; DoH, DoT and UDP were exercised
through the local proxy on the real server with the HTTP exit, so the SOCKS5 exit over DoH and
DoT is not claimed. The user's own UDP path was not checked: the user has no UDP resolver of
their own and a public one was the allowed substitute for the transport check.

**Failures and sides.** The kept failures are the DoT browser first fetch (browser side; the
same-listener control and the resolver check passed, and the planned `--disable-quic` recheck
passed) and one DoH preset (resolver side; both pinned 443 paths were open and the same
resolver's DoT preset passed). No `dnstt-cli` defect was found in the runs and no defect child
was opened.

**Recorded limits.** The 52.8 s restart recovery is the cost of the 30/10/25 s policy, not a
defect; the source-address proof is indirect (the image has no `ip`/`ss`); the synthetic
clean stop's SIGINT reached the `script` wrapper rather than the product; the host comparison
is not literal because of other projects' Docker churn; there is no end-to-end app-level
keepalive behavioural test (the session configuration and the supervisor timers are covered,
and the live restart shows the behaviour). The `network` resolver has unit and injected-DNS
coverage and no live run; its Windows path is cross-built and test-compiled only. A loopback
system stub that is not the systemd-resolved stub is not used by the reader, and a `udp:`
resolver given by name uses the first of its pinned addresses while DoH and DoT try them in
turn (architecture R5 says so); a UDP address failover is not implemented. Both are recorded
limitations; allowing either is a separate change.

**Verification on the final tree.** The full matrix passes: the offline Linux build, the CI
vet split for Linux and Windows, `go test -race -count=1 ./...` with the isolated D-Bus tests
and the four external `netguard` live tests excluded, the Windows cross-build with all test
packages compiled, gofmt outside `core/` (the three upstream `core/` files stay as they are),
`git diff --check`, the `vendorcore -check` against the vendored commit, and the `linuxsys`
and `datapath` netns suites in a disposable container with no skips.

**Open points.** `TestApplyReadsV6Only` still skips on a kernel without IPv6 and the CI
`test` job runs without `-v`, so its run there is not yet confirmed. uTLS stays at `v1.7.x`
until the `v1.8` randomized-hello bug is fixed; the five `DefaultUTLS` labels have been seen
live through DoH and DoT.

**Privacy.** The run evidence stays in ignored `doc/local/` and `doc/runs/`; the temporary
profiles and stores were deleted; no domain, key, address, resolver, login, target, date or
environment detail is in a tracked file.
