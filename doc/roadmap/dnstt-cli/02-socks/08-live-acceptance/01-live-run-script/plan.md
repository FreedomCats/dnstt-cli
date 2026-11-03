---
kind: task
status: todo
detailed: true
---

# Live-run tool

## Task

A tool for the live runs: for one resolver at a time it runs the upstream `dnstt-client`
and then the `dnstt-cli` stack against the same server, fetches a page through each, and
says which side a failure belongs to — the server and the network, or `dnstt-cli`. Every
later run of this stage leans on it. It also stores the exit password, which no command
can do yet, and checks itself against a local server without the user.

It lives in `doc/runs/_live/`, outside git (`.gitignore`, `AGENTS.md`): it reads the
user's real profile and secret store, and its logs hold resolvers and addresses. A Go
program, like everything under `scripts/`, so one tool serves Linux and Windows. The
leading `_` keeps it out of `./...`: `go build ./...`, `go vet ./...` and CI never see it,
and it runs as `go run ./doc/runs/_live <command>`. Inside the module it may import
`internal/...` and `core/...`.

`E1`, `S8`; former plan (`notes/former-plan.md`, S0): upstream `dnstt-client` against the
user's server, so that a failure can be told apart.

## Steps

1. **Entry.** `doc/runs/_live/main.go`, `package main`: commands `compare`, `secret`,
   `selftest`, `-h`. A header comment: the tool never prints the exit password or the
   server key, and its output and logs stay in `doc/runs/` and `doc/local/`.
2. **Profile and resolver.** `config.LoadProfiles`; `-profile` or the default profile.
   Resolvers: `-resolver` (an entry, a preset name or `network`), or every resolver of the
   profile in turn, through `resolvers.Expand` (`["network"]` when the list is empty).
   `network` becomes `resolvers.NetworkResolver` of the first server of
   `linuxsys.NetworkDNS()` / `winsys.NetworkDNS(0)` — files `network_linux.go`,
   `network_windows.go`, `network_other.go` (an error there) — for both sides, and the
   output says so: `connect` cannot read the network's DNS yet
   (`internal/app/tunnel.go:132`).
3. **Upstream side** (`upstream.go`).
   - The clone: `-upstream`, by default `dnstt` next to the repository root (the directory
     with `go.mod`), as `scripts/vendorcore` takes it. `git -C <clone> rev-parse HEAD`
     against `commit` in `core/CORE_VERSION`: a different commit is a warning, not an
     error.
   - Build `dnstt-client` (`.exe` on Windows) into a temporary directory with
     `go build -o`, `cmd.Dir` = `<clone>/dnstt-client`. `-client-bin` takes a ready binary
     instead.
   - Flags from the resolver: `doh:` → `-doh URL`, `dot:` → `-dot host:port`, `udp:` →
     `-udp host:port`. The upstream client resolves host names through the system and
     knows nothing of pins: print a note when the entry has a name or pins. `-utls` is
     passed when the profile or the tool's `-utls` flag sets one.
   - The key goes in a temporary file with mode 0600 (`-pubkey-file`), never on the command
     line: it would show in the process list and in the log. Removed afterwards.
   - The local address is a free loopback port. stderr goes to
     `doc/runs/logs/<UTC time>-<transport>-upstream.log`. Stop: `Process.Kill`, `Wait`.
4. **`dnstt-cli` side** (`cli.go`), in-process — the same code `connect` builds, from the
   working tree.
   - A copy of the profile: `Resolvers` = the one entry (for `network`, the `udp:` entry
     of step 2), `Datapath = "socks"`, `Socks.Listen = "127.0.0.1:0"`.
   - `app.BuildTunnel` with `Logf` into
     `doc/runs/logs/<UTC time>-<transport>-cli.log` (and stderr with `-v`) and
     `OnTransition` that catches the first `StateConnected`; `app.BuildDatapath`; the
     address through the type assertion `interface{ ListenAddr() net.Addr }`
     (`datapath.Socks.ListenAddr`).
   - `Supervisor.Run` in a goroutine; wait for `StateConnected`, the end of `Run` (a
     `*supervisor.NeverConnectedError`, a login refusal) or `-timeout`. Report
     `Supervisor.Status().Handshake`.
   - Stop: cancel the context, close the datapath, wait for `Run`.
5. **Probe** (`probe.go`). An HTTP GET of `-url` (default `https://example.com/`) through
   SOCKS5 with `golang.org/x/net/proxy` — a SOCKS5 client independent of `internal/exit`,
   so the probe does not share a bug with what it checks. The host name goes to the far
   side (remote resolution, like `curl --socks5-hostname`). The upstream side logs in to
   the exit with the profile's `exit.user` and `config.GetSecret(exit.password_ref)`; the
   `dnstt-cli` side talks to the local proxy without a login. `-n` attempts (default 3),
   each bounded by `-timeout` (default 90 s): time to the headers, status, bytes of the
   body. Success — a status below 400 and the body read to its end.
6. **Verdict** (`compare.go`). The upstream side, then the `dnstt-cli` side, never both at
   once. One line per resolver: its name, each side's result, the `dnstt-cli` handshake
   time, and the verdict:
   - both pass — OK;
   - upstream passes, `dnstt-cli` fails — **`dnstt-cli`**;
   - both fail — **not `dnstt-cli`**: the server, the exit, the resolver or the network;
   - upstream fails, `dnstt-cli` passes — upstream only (pins, the system resolver, the
     core patches P2–P3 are known differences).
   On a failure: the error of each side and the last 20 lines of the upstream log. Exit
   code 1 if any verdict is `dnstt-cli`, 2 on a setup error, otherwise 0. The exit
   password, the server key and the tunnel domain are never printed.
7. **`secret`** (`secret.go`). The exit password by hidden input
   (`golang.org/x/term.ReadPassword` on a terminal, otherwise one line of stdin), then
   `config.NewSecretStore().Put("<profile> exit", value)`, read back with `Get` to check.
   Prints the reference and `config.SecretRefKind(ref)`, and tells the user to put the
   reference into `exit.password_ref`. It does not rewrite `profiles.toml`: the user's
   comments and layout stay. On Windows the value is bound to the account it is stored
   under (DPAPI) — the one that will run `connect`.
8. **`selftest`** (`selftest.go`), no user and no network needed.
   - A temporary directory as the config directory: `XDG_CONFIG_HOME`, or `APPDATA` on
     Windows, set before any `config` call (`internal/config/dir.go` reads them on every
     call).
   - A target: `httptest` server with a known body.
   - Behind the dnstt server: `datapath.NewSocks` with a direct `exit.Dialer` (a
     `net.Dialer`) — a SOCKS5 server without a login; the login path is checked only live.
   - The server: `core/server.StartTest` for `t.example.com` with that SOCKS5 address
     behind it.
   - The profile through `config.SaveProfiles`: the domain, the generated key in hex,
     `resolvers = ["udp:127.0.0.1:<port>"]`, exit `socks5` without a user.
   - `compare` with the target's URL: both sides pass. Then `TestServer.Close` and
     `compare` with a short timeout: both fail, verdict "not `dnstt-cli`". PASS or FAIL and
     the exit code.
9. **`doc/runs/_live/README.md`** (local): the commands and flags, what each verdict
   means, where the logs go, `-upstream` and `-client-bin` for a machine where the clone is
   elsewhere, and the `AGENTS.md` rule that outputs and logs never go into the repository.
10. **Checks** — those of "Done when".
11. **With the user.** The user stores the exit password with `secret`, sets
    `password_ref`, and runs `compare` over a DoH and a UDP resolver of their own against
    their server. The output and the course go into `doc/local/`; the node's `Outcome` gets
    the verdicts without resolvers, addresses or timings.

## Done when

- `go vet ./doc/runs/_live` and `GOOS=windows go vet ./doc/runs/_live` are clean.
- `go list ./...` does not list the tool; `go build ./...`, `go vet ./...` and
  `go test -race ./...` are as before.
- `go run ./doc/runs/_live selftest` passes on Linux: both sides pass with the server up;
  after it stops both fail with the verdict "not `dnstt-cli`"; exit code 0. The generated
  key does not appear in its output.
- `git check-ignore doc/runs/_live/main.go` names the rule; `git status --short` shows no
  tracked file changed by the tool.
- With the user: over a DoH and a UDP resolver the upstream client reaches the site through
  the exit with a login, and `compare` gives a verdict for the `dnstt-cli` side; recorded
  in `doc/local/`.

## Risks and what is needed from the user

- The task's commit holds only this node (status, `Outcome`): the tool itself is outside
  git. The reviewer reads the files in `doc/runs/_live/` directly.
- Building the upstream client needs the clone's module dependencies (a download if they
  are not cached); `-client-bin` is the way around. On the Windows machine the clone may
  be elsewhere — `-upstream`.
- A host name without a pin fails on the `dnstt-cli` side unless the profile has its own
  `bootstrap` (`internal/app/tunnel.go:78`), while the upstream side resolves it through
  the system. The tool says so next to such an entry; it is a known gap, not a verdict.
- Needed from the user: their server with a SOCKS5 exit and a login, a profile for it,
  resolvers that pass in their network, and the run of step 11. The values are given in
  chat or typed into the tool; none of them goes into a file of the repository.
