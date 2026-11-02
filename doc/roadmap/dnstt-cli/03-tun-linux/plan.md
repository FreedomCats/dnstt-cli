---
kind: stage
status: todo
---

# System VPN on Linux

Goal (`architecture.md` §12, E2): `tun` mode sends all system traffic through the tunnel on
Linux, and nothing leaks while the tunnel is down. `architecture.md` R6, R7, §05, §05a, §09.

The stage has no children yet. It is detailed when its turn comes: the nodes below are the
outline from `architecture.md`, to be turned into stages and tasks by the planner.

## Done when

- `/dev/net/tun` → gVisor netstack in the shell → exit carries all TCP traffic.
- UDP other than DNS :53 from the adapter is rejected at once with ICMP "port
  unreachable", so QUIC falls back to TCP without a timeout.
- DNS from the adapter goes as DNS-over-TCP through the exit to the tunnel's DNS; the
  system gets the adapter's DNS address.
- Routes, system DNS, DNS protection and the IPv6 block are set through nftables and
  netlink without `ip` and `nft`, and disappear with the process even after `kill -9`.
- The kill-switch blocks system traffic between sessions, with the exceptions of §05a
  (dnstt-cli itself, loopback, DHCP, ND/RA, the local network without port 53).
- A network change is noticed and the session restarts.
- `sudo` is used as in csqtt-cli: the files and the keyring belong to the calling user.

## Outline

1. Netstack and the adapter: `tun*`, `netstack` in `internal/datapath`; every TCP connection
   a stream through the exit.
2. DNS: UDP :53 from the adapter as a DNS-over-TCP query through the exit; the system's
   DNS set to the adapter.
3. UDP and IPv6: fast rejection, the outward IPv6 block.
4. Routes and `rp_filter`.
5. DNS protection and the kill-switch: the nftables table `inet dnstt-cli` with the `owner`
   flag and the mark of our own sockets.
6. Network tracking: `netguard.Watch` → `supervisor.Restart`.
7. `sudo` and privileges: the same connection re-run elevated, files and keyring of the
   calling user.
8. Live acceptance on Linux.
