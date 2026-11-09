---
kind: task
status: todo
depends: [02-dns-udp-ipv6]
---

# TUN adapter, routes and system DNS

## Task

The real Linux adapter and the datapath lifecycle (`architecture.md` R6, R7, §05):
`/dev/net/tun` with `IFF_TUN|IFF_NO_PI|IFF_TUN_EXCL`, the one-adapter lock on
`@dnstt-cli-tun`, a private `/32` address and MTU 1500, the `0.0.0.0/1` and `128.0.0.0/1`
routes into the adapter, the system DNS pointing at the adapter's address, and the
persistent lifecycle: `Start` on a session, `Suspend` between sessions, `Close` at the
end. The descriptor is handed to the netstack as a `fdbased` link endpoint; there is no
second process or descriptor handover. This is the datapath level only: `connect` is wired
to it in [Connect wiring](../04-connect-wiring/plan.md).

The Linux pieces may be carried from csqtt-cli (MIT): the adapter opener and its abstract
socket lock, `linuxsys.ConfigureAdapter`, `SetTunnelRoutes`, `DetectSystemDNS` and the
`Persistent` lifecycle. The adapter name is `app.Name`, the lock name is
`"@" + app.Name + "-tun"` — `scripts/checktunnel/main_linux.go` already looks for both.
Use one private `/32` constant (`10.66.67.1`); the link is point-to-point and only the DNS
listener depends on the address.

## Done when

- A fake adapter drives `Tun.Start`, `Suspend` and `Close` without root: the order is
  configure → routes → DNS, and the reverse on suspend; repeated calls are harmless; a
  failure in one step still runs the cleanup of the others; a state after `Close` is
  refused.
- In a container with `NET_ADMIN` and `/dev/net/tun` (the CI `netns` job), the real
  adapter appears with the private `/32` and MTU 1500, the two `/1` routes point into it,
  and a TCP client routed into it reaches an echo server through a fake exit. `Close`
  removes the interface and its routes; no `ip` or `nft` binary is called.
- The adapter's `/32` is added to the netstack in `Start` and removed on `Close`; the DNS
  handler of the previous child is reachable at that address.
- The system DNS is set to the adapter's address through `linuxsys.DetectSystemDNS` and
  restored on `Close`; a resolvconf record or an overwritten `resolv.conf` left by a
  crashed previous run is removed by `Recover` before `Set`. An unknown resolv.conf
  generator is a refusal before the adapter is created.
- A second instance is refused by the abstract socket; an existing foreign interface with
  the adapter's name is refused with its kind and a hint, never deleted.
- The netns tests are included in the CI command that already runs `internal/linuxsys`
  with `DNSTT_CLI_TEST_NETNS=1` (build and run the `internal/datapath` test binary in the
  same container); `go vet ./...`, `go test -race ./...`, the Linux build and the Windows
  cross-build pass.

## Depends on

[DNS over TCP, other UDP and IPv6](../02-dns-udp-ipv6/plan.md).
