---
kind: stage
status: in-progress
base: 2524915
---

# System VPN on Linux

Goal (`architecture.md` §12, E2): `tun` mode sends all system traffic through the tunnel on
Linux, and nothing leaks while the tunnel is down. `architecture.md` R6, R7, §05, §05a, §09.

Most of the Linux system layer is already carried over and accepted: `internal/linuxsys`
has the adapter configuration, routes, system DNS, the nftables DNS protection and
kill-switch, the network monitor and the `rp_filter` check; `internal/netguard`,
`internal/sudo` and the `socks` mode of `internal/datapath` are in place. The children
below add the gVisor netstack, the `tun` datapath, its wiring into `connect`, and the live
acceptance.

The accepted `sudo` behaviour — the files and the keyring of the calling user — is already
in `internal/sudo`, `internal/config` and `internal/secretservice`; the live acceptance
checks it under a real `sudo`. Re-running a connection elevated from the menu is
[stage 5](../05-menu-and-release/plan.md), not this stage; here `connect` in `tun` mode
without root refuses with a hint.

## Done when

- `/dev/net/tun` → gVisor netstack in the shell → exit carries all TCP traffic.
- UDP other than DNS :53 from the adapter is rejected at once with ICMP "port
  unreachable", so QUIC falls back to TCP without a timeout.
- DNS from the adapter goes as DNS-over-TCP through the exit to the tunnel's DNS; the
  system gets the adapter's DNS address.
- Routes, DNS protection and the IPv6 block are set through nftables and netlink
  without `ip` and `nft`, and disappear with the process even after `kill -9`; system DNS
  is restored on a clean exit, and a `resolvconf` entry or an overwritten
  `/etc/resolv.conf` left after `kill -9` is removed by the next start with a message.
- The kill-switch blocks system traffic between sessions, with the exceptions of §05a
  (dnstt-cli itself, loopback, DHCP, ND/RA, the local network without port 53).
- A network change is noticed and the session restarts.
- `sudo` is used as in csqtt-cli: the files and the keyring belong to the calling user.

## Children

1. [Netstack TCP](01-netstack-tcp/plan.md) — the gVisor dependency and TCP from the
   adapter through the exit. Task; detailed.
2. [DNS over TCP, other UDP and IPv6](02-dns-udp-ipv6/plan.md) — DNS :53 through the
   exit, an instant ICMP refusal for other UDP, no IPv6. Task.
3. [TUN adapter, routes and system DNS](03-tun-adapter/plan.md) — `/dev/net/tun`, the
   one-adapter lock, the `/1` routes, the system DNS and the persistent lifecycle. Task.
4. [Connect wiring, core bypass and network changes](04-connect-wiring/plan.md) —
   `connect -datapath tun`, `netguard` on the core's sockets, `rp_filter`, and
   `netguard.Watch` → `Restart`. Task.
5. [DNS protection and kill-switch](05-protection/plan.md) — the nftables guard in the
   connect flow, the IPv6 block, `-no-kill-switch`, `-allow-leak`. Task.
6. [Live acceptance](06-live-acceptance/plan.md) — the user-run Linux matrix. Stage.

The netstack comes first: it is the new module dependency and the riskiest part. The UDP
and DNS behaviour follows on the same stack. The adapter is the first root-only piece; it
is tested at the datapath level with a fake exit before `connect` is wired to it. The
bypass child makes a real session carry system traffic, and protection comes last in code
so that its flags are settled on a working datapath. The live acceptance is the user's: it
changes the machine's real network and needs the user's server and profile; agents do not
run it.
