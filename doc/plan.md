# dnstt-cli implementation plan

A step-by-step plan for `architecture.md`. Decisions R1–R9, core patches P1–P7 and stages
E0–E5 are defined there; here are the tasks, their order and how we know each is done.

How to use it:

- **Task** — one logical change and, as a rule, one commit. A task number is the stage
  letter and a number (`F3`, `S2`); commits and the stage log refer to it.
- **Stage log** — `doc/local/plan-eN.md`, outside git: the course of work, the environment,
  live runs, decisions on the way. Only results go into this file, without dates or
  environment.
- **Done when** — a checkable condition: a test, a command or a live run. A task is not
  closed without it.
- **Carrying over from csqtt-cli** means copying, renaming and tests. csqtt-cli is MIT and
  may be read; the closed sources are listed in `AGENTS.md`.

```
E0 foundation ──▶ E1 socks ──▶ E2 Linux tun ──▶ E3 Windows tun ──▶ E4 menu and release ──▶ E5 SSH exit
```

---

## Status

- Done: rules (`AGENTS.md`), `doc/analysis.md`, `doc/architecture.md`, this plan.
- Next: E0, starting with F0.
- Open decisions: the license (MIT recommended, `analysis.md` §6) — needed before the
  first release (E4), not for E0.
- Working notes in `doc/local/` do not survive a cloud session (the directory is outside
  git): everything a new session needs is in this file and in `architecture.md`.

Update this section at the end of every session.

---

## E0. Foundation

Goal: the repository builds, the core is vendored and tested, the packages carried over
from csqtt-cli pass their tests, CI is green on Linux and Windows. No server or network
access to a tunnel is needed yet.

### F0. Workspace (no commit)

- Clone next to the repository: the core mirror `https://github.com/FreedomCats/dnstt`
  (as `../dnstt`) and `https://github.com/FreedomCats/csqtt-cli` (as `../csqtt-cli`).
- Go: the version from csqtt-cli's `go.mod` (1.26.x). An older local Go fetches it by
  itself through `GOTOOLCHAIN=auto` when `go.mod` asks for it.
- `git config user.name FreedomCats` and `user.email noreply@freedomcats.invalid` in the
  repository (`AGENTS.md`, "Commits").
- Start the stage log `doc/local/plan-e0.md`.

**Done when** `go version` in the repository reports 1.26.x and both clones are in place.

### F1. Module and skeleton

- `go.mod`: `module github.com/FreedomCats/dnstt-cli`, the Go version of csqtt-cli.
- `internal/app`: `Name = "dnstt-cli"` — every name the program leaves in the system comes
  from it (adapter, nftables table, WFP provider, locks, config directory, resolvconf
  entries, secret labels).
- `cmd/dnstt-cli/main.go`: `version` and `commit` set by `-ldflags`, `cli.Main`.
- `internal/cli`: a minimal `Main` — `version`, `help` and a command list; the real
  commands come in E1.
- `README.md`: what the program is, that it is in development, that the core is dnstt by
  David Fifield (public domain).

**Done when** `go build ./...` and `go vet ./...` pass and `go run ./cmd/dnstt-cli version`
prints `dev`.

### F2. Vendoring the core — `scripts/vendorcore`

- Carry the rule engine over from csqtt-cli `scripts/vendorcore` (`rules.go`, `apply.go`,
  `main.go`): a rule is a file, a pattern, a replacement and a mandatory match count; files
  of our own come from `scripts/vendorcore/files/`; excluded upstream files must exist
  upstream; `-check` compares `core/` with the upstream plus rules and names every extra or
  changed file. Not carried: `crates.go` and everything about Cargo; `sockets.go` (the
  socket guard) comes in F3 rewritten for Go.
- What is carried from the mirror:

  | Upstream | `core/` | Notes |
  |---|---|---|
  | `dns/` | `core/dns/` | as is, `fuzz.go` included (build tag `gofuzz`) |
  | `noise/` | `core/noise/` | as is |
  | `turbotunnel/` | `core/turbotunnel/` | as is |
  | `dnstt-client/` | `core/client/` | `package main` → `package client`; `func main` stays — legal in a non-main package and unused |
  | `dnstt-server/` | `core/server/` | `package main` → `package server`; used only by tests (F3) |
  | `COPYING` | `core/COPYING` | the CC0 text goes with the code |

  `README`, `CHANGELOG`, `TODO`, `man/`, `go.mod`, `go.sum` stay upstream; their
  absence in `core/` is not an error.
- Go conversion, done by the command, not by rules: `package main` → the package name of
  the target directory; the import prefix `www.bamsoftware.com/git/dnstt.git/` →
  `github.com/FreedomCats/dnstt-cli/core/`; CRLF → LF before the rules.
- Upstream dependencies (`flynn/noise`, `refraction-networking/utls`,
  `xtaci/kcp-go/v5`, `xtaci/smux`, `x/crypto`, `x/net`) go into our `go.mod` at the
  upstream versions; updating uTLS is P5 (E1).
- `core/CORE_VERSION`, written by the command: upstream URL (the mirror), commit, commit
  date, version from the first heading of `CHANGELOG` (`v1.20260501.0`), numbers of files,
  rules and added files.
- No rules yet except the package and import conversion — the engine and its tests already
  work on an empty set.

**Done when** `go run ./scripts/vendorcore -upstream ../dnstt` fills `core/`, `go build
./core/...` and `go test ./core/...` pass (the upstream tests included), `-check` passes,
and a manual edit of any file in `core/` makes `-check` fail with that file's name.

### F3. P1 — the session API and the socket hook

New files in `scripts/vendorcore/files/client/` (they land in `core/client/`), rules only
where an upstream line must change:

- `session.go`: `Config`, `Dial(ctx, Config) (*Session, error)`, `Session` with
  `OpenStream`, `Done`, `Err`, `Stats`, `Close` (`architecture.md` §04). `Dial` builds
  transport → `NewDNSPacketConn` → KCP → Noise → smux exactly as upstream `run` does
  (same KCP and smux settings), and returns after the Noise handshake. The client-side
  smux keepalive timeout is a `Config` field (default: upstream's 2 min until E1 measures
  a shorter one).
- Transports in a new file, built from the upstream constructors (`NewHTTPPacketConn`,
  `NewTLSPacketConn`, `NewUTLSRoundTripper`) with `Config.Control` on every dialer and
  `Config.LookupIP` for host names (P4 comes in E1; in F3 a `nil` `LookupIP` keeps the
  system resolver).
- Rules: `utlsDialContext` takes the `net.Dialer` from its caller instead of building
  `&net.Dialer{}` itself; nothing else in upstream files.
- `core/server/testing.go` (added file): a test helper that starts the upstream server
  `run` on a loopback UDP socket with a generated key and a given upstream TCP address.
- Socket guard (`scripts/vendorcore/sockets.go`, Go version of the csqtt-cli guard): scans
  `core/` for `net.Dial*`, `net.Listen*`, `net.Dialer{`, `tls.Dial*`, `tls.Dialer{`,
  `http.Transport`, `net.Resolve*`, `LookupHost`/`LookupIP` and checks every hit against a
  table (file, call, count, reason: a hooked point of §06, the server used only in tests,
  or test code). A new hit or a changed count fails `vendorcore` and `-check`.

**Done when** a test in `core/client`:

1. starts the in-process server with an echo TCP upstream, dials it over UDP on loopback,
   opens two streams in parallel and gets the echo back;
2. counts `Control` calls — one per socket the client created;
3. stops the server and sees `Session.Done` closed and `Err` set within the (shortened in
   the test) keepalive timeout;
4. `vendorcore -check` passes and the guard fails when a `net.Dial` is added to a core file.

### F4. Packages carried over from csqtt-cli

Everything visible to the system is renamed through `app.Name`. Constants by which the
program recognises its own traces get **new** values, not csqtt-cli's: adapter GUID
(`winsys.AdapterGUID`), WFP provider and sublayer GUIDs, the `SO_MARK` value. Both
programs can live on one machine, and neither may take the other's filters or adapter for
its own. Test environment variables `CSQTT_TEST_*` → `DNSTT_CLI_TEST_*`.

| Package | What is carried | What is not |
|---|---|---|
| `netguard` | everything: interface choice, `Control`, `watch.go` | — |
| `linuxsys` | everything: adapter, routes, system DNS, `DNSGuard`, kill-switch, monitor, `rp_filter` | — |
| `winsys` | everything; WFP "own program" rules — one app ID instead of two | the `csqtt-core.exe` app ID |
| `sudo`, `secretservice` | everything | — |
| `lines` | everything | — |
| `config` | config directory, file ownership under `sudo`, secret stores (DPAPI, keyring, file) | profile schema, validation, `csqtt://` links — rewritten in E1 and E4 |
| `datapath` | `datapath.go` interfaces (`Requirements`, `Persistent`, `Reserver`, `DNSGuard`, `IPv6Blocker`) | `link*`, `tunfd*` (channels to a core process); `socks*`, `netstack`, `tun*` come in E1 and E2 |
| `scripts` | `internal/script`, `internal/wintun`, `wintun`, `checktunnel`, `ctrlbreak`, `distcheck`, `sha256sums` | `buildcore`; `release`, `notices`, `releasenotes` — in E4 |

The `tailscale/wf` fork comes with the same `replace` as in csqtt-cli. Comments carried
over may stay in Russian (`AGENTS.md`, "Language"); new and changed comments are English.

**Done when** `go test ./...` passes on Linux and on Windows, `-race` too, and `git grep -i
csqtt` finds only the credits to the base project in the documentation.

### F5. CI

Two workflows, as in csqtt-cli: `.github/workflows/linux.yml` and `windows.yml`. Actions
pinned by commit hash. In each:

- `test` — `go vet ./...`, `go test -race ./...`; on Linux with `DNSTT_CLI_TEST_DBUS=1`;
- `netns` (Linux) — `linuxsys` tests that change the network, in a container with
  `DNSTT_CLI_TEST_NETNS=1`; a skip is a failure;
- `core` — the mirror cloned at the commit from `core/CORE_VERSION`, then `go run
  ./scripts/vendorcore -check`.

No Cargo and no core build step: the core is Go and built by `go build`.

**Done when** both workflows are green on `main`.

### F6. Close the stage

- `architecture.md`: what E0 established (Go version, dependency versions, anything the
  core turned out to need).
- This file: E0 results in one paragraph, `Status` updated, E1 tasks detailed (below is
  only their outline).

**Done when** the documents are committed and the next session can start E1 from this file
alone.

---

## E1. `socks` with reconnection — outline

Detailed at the end of E0.

- **S0. Test server.** The user's own dnstt server with `danted` and a login; its values
  live only in `doc/local/`. Plus the in-process server of F3 for automated tests.
- **S1. P2–P5.** DoT redial with backoff; a socket per query in UDP mode; `LookupIP` for
  resolver names (bootstrap over bound sockets, `@address` pins); newer uTLS.
- **S2. Exit — `internal/exit`.** SOCKS5 client over a stream: `CONNECT` with a domain or an
  address, no auth or RFC 1929 login; a login refusal is its own error type.
- **S3. Resolvers — `internal/resolvers`.** The list, `doh:`/`dot:`/`udp:` parsing, presets
  with addresses, health and putting aside.
- **S4. Supervisor.** States of §08, backoff, server silence on the first connect,
  `Restart`, the second Ctrl+C; the measured keepalive timeout.
- **S5. `socks` datapath.** Local SOCKS5 server on loopback → exit; refusals at once.
- **S6. Profiles and secrets.** `profiles.toml` schema of §10, validation, the exit password
  in the secret store.
- **S7. Commands.** `connect`, `profile add|set|show|rm|list`, `resolvers check`, the status
  line.
- **S8. Live acceptance.** A browser through `socks` to the server's `danted` with a login;
  the session survives a server restart and a network change; `resolvers check` on a list
  with a dead resolver.
