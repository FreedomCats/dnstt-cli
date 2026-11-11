---
kind: task
status: done
detailed: true
---

# Recover the system DNS before the first dial

## Task

The matrix of P2 found a hole in the `kill -9` promise (`architecture.md` §05a, "If dnstt-cli
crashes or is killed", lines 403-409; the parent stage, `03-tun-linux/plan.md:32-35`). The
adapter, the routes, the `inet dnstt-cli` table and the lock disappear with the process, but
the `resolvconf` record `tun.dnstt-cli` stays and the adapter's `10.66.67.1`
(`internal/datapath/tun.go:22-29`) stays first in `resolv.conf`. The next `connect -datapath
tun` then cannot start: the profile's resolver is given by host name, and the first session
resolves that name through the bootstrap resolver, whose servers come from the system DNS
(`internal/app/tunnel.go:78-96`, `internal/app/networkdns_linux.go:11-13`,
`internal/resolvers/bootstrap.go:89-98`, R5). The system DNS points at the adapter of the dead
run, which the new run has not opened yet, so the lookup fails and no session comes up.

The recovery that exists today runs in `openTunAdapter` (`internal/datapath/tun_linux.go:83-92`)
and therefore in `Tun.Start`, which the supervisor calls only for a session that has already
passed the exit check (`internal/supervisor/tunnel.go:492-499`). The first attempt must resolve
a name before that, so the recovery waits for the very session that needs it: the leftover is
never removed, and the run ends in handshake failures instead of the promised line and a
working session.

This task moves the recovery to where the machine is already claimed for the run: right after
the TUN reservation in `connect` (`internal/cli/connect.go:160-167`), before the tunnel is run.
The per-adapter recovery stays as a second guard. Nothing changes for `socks`, for a clean
system, for a `resolvconf` record of another name, or for a `resolv.conf` rewritten by someone
else after the crash; the recovery message stays exactly as it is.

**Order chosen: recover under the reservation, before the first attempt.** The reservation is
the only thing that tells that no other dnstt-cli is running, which is the condition `Recover`
documents (`internal/linuxsys/dns_linux.go:252-255`), and it is taken before `Supervisor.Run`
(`internal/cli/connect.go:256-259`). Rejected: skipping our own address in
`linuxsys.NetworkDNS()` (`internal/linuxsys/networkdns_linux.go:21-46`) — that only hides the
leftover from the bootstrap; every other program would still send DNS to a dead address, a run
that never reaches the adapter (`socks`, a busy lock, an `rp_filter` refusal) would leave it,
and §05a's "removes it and says so" would not happen. Rejected: recovering in `BuildDatapath`
or `BuildTunnel` — both run before the lock, where they could remove the system DNS of a
running dnstt-cli. Rejected: calling `Start` earlier, before the session — the adapter must not
carry system traffic into a tunnel that is not up (`internal/app/tunnel.go:249-255`).

The plain-file leftover is already cushioned in the bootstrap: `NetworkDNS` reads the copy when
the file is ours (`internal/linuxsys/networkdns_linux.go:26-30`). The `resolvconf` record has
no such cushion — the record's servers are what the file and `NetworkDNS` see — and
`systemd-resolved` leaves nothing. The early recovery covers all three methods the same way.

## Steps

1. **The seam in the datapath.** In `internal/datapath/datapath.go`, next to `Reserver`
   (lines 56-63), add:

   ```go
   type DNSRecoverer interface {
       RecoverSystemDNS() (string, error)
   }
   ```

   Document: what a crash can leave in the system DNS on Linux, that `""` means nothing was
   left, that the caller holds the reservation and this is what makes the call safe (the
   adapter lock, `internal/linuxsys/dns_linux.go:252-255`), that a second call is harmless,
   and that it must run before the first attempt because the first attempt may need the
   system DNS.

2. **The method on the TUN datapath (Linux).** In `internal/datapath/tun_linux.go`, next to
   `openTunAdapter`, add:

   ```go
   func (t *Tun) RecoverSystemDNS() (string, error) {
       dns, err := detectSystemDNS(t.opts.Name)
       ...
       done, err := dns.Recover()
       ...
   }
   ```

   - The name is the adapter name; `NewTun` already defaulted it (`internal/datapath/tun.go:127-130`),
     and it is the name the record was written under (`tun.<name>`, `dns_linux.go:249-250`).
   - Wrap every error as `system DNS: %w`, the wording `openTunAdapter` uses for the same
     detection and recovery (`tun_linux.go:74-89`); return the `Recover` message unchanged.
   - It opens no adapter, calls no `Set`/`Revert` and needs no root of its own: the caller has
     it for the TUN datapath already.
   - Non-Linux `Tun` (`tun_other.go`) stays without the interface: Windows leaves a registry key
     that affects nothing (`architecture.md:408-409`) and is out of scope.
   - Make `detectSystemDNS` replaceable by tests: give it a type a fake can satisfy — a small
     interface with the methods the adapter and this method use (`String`, `Method`, `Warning`,
     `Applied`, `Recover`, `Set`, `Revert`) and
     `var detectSystemDNS = func(name string) (systemDNS, error) { return linuxsys.DetectSystemDNS(name) }`.
     The variable is already documented as "a variable for the tests" (`tun_linux.go:36-37`)
     and no test uses it today; the closures in `openTunAdapter` need no other change (`%s`
     still calls `String`). An interface just for tests follows `linuxsys`'s own `resolvedBus`
     (`internal/linuxsys/dns_linux.go:384-390`).
   - Keep the `Recover` in `openTunAdapter` (`tun_linux.go:83-92`) and add one line to its
     comment: the run also removes the leftover before the first attempt (`connect`), and this
     one covers a caller that did not; on a clean system it stays silent because the record is
     already gone.

3. **The call site, in order.** In `internal/cli/connect.go`, replace the reservation block
   (lines 160-167) with a call to a small helper in the same file:

   ```go
   release, err := prepareDatapath(dp, logf)
   if err != nil {
       return err
   }
   defer release()
   ```

   `prepareDatapath(dp app.Datapath, logf func(string, ...any)) (release func(), err error)`
   does, in this order:
   - if `dp` is a `datapath.Reserver`, call `Reserve` and keep its release;
   - if it is a `datapath.DNSRecoverer`, call `RecoverSystemDNS`; on error release the
     reservation and return the error; on a non-empty message `logf("[TUN] %s", done)` — the
     same prefix and text as `openTunAdapter` (`tun_linux.go:90-92`), so the P2 expectation in
     the runbook does not change, only its place in the log: the line now comes before the
     kill-switch notice and the first attempt;
   - a datapath with neither interface (`socks`) is left alone; the release for it is a no-op.

   Keep the existing comment at the call site and add the reason for the order:
   `Supervisor.Run` comes later in the same function (lines 256-259) and its first session
   resolves a resolver's host name through the system DNS, while `Tun.Start` and its recovery
   come only after the first session; without this call the leftover outlives the attempt.
   Do not add an exception for `-allow-leak`: the flag is about the kill-switch and DNS
   protection, not about the system DNS. A recovery error stops the run: the same record breaks
   the first lookup anyway, and a failed `resolvconf -d` deserves the method's name instead of
   a handshake failure.

4. **Tests: the order and the reservation (`internal/cli`).** New
   `internal/cli/connect_prepare_test.go`, package `cli`, no build tag (the interfaces are in
   `datapath.go` and the helper is not platform-specific). A fake `app.Datapath` (`Close`,
   `Requires`) with the optional interfaces, recording calls in a shared slice:
   - reservation, then recovery, in that order and once each; the returned release releases;
   - a recovery error: the error is returned, the reservation is released, nothing is logged
     about the record;
   - the message `record … removed`: logged once as `[TUN] record … removed`; `""` logs
     nothing;
   - a datapath with neither interface: no calls and a release that is safe to call.
   This pins "the leftover is removed under the reservation"; that the call stands above
   `Supervisor.Run` is the same function and stays visible at the call site.

5. **Tests: the datapath method (`internal/datapath`).** New
   `internal/datapath/tun_recover_linux_test.go`, package `datapath`, Linux tag:
   - replace `detectSystemDNS` with a fake, restore it in `t.Cleanup`; build the datapath with
     `NewTun` and a documentation `TunDNS` (`203.0.113.53`) and the tests' `dialerFunc`; no
     adapter, no `/dev/net/tun` and no root are needed;
   - a leftover: the fake's `Recover` returns the message → the method returns it unchanged,
     no error, `Set`/`Revert` are not called;
   - a clean system: `Recover` returns `""` → `""`, nil;
   - a `Recover` error and a detection error → `system DNS: <cause>`, an empty message.
   The real `linuxsys` methods are not re-tested here.

6. **Tests: the three system-DNS methods (`internal/linuxsys/dns_linux_test.go`).** The
   existing cases already cover a clean start (inside `TestSystemDNSFile`, line 273), the plain
   file (`TestSystemDNSFileRecover`: our own file restored, and a file rewritten by someone
   else kept with the copy deleted), both `resolvconf` flavours (`TestSystemDNSResolvconfRecover`:
   the record removed, a repeated call harmless) and "nothing left, no `resolvconf` call"
   (inside `TestSystemDNSResolvconf`). Add the one missing case: a record of another name in
   `/run/resolvconf/interface` (ours absent) is left alone and no `resolvconf -d` runs — a
   leftover that is not ours. No change to `Recover` itself is expected; if a test shows one,
   it belongs to this task and the plan is updated with it, not silently.

7. **Leave the live path alone.** `doc/local/runbook-tun-linux.md` and `doc/runs/tun-matrix/`
   are not read or edited; the P2 expectation of `01-runbook` (lines 114-123) stays as written.
   The live acceptance is the repeated P2 in `02-user-run`; this node is one of its
   dependencies.

8. **Checks.** `go build ./...`; `go vet ./...`;
   `go test ./internal/linuxsys ./internal/datapath ./internal/cli ./internal/app ./internal/supervisor`;
   then `go test ./...` from the module cache without downloading a module. Root-only and netns
   tests skip as before (`tun_lock_linux_test.go`, `tun_netns_linux_test.go`): no container, no
   network, no live run. `git status --short` shows only code, tests and this node.

## Done when

- `connect -datapath tun` calls `RecoverSystemDNS` after `Reserve` and before `Supervisor.Run`;
  the order is visible in `connect.go` and asserted by the helper's test.
- A leftover removed by the early call prints exactly `[TUN] <the Recover message>`, once; a
  clean system prints nothing, and the second, per-adapter call stays silent.
- A recovery error reads `system DNS: <cause>`, releases the TUN reservation and stops the run
  before any dial; an unknown `resolv.conf` generator is refused at the same point, not after
  the first session.
- `socks` and any datapath without the interface are unchanged: no recovery call, no extra
  privilege, no new output.
- The tests pass: order and release (`internal/cli`), the method and its wrapping
  (`internal/datapath`), clean start, plain file, both `resolvconf` flavours, a repeated call,
  a foreign file and a foreign record (`internal/linuxsys`).
- `go build ./...`, `go vet ./...` and `go test ./...` pass; no network, container or live run;
  no real value and no environment detail in the changed files.
- The acceptance on the machine is the repeated P2 in `02-user-run`: after `kill -9` the next
  start removes the record with the line above, brings the session up and needs no manual
  `resolvconf -d`; the runbook's P2 text is unchanged.

## Risks and what is needed from the user

- The DNS method is detected twice per run (early recovery and `openTunAdapter`): one extra
  `resolvconf --version` on a `resolvconf` system. Harmless; keep both, the second covers a
  caller that did not recover.
- A failing `resolvconf -d` (its lock, up to the 20 s command timeout,
  `dns_linux.go:379-380`) now stops the run before any dial. Intended — the first lookup would
  fail anyway — but the user sees the error instead of a reconnect loop; the message names the
  record.
- The recovery now also runs on a start that is refused afterwards (a foreign adapter, an
  `rp_filter` refusal, a missing DNS protection without `-allow-leak`). That is the point: the
  leftover is removed by the next start, and a refused start is a start; it must not be read as
  a failure of P3/P6/P7, whose verdicts do not change.
- The plain-file branch deletes the copy and keeps a file rewritten by someone else after the
  crash (`dns_linux.go:537-560`); the next `Set` then backs up that file. The current behaviour
  stays and is the branch the file test checks.
- The unit test pins the order inside the helper, not inside `runConnect`. A full app-level
  order test needs root and `/dev/net/tun`; in the netns container it would also need a fake
  `resolvconf` and a rewritten container `/etc/resolv.conf`, more machinery than this fix
  carries. If a reviewer asks for it, it is a separate netns task.
- Windows stays untouched.
- Nothing is needed from the user for this task; the live check is the repeated P2, already in
  `02-user-run`.

## Outcome

The recovery now runs where the machine is already claimed for the run: `connect -datapath
tun` removes the system DNS a `kill -9` run left behind under the reservation and before
`Supervisor.Run`, so the first session's lookup of a resolver's host name no longer leads
into the adapter of the dead run. The per-adapter recovery in `openTunAdapter` stays as a
second guard and is silent after the early call.

- `datapath.DNSRecoverer` is the new optional seam, `RecoverSystemDNS() (string, error)`,
  next to `Reserver`; its comment carries the reason for the order and the reservation.
  `socks` and any datapath without the interface are untouched.
- `(*Tun).RecoverSystemDNS` (Linux only) detects the method by the adapter name, wraps
  every error as `system DNS: %w` and returns the `Recover` message unchanged. It opens no
  adapter and calls no `Set`/`Revert`, so it needs no privilege of its own; Windows has no
  method and no interface. `detectSystemDNS` is now a variable returning the small
  `systemDNS` interface (`String`, `Method`, `Warning`, `Applied`, `Recover`, `Set`,
  `Revert`), replaceable by tests; `openTunAdapter` is otherwise unchanged and still
  formats through `String`. The comment there notes the early call and the silence on a
  clean system.
- `connect` reserves and recovers through `prepareDatapath`, which returns the release: a
  recovery error releases the reservation, is returned as it is (`system DNS: <cause>`) and
  stops the run before any dial; a non-empty message is logged once as `[TUN] <message>`,
  and a clean system prints nothing. The reservation is released exactly once on every
  path. No exception is made for `-allow-leak`.
- Tests: `internal/cli/connect_prepare_test.go` pins the order, the release on error, the
  logged and the silent message and a datapath with neither interface;
  `internal/datapath/tun_recover_linux_test.go` covers the method with a fake detector (a
  leftover passed through, a clean system, a `Recover` error, a detection error) with no
  adapter, no `/dev/net/tun` and no root; `internal/linuxsys/dns_linux_test.go` gains
  `TestSystemDNSResolvconfForeignRecord`, where a `tun.other` record is left alone and no
  `resolvconf -d` runs. `Recover` itself needed no change.
- Checks: `gofmt` clean on the touched files; `git diff --check`; `go build ./...`; the CI
  vet split on Linux and for Windows; `CGO_ENABLED=0 go build ./...`; the Windows
  cross-build and `go test -c` of the packages with tests; the full
  `go test -race -count=1 ./...` with `DNSTT_CLI_TEST_DBUS=1` and
  `DNSTT_CLI_TEST_NETNS=0`, the four external-network netguard live checks excluded by
  `-skip`, so no live call was made. No root-only or netns suite ran, no network, no new
  dependency; `core/`, `go.mod` and `go.sum` are untouched.
- Independent review found no blocker and no remark worth changing. One line was added to
  the `Tun` comment in `tun.go` (it is also `DNSRecoverer` on Linux), beyond the written
  steps: the comment lists the type's roles and was otherwise incomplete.
- Not here, as the plan says: an app-level order test needs root, `/dev/net/tun` and a fake
  `resolvconf` in a netns container, which is a separate task; the live acceptance is the
  repeated P2 in `02-user-run`.

No real problem was left incompletely fixed because of cost.
