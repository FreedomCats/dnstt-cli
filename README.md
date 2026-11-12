# dnstt-cli

A desktop console client for [dnstt](https://www.bamsoftware.com/software/dnstt/): a local
SOCKS5 proxy or a system-wide VPN through a DNS tunnel, with reconnection, a list of
resolvers, profiles and secret storage. One Go binary for Linux and Windows (macOS — the
proxy mode only), the dnstt client core built in.

> **In development.** Nothing works yet: the repository is at its foundation stage. What
> is planned and in which order — [`doc/architecture.md`](doc/architecture.md) and
> [`doc/roadmap/`](doc/roadmap/README.md).

This is only a client. The server side stays with dnstt itself: `dnstt-server` and
whatever its owner runs behind it (a SOCKS5 proxy with a login is the main case).

## What leaks and what does not

`tun` is the protected mode: all system TCP goes through the exit, and the ISP sees the
server, not the sites. What can still be seen:

- **DNS** — the system resolver is the tunnel's own address; names go as DNS over TCP to
  `tun_dns` behind the server (`1.1.1.1` by default) and not to the ISP. A browser's own
  DNS-over-HTTPS uses port 443, follows the routes into the tunnel and does not leak either.
- **Other UDP** — refused at once with ICMP "port unreachable", so QUIC falls back to TCP;
  it does not pass the tunnel. Port 53 elsewhere is rejected even on the local network.
- **IPv6** — closed outward while the tunnel is up; the local IPv6 network keeps working and
  applications fall back to IPv4.
- **While the tunnel is down** — the kill-switch is on by default: system traffic is closed
  from the start of `connect` and during a reconnection. The exceptions are dnstt-cli itself,
  loopback, DHCP/ND/RA and the local network (without port 53). `-no-kill-switch` and
  `kill_switch = false` let traffic pass while the tunnel is down; `-allow-leak` connects
  even if the protection could not be set.
- **After `kill -9`** — the adapter, routes and nftables table disappear with the process; a
  system-DNS record left behind is removed by the next `tun` start, with a message under `-v`.
  Ctrl+C removes everything at once.

`socks` mode has no such protection: only what an application sends to the local proxy goes
through the tunnel. If the application resolves names itself, its DNS goes to the ISP; passing
the name to the proxy (`socks5h`, "proxy DNS") keeps it inside, and WebRTC can still reveal
the real address over UDP. Use `tun` when that matters.

## Credits

The tunnel core is dnstt by David Fifield, public domain (CC0), carried into `core/` from
the [FreedomCats mirror](https://github.com/FreedomCats/dnstt) of the upstream. The shell —
profiles, secrets, routes, DNS protection, kill-switch, network tracking — is based on
[csqtt-cli](https://github.com/FreedomCats/csqtt-cli) (MIT).

## Building

Go 1.26 or newer:

    go build ./cmd/dnstt-cli

## License

MIT — see [`LICENSE`](LICENSE). You may use, modify and redistribute the program, also in
a closed-source product, as long as the copyright notice and the permission notice are kept.
The program comes with no warranty.

The tunnel core in `core/` is dnstt, public domain (CC0); the licenses of the Go
dependencies are listed in `THIRD_PARTY_NOTICES.txt` of the release archives.
