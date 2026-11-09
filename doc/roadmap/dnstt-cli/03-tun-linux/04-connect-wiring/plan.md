---
kind: task
status: done
detailed: true
depends: [03-tun-adapter]
---

# Connect wiring, core bypass and network changes

## Task

Make `connect -datapath tun` a working tunnel and keep its own sockets out of it
(`architecture.md` R4, R6, R7, §06, §08, §09). The accepted [TUN adapter](../03-tun-adapter/plan.md)
already opens `/dev/net/tun`, feeds the netstack in-process, sets the point-to-point link,
the `/1` routes and the system DNS, and exposes the lifecycle: `Start` for a session,
`Suspend` between sessions, `Close` at the end. This child wires that lifecycle into the
command:

- `connect` takes the datapath from the profile and from `-datapath socks|tun`, checks
  `Requirements` (no TUN, no root) before taking the reservation and before any dial,
  reserves the adapter for the whole run and gives the datapath to the supervisor;
- the supervisor calls `Start` when a session has passed the exit check, and `Suspend`
  after every session, including the stop path; a failed `Start` closes the session and is
  retried with the normal delay;
- `tun_dns` is parsed from the profile before the adapter can be created;
- the session's `client.Config.Control` is `netguard.Guard.Control`, so the core's sockets
  are bound to the physical interface and, with protection later, marked;
- `netguard` chooses that interface with the adapter excluded, checks the system filters
  that would drop the core's replies, and restarts the session once per folded network
  change.

Only the wiring is here. The netstack, the Linux opener, `linuxsys` and the accepted
`Start`/`Suspend` order of `Tun` do not change; `Tun` gets one small seam, the adapter index,
because `netguard` must exclude the adapter from the physical interface choice. The system
resolver keeps getting the adapter's own address `10.66.67.1` (the previous child does
that); `tun_dns` is the DNS behind the exit and goes only to the netstack. `Guard.Mark`
stays 0: the mark belongs to DNS protection and the kill-switch (the next child).

Out of scope: the nftables DNS protection, the kill-switch, `-no-kill-switch`,
`-allow-leak`, the profile's `kill_switch` default and its loader change — all that is
[DNS protection and kill-switch](../05-protection/plan.md); the menu and the elevated
re-run — stage 5. The real tunnel end to end is [Live acceptance](../06-live-acceptance/plan.md).

The places that are easy to get wrong: the guard must exist before `BuildTunnel` (the
session config takes `Control` at construction) but `SelectInterfaces` can run only after
the datapath is built, because the adapter index is the datapath's; the adapter is opened
lazily by the first `Start`, so its index is 0 until then; firewalld's IPv6 decision must
be taken before `SelectInterfaces` and `Watch`, which read `NoIPv6`; and `Suspend` has to
run on the stop path, not only on a reconnection.

## Steps

1. **The adapter index for netguard** — `internal/datapath/datapath.go`, `tun.go`,
   `tun_linux.go`:
   - add `id uint64` to `tunAdapter` and set it from the link index `openTunAdapter`
     already resolves with `linuxsys.LinkIndex`, before the netstack is built;
   - add `func (t *Tun) AdapterID() uint64` under `t.mu`: 0 while no adapter exists (before
     the first `Start` and after `Close`), the interface index once it does. Do not open the
     adapter earlier: the accepted lifecycle is lazy;
   - add an optional `Adapter` interface in `datapath.go` next to `Reserver`, `Persistent`
     and `Failing` with `AdapterID() uint64`, and `var _ Adapter = (*Tun)(nil)` in
     `tun.go`. The name matches `netguard`'s documentation of the exclusion ("the
     datapath's AdapterID: the LUID on Windows, the index on Linux").
   Test (`internal/datapath/tun_test.go`): the fake adapter carries an id; `AdapterID` is 0
   before `Start`, the id after it, 0 after `Close`.

2. **The datapath lifecycle in the supervisor** — `internal/supervisor/tunnel.go`:
   - a `Datapath` interface next to `Session`:
     `Start() error; Suspend() error`. `*datapath.Tun` satisfies it; `socks` has no such
     part and the default stays nil;
   - `func (t *Tunnel) SetDatapath(dp Datapath)`: store it under `t.mu`; called once before
     `Run` (the command builds the datapath after the supervisor, because the datapath
     needs the supervisor's exit). `Run` reads it through a small helper under `t.mu`;
   - in `Run`, call `Start` after `checkExit` has returned nil and after the branch that
     handles fatal, `ctx` end and Restart at the end of a dial, and before `serve`: by then
     the session is accepted and the exit is known to serve, so the routes may lead into
     it. A Restart that arrives between that branch and `serve` is taken by `serve` and
     still ends with the `Suspend` below. On error: close the session, clear `round`, set
     `prev = i - 1` (the same resolver first) and `t.pause(ctx, name, err,
     t.backoff.next())`. Do not call `pool.SessionEnded`: the local adapter failed, the
     resolver is not to blame. The comment of the neighbouring "the handshake went through,
     the exit did not" branch grows the datapath case;
   - call `Suspend` once right after `serve` returns and before the switch on its `end`:
     the stop, restart, died and fatal paths have all had a session, and none may leave the
     routes and the system DNS pointing into an adapter that is not serving. A `Suspend`
     error is logged and does not replace the session's outcome; `Tun` is idempotent, so
     the next `Start` or `Close` retries it. When `Start` was never called (failed dial or
     exit check), no `Suspend` is needed, and `Tun.Suspend` before `Start` is harmless
     anyway;
   - `serve` can return `end{stop: true}` before publishing the session (ctx ended or
     `FatalExit` while `Start` ran); `Suspend` still runs after it. Do not add `Suspend` to
     `Run`'s deferred `stop`: with no session there is nothing to take out, and the
     command's `Close` cleans up.
   Tests (`internal/supervisor/fake_test.go`, `tunnel_test.go`) with a `fakeDatapath` that
   records `Start`/`Suspend` in order under a mutex and can fail either:
   - a session comes up → one `Start`; the session dies → `Suspend` before the next `Start`;
     `Restart` while connected → `Suspend` then `Start` without a delay; `h.stop()` →
     `Suspend` is the last call and `Run` returns nil;
   - `Start` fails: the session is closed, no `Connected` follows, the transition to
     `Reconnecting` carries the error, the next dial reuses the same resolver, the first
     sleep is `1s`; with the fault cleared the session connects and `Start` succeeds;
   - `CheckExit` fails or the dial fails: `Start` is never called;
   - `Suspend` fails: the error is logged and the tunnel still reconnects; the next `Start`
     runs;
   - without `SetDatapath` the existing tests behave as before.

3. **The datapath from the profile** — `internal/app/tunnel.go` (and one small method in
   `internal/datapath/socks.go`):
   - `TunnelOptions` gains `Control func(network, address string, c syscall.RawConn) error`,
     put into `client.Config.Control` in `buildTunnel`; the TODO stub goes away. A nil
     `Control` leaves the core's sockets as they are, which is the `socks` path;
   - an `app.Datapath` interface: `Close() error` and `Requires() datapath.Requirements`.
     `BuildDatapath` returns it; `*datapath.Tun` and `*datapath.Socks` satisfy it. In
     `internal/datapath/socks.go` add `func (s *Socks) Requires() Requirements { return
     Requirements{} }` (the zero value is "nothing needed", as the type says);
   - the `tun` branch of `BuildDatapath` parses `tun_dns`, builds
     `datapath.NewTun(datapath.TunOptions{Dialer: tun.Exit, TunDNS: dns, Logf: logf})` and
     returns it. The options' `Name` default (`identity.Name`) stays. Nothing is opened
     until the supervisor's `Start`;
   - `parseTunDNS(s string) (netip.Addr, error)`: empty is the constant `1.1.1.1`
     (`defaultTunDNS`); otherwise `netip.ParseAddr` of the trimmed value, and the address
     must be IPv4 (`Is4`, so an IPv4-mapped IPv6 form is refused too). A port is not part
     of the value: the netstack dials port 53 itself. The error names `tun_dns` and the
     value, never anything else;
   - update `BuildDatapath`'s comment: for `socks` the listener is already open; for `tun`
     nothing is started until a session. Remove the unused `Tunnel.Datapath` field (nothing
     sets or reads it; the built datapath is now the returned `app.Datapath`). The profile
     loader and `profile show` already carry `datapath` and `tun_dns`; `internal/config` and
     `internal/cli/profile.go` do not change.
   Tests (`internal/app/datapath_test.go`, package `app`): `parseTunDNS` with empty,
   `9.9.9.9`, `1.1.1.1:53`, `2001:db8::53`, a name and spaces; `BuildDatapath` with the
   `tun` profile gives `*datapath.Tun` with `Requires().Admin` and `Netguard`, and a bad
   `tun_dns` fails before anything is opened; the `socks` profile still gives
   `*datapath.Socks` (the existing HTTP tests already do that).

4. **The platform checks in netguard** — `internal/netguard/netguard_linux.go`,
   `netguard_windows.go`, `netguard_other.go`:
   - `func (g *Guard) CheckFirewall() error`: Linux asks
     `linuxsys.FirewalldStrictIPv6`; when it is strict, set `g.NoIPv6 = true` (so the IPv6
     interface is dropped from the choice and an IPv6 socket is not created) and log one
     line through `g.Logf`; a detection error is returned. On Windows and other systems it
     returns nil. It must run before `SelectInterfaces` and `Watch`, as `NoIPv6` says;
   - `func (g *Guard) CheckReversePath() error`: Linux reads the committed choice
     (`g.Choice()`), skips when its IPv4 interface is empty, and calls
     `linuxsys.ReversePathFilter(name)`. `linuxsys.RPFilterStrict` becomes an error with
     the interface name, the `sysctl` command that loosens it
     (`sudo sysctl -w net.ipv4.conf.all.rp_filter=2 net.ipv4.conf.<iface>.rp_filter=2`) and
     the `-datapath socks` alternative; a read error is wrapped; off and loose pass. On
     Windows and other systems it returns nil;
   - keep the delegation in package variables (`firewalldStrictIPv6`,
     `reversePathFilter`) as `datapath` does with `detectSystemDNS`, so the checks are
     tested without `/proc` and `firewalld`. `scripts/checktunnel/main_linux.go` already
     prints both (`printReversePath`) and is not changed.
   Tests (`internal/netguard/netguard_linux_test.go`, or a new `syscheck_linux_test.go`):
   strict → the error names the interface, `sysctl` and `-datapath socks`; loose and off →
   nil; a read error → wrapped; no IPv4 choice → the reader is not called; firewalld
   strict → `NoIPv6` and one log line, not strict → no change, an error → returned. The
   Windows and other builds only have to compile: the methods are no-ops there.

5. **The guard wiring and the watcher** — new `internal/app/netguard.go`:
   - `NetguardOptions{Guard *netguard.Guard; Exclude func() uint64; Restart func(error);
     Logf func(format string, args ...any)}`;
   - `EnableNetguard(opts NetguardOptions) (stop func(), err error)`, the real entry point,
     and an unexported `enableNetguard(opts, hooks)` for the tests. `hooks` holds
     `checkFirewall`, `selectInterfaces`, `checkReversePath` and `watch`, defaulting to
     method expressions of `*netguard.Guard`; nil `Guard` or `Restart` is an error, nil
     `Exclude` means 0;
   - the order is the contract: `CheckFirewall` → `SelectInterfaces(exclude())` (log the
     returned choice through `Logf`) → `CheckReversePath` → `Watch` with
     `Exclude: exclude`. An error at any step returns before the next one, so a refusal
     leaves no watcher behind;
   - the watcher's `Changed` callback: an empty choice (`Choice.Empty`, no default route in
     either family) is logged and does nothing — the next change is the one to follow; any
     other choice calls `Restart(fmt.Errorf("network changed: %s", now))` once. The
     watcher already folds a burst and commits the new indexes before `Changed`
     (`netguard`'s tests cover that), so the next session's sockets take them;
   - return the watcher's `stop` unchanged; the command defers it.
   Tests (`internal/app/netguard_test.go`, no root): the order of the four hooks and the
   exclude passed to `SelectInterfaces`; each hook's error stops the sequence; the choice
   is logged; one non-empty change → exactly one `Restart` with "network changed"; an empty
   choice → none (and a following change still restarts); the returned `stop` is the
   watcher's.

6. **`connect`** — `internal/cli/connect.go`:
   - a `-datapath` flag next to `-v`, values `socks` and `tun`; an unknown value is a usage
     error. The effective type is the flag, otherwise the profile's `datapath`, otherwise
     `socks` (the profile loader already validates its own value). Pass it to
     `BuildDatapath` through a shallow copy of the profile (`p := *prof; p.Datapath = kind`)
     so the only switch on the type stays in `BuildDatapath`;
   - when the type is `tun`, create the guard **before** `BuildTunnel`
     (`&netguard.Guard{Logf: logf}`) and pass `guard.Control` in
     `app.TunnelOptions.Control`. For `socks` the guard is nil and `Control` is nil: the
     socks path must not bind its sockets to an interface;
   - build the datapath, `defer dp.Close()`, then check `dp.Requires()`, in this order:
     `Unsupported` first — wrap it with the platform reason (`tunUnsupported` names the fix:
     `modprobe tun` on Linux without the module, `-datapath socks` where `tun` does not
     exist) — then `Admin`: without root, refuse with `sudo dnstt-cli connect <profile>`
     and `-datapath socks`. Both refusals happen before `Reserve` and before any dial;
   - give the datapath to the supervisor when it is a `supervisor.Datapath`
     (`if p, ok := dp.(supervisor.Datapath); ok { tunnel.Supervisor.SetDatapath(p) }`);
   - reserve when the datapath is a `datapath.Reserver`, `defer release()`; a reservation
     error is the command's error, before the first attempt. Then, when
     `Requirements.Netguard` is set, call `app.EnableNetguard` with the guard, the exclude
     from `dp.(datapath.Adapter).AdapterID` (a datapath that needs `Netguard` but does not
     name an adapter is an internal error), `tunnel.Supervisor.Restart` and `logf`;
     `defer stop()`. The defers run watcher → reservation → `Close`;
   - the hints and the choice line go to `logf`/the error, not to stdout; the status line is
     untouched.
   Tests (`internal/cli/connect_test.go`, plus a Linux-only case): `-datapath wireguard` is
   a usage error; a `tun` profile with `-datapath socks` reaches the local SOCKS5 listener
   (the in-process test server of the existing tests or a quick cancel after the
   `[SOCKS] listening` line) and prints no `tun` refusal; on Linux, not root, with
   `/dev/net/tun` present, `-datapath tun` exits with the `sudo` and `-datapath socks`
   hints, opens no SOCKS listener and prints no dial (skip the test without the device or
   under root). The test profiles use `t.example.com`, a synthetic public key and
   documentation addresses; no real profile, secret or live resolver is touched. The
   existing `socks` tests must pass unchanged.

7. **Checks** — `gofmt` on the touched files; the CI vet split (`go vet $(go list ./... |
   grep -v /core/)` and `go vet -testinggoroutine=false ./core/...`; the two pre-existing
   `core/noise` warnings stay); `go test -race -count=1 ./internal/supervisor ./internal/app
   ./internal/netguard ./internal/datapath ./internal/cli` and the full
   `go test -race -count=1 ./...`; `CGO_ENABLED=0 go build ./...`;
   `GOOS=windows CGO_ENABLED=0 go build ./...` (the Windows tests themselves run in the
   Windows CI). The netns tests of
   [TUN adapter](../03-tun-adapter/plan.md) are not changed; they skip without root and run
   in the CI `netns` job as before. If a local container run is wanted, use an image already
   present locally with `NET_ADMIN` and `/dev/net/tun`; do not pull the CI-pinned image.
   Nothing in the tests touches the host's network or `/etc/resolv.conf`: the guard hooks
   are faked, and the netns tests stay in the container's namespace.
   No new dependency, no download, no `ip` or `nft` binary.

## Done when

- `connect` takes the datapath from the profile and accepts `-datapath socks|tun`, the flag
  wins over the profile, an unknown value is a usage error, `-datapath socks` on a `tun`
  profile reaches the local SOCKS5 listener, and the `socks` path is otherwise unchanged.
- `tun` on a system that cannot run it is refused before the reservation and before any
  dial: `Requirements.Unsupported` is printed with its own fix (`modprobe tun`, or
  `-datapath socks` where `tun` does not exist), and without root the refusal names
  `sudo dnstt-cli connect` and `-datapath socks`. The process opens no SOCKS listener and
  prints no dial line.
- `app.BuildDatapath` builds `*datapath.Tun` from `tun_dns` alone (empty → `1.1.1.1`, IPv4
  only, port 53) and returns the datapath with its `Requirements`; a bad `tun_dns` fails
  before the adapter exists. `Socks` and `Tun` share the returned `app.Datapath` interface,
  and the old `Tunnel.Datapath` field is gone.
- The supervisor has `SetDatapath` and calls `Start` after the exit check and before it
  serves streams; a failed `Start` closes the session, is reported in `Reconnecting` with
  its cause and retried after the normal backoff with the same resolver first, without a
  resolver penalty. `Suspend` runs after every session — died, restarted, stop, fatal — its
  error is logged, not fatal, and the next `Start` or `Close` retries it.
- For `tun`, the session's `client.Config.Control` is `guard.Control`; the TODO in
  `internal/app/tunnel.go` is gone, `socks` sessions keep a nil hook, and the core's
  existing `Control` tests pass.
- `SelectInterfaces` gets the adapter's index (0 until the adapter is opened by the first
  `Start`, the index after; the watcher excludes the same), `CheckFirewall` runs before it
  and `CheckReversePath` after it. A strict IPv4 `rp_filter` on the chosen interface
  refuses before any dial with the interface, the `sysctl` hint and `-datapath socks`;
  firewalld's strict IPv6 filter sets `NoIPv6` instead, and the core's sockets fall back to
  IPv4.
- A folded network change calls `Restart` once with a "network changed" reason, after the
  new indexes are committed; a change to no network does not restart and waits for the
  next one; the watcher is stopped before the run ends.
- The unit tests above run with no root and no TUN: the supervisor's fake datapath, the
  app's fake hooks, netguard's fake `rp_filter`/firewalld readers and watcher, the
  datapath's fake adapter. The previous child's netns tests are unchanged and pass in their
  job; `gofmt`, the CI vet split, `go test -race ./...`, the Linux build and the Windows
  cross-build pass; no new dependency, no download, no `ip`/`nft`.
- Only `internal/datapath`, `internal/supervisor`, `internal/app`, `internal/netguard` and
  `internal/cli` change. `linuxsys`, the netstack, the TUN opener and the CI workflows are
  untouched; `Guard.Mark`, nftables, the kill-switch and the profile's `kill_switch` are
  [DNS protection and kill-switch](../05-protection/plan.md).

## Depends on

[TUN adapter, routes and system DNS](../03-tun-adapter/plan.md). The stage's
[foundation](../../01-foundation/plan.md) is done.

## Outcome

`connect -datapath tun` is wired end to end: the command chooses the datapath (flag,
profile, socks), refuses a system that cannot run it before the reservation and before any
dial, reserves the adapter for the run and hands the datapath to the supervisor. The
supervisor starts the adapter after the exit check and suspends it after every session; a
failed start is retried like any failed attempt, without blaming the resolver. The core's
sockets of a `tun` session go through `netguard.Guard.Control`; for `socks` the hook is
nil.

- `Tun` gains the adapter index as `datapath.Adapter` (`AdapterID`: 0 before the first
  `Start`, the interface index while the adapter exists, 0 after `Close`), set from the
  link index the Linux opener already resolves. `netguard` excludes that index from the
  choice of the physical interface and from the watcher.
- The supervisor has `SetDatapath` before `Run` and reads the value under `mu`. `Start`
  runs after `checkExit` and the fatal/`ctx`/`Restart` branches, before `serve`; a failed
  `Start` closes the session, clears the round, keeps the same resolver first, charges no
  resolver penalty and retries after the normal backoff. `Suspend` runs after every
  `serve`, including the stop and fatal paths, before the session is closed; its error is
  logged and the next `Start` or `Close` retries it.
- `app.BuildDatapath` returns the common `app.Datapath` (`Close`, `Requires`) for socks
  and tun; the old `Tunnel.Datapath` field is gone. For tun it parses `tun_dns` (empty →
  `1.1.1.1`, IPv4 only, no port) and builds `*datapath.Tun` without opening anything; the
  adapter is a first-`Start` business. `datapath.Socks` answers the zero `Requirements`.
- `netguard` gains `CheckFirewall` (Linux: firewalld's strict IPv6 filter sets `NoIPv6`
  and logs one line; Windows/other: no-op) and `CheckReversePath` (Linux: a strict IPv4
  `rp_filter` on the chosen interface is a refusal naming the interface, the `sysctl`
  command and `-datapath socks`; Windows/other: no-op). The readers are package variables,
  as `datapath.detectSystemDNS` is.
- New `app.EnableNetguard` runs the checks in the contract order: firewall →
  `SelectInterfaces(exclude)` → reverse path → watch. It logs the applied choice, passes
  the same exclude to the watcher, and its `Changed` callback restarts once with a
  "network changed" reason for a real network; an empty choice only logs and waits for the
  next change. `SelectInterfaces` now returns the applied choice (IPv6 dropped with
  `NoIPv6`), so the log cannot contradict the firewall line.
- `connect` accepts `-datapath socks|tun` (the flag wins over the profile; unknown is a
  usage error), creates the guard before `BuildTunnel` for tun, refuses `Unsupported` and
  then missing root before `Reserve` and any dial, gives the datapath to the supervisor,
  reserves when it can, and enables the guard only for a datapath that requires it. The
  defers run watcher → reservation → `Close`. `Guard.Mark` stays 0.
- Tests: the supervisor's ordered `fakeDatapath` (a session, a death, a `Restart`, the
  stop, a failed `Start`, a failed `Suspend`, no session on a failed dial/exit check); the
  app's `parseTunDNS`/`BuildDatapath` and the fake netguard hooks (order, exclude, errors,
  changes); netguard's fake `rp_filter`/firewalld readers; the datapath's fake adapter
  index; the CLI's usage error, the `tun` profile with `-datapath socks`, and the Linux
  refusal without root. No test needs root, a TUN or a live resolver.
- Checks: `gofmt` on the touched files; the CI vet split; `go test -race -count=1 ./...`
  with the keyring/D-Bus tests enabled; the Linux build and the Windows cross-build and
  vet. The netns tests skipped by the accepted matrix switch (outside the container job);
  the four external-network netguard checks were not skipped and passed. No new
  dependency, no download, no `ip`/`nft`.
- Independent review found two remarks, both accepted and fixed: the `NoIPv6` log
  contradiction above, and two assertions of the Linux refusal test that could never fire
  (now `-v` is passed and the resolver address is checked). No rejected finding.
- Only `internal/datapath`, `internal/supervisor`, `internal/app`, `internal/netguard`,
  `internal/cli` and this plan change. `linuxsys`, the netstack, the TUN opener and the CI
  workflows are untouched; nftables, the kill-switch and the profile's `kill_switch` are
  [DNS protection and kill-switch](../05-protection/plan.md).
- Noted for the next child, not changed here: the bootstrap resolver's sockets still have
  no `Control` (`internal/resolvers/bootstrap.go`); once the kill-switch is on, they need
  the same netguard hook. The previous command stage already left this deferred.

No real problem was left incompletely fixed because of cost.
