---
kind: task
status: todo
depends: [01-menu-and-commands, 02-profile-editing]
---

# Resolvers run out mid-session

## Task

The §10a promise: when the resolvers run out mid-session — none completes a handshake for a
full round — the menu offers to check and edit them and connects again.

The supervisor already detects exactly that case, but reports it only to its log and the
backoff: after a session has succeeded at least once (`heard`), a round of failed dials ends
with `cause := "no resolver of %d completed a handshake"` and a pause
(`internal/supervisor/tunnel.go:482-495`); the first connect instead stops with
`NeverConnectedError` (`:487-488`), which `connect` reports with its own four-way message and
the menu does not need to offer anything. The pool puts failed resolvers aside for a minute
and grows that time (`internal/resolvers/pool.go:17-21`, `:88-121`), so a full round means the
tunnel is down for a while, not a one-query blip.

The change is one narrow seam, not a state-machine rewrite: the supervisor gets an optional
callback (nil in the command path, so `connect` is unchanged) fired when the `heard` round
above fails, with the failed resolvers and their errors. The menu installs it for a
connection, offers at most once per connection until the resolvers change, and on a yes runs
the child 2 editor and reconnects with the saved list; on a no it keeps the connection
retrying as now.

Out of scope: the supervisor's retry and backoff policy, the resolvers' side-aside times, and
`connect`'s output on the first connect.

## Done when

- The supervisor fires the callback exactly at the full failed round after a success, once
  per round, and never on the first connect (which still returns `NeverConnectedError`), and
  never while a session is healthy; the command path passes nil and its behaviour and output
  are unchanged.
- The menu offers "check and edit resolvers? (y/N)" when the callback fires, at most once per
  connection until the list changes; the connection keeps retrying while the question is
  shown, and the prompt does not block the supervisor.
- A yes runs the check, offers to drop the dead ones, opens the editor, saves the list and
  reconnects with it; declined or unchanged, the offer is not repeated for that connection.
- A test with a pool whose every dial fails after one success proves the callback fires once
  per round; menu tests with a fake prove the offer, the once-only rule, the declined path
  and the reconnect with the saved list.
- The local matrix of child 1 passes.
