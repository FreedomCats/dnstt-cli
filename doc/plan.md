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

- Done: rules (`AGENTS.md`), `doc/analysis.md`, `doc/architecture.md`, this plan; stage
  E0 (results below, under "E0. Foundation").
- E1: S1 (core patches P2–P5) and S2 (exit) done, results under each. Next: S3
  (resolvers); S0 (the user's test server) when the user has one: the server's values are
  asked for in chat and kept only in `doc/local/plan-e1.md`.
- CI is green on `main` (`linux`: test, netns, core; `windows`: test, core).
- A new session first sets up the workspace as in F0: the mirror as `../dnstt` and
  csqtt-cli as `../csqtt-cli`; `go run ./scripts/vendorcore -check` and `go test ./...`
  confirm the state.
- Carried over within E1: a full session over DoH and DoT and the new uTLS fingerprints
  against public resolvers are first seen live (S8); `profiles.toml`, `Save` and its sudo
  tests (S6).
- Open checks: `TestApplyReadsV6Only` (netguard) skips without IPv6 in the kernel; CI
  runs tests without `-v`, so it is not yet confirmed that it runs there rather than
  skips. uTLS stays at `v1.7.x` until the randomized-hello bug of `v1.8` is fixed
  (`architecture.md` §04; `TestDefaultUTLSFingerprints` catches it).
- Open decisions: the license (MIT recommended, `analysis.md` §6) — needed before the
  first release (E4).
- Working notes in `doc/local/` do not survive a cloud session (the directory is outside
  git): everything a new session needs is in this file and in `architecture.md`.

Update this section at the end of every session.

---

## E0. Foundation — done

Goal: the repository builds, the core is vendored and tested, the packages carried over
from csqtt-cli pass their tests, CI is green on Linux and Windows. No server or network
access to a tunnel is needed yet.

**Result.** Go 1.26.2; `core/` is the mirror at `v1.20260501.0` with P1 (15 rules, 5 own
files in `scripts/vendorcore/_files/`), checked in CI against the mirror. The session API
dials the upstream server run in-process and passes its tests under `-race`: parallel
streams, one `Control` call per socket, the end of a session when the server goes silent,
no goroutines left after `Close`, DoH and DoT connecting through `Control`. The socket
guard parses `core/` and fails on any socket outside its table. The csqtt-cli packages are
carried with new GUIDs and `SO_MARK` (0x1076) and translated into English. Deviations from
the tasks below, each recorded in `architecture.md`: own core files in `_files/` (a plain
`files/` was compiled by `go build ./...`); uTLS takes a dial *function* from its caller,
not a `*net.Dialer` — `LookupIP` has to sit between the name and the socket; P1 also ends
the upstream transport loops on close (upstream never closes a session); `distcheck` moves
to E4 with the release scripts. Not covered yet: a full session over DoH or DoT (the
in-process server speaks UDP only) — S8.

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
| `scripts` | `internal/script`, `internal/wintun`, `wintun`, `checktunnel`, `ctrlbreak`, `sha256sums` | `buildcore`; `release`, `notices`, `releasenotes`, `distcheck` — in E4 |

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

## E1. `socks` with reconnection

Goal (`architecture.md` §12): a browser goes through `dnstt-cli connect` in `socks` mode to
the server's `danted` with a login, on Linux and Windows, and the session survives a
server restart and a network change. Tasks in order; S1–S3 do not depend on each other.

### S0. Test server (no commit)

- The user's own dnstt server: `dnstt-server` with `danted` behind it, a SOCKS5 login. Its
  domain, key, address, login and the resolvers that pass the tunnel from the user's
  network live only in `doc/local/plan-e1.md` (`AGENTS.md`).
- A live-run script in `doc/runs/` that starts upstream `dnstt-client` against it, so a
  failure can be told apart: server or dnstt-cli.

**Done when** upstream `dnstt-client` plus `curl --socks5-hostname` through it reaches a
site, over DoH and over UDP.

### S1. Core patches P2–P5 — done

**Result.** P2: `TLSPacketConn.redial` (own file) retries a failed redial after 1 s, 2, 4…
up to 64 s until the transport is closed; tested against a TCP echo resolver whose dials
fail. P3: `udpTransport` (own file) sends every query from a new socket through `Control`,
waits up to 10 s for one answer from any address, keeps at most 256 queries waiting and
drops one over the limit as a lost packet; a session through a forwarding resolver sees a
source port per query. `netguard` reads `IPV6_V6ONLY`, so a `udp6` socket on `:0` is bound
for IPv6 only (`architecture.md` §06). P4: `Dial` refuses a host name without `LookupIP`
at once, also for DoH and DoT, which dial only when they first send; the
`net.DefaultResolver` entry left the guard table. P5: uTLS `v1.7.3`, not `v1.8`: from
`v1.8.0` about one randomized handshake in seven fails against a server that prefers
X25519MLKEM768 (`architecture.md` §04); `Chrome_131` and `Safari_16_0` in the table; our
own `DefaultUTLS`, every fingerprint of it tested through a DoH request to a Go TLS server.
The transports log through a logf their constructor takes (rules), and `Dial` gives them
`Config.Logf`. On the way: the upstream server, used only by tests, could panic when a test
stopped it (a rule keeps its channel open). Not checked yet: the new fingerprints against
public DoH and DoT resolvers — S8.

As rules and own files, each with a test against the in-process server where it can be
made:

- **P2** — DoT redial with a backoff (1·2^n s up to 64 s) instead of giving up after one
  failed redial; the session's end stops it.
- **P3** — UDP mode: a socket per query (or a small rotating pool), all through `Control`;
  the per-query socket is opened for the resolver's family. Check `netguard`'s choice of
  families for a `udp6` socket on `:0` (E0 note: it is taken as dual-stack).
- **P4** — host names of DoH/DoT resolvers through `LookupIP` only; the `net.DefaultResolver`
  entry leaves the guard table. The bootstrap resolver itself is S3.
- **P5** — newer uTLS and its fingerprints; `DefaultUTLS` follows the upstream default or
  is our own, decided here.
- The upstream `log` output of the core goes to `Config.Logf` (a rule or a logger set per
  session), so `-v` shows it and the default output stays quiet.

**Done when** the tests of each patch pass, the guard table is updated, `vendorcore -check`
passes.

### S2. Exit — `internal/exit` — done

**Result.** `exit.Dialer` (`Dial(ctx, "host:port")`) and its SOCKS5 implementation over
`exit.OpenStream` (`Session.OpenStream`). A host that parses as an address goes as IPv4 or
IPv6, anything else as a name resolved on the server. The greeting always offers "none", and
"username/password" too when the profile has a login, so an exit open to anyone never gets
the password. Errors: `*LoginError` (wrong login, a login required and the profile has
none, no offered method accepted) — `IsLogin` is the supervisor's reason to stop;
`*ConnectError` with the RFC 1928 reply code for the datapath to pass on; `ErrNotSOCKS5`
for an exit that answers as something else; anything else is of the stream or the session.
`Check(ctx)` logs in without a request, for the supervisor on a new session. The exchange
is bounded by ctx and by 60 s of its own; the stream comes back without deadlines, and the
reply is read to its end, so the target's first bytes stay in the stream. Messages go one
at a time, each after its answer (`architecture.md` R3). Tests against a SOCKS5 server in
the test, in memory and through the in-process dnstt server (a name to an echo server, a
refused port, a wrong password), all under `-race`.

SOCKS5 client over a stream: `CONNECT` with a domain or an address, authentication "none"
or RFC 1929 login; a login refusal is its own error type (`Stopping` in the supervisor),
other failures are retries. `exit.Dialer` over a function that opens a stream.

**Done when** tests against a SOCKS5 server in the test (with and without a login, wrong
password, refused `CONNECT`) pass, and a test through the in-process dnstt server reaches
an echo server behind a SOCKS5 server.

### S3. Resolvers — `internal/resolvers`

The list of a profile: `doh:URL`, `dot:host:port`, `udp:host:port`, `@address` pins;
presets with built-in addresses; the bootstrap resolver for host names over sockets with
`Control` (point 5 of §06); health: a resolver that fails the handshake or kills sessions
repeatedly is put aside for a while.

**Done when** parsing, presets, pins, the bootstrap resolver (against a DNS server in the
test) and putting aside are tested.

### S4. Supervisor — `internal/supervisor`

States of §08: dialing through the list, a handshake timeout, backoff after a full round,
server silence on the first connect (stop, naming the four reasons), `Restart` from
`netguard.Watch`, the second Ctrl+C. The client keepalive timeout measured on the live
server (S0) and set.

**Done when** state tests with a fake dialer pass, and a test with the in-process server
sees a session recreated after the server restarts.

### S5. `socks` datapath — `internal/datapath`

Local SOCKS5 server on loopback (default `127.0.0.1:1080`) → `exit.Dialer`; `BIND`,
`UDP ASSOCIATE` and "no session" refused at once; the listener opens before the first
session. From csqtt-cli `socks5.go` (server protocol) where it fits.

**Done when** tests with a SOCKS5 client in the test pass through the in-process server.

### S6. Profiles and secrets — `internal/config`

`profiles.toml` schema of §10, validation (unknown keys and secrets in the TOML are
errors), the exit password in the secret store; `Save` with its ownership tests under sudo
(left out of E0).

**Done when** load, save, validation and secret tests pass on Linux and Windows.

### S7. Commands

`connect`, `profile add|set|show|rm|list`, `resolvers check` (a real handshake through
every resolver, exit code 1 if none passes), the status line.

**Done when** command tests pass and `dnstt-cli connect` works against the in-process
server in a test.

### S8. Live acceptance

A browser through `socks` to the server's `danted` with a login, over DoH, DoT and UDP; the
session survives a server restart and a network change; `resolvers check` on a list with a
dead resolver. Whether `danted` takes the SOCKS5 greeting, login and request in one write
(two round trips less per connection, R3). On Linux and Windows. Results without
environment in this file, the course of the runs in `doc/local/plan-e1.md`.

**Done when** all of the above is seen live and recorded.
