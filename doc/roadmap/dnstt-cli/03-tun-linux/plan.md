---
kind: stage
status: done
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

## Outcome

All seven `Done when` lines hold on the final tree. Two of them rest on gaps the user
accepted during the live matrix: the network change (P5) has no live run, and the live
IPv6/Happy-Eyeballs part of the browser branch has none either. Everything else was seen on
the real Linux machine, or in the isolated container the user chose as the venue.

- **`/dev/net/tun` → gVisor netstack → exit.** `internal/datapath/tun_linux.go` opens the
  adapter with `IFF_TUN_EXCL` and hands its fd to a `fdbased` endpoint; `netstack.go` turns
  every TCP connection into a stream through `exit.Dialer` at the original destination. Two
  stacks joined by channel endpoints cover both directions, four concurrent connections, the
  original destination, a refused dial resetting the client and no goroutine left after
  `close`; the netns suite repeats the TCP path through the real adapter and a fake exit.
  Live P1 carried `curl` through the exit to 200, and the browser branch answered 65 of 65
  CONNECTs.
- **Other UDP.** No default UDP handler is registered, so gVisor's IPv4 layer itself answers
  an unbound datagram with ICMP type 3 code 3 for the original packet
  (`TestNetstackICMPPortUnreachable`). Live P4 refused a connected public UDP send at once,
  and the browser's IPv4 QUIC fell back to TCP.
- **DNS.** `internal/datapath/dns.go` makes one DNS-over-TCP exchange to `tun_dns:53`
  through the exit, checks the length prefix, the ID and the QR bit and answers SERVFAIL on
  every failure; the netns test gets an A record from the adapter's `:53`. Live: the system
  DNS method was `resolvconf`, the system resolver became the adapter's address, and names
  resolved through the tunnel (P1, P4).
- **Routes, protection and the IPv6 block without `ip` and `nft`; lifecycle.** netlink
  configures the point-to-point `/32` pair, the two `/1` routes and the system DNS; nftables
  holds the `inet dnstt-cli` table with the owner flag and the IPv6 block. The block's effect
  and the local IPv6/ND/DHCPv6 exceptions are proven by the netns suite with IPv6 switched
  on; the live machine has no global IPv6, so the live effect was not observed. The adapter
  is not persistent, so `kill -9` left no adapter, route, table or lock (P2); a clean stop
  restored the system DNS (P1). A DNS record left by `kill -9` is removed right after the
  reservation, before the first session, with a line visible under `-v` (P2, fixed by
  `af23d36`).
- **Kill-switch.** `internal/app/protection.go` installs the "closed" set before the first
  dial and flips between down and up on every session; the locked state, `-no-kill-switch`,
  `kill_switch = false`, loopback and the local network without port 53 behaved as written
  (P3, the container for the exceptions), and the IPv6/DHCPv6/ND exceptions and the outward
  block are proven by the netns suite. `-allow-leak` was covered in the container — the
  refusal, the warning and the empty rule set — because the machine has no `nft`; a working
  tunnel under `-allow-leak` was not exercised.
- **Network change — accepted gap.** `netguard.Watch` → `supervisor.Restart` exists and its
  unit tests cover a change, an empty choice and an own-adapter event, but no live run was
  made: no second network was available and changing the link would cut the session. The user
  accepted the gap.
- **`sudo`.** The files and the keyring belong to the calling user (`internal/sudo`,
  `internal/config`, `internal/secretservice`). The matrix found the hidden helper was never
  dispatched; `ebdd65e` wired it before flag parsing, and the live re-check read a
  `secretservice:` reference under `sudo` and brought a session up (P0).

The live IPv6/Happy-Eyeballs part of the browser branch is the second accepted gap: the
container is IPv4 only, and the IPv4 QUIC fallback is covered.

The stage's code is in `f2c4fa9` … `ea6edd1` (children 1–5), the runbook in `4005c04`, the
two fixes the matrix found in `af23d36` and `ebdd65e`, the latency diagnosis in `08489d5`
(closed without a fix: the cost is the tunnel's own capacity), the browser container in
`d427b4b`, and the matrix record and acceptance in `7163c91`, `ae63a91` and `2c028ea`. The
acceptance itself found and fixed two defects in `internal/datapath`: the netstack now closes
its exit streams with the stack, so a relay stuck in a write into a stalled exit cannot hold
`Tun.Close`, and `NewTun` normalizes a nil `Logf` that the platform opener called unguarded.
The evidence is in `doc/runs/tun-matrix/` and `doc/runs/browser-container/`, the filled
checklist in `doc/local/runbook-tun-linux.md`; all outside git, and no value is in a tracked
file.

Accepted on the final tree: `gofmt` outside `core/` (the three upstream `core/` files stay as
they are), the CI vet split on Linux and for Windows, the Linux build, the Windows
cross-build with the compilation of every test package, `git diff --check`, the links in the
changed `.md`, the full `go test -race -count=1 ./...` with the keyring/D-Bus tests enabled,
`DNSTT_CLI_TEST_NETNS=0` and the four stated netguard exclusions by `-skip`, and the netns
suites in the already-present `debian:trixie` container (`linuxsys.test` 72 passed,
`datapath.test` 94 passed, no skips). The acceptance made no live call, pulled no image and
changed no host route or DNS.

Recorded as limits, not defects: the DNS listener's gate of 64 exchanges in flight (the 65th
gets an immediate SERVFAIL; the browser's fan-out peaked at four), and the tunnel's shared
channel as a reference rather than a ceiling (about 58 KB/s in the diagnosis, about 52 KiB/s
at 65 concurrent fetches and about 100 KiB/s for the quiet heavy page; changing the payload
size, MTU, resolver or KCP is a user decision). One force-stop limit stays: the second Ctrl+C
now closes the exit streams with the datapath, so a relay stuck in a write no longer holds
it, but closing an smux stream can still wait for its FIN through a stalled session (up to
the 30-second open/close timeout, per stream); a session that stalled that badly is ended by
the third Ctrl+C. Open product question: the `kill -9` recovery line is verbose-only, and
making it a notice would change the logging convention the recovery task fixed.

No other defect found in this stage was left unfixed.
