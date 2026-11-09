---
kind: task
status: todo
depends: [03-tun-adapter]
---

# Connect wiring, core bypass and network changes

## Task

Make `connect -datapath tun` a working tunnel and keep its own sockets out of it
(`architecture.md` R4, §06, §08, §09): the supervisor starts and suspends the persistent
datapath with each session; `netguard.Guard` chooses the physical interface, binds the
core's sockets to it (`SO_BINDTOIFINDEX`) and gives them the mark when protection is on; a
strict IPv4 `rp_filter` on that interface is a refusal with a hint; `netguard.Watch` sees
a new default route and restarts the session. The TUN datapath is from
[TUN adapter](../03-tun-adapter/plan.md); the guard and the watcher are already in
`internal/netguard`.

## Done when

- The `connect` command reads `datapath` from the profile and accepts `-datapath
  socks|tun`; `tun` on a system without a TUN (macOS, a kernel without the module) or
  without root is refused with a clear message and the command to use (`sudo dnstt-cli
  connect`, `-datapath socks`), and the `socks` path is unchanged. `Requirements.Admin` is
  checked before any reserve or dial.
- `app.BuildDatapath` builds the TUN datapath with its `Requirements`, and `connect` calls
  `Reserve` before the first attempt and releases it on every exit path. The supervisor
  has a way to take the datapath (`SetDatapath` or an equivalent set once before `Run`)
  and calls `Start` after a session is accepted and `Suspend` when it ends, including the
  stop path; a failed `Start` closes the session and is retried with the normal delay.
- `tun_dns` is parsed from the profile before the adapter is created: empty means
  `1.1.1.1`, the port is 53, and anything that is not an IPv4 address is an error. The
  value is the DNS behind the server; the system resolver gets the adapter's own address.
- The session's `client.Config.Control` is `netguard.Guard.Control`; the TODO stub in
  `internal/app/tunnel.go` is gone, and the core's existing `Control` coverage passes.
- `SelectInterfaces` excludes the adapter's index and gives the physical interface; a
  folded network change calls `supervisor.Restart` once with a "network changed" reason,
  and the next session's sockets use the new indexes. A change to no network waits for
  the next one instead of restarting.
- A strict IPv4 `rp_filter` on the chosen interface refuses before any dial with the
  `sysctl` hint and the `-datapath socks` alternative; firewalld's strict IPv6 filter
  sets `NoIPv6` on the guard instead of refusing (the core falls back to IPv4).
- The checks: unit tests with fake selectors, checkers and watcher (no root); the netns
  datapath test from the previous child still passes; `go vet ./...`, `go test -race
  ./...`, the Linux build and the Windows cross-build. The real tunnel end to end is
  [Live acceptance](../06-live-acceptance/plan.md), not this task.

## Depends on

[TUN adapter, routes and system DNS](../03-tun-adapter/plan.md).
