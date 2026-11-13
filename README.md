# dnstt-cli

A desktop console client for [dnstt](https://www.bamsoftware.com/software/dnstt/): a local
SOCKS5 proxy or a system-wide VPN through a DNS tunnel, with reconnection, a list of
resolvers, profiles and secret storage. One Go binary for Linux and Windows (macOS — the
proxy mode only), the dnstt client core built in.

This is only a client. The server side stays with dnstt itself: `dnstt-server` and
whatever its owner runs behind it (a SOCKS5 proxy with a login is the main case).

## What it does

`dnstt-cli` without arguments in a terminal opens the menu: connect; check the resolvers
and drop the dead ones; edit them; a new exit password or key; change the pinned
fingerprint; change the domain or the key; another profile; delete a profile. With no
profile yet the wizard runs: paste a `slipnet://` link, or answer the questions — name,
tunnel domain, server key, exit type (`socks5`, `http` or `ssh`), the exit login and its
password or, for `ssh`, the authentication method with the password or the path of a
private key and its passphrase, the pinned fingerprint of the SSH server, resolvers, and
the mode: all traffic (`tun`) or only the programs with a proxy (`socks`).

The exit behind the dnstt server is one of three:

- `socks5` — RFC 1928 with an optional login; the main case.
- `http` — HTTP/1.1 `CONNECT` with Basic authentication.
- `ssh` — one SSH connection inside the tunnel, a `direct-tcpip` channel per application
  connection, password or private-key authentication, and the server's host key pinned by
  fingerprint in the profile. The server's `sshd` must allow TCP forwarding.

The same actions are commands:

    dnstt-cli connect [profile]     connect to the dnstt server
    dnstt-cli menu                  the interactive menu
    dnstt-cli profile add|set|show|rm|list
    dnstt-cli profile import        import a profile from a slipnet:// link
    dnstt-cli resolvers check [profile]
    dnstt-cli version
    dnstt-cli help [command]

`profile set` is still a placeholder: the menu and the wizard (`profile add`) are the ways
to change a profile.

## How it differs from dnstt's own client

The tunnel is the same: the dnstt core is vendored in `core/`, the protocol is Noise over
KCP in DNS queries, and the client works with a stock `dnstt-server`. What differs is the
shell around the tunnel.

| | `dnstt-client` (upstream) | `dnstt-cli` |
|---|---|---|
| Local side | one TCP port forwarded to one fixed address, like `ssh -L` | a local SOCKS5 proxy, or `tun` with a TCP/IP stack, routes and system DNS |
| What the server reaches | one fixed TCP address | SOCKS5 with an optional login, HTTP `CONNECT` with Basic authentication, or SSH `direct-tcpip` with a password or a key and a pinned host key |
| Resolvers | one per run: `-doh`, `-dot` or `-udp` | a list per profile over DoH, DoT, plain TCP and UDP, with failover and `resolvers check` |
| Reconnection | none: a dead session needs a restart | a supervisor that redials with backoff, rotates resolvers and follows a network change in `tun` |
| Settings | command-line flags | `profiles.toml`, a `slipnet://` import, the wizard and the menu; secrets in the system store |
| Leak protection | none | `tun`: kill-switch, DNS protection, IPv6 block, `-allow-leak` |
| Output | logs | the same logs, plus a status line while connected |

Plain UDP is a testing mode in both, as upstream's README says: it does not hide the tunnel.

`connect` runs until it is stopped: the first Ctrl+C stops cleanly, the second does not
wait for open connections. `-datapath socks` is a local SOCKS5 proxy (`127.0.0.1:1080`
unless the profile says otherwise); `-datapath tun` sends all system traffic through the
tunnel and needs root on Linux. `menu -connect <profile>` makes one connection at once and
returns, without showing the menu.

Profiles live in `profiles.toml`, in `~/.config/dnstt-cli/` on Linux and
`%APPDATA%\dnstt-cli\` on Windows. The exit password and an SSH private key do not live
there: they go to the system secret store (the keyring on Linux, DPAPI on Windows), and the
profile keeps only a reference to them. [`dnstt-cli.example.toml`](dnstt-cli.example.toml)
is the same file with every field described.

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

## Platforms

| System | `socks` | `tun` | State |
|---|---|---|---|
| Linux, x86-64 | yes | yes | checked live |
| Windows, x86-64 | yes | not yet | built and tested in CI, not run |
| macOS | yes | no | the code builds; not checked live, no archive |

The SSH exit was exercised through a real tunnel against an `sshd` in a disposable
container; the real server was exercised through its HTTP `CONNECT` exit. The Windows build
is cross-compiled on Linux and its tests are compiled, but nothing was run on Windows.

`tun` on Windows is the stage
[`04-tun-windows`](doc/roadmap/dnstt-cli/04-tun-windows/plan.md), which is blocked for now:
`connect -datapath tun` refuses before dialling and asks for `-datapath socks`. The Windows
archive already carries `wintun.dll` and its license, which that stage will use.

Releases are built for `linux/amd64` and `windows/amd64`, both with `CGO_ENABLED=0`; more
architectures are a later decision.

## Releases

Each release has two archives:

    dnstt-cli_<version>_linux_amd64.tar.gz
    dnstt-cli_<version>_windows_amd64.zip

Download them from the [releases page](https://github.com/FreedomCats/dnstt-cli/releases).

Both hold the binary (`dnstt-cli`, `dnstt-cli.exe`), `README.md`, `LICENSE`, `COPYING` (the
license of the dnstt core), `THIRD_PARTY_NOTICES.txt` and `dnstt-cli.example.toml`. The
Windows archive also has `wintun.dll` and `wintun-LICENSE.txt`: keep `wintun.dll` next to
`dnstt-cli.exe`, or the `tun` adapter will not find it. In the tar.gz the binary is already
executable after `tar xzf`.

`SHA256SUMS.txt` in the release holds the SHA-256 of the archives:

    sha256sum -c SHA256SUMS.txt

## Building

Go 1.26 or newer:

    go build ./cmd/dnstt-cli

`go run ./scripts/release -version vX.Y.Z` builds both archives into `dist/`; it downloads
`wintun.dll` for the Windows one, or takes a copy already downloaded with `-wintun-archive`.
`THIRD_PARTY_NOTICES.txt` is generated by `go run ./scripts/notices -dest .` and committed:
the release archives carry the committed file.

## Credits

The tunnel core is dnstt by David Fifield, public domain (CC0), carried into `core/` from
the [FreedomCats mirror](https://github.com/FreedomCats/dnstt) of the upstream. The shell —
profiles, secrets, routes, DNS protection, kill-switch, network tracking — is based on
[csqtt-cli](https://github.com/FreedomCats/csqtt-cli) (MIT).

## License

MIT — see [`LICENSE`](LICENSE). You may use, modify and redistribute the program, also in
a closed-source product, as long as the copyright notice and the permission notice are kept.
The program comes with no warranty.

The tunnel core in `core/` is dnstt, public domain (CC0); its text is `COPYING` next to the
binary (`core/COPYING` in the repository).

The licenses of the Go dependencies are in
[`THIRD_PARTY_NOTICES.txt`](THIRD_PARTY_NOTICES.txt), which is in the release archives too.
The license of `wintun.dll` is not there: it is `wintun-LICENSE.txt`, next to the DLL in
the Windows archive.
