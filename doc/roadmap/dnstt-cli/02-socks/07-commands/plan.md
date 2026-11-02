---
kind: task
status: todo
---

# Commands

## Task

The command line of the SOCKS5 stage: `connect`, the profile commands, `resolvers check`
and the status line. Until this node the parts work but nothing a user can call. `E1`,
`S7`; `architecture.md` §10a for the status line, R5 for the check.

## Steps

1. `connect [profile]` — reads the profile, builds the tunnel and the datapath, prints the
   status line, stops on Ctrl+C (the second one without waiting for open connections).
2. `profile add|set|show|rm|list` — through `internal/config`; `show` never prints the exit
   password, and prints the domain and key only on an explicit request.
3. `resolvers check` — a real handshake through every resolver of the profile, each with
   its result; exit code 1 if none passes.
4. The status line: state, uptime, current resolver, open streams, received and sent,
   latency of the last handshake; absent when output is redirected.
5. Tests: command parsing and output, and `dnstt-cli connect` against the in-process
   server.

## Done when

Command tests pass and `dnstt-cli connect` works against the in-process server in a test.

## Depends on

[Profiles and secrets](../06-profiles-secrets/plan.md), [SOCKS5 datapath](../05-socks-datapath/plan.md).
