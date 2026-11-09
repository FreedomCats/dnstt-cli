---
kind: task
status: done
detailed: true
depends: [04-connect-wiring]
---

# DNS protection and kill-switch

## Task

The leak protection of `architecture.md` §05a in the `tun` connect flow: the nftables table
`inet dnstt-cli` with the `owner` flag is opened before connecting; while a session is up,
port 53 is open only to the adapter's DNS address, loopback and the marked sockets; while
no session is up, the kill-switch's "closed" set blocks system traffic with the §05a
exceptions; IPv6 is closed outward; `-no-kill-switch`, `-allow-leak` and the profile's
`kill_switch` choose the behaviour for one run or for good. The nftables implementation,
the mark and the netns tests are already in `internal/linuxsys`, `internal/netguard` and
`internal/datapath`; this child wires them into the datapath and the command.

The wrapper that turns the guard's `Disable` into `Lockdown` can be carried from
csqtt-cli (MIT, the `killSwitchGuard` with its tests), without the captcha branches: there
is no captcha, so the kill-switch is never lifted mid-session. The core's sockets are
recognised by `netguard.SocketMark` (`SO_MARK`), not by uid: under `sudo` every root
program would otherwise pass.

The wrapper and the session setup live in `internal/app` next to `EnableNetguard`: the
state machine in `internal/app/protection.go`, the Linux opener in
`internal/app/protection_linux.go` (`linuxsys.OpenDNSGuard(netguard.SocketMark)`), a stub
for other systems until the Windows stage. The datapath is built before the protection is
opened (only `Requires` tells whether it is needed), so it learns the guard through the new
`datapath.Guarded.SetDNSGuard`. The §05a lines are user-facing, not `-v` debug: the
supervisor gets a `Notice` sink (nil falls back to `Logf`), and the CLI passes one that
writes to stderr. The bootstrap resolver of `internal/resolvers` gets the same
`opts.Control` on its sockets: under a closed kill-switch they are the core's sockets too.

The places that are easy to get wrong:

- the guard goes on after the routes and before the system DNS; on `Suspend` the system DNS
  is reverted while the protection still stands, then the guard goes back to "closed" (or
  is removed), and only then the routes are taken away;
- the allow-list of `Enable` is the adapter's DNS address `10.66.67.1`, not `tun_dns`:
  `tun_dns` is reached by the netstack through the exit and needs no host rule;
- the initial "closed" is installed by `EnableProtection` before `Run`, not by
  `Tun.Start`: the adapter does not exist yet and there is no session;
- `Guard.Mark` is set only when the table was opened (it needs `CAP_NET_ADMIN`); with
  `-allow-leak` and no table every core socket would otherwise fail with `EPERM`;
- the guard session outlives `Tun.Close`: the table is closed last, after the datapath has
  taken its rules and routes away;
- a `*bool` profile field and `omitempty`: a missing key must stay "on" after a save, never
  become `false`.

Out of scope: the Windows WFP session (stage 4), the real-machine matrix (stage 6), the
menu and the elevated re-run (stage 5).

## Steps

1. **The profile default** — `internal/config/profiles.go`, `internal/cli/profile.go`,
   `internal/config/profiles_test.go`:
   - `Profile.KillSwitch` becomes `*bool` with `toml:"kill_switch,omitempty"` and the
     comment "a pointer: a missing key means on; only an explicit false turns it off. Read
     it with `KillSwitchOn`". Add
     `func (p *Profile) KillSwitchOn() bool { return p.KillSwitch == nil || *p.KillSwitch }`
     (the csqtt-cli shape).
   - `SaveProfilesTo` keeps using `toml.Marshal`: `omitempty` leaves a nil pointer out, so a
     profile written without the key still means on; a non-nil `false`/`true` is written
     as-is. `profile add` sets a pointer to `true`, so a new profile names the default.
     `profile show` prints `KillSwitchOn()`.
   - Update the literals: `internal/config/profiles_test.go:28` and `:178`,
     `internal/cli/connect_test.go:417` (the `socks` test; nil is fine, it is unused).
   - Tests (no root): raw TOML with no `kill_switch` → on; `kill_switch = false` → off;
     `true` → on. Save of nil omits the key and reloads on; save of `false` writes
     `kill_switch = false` and reloads off (assert the raw file too). A profile with
     `allow_leak = true` is an unknown-key error — the loader already has no such key, the
     test pins it.

2. **The bootstrap hook** — `internal/resolvers/bootstrap.go`, `internal/app/tunnel.go`,
   `internal/cli/resolvers.go`, their tests:
   - `NewProfileBootstrap` gains a `control func(network, address string, c
     syscall.RawConn) error` parameter (its `own`, `list` and `networkDNS` stay) and
     passes it to `newBootstrapWithError`. The doc: the bootstrap sockets are the core's
     sockets, and `Control` binds and marks them like the rest (§06, point 5); under a
     closed kill-switch an unmarked query to the network's DNS would be dropped.
   - `app.newProfileBootstrap(..., control)` and `buildTunnel` pass `opts.Control` (the
     same `netguard.Guard.Control` that goes into `client.Config.Control`).
     `internal/cli/resolvers.go:171` (`resolvers check`) passes nil: it runs outside a
     tunnel.
   - Update the calls in `internal/resolvers/bootstrap_test.go:417` and the three in
     `internal/app/tunnel_test.go`; add one bootstrap test that starts the existing local
     DNS helper, builds a `NewProfileBootstrap` with a recording control and checks that
     the control saw the UDP query (the `NewBootstrap` control test already proves the
     hook; this one proves the profile constructor passes it on).

3. **The wrapper and the session setup** — new `internal/app/protection.go`,
   `internal/app/protection_linux.go`, `internal/app/protection_other.go`,
   `internal/app/protection_test.go`:
   - `dnsGuardSession` is `datapath.DNSGuard` plus `Lockdown() error` and `Close() error`.
     `var openDNSGuard = openSystemDNSGuard` is the test seam.
     `ProtectionOptions{KillSwitch, AllowLeak bool; Notice func(format string, args ...any)}`;
     `Protection{Guard datapath.DNSGuard; KillSwitch *killSwitchGuard; session
     dnsGuardSession; ...}` with `Close()` and `Locked() bool` (nil-safe).
   - `protection_linux.go` (`//go:build linux`) opens
     `linuxsys.OpenDNSGuard(netguard.SocketMark)`. `protection_other.go` (`//go:build
     !linux`) returns "DNS protection is not implemented on this system"; the Windows WFP
     session is stage 4. The `tun` datapath is unsupported there, so the opener is a
     backstop.
   - `EnableProtection`: open the table; on failure without `-allow-leak` return an error
     naming the cause and `-allow-leak`; with it, warn through `Notice` (DNS queries and
     system traffic may go past the tunnel) and return `Guard == nil`. If the kill-switch
     is on, `newKillSwitchGuard` installs `Lockdown` at once and the startup line is
     printed: "kill-switch: system traffic is blocked while the tunnel is down". On a
     `Lockdown` failure without allow-leak close the session and return an error naming
     `-no-kill-switch` and `-allow-leak`; with allow-leak warn and continue with the
     tolerant DNS guard (the session without the kill-switch wrapper). With the
     kill-switch off there is no `Lockdown` and `Guard` is the session (under allow-leak,
     the tolerant wrapper below). There is no captcha: nothing lifts the kill-switch
     mid-session.
   - `killSwitchGuard`: `session`, `allowLeak`, `notice`, `mu`, `locked`, `closed`.
     `newKillSwitchGuard` calls `Lockdown` before returning. `Enable(dns)` calls the
     session's `Enable`: on success `locked = false`; on failure without allow-leak return
     the error and stay closed (`downLocked` retries `Lockdown`); with allow-leak print a
     warning, drop all rules (`Disable`) so the tunnel works and the system is open, and
     return nil. `Disable()` means "tunnel down" and installs `Lockdown`; if that fails,
     print a loud line, disable all rules and return only the `Disable` error.
     `BlockIPv6` forwards if the session implements `datapath.IPv6Blocker`. `shutdown`
     marks the wrapper closed; later `Enable` returns an error, later `Disable` is nil and
     touches no rules. No captcha methods.
   - `tolerantDNSGuard` (allow-leak: kill-switch off or failed): `Enable` prints the
     failure and returns nil, `Disable` and `BlockIPv6` forward.
   - Tests with a fake session, no root: `Lockdown` before the first attempt; `Enable` →
     DNS protection, `Disable` → "closed"; an open failure with and without allow-leak; a
     `Lockdown` failure with and without allow-leak (the session is closed without it); an
     `Enable` failure (error and closed without allow-leak; open and warning with it); a
     `Lockdown` failure in `Disable` (loud line, rules removed, nil error); `BlockIPv6`
     forwarding; `Close` closes the session and a later `Disable` is a silent no-op.

4. **The datapath seam** — `internal/datapath/datapath.go`, `tun.go`, `tun_test.go`:
   - Add `Guarded` next to the other optional interfaces:
     `SetDNSGuard(guard DNSGuard)`; the caller opens the protection before the first
     session and hands it over before `Run`. `Tun` stores it in a `guard` field under
     `t.mu` and answers `var _ Guarded = (*Tun)(nil)`.
   - `Tun.Requires()` gains `DNSGuard: true` (the adapter takes all system traffic);
     update the comment and `tun_test.go`'s `TestTunRequires` (it now expects it) and the
     `app/datapath_test.go` assertion.
   - `Start`: configure (once) → `addAddress` → `addRoutes` → if the guard implements
     `IPv6Blocker`, `BlockIPv6(true)` → `guard.Enable([]netip.Addr{tunAddress})` → log the
     protection → `setDNS`. `tunAddress` is `10.66.67.1`; `tun_dns` must not be in the
     list. A guard error is joined with `suspendLocked()`, as `setDNS` already is, so no
     route or system DNS stays behind.
   - `Suspend`: `revertDNS` → `guard.Disable()` → `removeRoutes` → `removeAddress`, each
     attempted even if the previous one failed. Before the first `Start` (no adapter) it
     does nothing; with no guard only the adapter steps run and the initial "closed" set
     stays.
   - `Close`: `suspendLocked()` before `adapter.close`; the guard session itself is the
     caller's and is closed after the datapath.
   - Tests with the existing `fakeAdapter` and a fake guard that records into the same
     call log: the order on `Start` and `Suspend`, `BlockIPv6` before `Enable`; an
     `Enable` failure leaves no routes/address/DNS and the next `Start` retries with the
     protection; `Close` disables before the adapter close; without `SetDNSGuard` the old
     lifecycle tests see the old calls.

5. **The lines between sessions** — `internal/supervisor/tunnel.go`,
   `internal/app/tunnel.go`, `internal/supervisor/tunnel_test.go`:
   - `Options.Notice` (nil → `Logf`) is the user-facing sink; `Tunnel` gets a
     `killSwitch func() bool` under `mu` and `SetKillSwitch`, called once before `Run` like
     `SetDatapath` (the protection is opened after the tunnel is built).
   - In `Run`, a local `routed`/`bypassed` pair: after a successful `Start`, if the
     previous `Suspend` reported the system as down, print "tunnel up again — system
     traffic goes through it" and clear it; after `Suspend`, when a session had been
     routed and the run continues (not stop, not fatal), print "tunnel reconnecting —
     system traffic is blocked (kill-switch)" when `KillSwitch()` is true, otherwise
     "tunnel reconnecting — system traffic goes past the tunnel (kill-switch off)". No
     line on the first connection, on the stop or on a fatal end. A `Start` that fails
     prints no up line; the first successful `Start` after a down line does.
   - `app.TunnelOptions.Notice` is passed to `supervisor.Options.Notice`.
   - Tests with the fake datapath and a captured `Notice`: a death and a `Restart` print
     down then up; the stop prints no down line; the first connection prints no up line;
     a kill-switch off prints the off line; a nil `Notice` falls back to `Logf`.

6. **`connect`** — `internal/cli/connect.go`, `internal/cli/connect_test.go`:
   - Add `-no-kill-switch` ("tun: let system traffic pass between sessions") and
     `-allow-leak` ("tun: connect even if DNS protection or the kill-switch cannot be
     set"); both are accepted for `socks` and change nothing there, as in csqtt-cli.
   - `killSwitchPolicy(prof *config.Profile, noKillSwitch bool) (on bool, off string)`:
     the flag wins, then `KillSwitchOn()`; the off reason is `-no-kill-switch` or
     `kill_switch = false in the profile`. Unit-test the four cases.
   - Separate `notice` from `logf`: `notice` always writes to `e.stderr`, `logf` stays
     `-v`-only. Pass `notice` to `app.TunnelOptions.Notice` and to
     `app.ProtectionOptions.Notice`.
   - Replace `defer dp.Close()` with one cleanup closure registered right after
     `BuildDatapath`, before the `Requires` checks: it closes the datapath, then the
     protection (assigned later). The later defers stay: watcher stop → reservation
     release → this closure. The second Ctrl+C keeps its early `dp.Close()` (idempotent).
   - After `EnableNetguard`, in the `requires.DNSGuard` block:
     `prot, err := app.EnableProtection(app.ProtectionOptions{KillSwitch: on, AllowLeak:
     *allowLeak, Notice: notice})`; an error returns before `Run`. If the policy is off,
     print the off line through `notice`. When `prot.Guard != nil`, require
     `dp.(datapath.Guarded)`, call `SetDNSGuard(prot.Guard)` and set
     `guard.Mark = netguard.SocketMark` (only here; `SO_MARK` needs the table's
     `CAP_NET_ADMIN`). Always `tunnel.Supervisor.SetKillSwitch(prot.Locked)`. When
     `prot.Guard == nil`, allow-leak was given and the app already warned: no mark and no
     `SetDNSGuard`, while `SetKillSwitch` still reports the actual state.
   - Tests: the policy table; `-no-kill-switch` and `-allow-leak` on a `socks` profile
     reach the local listener and are not usage errors (the existing in-process test
     pattern); the Linux non-root `tun` refusal still names `sudo dnstt-cli connect` and
     `-datapath socks` and never reaches the table; no test opens nftables.

7. **Checks** — `gofmt` on the touched files; the CI vet split (`go vet $(go list ./... |
   grep -v /core/)` and `go vet -testinggoroutine=false ./core/...`; the two pre-existing
   `core/noise` warnings stay); `go test -race -count=1 ./internal/config
   ./internal/resolvers ./internal/app ./internal/datapath ./internal/supervisor
   ./internal/cli` and the full `go test -race -count=1 ./...` (with the keyring/D-Bus
   tests enabled as in the previous child); `CGO_ENABLED=0 go build ./...`;
   `GOOS=windows CGO_ENABLED=0 go build ./...` (the `protection_other.go` stub keeps the
   cross-build honest). The netns tests of `internal/linuxsys` and `internal/datapath` are
   unchanged and skip without `DNSTT_CLI_TEST_NETNS=1`; in the CI `netns` job they run in
   the container. If a local container run is wanted, use an image already present
   (`debian:trixie` or `alpine:latest`) with `--cap-add NET_ADMIN --device /dev/net/tun`
   and record the image; do not pull the CI-pinned image. Nothing touches the host's
   network, `/etc/resolv.conf` or firewall. No live service or resolver, no real profile
   or secret, no new dependency, no download, no `ip` or `nft` binary.

## Done when

- A profile with no `kill_switch` key means on (`KillSwitchOn()` true); an explicit
  `false` is off and `true` is on. `SaveProfilesTo` leaves an unset key out and reloads it
  as on, and writes explicit values otherwise; `profile add` writes an explicit `true`;
  `profile show` prints the effective value. `allow_leak` in a profile is an unknown-key
  error.
- With the kill-switch on (the default), the table is opened and "closed" is installed
  before the first dial: a public UDP send gets `EPERM`; loopback, DHCP, ND/RA and the
  local network pass; DNS to the router is dropped even on the local network. The startup
  line says system traffic is blocked on stderr, also without `-v`.
- When a session comes up, `Tun.Start` installs DNS protection after the routes and before
  the system DNS: port 53 passes only to the adapter's DNS address `10.66.67.1`, loopback
  and the marked sockets; non-DNS traffic passes; IPv6 outward is closed while the local
  IPv6 network and service protocols stay open. After a reconnection the "tunnel up again"
  line is printed; the first connection prints no such line.
- Every `Suspend` returns the guard to "closed" before the routes are removed; the
  "tunnel reconnecting" line is printed for a death and a `Restart`, not for the stop or a
  fatal end. `Close` leaves no `inet dnstt-cli` table; after `kill -9` the `owner` flag
  removes it as well.
- `-no-kill-switch` for one run and `kill_switch = false` in the profile remove the
  "closed" set and let traffic pass between sessions, with the startup and reconnecting
  lines saying so. `-allow-leak` connects when the table cannot be opened or a rule
  cannot be set, with a loud warning, and is a flag only: the profile has no such key.
- The guard and the session use the same `netguard.SocketMark`; the mark is set only when
  the protection session is open, and only the core's sockets (including the bootstrap
  resolver's) carry it. Opening the table without `CAP_NET_ADMIN` fails before `Run`
  unless `-allow-leak` was given.
- A failed protection setup leaves no routes or system DNS behind: a `guard.Enable`
  failure in `Start` runs the datapath's suspend path and the next `Start` retries; the
  session-setup failure happens before the adapter exists.
- The bootstrap resolver's sockets go through `opts.Control`: `NewProfileBootstrap` takes
  the hook, `buildTunnel` passes the guard's `Control`, and `resolvers check` passes nil.
- Tests run with no root and no TUN: fake guard sessions, the fake datapath and adapter,
  the config round-trip, the supervisor's `Notice` capture; the existing netns tests of
  `linuxsys` and `datapath` pass unchanged in their container job (`NET_ADMIN`,
  `/dev/net/tun`; an already-present image, recorded).
- `gofmt`, the CI vet split, `go test -race -count=1 ./...`, the Linux build and the
  Windows cross-build pass. No new dependency, no download, no `ip`/`nft`.
- Only `internal/config`, `internal/resolvers`, `internal/app`, `internal/datapath`,
  `internal/supervisor` and `internal/cli` change; `internal/linuxsys`,
  `internal/netguard`, `internal/winsys`, the netstack, the adapter opener and the CI
  workflows are untouched.

## Depends on

[Connect wiring, core bypass and network changes](../04-connect-wiring/plan.md). The
stage's `linuxsys` and `netguard` are already accepted; this child adds only the internal
seams (`datapath.Guarded`, the supervisor's `Notice` and `SetKillSwitch`) and the profile
default.

## Outcome

The leak protection of `architecture.md` §05a is wired into `connect -datapath tun`: the
nftables session is opened before the first dial, the kill-switch keeps system traffic
closed while no session is up, DNS is open only to the adapter's address and the core's
marked sockets while a session runs, and IPv6 is closed outward. The profile default is
on; `-no-kill-switch` and `-allow-leak` choose the behaviour for one run.

- `Profile.KillSwitch` is a `*bool` with `omitempty` and `KillSwitchOn()`: a missing key
  means on, only an explicit `false` turns it off. A nil key is not written by a save and
  reloads as on; explicit values are written as they are. `profile add` sets an explicit
  `true`, `profile show` prints the effective value, and `allow_leak` stays an unknown
  key of the profile (pinned by a test).
- New `internal/app/protection.go` holds the session setup and the state machine:
  `EnableProtection`, `Protection` (`Guard`, `KillSwitch`, `Locked`, `Close`),
  `killSwitchGuard` and `tolerantDNSGuard`. With the kill-switch on, "closed" is
  installed before `Run` and the startup line is printed through `Notice`; a failed
  `Lockdown` without `-allow-leak` closes the session and names `-no-kill-switch` and
  `-allow-leak`; with it, the run continues with the tolerant guard. `Enable` is DNS
  protection, `Disable` is "closed"; with `-allow-leak` a refused `Enable` drops all rules
  and warns instead of failing the attempt; a refused `Lockdown` in `Disable` warns,
  drops the rules and returns only the removal error. `Close` shuts the wrapper down
  before closing the session, so a late `Disable` touches nothing. There is no captcha,
  so nothing lifts the kill-switch mid-session. The Linux opener is
  `linuxsys.OpenDNSGuard(netguard.SocketMark)`; other systems get a backstop error until
  the Windows WFP stage.
- `datapath.Guarded` is the new seam; `Tun` stores the guard under `mu` and answers
  `Requires` with `DNSGuard: true`. `Start` runs configure → address → routes →
  `BlockIPv6(true)` → `Enable([10.66.67.1])` → the protection line → system DNS; a guard
  error runs the suspend path, so no route, address or system DNS stays behind, and the
  next `Start` retries. `Suspend` and `Close` go system DNS → guard → routes → address,
  each step attempted even if the previous one failed. The guard session outlives
  `Tun.Close`: the command closes the datapath first, then the protection.
- `supervisor` gains `Options.Notice` (nil → `Logf`) and `SetKillSwitch` (like
  `SetDatapath`, once before `Run`). After a `Suspend` on a running tunnel it prints the
  reconnecting line — blocked with the kill-switch, past the tunnel without it; after the
  next successful `Start` it prints "tunnel up again". No line on the first connection, on
  the stop or on a fatal end, and none without a datapath (socks).
- `connect` takes `-no-kill-switch` and `-allow-leak` (accepted for socks, where they
  change nothing), decides the policy with `killSwitchPolicy` (flag, then the profile),
  keeps a user-facing `notice` separate from the `-v`-only `logf`, and opens the
  protection in the `requires.DNSGuard` block after `EnableNetguard`. `SetDNSGuard` and
  `Guard.Mark = netguard.SocketMark` are set only when the session is open (the mark needs
  the table's `CAP_NET_ADMIN`); `SetKillSwitch(prot.Locked)` is always set and reports the
  actual state under `-allow-leak`. One cleanup closes the datapath and then the
  protection; the second Ctrl+C still closes the datapath early.
- The bootstrap resolver's sockets carry the same hook: `NewProfileBootstrap` takes
  `control` and passes it to `newBootstrapWithError`; `buildTunnel` passes
  `opts.Control`, `resolvers check` passes nil.
- Tests use a fake guard session, the fake datapath and adapter and the config
  round-trip; no root, TUN, nftables or live resolver is needed. `profile show` was also
  checked by hand with a temporary config directory.
- Checks: `gofmt` clean; the CI vet split on Linux and for Windows; `CGO_ENABLED=0 go
  build ./...` and the Windows cross-build; the full `go test -race -count=1 ./...` with
  the keyring/D-Bus tests enabled and `DNSTT_CLI_TEST_NETNS=0`. The four live netguard
  checks were excluded by `-skip` and made no live calls. The netns suites ran in the
  already-present `debian:trixie` image with `NET_ADMIN` and `/dev/net/tun`:
  `datapath.test` 52 passed and `linuxsys.test` 35 passed, no skips. One intermediate full
  run hit a timeout in the unchanged `core/client` test; the package alone and the next
  full run passed.
- Independent review found one remark, accepted and fixed: `routed` was set for a datapath
  that does not exist, so a socks run printed the kill-switch lines on a death or a
  `Restart`; it is now set only after a successful `Start`, with a test. No rejected
  finding.
- Not changed here, pre-existing: `profile add` cannot save its minimal profile, because
  `SaveProfilesTo` requires `domain` and `pubkey`. The explicit `true` is set in code, but
  the command's end-to-end path stays broken until that validation is revisited.
- Only `internal/config`, `internal/resolvers`, `internal/app`, `internal/datapath`,
  `internal/supervisor`, `internal/cli` and this plan change; `internal/datapath` also
  needed one `Requires` assertion in `tun_lock_linux_test.go`. `internal/linuxsys`,
  `internal/netguard`, `internal/winsys`, the netstack, the adapter opener and the CI
  workflows are untouched. No new dependency, no download, no `ip`/`nft`.

No real problem was left incompletely fixed because of cost.
