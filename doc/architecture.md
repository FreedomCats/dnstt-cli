# dnstt-cli architecture

A desktop **console** client for [dnstt](https://www.bamsoftware.com/software/dnstt/): a
local SOCKS5 proxy or a system-wide VPN through a DNS tunnel. One Go binary, the dnstt
client core built in.

This is only a client. The server side stays with dnstt itself (`dnstt-server` plus
whatever the owner runs behind it). There is no service and no autostart: the client runs
in the foreground while `dnstt-cli connect` runs.

- **Core:** the dnstt client from the [FreedomCats mirror](https://github.com/FreedomCats/dnstt)
  of the upstream, version `v1.20260501.0`, public domain (CC0)
- **Own code:** license not chosen yet; MIT recommended (`analysis.md`, §6; §11 below)
- **Shell base:** [csqtt-cli](https://github.com/FreedomCats/csqtt-cli) (MIT) — profiles,
  secrets, menu, routes, DNS, kill-switch, network tracking, release scripts
- **Platform priority:** Linux → Windows. macOS — `socks` only
- **Status:** draft for discussion. Facts and differences from qwdtt-cli and csqtt-cli —
  `analysis.md`

---

## 01. Foundations

Checked by reading the upstream code, not taken from descriptions.

| Fact | Where in the core | Consequence |
|---|---|---|
| The core is Go and public domain | the whole upstream | It is built into the shell's binary; any license fits |
| The client carries TCP streams (smux), not IP packets | `dnstt-client/main.go` (`run`, `handle`) | `tun` needs its own TCP stack (gVisor netstack), and every TCP connection becomes a stream |
| The server hands every stream to one fixed TCP address | `dnstt-server/main.go` (`handleStream`) | The destination address is told to the server's exit: SOCKS5 or SSH (R3) |
| KCP, Noise and smux are created once per process, nothing recreates them | `dnstt-client/main.go` (`run`) | Session lifetime is our code (R2); upstream `run` and `main` are not used |
| Noise_NK: the handshake needs one answer from the server | `noise/noise.go` | A finished handshake proves that the resolver passes the tunnel and that the domain and key are right |
| On the server smux sends keepalives every 10 s (library default) and closes the session after 2 min of silence | `dnstt-server/main.go` | The client can notice a dead session much sooner than its 2 min (R2) |
| All outgoing sockets of the core come from `net.Dialer`, `tls.Dialer`, `http.Transport` and `net.ListenUDP` | `main.go`, `utls.go` | Bypassing the tunnel is one `Control` hook (R4, §06) |
| DoT gives up for good after one failed redial | `dnstt-client/tls.go` | Core patch P2 |
| UDP mode sends every query from one socket | `dnstt-client/main.go` | Core patch P3 |
| The resolver's host name is resolved by the system | `main.go` (`ResolveUDPAddr`), `http.Transport`, `utls.go` | Own bootstrap resolution through bound sockets (R5) |
| uTLS `v1.6.6` | `go.mod` | Update the dependency; P5 if its API changed |

---

## 02. Decisions

### R1. The core is vendored into `core/` and built into the binary

The upstream packages `dns`, `noise`, `turbotunnel` and `dnstt-client` are carried from
the mirror into `core/` by `go run ./scripts/vendorcore`, as in qwdtt-cli and csqtt-cli:
every patch is a rule with a pattern and a mandatory match count, `vendorcore -check`
compares `core/` with the upstream and the rules, the upstream version is in
`core/CORE_VERSION`, the upstream `COPYING` goes along. Import paths are rewritten to the
module of dnstt-cli; `dnstt-client` stops being `package main`.

Upstream files stay as close to verbatim as possible. New code lives in separate files of
`core/` that `vendorcore` puts in from `scripts/vendorcore/_files/` (the csqtt-cli layout;
the underscore keeps `go build ./...` from compiling them outside `core/`): the session API
(§04), the transports with the socket hook, the in-process test server and, later, the
bootstrap resolver. `go vet` runs on `core/` with `-testinggoroutine=false`: two upstream
`noise` tests call `t.Fatal` from a goroutine. `core/` is never run through `gofmt`: the
rewritten import paths change the order of imports.

**Why.** The core is small and in Go, so a process boundary gives nothing and costs a
protocol. In-process the core's sockets can be bound to the physical interface without a
single upstream patch at the socket points (R4). Rules keep our changes visible apart from
upstream code, so an upstream update is not a manual merge.

### R2. One session at a time, owned by the supervisor

Upstream `run` is replaced by `core.Dial`, which builds the chain for one resolver:
transport → `DNSPacketConn` → KCP → Noise → smux, and returns a `Session` after the Noise
handshake. The supervisor holds one session, opens streams in it and recreates it when it
dies, when the network changes and when it switches resolvers.

A dead session is noticed by the smux keepalive with a shorter timeout on the client side
(about 30 s against the server's 10 s keepalives; the number is set by a live run, E1).
Connections open in a dead session break; new ones wait for the next session — the `socks`
listener and the `tun` adapter live the whole `connect`.

**Why.** Without it the upstream client stops working until a manual restart after any
long break (`analysis.md`, §1).

### R3. The server's exit: SOCKS5 first, SSH second

Every stream starts with a handshake to whatever the server hands it to:

- **`socks5`** — RFC 1928 `CONNECT` with a domain or an address; authentication "none" or
  username/password (RFC 1929). The priority case: a server with `danted` and a login.
- **`ssh`** — one SSH connection inside one stream, `direct-tcpip` channels for
  connections, password or key authentication, the server host key pinned in the profile.
  A second stage.

The shell's own `socks` listener never passes app traffic to the exit as is: it parses the
app's SOCKS5 request and opens the exit itself. So apps do not need the exit's password,
the same local proxy works with either exit, and a failed exit login is one clear error,
not an error in every app.

`UDP ASSOCIATE` is not carried: the relay needs UDP straight to the server, and the tunnel
has only streams.

### R4. The core's own sockets bypass the tunnel through one hook

In `tun` all system traffic goes into the adapter, and the core's queries to the resolver
would go into the tunnel too. `netguard` (from csqtt-cli) chooses the physical interface
and gives a `Control` function; the core puts it on every socket it creates (§06):

- **Linux** — `SO_BINDTOIFINDEX` and `SO_MARK`; DNS protection and kill-switch let marked
  sockets through;
- **Windows** — `IP_UNICAST_IF`/`IPV6_UNICAST_IF`; WFP lets `dnstt-cli.exe` through by
  app ID — one program, unlike csqtt-cli.

In UDP mode the core itself sends queries to port 53 — without the mark DNS protection
would kill the tunnel.

### R5. Resolvers: a list, DoH by default, no system DNS

A profile has a list of resolvers, each `doh:URL`, `dot:host:port`, `udp:host:port` or
`tcp:host:port` (plain DNS over TCP, P6). DoH is the default for new profiles and for the
built-in presets; all four are supported. The list is tried in order; a resolver that fails the handshake or kills
sessions repeatedly is put aside for a while, and the session moves to the next one.

A host name in a resolver is resolved without the system: a `@address` suffix pins the
address (`doh:https://doh.example/dns-query@192.0.2.1`, like `curl --resolve`), and
without it the name is resolved through a bootstrap resolver over bound sockets. Built-in
presets carry addresses, so they work under a closed kill-switch.

`dnstt-cli resolvers check [profile]` performs a real Noise handshake through every
resolver of the profile and prints the time or the reason; exit code 1 if none passes.

### R6. Datapaths: `socks` and `tun`

| Datapath | How traffic gets in | Privileges | Scope | Stage |
|---|---|---|---|---|
| `socks` | own SOCKS5 on `127.0.0.1:1080`, connections → exit | none | programs with a proxy setting | E1 |
| `tun` (Linux) | `/dev/net/tun` → gVisor netstack in the shell → exit | root | all system traffic | E2 |
| `tun` (Windows) | Wintun → gVisor netstack in the shell → exit | administrator | all system traffic | E3 |

`external` from qwdtt-cli does not exist: WireGuard plays no part.

### R7. DNS, UDP and IPv6 in `tun`

- **DNS.** UDP :53 from the adapter goes as a DNS-over-TCP query through the exit to the
  tunnel's DNS (`tun_dns` in the profile, default a public resolver reachable from the
  server). The system gets the adapter's DNS address; DNS protection opens port 53 only to
  it (§05).
- **Other UDP** is rejected at once with ICMP "port unreachable", so QUIC falls back to TCP
  without a timeout.
- **IPv6** is closed outward as in csqtt-cli: routes and DNS protection let only the local
  network and service protocols through. Carrying TCP to IPv6 addresses through an exit
  that supports them is an option for later.
- **MTU.** TCP ends in the shell, so the adapter's MTU does not depend on the DNS channel:
  1500.

### R8. Profiles in TOML, secrets in DPAPI and the keyring

As in csqtt-cli: `profiles.toml` holds no secrets. Exit passwords and SSH keys go to DPAPI
on Windows and the Secret Service keyring on Linux, without a keyring — to `secrets.json`
with mode `0600`. The domain and the server key are not secrets but are sensitive: they
stay in the profile and are never printed in logs in full.

### R9. Profile links: import of `slipnet://`

Upstream dnstt has no link format. The de-facto format among DNS tunnel clients is
`slipnet://`: the SlipGate server manager prints it for every user and tunnel, and the
SlipNet Android client imports it. Both are AGPL-3, so the format was described from their
code by a separate participant and the parser is written anew from that description
(`AGENTS.md`). dnstt-cli imports `dnstt` and `dnstt_ssh` profiles (§10). NoizDNS profiles
(`sayedns`) are refused with a clear message: NoizDNS changes the query encoding, and the
upstream dnstt core does not speak it. `slipnet-enc://` is refused too: its key is built
into the SlipNet app.

---

## 03. Module layout

```
dnstt-cli/
├── cmd/dnstt-cli/             // entry point
├── core/                      // vendored dnstt · CC0 · R1
│   ├── CORE_VERSION           //   upstream version and source
│   ├── COPYING                //   from the upstream
│   ├── dns/ · noise/ · turbotunnel/
│   ├── client/                //   dnstt-client as a package + session API (§04)
│   └── server/                //   dnstt-server as a package, used only by tests
├── internal/
│   ├── cli/                   // connect · resolvers · profile · menu · wizard · privileges
│   ├── config/                // profiles · validation · secrets · links
│   ├── exit/                  // NEW · SOCKS5 client (RFC 1928/1929) · SSH (E5)
│   ├── datapath/
│   │   ├── datapath.go        // Requirements · Persistent · Reserver · DNSGuard
│   │   ├── socks.go           // SOCKS5 server on loopback → exit
│   │   ├── socks5.go          //   SOCKS5 server protocol
│   │   ├── netstack.go        //   gVisor netstack: TCP → exit, DNS → exit, UDP reject
│   │   ├── dns.go             //   NEW · DNS over TCP through the exit
│   │   ├── tun.go             // adapter per session: address · routes · Suspend · Close
│   │   ├── tun_linux.go       //   /dev/net/tun · second-instance lock
│   │   └── tun_windows.go     //   Wintun
│   ├── resolvers/             // NEW · list · presets · health · bootstrap names
│   ├── lines/                 // shared stdin line reader
│   ├── netguard/              // physical interface · network tracking · Control hook
│   ├── supervisor/            // state machine · reconnection · shutdown
│   ├── winsys/                // Windows: adapter · routes · DNS · WFP
│   ├── linuxsys/              // Linux: adapter · routes · DNS · nftables · rp_filter
│   ├── sudo/                  // Linux: who called sudo
│   └── secretservice/         // Linux: keyring
├── scripts/
│   ├── vendorcore/            // carrying the core over by rules (R1) · socket guard (§06)
│   │   └── _files/            //   own files of core/
│   ├── wintun/                // wintun.dll with hash and signature check
│   ├── release/ · notices/    // release archives · dependency licenses
│   └── checktunnel/           // system snapshot before and after a run
├── dnstt-cli.example.toml
└── doc/
```

Packages without the "NEW" mark come from csqtt-cli and are reworked for dnstt.

---

## 04. Core API

```go
package client // core/client, new files next to the upstream ones

type Config struct {
    Domain           dns.Name
    PubKey           []byte
    Resolver         Resolver // {Transport: DoH | DoT | UDP, Address: URL or host:port}
    UTLS             string   // upstream syntax of -utls; "" — DefaultUTLS, "none" — Go TLS
    Control          func(network, address string, c syscall.RawConn) error // R4
    LookupIP         func(ctx context.Context, host string) ([]netip.Addr, error) // R5
    KeepAliveTimeout time.Duration // client smux timeout; 0 — upstream's 2 min
    Logf             func(format string, args ...any)
}

// Dial builds transport → DNSPacketConn → KCP → Noise → smux with the settings of
// upstream run and returns after the Noise handshake, or with an error when ctx ends first.
func Dial(ctx context.Context, cfg Config) (*Session, error)

type Session struct{ /* … */ }

func (s *Session) OpenStream() (net.Conn, error)
func (s *Session) Done() <-chan struct{} // closed when the session ended and its sockets closed
func (s *Session) Err() error            // ErrClosed (Close) or ErrSilent (keepalive timeout)
func (s *Session) Stats() Stats          // open streams, bytes of the Noise channel, KCP RTT
func (s *Session) Close() error
```

- Every socket of a session is made by it (`sockets` in `transport.go`) and closed with it;
  the dials in progress end with it too. A closed session leaves no goroutines: upstream
  keeps its transport loops forever (one session per process), so they end on close by
  rules (§07, P1).
- DoH takes only `https://` URLs: an `http://` one would go to `http.DefaultTransport`
  past the hook.
- A host name of a resolver goes through `LookupIP`; without it the system resolver
  answers until P4.
- The smux keepalive interval is the library's 10 s, or half the timeout if that is
  shorter. smux checks the timeout once per its period, so a dead session ends between one
  and two timeouts after the last data.

The exit sits on top: `exit.Dialer` turns a stream into a connection to `host:port`
(SOCKS5 `CONNECT`, SSH `direct-tcpip`). Datapaths see only `exit.Dialer`.

`core/server/testing.go` starts the upstream server `run` in-process on loopback UDP with a
generated key; the session tests run against it.

---

## 05. Datapath

**`socks`.** Listens on loopback only (default `127.0.0.1:1080`): there is no password on
the local side, otherwise the tunnel would be open to the whole network. A `CONNECT` with a
domain goes to the exit with the domain, so names are resolved on the server — there is no
local DNS at all in this mode. A request the tunnel cannot serve (`BIND`, `UDP
ASSOCIATE`, no session) gets a refusal at once. The listener opens before the first
session, so a busy port is found out before connecting.

**`tun`, common.** Routes `0.0.0.0/1` and `128.0.0.0/1` into the adapter, the original
gateway untouched. The adapter has a private `/32` address chosen by the shell and MTU
1500. Packets go to gVisor netstack in the shell: TCP → `exit.Dialer` to the original
destination, UDP :53 → DNS over TCP (R7), other UDP and IPv6 — rejected. DNS protection
and kill-switch are the csqtt-cli ones (`linuxsys.DNSGuard`, `winsys` WFP filters) with the
adapter's DNS address as the only allowed resolver; the core's sockets pass by mark or app
ID (R4). Between sessions the adapter stays, and with the kill-switch the system traffic is
closed until the next session is up.

**`tun` on Linux.** `/dev/net/tun` with `IFF_TUN | IFF_NO_PI`, the descriptor read by
netstack in the shell (no handover to a core process). Second instance — refused by an
abstract socket. System DNS — the csqtt-cli three ways (systemd-resolved, resolvconf,
`resolv.conf`), leftovers of a killed run recovered on the next start. Strict `rp_filter`
on the physical interface — refusal with a hint, as in csqtt-cli.

**`tun` on Windows.** Wintun through `golang.zx2c4.com/wireguard/tun` with a fixed GUID,
`wintun.dll` next to the exe, the rest as in csqtt-cli §05 and §09a.

---

## 05a. Leak protection and kill-switch

What qwdtt-cli and csqtt-cli do, carried over as is, and what dnstt changes. Applies to
`tun`; `socks` has no protection (below).

**DNS protection — while the tunnel is up.** Port 53, UDP and TCP, is open only to the
adapter's DNS address, loopback and dnstt-cli's own sockets; everything else on port 53 is
rejected. Without it Windows would send queries to the resolvers of every adapter, and
names of visited sites would reach the ISP.

- Linux — nftables table `inet dnstt-cli` with the `owner` flag, chains for outgoing and
  forwarded traffic (containers, VMs); own sockets pass by `SO_MARK`, not by user — under
  `sudo` "root may do anything" would let every root program through.
- Windows — WFP filters in a dynamic session; own process passes by app ID.
- Both disappear with the process, even a killed one. If they cannot be set, `connect`
  refuses before connecting.
- Browser DoH goes to port 443 and is not touched, but it follows the routes into the
  tunnel — it does not leak either.

**IPv6 — always closed outward in `tun`.** The tunnel does not carry it (R7). Linux: the
same table rejects IPv6 except loopback, own sockets, the local network (`fe80::/10`,
`fc00::/7`, `ff00::/8`), ND, RA and DHCPv6. Windows: `::/1` and `8000::/1` lead into the
adapter, and netstack rejects the packets; if IPv6 is off on the adapter, WFP closes it as
on Linux. Apps fall back to IPv4.

**Kill-switch — while the tunnel is not up.** On by default. From the start of `connect`
until the first session, and during every reconnection, system traffic is closed. Allowed
while closed:

- dnstt-cli itself — it has to reach the resolvers;
- loopback;
- DHCP (IPv4 and IPv6), IPv6 neighbour discovery and router advertisements;
- the local network: `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `169.254.0.0/16`,
  `fe80::/10`, `fc00::/7`, multicast and broadcast — router, printer and NAS stay
  reachable.

Port 53 is closed even in the local network: the router's resolver would pass queries to
the ISP. Everything else is rejected: UDP at once, TCP by timeout. On Linux forwarded
traffic is closed too. When the session is up, the kill-switch blocks nothing — only DNS
protection and the IPv6 block work.

Differences from qwdtt-cli and csqtt-cli:

- **No exceptions mid-session.** There is no captcha, so the kill-switch is never lifted
  for a browser window.
- **Resolvers under a closed kill-switch.** The core's queries go through bound and marked
  sockets (R4); resolver names come from pinned addresses or the bootstrap resolver over
  the same sockets (R5), never from the closed system DNS.

Output lines, as in the other two clients: "kill-switch: system traffic is blocked while
the tunnel is down", "tunnel reconnecting — system traffic is blocked (kill-switch)",
"tunnel up again — system traffic goes through it".

**Turning it off.** For one run — `-no-kill-switch`; for good — `kill_switch = false` in the
profile. Then between sessions the routes and DNS protection are removed and traffic goes
past the tunnel, with lines saying so. `-allow-leak` (one run only, never in the profile)
connects even if DNS protection or the kill-switch could not be set.

**If dnstt-cli crashes or is killed.** The first Ctrl+C cleans up everything. After `kill
-9` or a crash the adapter, routes, nftables table and WFP filters disappear with the
process: the machine is not left without network, but it is not protected either — a
permanent block surviving dnstt-cli is deliberately absent. What can remain is system DNS
on Linux (a `resolvconf` entry or a rewritten `/etc/resolv.conf` with a backup); the next
`connect` removes it and says so. On Windows a registry key with the old adapter's DNS
remains; without the adapter it affects nothing.

**`socks` has no protection.** Only what apps send to the proxy goes through the tunnel.
Names go to the server only if the app passes them to the proxy (`socks5h` in curl, "proxy
DNS" in browsers); otherwise DNS goes to the ISP. Browsers' WebRTC may reveal the real
address over UDP. README says this plainly and points to `tun` for full protection.

---

## 06. Tunnel bypass

Upstream `main` and `run` are not used; the session builds its sockets in `transport.go`
(added file) and hands its dial function to the upstream uTLS code (a rule).

| # | Where | What | Binding |
|---|---|---|---|
| 1 | `transport.go` · `sockets.listenUDP` | UDP-mode queries | `net.ListenConfig{Control}`; a socket per query with P3 |
| 2 | `utls.go` · `utlsDialContext` | TCP to DoH/DoT with uTLS | `dial` of the caller (rule) = `sockets.dial`: `net.Dialer{Control}` |
| 3 | `transport.go` · `http.Transport` | DoH without uTLS | `DialContext: sockets.dial`, no proxy |
| 4 | `transport.go` · DoT dialer | DoT without uTLS | `tls.Client` over `sockets.dial` |
| 5 | bootstrap resolver (new, E1) | names of resolvers | own UDP/TCP sockets with `Control` |

The HTTP/1.1 transport of uTLS DoH is a clone of `http.DefaultTransport`; a rule gives it
`sockets.dial` as `DialContext` and takes away its proxy from the environment, which would
otherwise carry the connection past the hook.

**Guard** (`scripts/vendorcore/sockets.go`), as `TestNetguardCoverage` in qwdtt-cli and the
socket guard in csqtt-cli: `vendorcore` parses every non-test Go file of `core/` and finds
each use of an API that creates a socket, resolves a name or builds an HTTP client (`net`,
`crypto/tls`, `net/http`, `http2`, `utls`, `kcp`, raw sockets), by the package it is
imported from. Each place — file, function, identifier, count — must be in a table with a
reason: a hooked point above, upstream `main`/`run` that is never called, or the server
used only by tests. `net.Dialer` and `net.ListenConfig` of the hook must set `Control`. A
new place after a core update fails the transfer and `vendorcore -check` instead of
looping traffic through the adapter.

**Network change** — `netguard.Watch` from csqtt-cli: a new default-route interface
restarts the session at once, without the backoff.

---

## 07. Core patches

All as `vendorcore` rules on top of the upstream (R1), each with a test.

| # | Patch | Why | Stage |
|---|---|---|---|
| P1 | `dnstt-client` as a package; session API and `Control` in new files; uTLS dials through its caller's function; transport loops end and TLS stops redialing when their conn is closed | R1, R2, R4 | E0 |
| P2 | DoT redial with a backoff instead of giving up | `analysis.md` §1, item 2 | E1 |
| P3 | UDP mode: a socket per query (or a small rotating pool) | `analysis.md` §1, item 3 | E1 |
| P4 | names of DoH/DoT resolvers through `LookupIP` instead of the system | R5 | E1 |
| P5 | newer uTLS and its fingerprints | `analysis.md` §1, item 5 | E1 |
| P6 | transport `tcp`: plain DNS over TCP to the resolver, length-prefixed like DoT | `slipnet://` profiles use it; some networks pass TCP :53 where UDP is blocked | E4 |
| P7 | cap on the payload per query (smaller KCP MTU) | the `slipnet://` payload-size field; resolvers that cut large answers | E4 |

The wire format is not touched: `dnstt-server` does not tell dnstt-cli from the upstream
client. P2–P4 are worth proposing upstream.

---

## 08. Supervisor

```
Idle ──▶ Dialing ──▶ Connected ◀──▶ Degraded
           ▲  (resolver r)   (handshake)   (no data from the server
           │                                for longer than expected)
           └──── Reconnecting ◀── session died · network changed · resolver put aside

Stopping ◀── Ctrl+C · exit refused the login · no resolver ever completed a handshake
             on the first connect (wrong domain or key, server down)
```

- **Dialing** — `core.Dial` with the next resolver of the list; the handshake has its own
  timeout (to be measured, E1). A timeout or error moves to the next resolver at once; a
  full round without success — backoff `1·2^n` s up to 64 s, as in csqtt-cli.
- **Connected** — a finished handshake. The first stream also checks the exit: a SOCKS5
  login refusal is final (`Stopping`) with a clear message; an exit that does not answer is
  a retry.
- **Server silence on the first connect** — if no resolver has ever completed a handshake
  in this `connect`, the reason cannot be told apart (wrong domain, wrong key, server down,
  every resolver blocked): `connect` stops and names all four. After one success, silence
  is a retry.
- **Restart** — `Restart(reason)` from network tracking, as in csqtt-cli: the session is
  closed, the next dial starts at once with a reset backoff.
- **Second Ctrl+C** — as in csqtt-cli: the first one stops the tunnel and cleans up, the
  second one does not wait for open connections, the third one kills the process.

---

## 09. Platforms

**Linux (priority).** Root through `sudo`, files and the keyring of the calling user,
netlink without `ip` and `nft`, nftables table `inet dnstt-cli` with the `owner` flag (it
disappears with the process), kill-switch as a second rule set of the same table — all from
csqtt-cli §09b. One difference: the mark is set by the shell on its own sockets, there is
no second process.

**Windows.** Administrator, a mutex against a second instance, WFP in a dynamic session (the
filters disappear with the process), `wintun.dll` next to the exe — from csqtt-cli §09a,
with one app ID instead of two.

**macOS.** `socks` only; builds in CI, not checked live.

---

## 10. Config and secrets

```toml
# ~/.config/dnstt-cli/profiles.toml · %APPDATA%\dnstt-cli\profiles.toml

default_profile = "home"

[profile.home]
domain       = "t.example.com"
pubkey       = "<64 hex digits>"                # server's Noise public key
resolvers    = [
  "doh:https://doh.example/dns-query@192.0.2.1",
  "dot:dot.example:853@192.0.2.2",
  "udp:192.0.2.53:53",
]
utls         = ""                               # empty — upstream default distribution
datapath     = "tun"                            # tun | socks
kill_switch  = true                             # tun: traffic closed while the tunnel is down
tun_dns      = "1.1.1.1"                        # DNS behind the server for tun (R7)

[profile.home.exit]
type         = "socks5"                         # socks5 | ssh
user         = "user"                           # empty — no authentication
password_ref = "secretservice:…"                # the exit password lives in the secret store

[profile.home.socks]
listen       = "127.0.0.1:1080"
```

- `resolvers` may also hold preset names (`doh-cloudflare`, …) that expand into addresses
  built into the binary.
- `ssh` exits (E5): `host_key` (pinned fingerprint), `user`, `password_ref` or `key_ref`.
- `connect [flags] [profile]`: `-datapath`, `-resolver` (one resolver for this run),
  `-no-kill-switch`, `-allow-leak`, `-v`, `-config` — the csqtt-cli meanings.
- Commands: `connect`, `profile add|set|import|show|rm|list`, `resolvers check`, `menu`,
  `version`. Secrets are asked only by hidden input or read from stdin, never taken from
  arguments; `profile show` prints where a secret lives, not its value.
- Unknown keys and a secret written into the TOML are errors, as in csqtt-cli.
- **Links** — `profile import`, below.

### `slipnet://` links

`slipnet://` followed by base64 of a UTF-8 line. Generators write the standard alphabet
with padding on one line; the parser trims the input, removes whitespace inside (links
wrapped by a terminal), accepts missing padding and falls back to the URL-safe alphabet.
Several links — one per line; a bad line is a warning, not a failure of the whole import.

The line is fields separated by `|`, without escaping. Field 0 is the version as a decimal
number (SlipGate writes 22 with 60 fields, the SlipNet app writes 28 with 79). A field keeps
its position in every version; the parser reads position *i* only if it is present and
takes the default otherwise, and ignores extra fields. Booleans are `1`/`0`; some fields
are base64 of a UTF-8 string (marked b64 below).

| # | Field | Use in dnstt-cli |
|---|---|---|
| 1 | type: `dnstt`, `dnstt_ssh`; also `sayedns`, `sayedns_ssh` (NoizDNS), Slipstream, VayDNS, NaiveProxy, VLESS and others | `dnstt` → exit `socks5`, `dnstt_ssh` → exit `ssh`; anything else is refused |
| 2 | profile name | profile name (latin letters, digits and `-`; otherwise asked) |
| 3 | tunnel domain | `domain` |
| 4 | resolvers: `host:port:flag`, comma-separated; the flag is for Slipstream | `resolvers`, with the transport of field 22 |
| 11 | server public key, 64 hex digits | `pubkey` |
| 12, 13 | SOCKS5 username and password (plain text); empty — no authentication | `exit.user`, the exit password secret |
| 15, 16 | SSH username and password | the SSH exit (E5) |
| 17, 19 | SSH port and host | not used: the server decides where the stream goes |
| 21 | DoH URL | `doh:URL` when field 22 is `doh` |
| 22 | transport: `udp` (default), `tcp`, `dot`, `doh`; unknown — `udp` | resolver prefix; for `dot` port 53 means 853 |
| 23–25 | SSH auth type `password`/`key`, private key in PEM (b64), passphrase (b64) | the SSH exit (E5) |
| 36, 37 | resolvers hidden in the app's interface; then field 4 is empty and the list is in 37 | read from 37; dnstt-cli does not hide them |
| 39 | payload size per query, 0 — maximum | P7 |
| 60, 61 | resolver mode `fanout`/`roundrobin`, spread 1–5 | not used yet (later: several resolvers at once) |

All other fields (Slipstream, VayDNS, NaiveProxy, VLESS, Tor, locks, expiry, device binding,
DPI options) are ignored. Validation follows the app: domain and name not blank, the key
exactly 64 hex digits, at least one resolver unless the transport is `doh`, a DoH URL
starting with `https://`, an SSH user plus a password or key for `dnstt_ssh`.

A link carries the exit password in plain text: it is asked by hidden input or read from
stdin, never taken as an argument, and its values go straight to the secret store, as with
links in csqtt-cli.

---

## 10a. Interactive mode and output

From csqtt-cli: the menu has no logic of its own, it calls the same code as the commands.

- **Start.** `dnstt-cli` without arguments in a terminal opens the menu (on Windows — a
  double click on the exe); `menu` opens it explicitly; `menu -connect <profile>` connects
  at once with the same questions. Without a terminal, no arguments prints the command
  list.
- **No profile yet** — the wizard: paste a `slipnet://` link (hidden input: it holds the
  exit password) or answer questions — profile name (Enter — `home`), tunnel domain, server
  key, exit type, exit login and password (hidden, twice), resolvers (DoH presets offered
  first, own ones possible), then the mode: all traffic (`tun`, Enter, where supported) or
  only programs with the proxy (`socks`). End of input on any question cancels without
  writing.
- **Privileges.** `tun` without root: the menu re-runs one connection through `sudo` in the
  same terminal on Linux, through UAC in a new window on Windows. No way to get them (no
  `sudo`, a non-admin Windows account) — `socks` for this time.
- **Menu items.** Connect; check resolvers (drops dead ones on request); edit resolvers;
  new exit password; change domain or key; another profile; delete profile; `0` — exit.
- **When resolvers run out mid-session** (none completes a handshake for a full round),
  the menu offers to check and edit them and connects again.
- **Signals.** Ctrl+C while connected returns to the menu, the second one does not wait for
  open connections, in the menu it closes it; SIGTERM and SIGHUP remove the tunnel and close
  the menu. A window opened by a double click does not close on an error: Enter does.
- **Status line.** At the bottom of the terminal: state, uptime, current resolver, open
  streams, received and sent, latency of the last handshake. Absent when output is
  redirected.

---

## 11. Licenses

| Part | License | Where |
|---|---|---|
| dnstt core and our patches to it | public domain (CC0) | `core/` |
| shell | not chosen; MIT recommended | everything else |
| Go dependencies | MIT, BSD, Apache-2.0 | `THIRD_PARTY_NOTICES.txt` |

No code under an incompatible license enters the shell (`AGENTS.md`): the GPL parts of
qwdtt-cli and the AGPL code of SlipNet and SlipGate (the `slipnet://` format) are not
opened by the implementer.
Formats and behaviour are facts and are implemented anew.

---

## 12. Stages

### E0. Foundation

Go module, the csqtt-cli packages without VK, captcha and `coreproc`, `scripts/vendorcore`
and `core/` from the mirror with P1, CI for Linux and Windows. Done: Go 1.26.2; the core
dependencies at the upstream versions (`flynn/noise` 1.0.0, `utls` 1.6.6, `kcp-go` 5.6.8,
`smux` 1.5.24), the shared `x/crypto` and `x/net` raised to the csqtt-cli versions; the
carried packages translated into English; `SO_MARK` 0x1076 and new adapter and WFP GUIDs.

### E1. `socks` with reconnection — Linux and Windows

Session API, supervisor, SOCKS5 exit with a password, local SOCKS5, resolver list with
failover and `resolvers check`, P2–P5, profiles and secrets, the status line. Result: a browser goes through
the server's `danted` with a login, and the session survives a server restart and a network
change.

### E2. System VPN on Linux

`tun` with netstack, DNS over TCP, UDP rejection, routes, system DNS, DNS protection,
kill-switch and IPv6 blocking (§05a), `rp_filter`, network tracking, `sudo`.

### E3. System VPN on Windows

Wintun, WFP with one app ID, the rest as in csqtt-cli.

### E4. Interactive mode and releases

Menu, wizard and privilege elevation (§10a), `slipnet://` import, release archives with
`SHA256SUMS.txt`.

### E5. SSH exit

`direct-tcpip`, password and key, pinned host key.

### Later

Lists of domains and networks past the tunnel; a search for working resolvers; DoQ;
spreading one session's queries over several resolvers (the `fanout` and `roundrobin`
modes of `slipnet://`); export of `slipnet://`.

---

## 13. Risks

| Risk | Likelihood | What we do |
|---|---|---|
| The DNS tunnel is recognised and the resolver blocked | high | resolver list with failover; DoH with uTLS by default; `resolvers check` |
| Public DoH and DoT closed in the user's network | high | UDP mode through the ISP resolver; a socket per query (P3) |
| The narrow channel cannot carry all system traffic | high | `socks` as a full mode; lists past the tunnel later |
| No UDP for apps (calls, games) | medium | said plainly in README; fast rejection for QUIC |
| Anyone with the domain and key can use the server | medium | README recommends a login on the exit; `danted` with a password is the main case |
| A dead session is noticed late | medium | shorter client keepalive (R2), measured in E1 |
| An upstream update adds a socket outside the hook | low | the guard (§06) |
| Antivirus reacts to an unsigned exe creating an adapter | medium | warning in README, release signing later |
