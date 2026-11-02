---
kind: task
status: todo
---

# Live acceptance

## Task

The stage's goal checked against a real server, on both platforms, and the numbers that
were left preliminary until then. `E1`, `S8`. Needs the user: their own dnstt server with a
SOCKS5 exit and a login, the resolvers that pass the tunnel from their network, and access
to a Windows machine.

## Steps

1. A live-run script in `doc/runs/` that starts the upstream `dnstt-client` against the
   same server, so that a failure can be attributed: server or dnstt-cli.
2. A browser through the local proxy to the server's exit with a login, once per transport:
   DoH, DoT and UDP.
3. A server restart in the middle of a session, and a network change: the session comes
   back by itself and new connections work without restarting the client.
4. `resolvers check` on a list that contains a dead resolver: the dead one is named.
5. Every built-in preset completes a handshake against the real service.
6. Measure on the live session: the client keepalive timeout to set against the upstream
   2 min, and the handshake timeout to set against the preliminary 20 s.
7. Whether `danted` takes the SOCKS5 greeting, login and request in one write. If it does,
   add it as a setting of the exit (two round trips saved per connection, R3).
8. Repeat on Windows.
9. Record the results without the environment, addresses or resolvers in the stage node and
   in `architecture.md`; keep the course of the runs in `doc/local/` (`AGENTS.md`).

## Done when

All of the above has been seen live and recorded; the measured timeouts are set in the code
and the deviations are noted.

## Depends on

[Commands](../07-commands/plan.md) and [Profiles and secrets](../06-profiles-secrets/plan.md):
there is nothing to point at a server before them.

## Risks and what is needed from the user

- The server's domain, key, address, exit login and the working resolvers are the user's;
  they are given in chat and kept only in `doc/local/` (`AGENTS.md`).
- A network that blocks every transport would turn this into a design problem, not an
  acceptance run: name it when it happens.
