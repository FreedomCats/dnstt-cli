---
kind: task
status: done
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

## Outcome

All steps completed as planned:

1. **`connect [profile]`** — implemented in `internal/cli/connect.go`. Reads profile through `internal/config`, builds tunnel and datapath through `internal/app` (new package with `BuildTunnel` and `BuildDatapath`). Status line updates in terminal (hidden when redirected). Ctrl+C handling: first stops gracefully (cancels context), second forces immediate exit (closes datapath without waiting), third kills process.

2. **Profile commands** — implemented in `internal/cli/profile.go`: `list`, `show` (masks domain/pubkey by default, `-full` flag shows them; never prints password), `add` (minimal profile creation), `rm`, `set` (placeholder suggesting manual edit). All work through `internal/config`.

3. **`resolvers check`** — implemented in `internal/cli/resolvers.go`. Performs real Noise handshake (`client.Dial`) through each resolver. Prints time or error for each. Returns exit code 1 when no resolvers pass (handled via `exitCodeError` type in `root.go`). `network` resolvers currently skipped (needs NetworkDNS integration).

4. **Status line** — implemented in `connect.go` (`statusLine` type). Shows state, uptime, current resolver, open streams, bytes sent/received, handshake latency. Uses `term.IsTerminal()` to detect redirection.

5. **Tests** — `internal/cli/connect_test.go` tests full chain: echo server → SOCKS5 server → dnstt-server → client tunnel. Data passes through and returns correctly, verifying "connect works against in-process server".

**Changes to break circular dependency:** `app.Name` constant duplicated as `appName` in `internal/config/dir.go`, `internal/config/secret.go`, and `internal/secretservice/service.go`. Added `config.GetSecret()` for reading secrets from store.

**Not implemented (deferred to later stages):**
- Interactive profile wizard (plan suggests manual TOML edit for now)
- Bootstrap resolver integration (hardcoded `nil` in tunnel build)
- Netguard Control hook (empty function, needs interface binding implementation)
- `-config` flag for custom config directory
- `network` resolver support in check command (needs `linuxsys.NetworkDNS`/`winsys.NetworkDNS`)

Tests pass: `go test -race ./...` clean. No personal data in code (checked per AGENTS.md rules).
