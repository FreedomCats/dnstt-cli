---
kind: task
status: done
detailed: true
depends: [02-dns-udp-ipv6]
---

# TUN adapter, routes and system DNS

## Task

The real Linux adapter and the datapath lifecycle (`architecture.md` R6, R7, §05):
`/dev/net/tun` with `IFF_TUN|IFF_NO_PI|IFF_TUN_EXCL`, the one-adapter lock on
`@dnstt-cli-tun`, a private `/32` and MTU 1500, the `0.0.0.0/1` and `128.0.0.0/1`
routes into the adapter, the system DNS pointing at the adapter's address, and the
persistent lifecycle: `Start` on a session, `Suspend` between sessions, `Close` at the
end. The descriptor feeds the netstack of the previous children as a `fdbased` link
endpoint; there is no second process or descriptor handover. This is the datapath
level only: `connect`, the supervisor, `netguard`, nftables and the profile wiring are
the next child.

The link is point-to-point with two ends. `10.66.67.1` is the tunnel's only address:
the netstack owns it, and the DNS listener of [DNS over TCP, other UDP and
IPv6](../02-dns-udp-ipv6/plan.md) binds to it. The host end is `10.66.67.2/32`, with
`10.66.67.1/32` as its peer. The peer is deliberate: a kernel-local `10.66.67.1`
would be delivered by the kernel itself (`ip route get 10.66.67.1` answers `local
10.66.67.1 dev lo`, and a UDP probe to it does not appear on the TUN), so a query to
it would never reach the netstack. With the peer, the kernel routes the query into
the adapter, which is what the system resolver needs. The two `/1` routes carry
everything else; the host route the kernel adds for the peer covers the DNS address
itself. The host end is the host's side of the link, not a second tunnel address.

`Tun` is a `datapath.Persistent` and a `datapath.Reserver`; its `Requires` is
`Admin` and `Netguard`. DNS protection is not in this child: `DNSGuard` and the
nftables guard come with [DNS protection and
kill-switch](../05-protection/plan.md). Here the system DNS is set to `10.66.67.1`
and restored on `Suspend` and `Close`. `tun_dns` — the DNS behind the exit — is a
`TunOptions` field parsed by the next child; the netstack sends every query there.
The accepted `netstack` is unchanged except for a corrected comment about the link's
ownership.

The Linux pieces may be carried from csqtt-cli (MIT): the `Tun` lifecycle with its
adapter seam, the opener with the abstract-socket lock and the foreign-link refusal,
`linuxsys.SetTunnelRoutes`, `DetectSystemDNS` with `Recover`, and the shape of the
lock and netns tests. They are rewritten for the in-process netstack: no `SendFD`, no
`dup`, no second process, no `TUNCONF`. `linuxsys` gains a point-to-point form next
to the existing no-peer `ConfigureAdapter`. The adapter name is `identity.Name` (the
same value as `app.Name`), the lock `"@" + identity.Name + "-tun"`;
`scripts/checktunnel/main_linux.go` already looks for both. The datapath does not
import `internal/app`: `app` imports `datapath`, so the name comes from
`internal/identity`.

Out of scope: `connect`/supervisor wiring, `netguard` and the core bypass, the
nftables DNS protection and kill-switch, the profile's `tun_dns` default, and the
Windows adapter.

## Steps

1. **The point-to-point address in `linuxsys`** — `internal/linuxsys/adapter_linux.go`:
   add `ConfigurePointToPoint(index int, addr, peer netip.Addr, mtu int, logf
   func(string, ...any)) error` next to the existing `ConfigureAdapter`. It sets
   `addr/32` with `peer/32` (`netlink.Addr{IPNet: ..., Peer: &net.IPNet{...}}`), the
   MTU and brings the link up, and removes any other global address as
   `ConfigureAdapter` does, so a reconfiguration is clean. The tun opener is its only
   caller; no existing call site changes. Watch: `netlink.AddrReplace` sends the
   local address in `IFA_LOCAL` and the peer in `IFA_ADDRESS`, and takes the prefix
   length from the peer's mask; both are /32. The netns test below proves the result
   (local address, peer, MTU, up, the peer route).

2. **The `Tun` core** — new file `internal/datapath/tun.go`, package `datapath`,
   plain Go with no Linux imports:
   - constants with their reasons: `tunAddress = netip.MustParseAddr("10.66.67.1")`
     (the netstack's address, the DNS listener's and the system DNS),
     `tunHostAddress = netip.MustParseAddr("10.66.67.2")` (the host end of the
     point-to-point link), `tunRoutes4 = []netip.Prefix{0.0.0.0/1, 128.0.0.0/1}`.
     `TunnelMTU` from `datapath.go` is the MTU of both the kernel link and the
     `fdbased` endpoint.
   - `TunOptions{Name string; Dialer exit.Dialer; TunDNS netip.Addr; Logf
     func(string, ...any)}`; `NewTun(opts) (*Tun, error)` refuses a nil dialer and a
     `TunDNS` that is not a usable IPv4 address, and defaults `Name` to
     `identity.Name`.
   - `Tun` fields: `opts`, `openAdapter` (the seam for the tests and the platform
     opener), `mu`, `closed`, `adapter`, `configured`, `up`, `closeOnce`, `closeErr`.
     `tunAdapter` is the platform seam: `configure`, `addAddress`, `removeAddress`,
     `addRoutes`, `removeRoutes`, `setDNS`, `revertDNS`, `close`, all `func() error`;
     the remove and revert ones are harmless when repeated.
   - `Requires() Requirements{Unsupported: tunUnsupported(), Admin: true, Netguard:
     true}` (no `DNSGuard` yet); `Reserve()` takes the abstract-socket lock for the
     whole run. `errTunBusy` and `errTunClosed` are the two refusals, with the
     `-datapath socks` hint and the closed state.
   - `Start() error`: under `mu`, refuse when closed; if `up`, return nil (a repeated
     Start is harmless); open the adapter; `configure` once per adapter; then
     `addAddress` (the netstack's `/32` and the DNS listener), `addRoutes`,
     `setDNS`; `up = true`. On any error run the reverse of the steps already taken
     (`suspendLocked`) and return the joined error: the adapter stays open for the
     next Start, but no route or system DNS is left pointing into it.
   - `Suspend() error`: under `mu`, nothing before the first Start (the adapter is
     not opened); otherwise `revertDNS`, `removeRoutes`, `removeAddress`, each
     attempted even if a previous one fails, errors joined; `up = false`. The steps
     are idempotent, so a repeated Suspend is harmless and retries a cleanup that
     failed once.
   - `Close() error`: `closeOnce`; under `mu`, set closed; when the adapter exists,
     run `suspendLocked` even if `up` is false (a cleanup that failed before is
     retried); then the adapter's `close` (it closes the fd, the link and the
     netstack and releases its lock reference), clear the adapter and join the
     errors. Concurrent calls all return after the same cleanup; a call before Start
     does not open the adapter.
   - The order is the contract the tests check: configure → netstack address →
     routes → system DNS; the reverse on suspend. Comments say why each step is
     ordered so. `var _ Persistent = (*Tun)(nil)`, `var _ Reserver = (*Tun)(nil)`.

3. **The Linux opener** — new file `internal/datapath/tun_linux.go`:
   - `tunDevice = "/dev/net/tun"`; `tunFlags = unix.IFF_TUN | unix.IFF_NO_PI`;
     `tunLockName = "@" + identity.Name + "-tun"` as a variable (tests replace it);
     `tunUnsupported()` checks the device and returns the `modprobe tun` hint.
   - `acquireTunLock()` as in csqtt-cli: `net.Listen("unix", tunLockName)`, an
     in-process reference count, `EADDRINUSE` → `errTunBusy`, and a release function
     that is safe to call twice. `Reserve` and the adapter open both take it; the
     caller's release is separate from the adapter's.
   - `openTunAdapter(name string, dialer exit.Dialer, tunDNS netip.Addr, logf
     func(string, ...any)) (*tunAdapter, error)`: lock; `linuxsys.LinkKind(name)` —
     an existing foreign interface is refused with its kind and the `sudo ip link del
     <name>` hint, never deleted; `detectSystemDNS(name)` (a package variable, `=
     linuxsys.DetectSystemDNS`, a test seam) — an unknown `resolv.conf` generator
     refuses before the adapter exists; log the method and its warning;
     `dns.Recover()` — remove a resolvconf record or restore the copy left by a
     crashed run, log what it removed, refuse on an error; `createTUN(name)` with
     `TUNSETIFF` and `tunFlags | unix.IFF_TUN_EXCL` (`EBUSY` becomes the
     foreign-interface refusal); `linuxsys.LinkIndex(name)`; `fdbased.New` with
     `FDs: []int{fd}`, `MTU: TunnelMTU`, `EthernetHeader: false` and
     `ProcessorsPerChannel: 1`; `newNetstack(link, dialer, tunDNS)`. Every error
     closes the fd and releases the lock; an error after the link exists also waits
     for it. The `fdbased` endpoint does not own the fd (`endpoint.go:257`), so the
     adapter closes it.
   - The `tunAdapter` functions: `configure` → `linuxsys.ConfigurePointToPoint(index,
     tunHostAddress, tunAddress, TunnelMTU, logf)`; `addAddress`/`removeAddress` →
     `ns.addAddress(tunAddress)` / `ns.removeAddress(tunAddress)`; `addRoutes`/
     `removeRoutes` → `linuxsys.SetTunnelRoutes(index, tunRoutes4)` /
     `ClearTunnelRoutes`; `setDNS` → `dns.Set(index, []netip.Addr{tunAddress})` and
     the log line; `revertDNS` → `if dns.Applied() { dns.Revert(index) }` and the
     log line; `close` → close the fd, `link.Wait()` (the reader stops on the closed
     fd), `ns.close()` (the stack and the streams), then `unlock()`, errors joined.
     The `fdbased` reader is not stopped by `stack.Close`, hence the order; a comment
     says it.
   - `foreignLinkError(name, kind)` with the hint; no deletion.
   - Correct the comment in `internal/datapath/netstack.go`: the link belongs to the
     caller for `fdbased`; `close` does not stop it.

4. **The non-Linux side** — new file `internal/datapath/tun_other.go`
   (`//go:build !linux`): `tunUnsupported` returns "tun is not implemented on this
   system yet; use -datapath socks"; `openTunAdapter` and `acquireTunLock` return the
   same error. The Windows stage replaces this file; the build tag is the seam.
   `tun.go` has no Linux imports, so `GOOS=windows` builds it.

5. **Lifecycle unit tests** — new file `internal/datapath/tun_test.go`, no root and no
   TUN: a `fakeAdapter` records the calls in order and can fail any step; `fakeTun`
   replaces `openAdapter`.
   - The order: `Start` gives configure (once) → addAddress → addRoutes → setDNS;
     `Suspend` gives revertDNS → removeRoutes → removeAddress; a repeated `Start`
     adds nothing, and a repeated `Suspend` re-runs the idempotent cleanup and
     returns nil; `Start` after `Suspend` repeats the forward order without
     configure; `Close` runs the cleanup and the adapter's close once; `Start` after
     `Close` is `errTunClosed`; `Suspend` and `Close` before `Start` do not open the
     adapter.
   - Failures: a table where configure, addAddress, addRoutes and setDNS each fail;
     each returns the error, runs the cleanup of the steps done (the fake records
     it), and the next `Start` with the fault cleared succeeds. A failing cleanup
     step (`revertDNS`, `removeRoutes` or `removeAddress`) does not stop the others:
     `Suspend` joins their errors and leaves the datapath suspended.
   - The DNS target: the fake sees `setDNS` with `10.66.67.1` — the adapter's
     address, not `TunDNS` — and `addAddress` with the same address; `TunDNS` goes
     only to the netstack.
   - `NewTun` refuses a nil dialer and a missing or non-IPv4 `TunDNS`; the default
     name is `identity.Name`; `Requires` says Admin and Netguard; a failing open is
     returned by `Start` and retried on the next call; concurrent `Close` calls all
     return after one cleanup.
   - These tests run on every platform with the fake; the Linux-only assertions
     (`Unsupported` is nil, the lock, the opener) are in the Linux test files.

6. **The lock and its test** — new file `internal/datapath/tun_lock_linux_test.go`:
   `TestMain` replaces `tunLockName` with `"@dnstt-cli-test-tun-" + pid` and records
   the real one, so the tests never take a running dnstt-cli's lock; a helper mode in
   the test binary (`DNSTT_CLI_TUN_HELPER=lock`, `DNSTT_CLI_TUN_LOCK=<name>`) tries to
   take the lock and exits 3 on refusal.
   `TestTunLockExcludesOtherProcess`: the reservation and the adapter's own
   acquisition share the socket inside one process; a second process is refused with
   the `-datapath socks` hint; releasing the adapter does not release the
   reservation; releasing the reservation lets the helper in; a repeated release
   does not decrement twice. No root: an abstract socket needs no privilege.

7. **The real adapter in a container** — new file
   `internal/datapath/tun_netns_linux_test.go`, build tag `linux`, skipped unless
   `DNSTT_CLI_TEST_NETNS=1` and root (the `linuxsys` pattern):
   - `TestTunAdapterNetns`: `NewTun` with a short test name, a fake exit and the
     documentation `tunDNS` `203.0.113.53`; `Reserve`, `Start`. Through netlink only
     (no `ip` binary): the link is `tuntap`, up, MTU 1500; its address is
     `10.66.67.2/32` with the peer `10.66.67.1/32`; `RouteGet` for `10.66.67.1`
     leads into the adapter, not to a local route; the two `/1` routes point into
     it. A TCP client (`net.Dial` to `203.0.113.9:80`) reaches an echo server
     through the fake exit, and the fake exit saw `203.0.113.9:80`. A UDP query
     built with `dnsmessage` to `10.66.67.1:53` reaches the fake DNS server behind
     the fake exit at `203.0.113.53:53` (the `dnsExit` helper of the accepted
     tests), and the answer with the A record comes back to the client. `Suspend`
     removes the `/1` routes and the netstack address (a query to `10.66.67.1`
     then gets no answer); `Start` again restores routes and queries; `Close`
     removes the interface and its routes; the lock is free after the reservation
     and the close are released (the helper process of step 6).
   - `TestTunSystemDNSNetns`: save `/etc/resolv.conf` (the container's file
     method); simulate a crashed run (a `resolv.conf` with dnstt-cli's marker and a
     backup of the original); `Start` → the current file has `nameserver
     10.66.67.1`, the backup holds the restored original, and the log says the
     previous file was restored; `Suspend` → the original is back; `Start` again →
     the tunnel DNS again; `Close` → the original is back and no backup remains.
     The `linuxsys` tests keep the resolvconf and systemd-resolved mechanics; this
     one proves the integration and the order (recover before set).
   - `TestTunForeignLinkNetns`: a dummy named like the adapter; `Start` refuses with
     `dummy` and the `ip link del` hint; the dummy is not deleted; the lock is free.
   - `TestTunExclusiveNetns`: a persistent TUN with the adapter's name; `createTUN`
     refuses with `EBUSY` (the window between the `LinkKind` check and `TUNSETIFF`),
     and `Start` refuses with the foreign-interface error; the interface is never
     adopted or deleted.
   - Cleanup: `defer tun.Close()` and the reservation's release in each test; unique
     interface names; no parallel tests; the routes are gone after each.

8. **The CI `netns` job** — `.github/workflows/linux.yml`: build
   `go test -c -o build/out/datapath.test ./internal/datapath` next to the `linuxsys`
   binary; run it with the same `docker run` setup (`--cap-add NET_ADMIN --device
   /dev/net/tun`, `DNSTT_CLI_TEST_NETNS=1`, the pinned image; no pull) and `tee` its
   log; fail the job when either log has `--- SKIP` or lacks `--- PASS`. The test
   overrides the container's default route only inside the container and closes its
   adapter, so the container's network comes back.

9. **Checks** — `gofmt` on the changed files; `go vet ./...` (full vet still reports
   only the two pre-existing `core/noise` warnings, the CI split); `go test -race
   -count=1 ./internal/datapath` (the fake and lock tests; the netns tests skip) and
   `go test -race -count=1 ./...`; `CGO_ENABLED=0 go build ./...`; `GOOS=windows
   CGO_ENABLED=0 go build ./...`; the Windows `go test ./...` runs the fake tests.
   The netns tests run in the CI container as above. No new dependency and no
   download; no `ip` or `nft` binary is called by the code or the tests.

## Done when

- A fake adapter drives `Tun.Start`, `Suspend` and `Close` without root: the order is
  configure (10.66.67.2/32, peer 10.66.67.1/32, MTU 1500, up; once per adapter) → the
  netstack's 10.66.67.1 and its DNS listener → the two `/1` routes → the system DNS
  10.66.67.1; `Suspend` reverses it (DNS revert → routes removed → netstack address
  removed); repeated calls are harmless; a failure in one step still runs the cleanup
  of the others and returns the error; `Start` after `Close` is refused; `Suspend`
  and `Close` before `Start` do not open the adapter.
- In a container with `NET_ADMIN` and `/dev/net/tun` (the CI `netns` job), the real
  adapter is a `tuntap` link, up, MTU 1500, with the host end `10.66.67.2/32` and the
  peer `10.66.67.1/32`; `10.66.67.1` is not a local route; the two `/1` routes point
  into it; a TCP client routed into it reaches an echo server through a fake exit
  that sees the original destination; a DNS query to `10.66.67.1:53` reaches the fake
  DNS server behind the fake exit and the answer comes back; `Suspend` removes the
  routes and the netstack address, `Start` restores them, and `Close` removes the
  interface and its routes; no `ip` or `nft` binary is called.
- The netstack's `/32` `10.66.67.1` is added in `Start` and removed in `Suspend` and
  `Close`; the DNS handler of the previous child is reachable at that address (the
  DNS exchange above).
- The system DNS is set to `10.66.67.1` through `linuxsys.DetectSystemDNS`; `Recover`
  runs after the lock and before `Set` and removes a resolvconf record or an
  overwritten `resolv.conf` left by a crashed previous run, with a log line; `Revert`
  runs on `Suspend` and `Close`; the container test shows the file set and restored;
  an unknown `resolv.conf` generator is refused before the adapter is created.
- The abstract socket `@dnstt-cli-tun` refuses a second process (a helper process, no
  root); the reservation and the adapter's own acquisition share it inside one
  process; an existing foreign interface with the adapter's name is refused with its
  kind and a hint and is never deleted; `IFF_TUN_EXCL` refuses an existing persistent
  TUN with the same name.
- The netns tests run in the CI `netns` job with `DNSTT_CLI_TEST_NETNS=1` in the
  container that already runs `internal/linuxsys`; a skip fails the job; `go vet
  ./...` (the known `core/noise` split), `go test -race ./...`, the Linux build and
  the Windows cross-build pass; no new dependency or download.
- Only `internal/datapath`, `internal/linuxsys` and the CI workflow change. `connect`,
  the supervisor, `netguard`, the nftables protection, the profile and the `tun_dns`
  default are untouched; `Tun` is ready as the `Persistent` and `Reserver` of the next
  child.

## Depends on

[DNS over TCP, other UDP and IPv6](../02-dns-udp-ipv6/plan.md). The stage's
[foundation](../../01-foundation/plan.md) is done.

## Outcome

The real Linux adapter sits under the netstack: the datapath opens `/dev/net/tun`,
configures the point-to-point link, installs the routes and the system DNS, and feeds the
accepted netstack through a `fdbased` endpoint in-process. No new dependency, no download,
and no `ip` or `nft` binary is called.

- `linuxsys.ConfigurePointToPoint(index, addr, peer, mtu, logf)` sets `addr/32` with
  `peer/32` (`netlink.Addr` with `Peer`), the MTU and the link up, and removes other
  global addresses as `ConfigureAdapter` does. The netns test proves the host end
  `10.66.67.2/32` with peer `10.66.67.1/32`, MTU 1500 and the link up.
- `Tun` (`internal/datapath/tun.go`, no Linux imports) is `Persistent` and `Reserver`:
  `Requires` is `Admin` and `Netguard` (no `DNSGuard` yet), `Reserve` holds the
  abstract-socket lock for the run, and the name defaults to `identity.Name`. The system
  DNS and the netstack's address are `10.66.67.1`; `TunDNS` goes only to the netstack.
  `Start` is configure once per adapter → the netstack's address and its DNS listener →
  the two `/1` routes → the system DNS; a failure runs the reverse cleanup and returns
  the joined error, and the adapter stays open for the next attempt. `Suspend` is the
  reverse order, each step attempted even when a previous one fails; `Close` runs the
  cleanup once, then closes the adapter, and `Start` after `Close` is refused. `Suspend`
  and `Close` before `Start` do not open the adapter.
- The Linux opener (`tun_linux.go`) takes the abstract socket `@dnstt-cli-tun` (a
  refcount inside the process; `EADDRINUSE` is `errTunBusy` with the `-datapath socks`
  hint), refuses an existing foreign interface with its kind and the `ip link del` hint
  and never deletes it, chooses the system DNS method before the adapter exists, runs
  `Recover` after the lock and before `Set`, creates the adapter with `IFF_TUN_EXCL`
  (`EBUSY` is the same foreign-interface refusal), and gives the fd to `fdbased` (`FDs`,
  `MTU: TunnelMTU`, no Ethernet header, one processor per channel) and the link to
  `newNetstack`. Every error closes the fd and releases the lock; an error after the link
  exists detaches the NIC and waits for the reader.
- The adapter's `close` differs from the plan in one order only: `ns.close()` →
  `link.Wait()` → close the fd → release the lock, instead of closing the fd first. A
  `fdbased` reader is stopped by removing the NIC (`Attach(nil)` → `StopFD`), not by
  closing the fd; closing it first could leave `Wait` asleep and let the descriptor
  number be reused by another goroutine while the reader is in `readv`. The comments in
  `netstack.go` now say that the link belongs to the caller and that `close` stops the
  reader by detaching the NIC.
- The non-Linux build refuses `tun` with "not implemented on this system yet; use
  -datapath socks"; the Windows stage replaces that file.
- Tests: a fake adapter drives the order, repeated calls, a failure in each forward step
  with cleanup and a retry, failing cleanup steps joined, the concurrent `Close` and the
  open retry, without root or a TUN; the lock test runs a helper process of the test
  binary (a package cannot have a second `TestMain`, so the test lock name is set in
  `init`); the netns tests create the real adapter and check its kind, state, MTU,
  address and peer, that `10.66.67.1` is routed into it and is not local, the two `/1`
  routes, TCP through the fake exit with the original destination, a DNS query to
  `10.66.67.1:53` answered as an A record, `Suspend`/`Start`/`Close`, a foreign dummy and
  a persistent TUN refused without deletion, and the system DNS set, restored on
  `Suspend` and `Close`, with a crashed run's file recovered and logged.
- `gofmt` is clean on the touched files; the CI vet split passes (the two pre-existing
  `core/noise` warnings stay); `go test -race -count=1 ./internal/datapath` and the full
  `go test -race -count=1 ./...` pass; `CGO_ENABLED=0 go build ./...` and `GOOS=windows
  CGO_ENABLED=0 go build ./...` succeed. The netns tests pass in a container with
  `NET_ADMIN` and `/dev/net/tun`, the setup of the CI `netns` job, which now builds and
  runs both test binaries and fails on a skip; the job itself was not executed from this
  tree.
- Independent review found no defect of behaviour; two remarks on a comment and on a
  tautological test assertion were accepted and fixed.
- Only `internal/datapath`, `internal/linuxsys` and the CI workflow change. `connect`,
  the supervisor, `netguard`, the nftables protection and the profile are untouched;
  `Tun` is ready as the `Persistent` and `Reserver` of
  [Connect wiring](../04-connect-wiring/plan.md).

No real problem was left incompletely fixed because of cost.
