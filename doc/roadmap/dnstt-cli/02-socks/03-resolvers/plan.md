---
kind: task
status: done
detailed: true
---

# Resolvers

## Task

`internal/resolvers` — the list of resolvers a profile connects through: how an entry is
written and parsed, the built-in presets, the bootstrap resolver for names, and health, so
that a resolver that fails the handshake or kills sessions steps aside instead of costing
every retry. `E1`, `S3`; `architecture.md` R5, §06 point 5.

The reason it exists: dnstt-cli is built for networks that let through only some resolvers.
The list is checked in order, and the default — `network`, the DNS the network gives the
system — follows the user from network to network.

## Steps

1. `Parse` reads `doh:URL`, `dot:host[:port]` (853 by default), `udp:host[:port]` (53), an
   IPv6 host bare or in brackets, and `@address[,address…]` that pins the addresses of a
   host name; an entry given by address needs no pin. `String` writes an entry back with
   its port, and `Parse` reads that back to the same resolver. `tcp:` is refused until P6.
2. Eight presets, `doh-`/`dot-` × cloudflare, google, quad9, adguard, each with its two
   IPv4 addresses pinned; `Expand` takes entries and preset names, drops repeats and names
   the unknown preset it was given.
3. `Resolver.LookupIP(fallback)` — the session's `LookupIP`: the pins for its own host, the
   fallback for anything else.
4. The bootstrap resolver: plain DNS over its own UDP sockets through `Control`, TCP after
   a truncated answer; A, then AAAA if there is no A; a silent server passed over after
   2 s, two rounds; NXDOMAIN and "no addresses" are final; answers cached up to their TTL,
   at most 5 min; `Forget` drops them.
5. Its servers (`BootstrapServers`, re-read on every lookup that is not cached): the
   profile's own `bootstrap` servers and only them if there are any; otherwise the DNS the
   network gave the system (`linuxsys.NetworkDNS`: resolv.conf, the servers behind the
   systemd-resolved stub, the copy of our own file; `winsys.NetworkDNS`: the interface of
   the default route), then the profile's UDP resolvers by address. No public resolver is
   asked unasked.
6. The entry `network` (`resolvers.NetworkEntry`, the default of a new profile) — the DNS
   the network gives the system at each dial; the supervisor reads it through
   `Options.NetworkDNS` and dials its servers one per dial, each counted in a full round.
7. Health (`Pool`): three failures in a row — a failed handshake, or a session that died by
   itself within a minute — put a resolver aside for 1 min, then 2, 4… up to 16; a session
   that lived a minute clears its record; `Next` goes down the list past the resolvers
   aside, and when all are aside takes the one that comes back first; `Reset` after a
   network change.
8. Tests against a DNS server in the test: UDP and TCP, truncation, an answer with another
   ID, a silent server, the cache.

## Done when

Parsing, presets, pins, the bootstrap resolver (against a DNS server in the test) and
putting aside are tested.

## Outcome

Done as planned. One own core file was added on the way — `TestServer.Reopen` — so that a
session can be seen to come back after the in-process server restarts; it is used by the
supervisor's test as well.

Not checked yet: the presets live. Each should complete a handshake against the real
service; that is live acceptance.
