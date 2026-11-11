---
kind: stage
status: in-progress
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

- A browser through the local proxy reaches a site over DoH, DoT and UDP resolvers, on
  Linux and Windows.
- The configured HTTP CONNECT exit with Basic authentication is also exercised through the
  local SOCKS5 proxy over working DoH and UDP resolvers, on Linux and Windows.
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

Filled in when every child is done.
