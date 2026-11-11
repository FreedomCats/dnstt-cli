---
kind: task
status: todo
depends: [01-runbook]
---

# Resolvers, presets and fingerprints

## Task

The resolver-facing checks on the real server, from a temporary profile that needs only
the domain and public key (no exit credentials): `resolvers check` with a dead resolver in
the list and with only dead resolvers; the eight built-in presets; and every fingerprint
of `client.DefaultUTLS` through at least one DoH and one DoT preset.

The dead resolver is a documentation address (`udp:192.0.2.53:53`, TEST-NET-1) or another
address that does not answer; the mixed list also has one of the user's working resolvers.
The check uses the final 10 s handshake timeout. The presets are the four DoH and four DoT
entries of `internal/resolvers/presets.go`; a preset that fails is attributed to the
resolver or the network (for example, a blocked port), not to the product without
evidence. The fingerprints are `random`, `Chrome_131`, `Firefox_120`, `Safari_16_0` and
`iOS_14` (`DefaultUTLS`); for each, a profile sets `utls = "<label>"` and a resolver list
of one DoH and one DoT preset. The run records into the [runbook](../01-runbook/plan.md)
and follows the [stage design](../plan.md#run-design).

## Done when

- `resolvers check` on a list with a dead resolver names it with its error, the working
  resolver passes, and a list with only dead resolvers exits 1.
- All eight presets complete a Noise handshake against the real server, or each failure is
  attributed to the resolver or the network with its output.
- Each of the five `DefaultUTLS` fingerprints completes a handshake through at least one
  DoH and one DoT preset, or its failure is attributed; the result says which labels were
  seen and which were attributed.
- The temporary profile is deleted; no address, resolver, key or date is in a tracked
  file; the host check passes; no code changed.
