---
kind: task
status: done
detailed: true
---

# SOCKS5 baseline live-run tool

## Task

Preserve the already implemented local live-run tool as the SOCKS5 baseline for the
acceptance stage. For one resolver at a time it runs the upstream `dnstt-client` and then
the in-process `dnstt-cli` stack against the same server, fetches a page through each,
and attributes a failure to the shared server/exit/resolver/network or to `dnstt-cli`.
This child owns the reviewed profile diagnostics, secret-store command, SOCKS5 probe and
local selftest. The HTTP protocol extension is a separate child.

Continue from the implementation and review evidence retained in the parent stage's
Outcome and the local journal; do not recreate or discard the ignored source. The
coordinator verifies and accepts the existing local result against this child's criteria.
The former user-dependent DoH/UDP criterion belongs to `04-live-runs`, so it does not keep
this implementation child open after its local acceptance.

The tool lives in `doc/runs/_live/`, outside git (`.gitignore`, `AGENTS.md`): it reads the
user's real profile and secret store, and its logs hold resolvers and addresses. A Go
program, like everything under `scripts/`, serves Linux and Windows. The leading `_`
keeps it out of `./...`: `go build ./...`, `go vet ./...` and CI never see it, and it runs
as `go run ./doc/runs/_live <command>`. Inside the module it may import `internal/...` and
`core/...`.

`E1`, `S8`; former plan (`notes/former-plan.md`, S0): upstream `dnstt-client` against the
user's server, so that a failure can be told apart.

## Steps

1. **Entry.** Keep `doc/runs/_live/main.go`, `package main`, with `compare`, `secret`,
   `selftest` and help. The header comment states that the tool never prints the exit
   password or server key and keeps output and logs in `doc/runs/` and `doc/local/`.
2. **Profile and resolver.** Use `config.LoadProfiles`; `-profile` selects a profile or the
   default. Select `-resolver` as an entry, preset or `network`, or expand every profile
   resolver through `resolvers.Expand` (`["network"]` when empty). Resolve `network` through
   the first server from `linuxsys.NetworkDNS()` or `winsys.NetworkDNS(0)` in the existing
   `network_linux.go`, `network_windows.go` and `network_other.go`, and use that resolver
   for both sides.
3. **Upstream side** (`upstream.go`). Preserve the clone or ready-binary selection, core
   revision warning, temporary build, resolver flags, uTLS override, private mode-0600
   public-key file, loopback listener and kill/wait lifecycle. Do not put the key on the
   command line or in logs.
4. **`dnstt-cli` side** (`cli.go`). Preserve the in-process `app.BuildTunnel` and
   `app.BuildDatapath` path with one selected resolver, `Datapath = "socks"` and
   `Socks.Listen = "127.0.0.1:0"`. Wait for `StateConnected`, record the handshake, and
   cancel, close the datapath and wait for `Supervisor.Run` on exit.
5. **SOCKS5 probe** (`probe.go`). Keep the independent `golang.org/x/net/proxy` SOCKS5
   client so the probe does not share `internal/exit` code with the path it checks. Pass
   the target host to the far side, authenticate the upstream side with the profile's
   exit credentials, and leave the local `dnstt-cli` listener unauthenticated. Bound each
   attempt by `-timeout` and require a status below 400 plus a complete response body.
6. **Verdict** (`compare.go`). Run upstream and `dnstt-cli` serially. Preserve the four
   verdicts, filtered errors and the last 20 upstream log lines, with exit code 1 for a
   `dnstt-cli` verdict, 2 for setup failure and 0 otherwise. Never print the password,
   public key, tunnel domain or private resolver values.
7. **`secret`** (`secret.go`). Preserve hidden input through `golang.org/x/term`, the
   platform secret store, read-back verification and the printed reference. Do not rewrite
   `profiles.toml`; the user keeps comments and layout. Windows storage remains bound to
   the account that runs `connect`.
8. **SOCKS5 `selftest`** (`selftest.go`). Keep the isolated temporary config directory,
   `httptest` target, direct local SOCKS5 exit, `core/server.StartTest`, generated identity
   filtering, both-side success while the server runs and both-side failure with verdict
   `not dnstt-cli` after the server stops.
9. **Local README.** Preserve `doc/runs/_live/README.md` instructions for commands, flags,
   verdicts, logs, upstream selection, profile setup and the privacy rule. The HTTP child
   adds its protocol-specific instructions there.
10. **Checks.** Run the checks in "Done when" and ensure the tool remains ignored and absent
    from `go list ./...`.

## Done when

- `go vet ./doc/runs/_live` and `GOOS=windows go vet ./doc/runs/_live` are clean.
- `go list ./...` does not list the tool; the project build, established vet command and
  race tests remain clean. Run `go test -race -count=1 ./doc/runs/_live` explicitly,
  because `./...` skips it; retain a Windows tool cross-build/test-compilation check.
- `go run ./doc/runs/_live selftest` passes on Linux for the SOCKS5 baseline: both sides
  pass with the server up, both fail with `not dnstt-cli` after it stops, and the generated
  key is absent from output and logs.
- The safe profile-loader diagnostics, secret-store read-back and private log checks remain
  covered by the focused tests.
- `git check-ignore doc/runs/_live/main.go` names the ignore rule and the tool changes no
  tracked file.

## Risks and what is needed from the user

- The ignored tool is reviewed directly under `doc/runs/_live/`; only this roadmap node is
  tracked. The HTTP child must preserve this baseline while extending its protocol choice.
- Building the upstream client needs cached module dependencies; `-client-bin` remains the
  fallback. On Windows, `-upstream` points to a checkout outside this repository.
- Authenticated SOCKS5 and HTTP user runs, including their resolvers, belong to the later
  live-runs stage. No live profile, credential or run result is part of this node.

## Outcome

The preserved local SOCKS5 baseline is accepted after a fresh code review and local
recheck. Its source and tests remain under ignored `doc/runs/_live/`; no implementation
changes were required. Acceptance used isolated synthetic configuration and loopback,
without reading or changing a real profile or secret store.

- The saved tool provides `compare`, `secret`, `selftest` and help. The upstream process
  and in-process application run serially with the same selected resolver; the probe uses
  an independent SOCKS5 client. Password storage verifies read-back and leaves the profile
  file unchanged. Safe loader diagnostics and private log filtering retain their focused
  synthetic tests.
- The coordinator repeated the SOCKS5 selftest: upstream and `dnstt-cli` both fetch the
  complete expected page while the local server runs, then both fail with verdict
  `not dnstt-cli` after it stops. The generated identity is absent from captured output
  and checked logs. Captures and run logs remain outside git.
- The local README now separates this baseline's local acceptance from the later live
  gate, preserves the SOCKS5 setup and comparison instructions, and documents the CI vet
  split and Windows runtime limitations.
- Tool vet passes for Linux and Windows, and its explicit race suite passes. The project
  build and full race suite pass, including the mandatory isolated D-Bus tests. The CI
  vet split passes with all checks enabled outside `core/` and
  `-testinggoroutine=false` only for `core/`. Plain project vet retains only the two
  existing upstream warnings in `core/noise/noise_test.go:158,209`.
- Windows cross-builds of the tool and product and test compilation for the tool and all
  applicable product packages pass. Windows binaries were not run.
- Focused tests retain safe profile-loader diagnostics, secret-store read-back and
  private log checks. The fresh review found no defects. The tool remains ignored and
  excluded from `go list ./...`; checks changed no tracked files. This commit records
  only this roadmap node.
- The upstream revision remains unverifiable with the available source, and the selftest
  emits the expected warning. Different default uTLS distributions remain a documented
  comparison caveat; an explicit upstream-compatible shared distribution can be selected.

HTTP product support and HTTP comparison remain separate children. Authenticated user
runs remain required in `04-live-runs`; neither loopback selftest nor Windows
cross-compilation establishes live or Windows runtime acceptance. Real setup stays
deferred until HTTP product support is accepted.
