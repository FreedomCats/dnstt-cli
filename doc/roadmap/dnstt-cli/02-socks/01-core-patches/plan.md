---
kind: task
status: done
detailed: true
---

# Core patches P2–P5

## Task

Four departures from the upstream that the shell needs and the upstream does not have
(`architecture.md` §07): DoT has to keep redialing, UDP mode has to spread its queries over
sockets, the names of DoH/DoT resolvers must not go to the system resolver, and uTLS must
be recent. Each is a rule or an own file in `core/`, with a test. `E1`, `S1`.

## Steps

1. **P2** — DoT redial with a backoff (1·2^n s up to 64 s) instead of giving up after one
   failed redial; the end of the session stops it. Against a TCP echo resolver whose dials
   fail.
2. **P3** — UDP mode: a socket per query through `Control`, opened for the resolver's
   family; wait for an answer from any address; a limit on the queries in flight and
   a dropped one counted as a lost packet. Check `netguard`'s choice of families for a
   `udp6` socket on `:0`.
3. **P4** — host names of DoH and DoT resolvers through `LookupIP` only; `Dial` refuses a
   host name without `LookupIP`; the `net.DefaultResolver` entry leaves the guard table.
4. **P5** — uTLS `v1.7.3`, `Chrome_131` and `Safari_16_0` in the table, our own
   `DefaultUTLS`, every fingerprint tested through a DoH request to a Go TLS server.
5. The core's transport log output goes through a `logf` the constructor takes, and `Dial`
   passes `Config.Logf`, so `-v` shows the core and the default output stays quiet.
6. Update the guard table for every new socket.

## Done when

The tests of each patch pass, the guard table is updated, and `vendorcore -check` passes.

## Outcome

- P2: `TLSPacketConn.redial` (own file) retries a failed redial after 1 s, 2, 4… up to 64 s
  until the transport is closed; tested against a TCP echo resolver whose dials fail.
- P3: `udpTransport` (own file) sends every query from a new socket through `Control`,
  waits up to 10 s for one answer from any address, keeps at most 256 queries waiting and
  drops one over the limit as a lost packet; a session through a forwarding resolver sees
  a source port per query. `netguard` reads `IPV6_V6ONLY`, so a `udp6` socket on `:0` is
  bound for IPv6 only (`architecture.md` §06).
- P4: `Dial` refuses a host name without `LookupIP` at once, also for DoH and DoT, which
  dial only when they first send; the `net.DefaultResolver` entry left the guard table.
- P5: uTLS `v1.7.3`, not `v1.8`: from `v1.8.0` about one randomized handshake in seven
  fails against a server that prefers X25519MLKEM768 (`architecture.md` §04);
  `Chrome_131` and `Safari_16_0` in the table; our own `DefaultUTLS`, every fingerprint of
  it tested through a DoH request to a Go TLS server.

On the way: the upstream server, used only by tests, could panic when a test stopped it (a
rule keeps its channel open).

Not checked yet: the new fingerprints against public DoH and DoT resolvers — live
acceptance.
