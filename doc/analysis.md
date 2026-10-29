# dnstt, qwdtt-cli and csqtt-cli: what to carry over into dnstt-cli

Groundwork for the dnstt-cli architecture: what the dnstt client can do today, how dnstt
differs from qWDTT and CSQTT, what carries over from
[qwdtt-cli](https://github.com/FreedomCats/qwdtt-cli) and
[csqtt-cli](https://github.com/FreedomCats/csqtt-cli), and what has to be built anew. Facts
about dnstt were checked by reading the upstream code at `v1.20260501.0`
([mirror](https://github.com/FreedomCats/dnstt)); facts about the other two clients come
from their `README.md` and `doc/architecture.md`.

---

## 1. The dnstt client today

### What it does

`dnstt-client` forwards a single TCP port through DNS, like `ssh -L` or `ncat`. It listens
on a local address (`127.0.0.1:7000`), opens every accepted connection as a stream inside
the tunnel, and the server hands the stream to one fixed TCP address.

```
app → 127.0.0.1:7000 → smux stream → Noise → KCP → DNS queries → resolver → dnstt-server → exit TCP address
```

- **Transport to the resolver** — one of three: DoH (`-doh URL`), DoT (`-dot address`) or
  plaintext UDP (`-udp address`). The upstream README calls UDP a testing mode: it does
  not hide the tunnel.
- **TLS camouflage** — uTLS: the Client Hello fingerprint is picked at random from a
  weighted list (`-utls`); the default is a mix of Firefox, Chrome and iOS fingerprints.
- **Encryption** — Noise_NK: the client knows the server's public key (`-pubkey`); the
  server does not identify the client.
- **Code** — Go, public domain (CC0). The client is about 1600 lines without tests, plus
  the shared packages `dns`, `noise`, `turbotunnel`.

### What it lacks

No SOCKS, no TUN adapter, no routes: the client does not manage the system at all. Hence
nothing that depends on managing the system either: no kill-switch, no DNS protection, no
IPv6 blocking. No reconnection, no multiple resolvers, profiles, secret storage, menu,
status line or network-change tracking. Upstream does not intend to add any of this: the
README says plainly that dnstt is an application-level tunnel and the proxy belongs on
the server.

### Weak spots of the core

dnstt-cli must fix these, otherwise the client is not usable day to day.

1. **One session per process.** `run` in `dnstt-client/main.go` creates KCP, Noise and
   smux once and then only accepts connections. If the session dies (server restarted,
   network gone for long, the session closed by the idle timeout), new streams fail in
   `OpenStream` until the process is restarted. The break is not noticed quickly either:
   the smux keepalive waits `idleTimeout` — 2 minutes.
2. **DoT does not survive a failed redial.** `tls.go`: the connection to the resolver is
   redialed, but the very first failure leaves the loop for good, and the tunnel hangs on
   "operation on closed connection" until the session times out. This is the first item
   in the upstream `TODO`.
3. **In UDP mode one socket serves all queries.** All queries leave from the same source
   port. The upstream `TODO` cites a report that in some networks UDP associations with
   the same address and port are blocked within seconds, and suggests a socket per query.
4. **The system resolves the resolver's name.** A DoH or DoT address with a host name is
   resolved by the system resolver. With a kill-switch this fails (system DNS is closed),
   and without one the tunnel resolver's name goes to the ISP. qwdtt-cli hits the same
   problem and advises writing an IP in the address.
5. **Ageing TLS fingerprints.** uTLS `v1.6.6`, the newest fingerprints are Chrome 120 and
   Firefox 120. An old fingerprint eventually becomes a signature in itself.
6. **A single resolver.** If it does not answer or is blocked, there is no tunnel and
   nothing to switch to.
7. **No DoQ** (DNS over QUIC) — also an item in the upstream `TODO`.

The latest upstream fix (`v1.20260501.0`, `sendLoop` stopping on a `sendto` error)
concerns only the server.

### The server side

`dnstt-server` hands every stream to one TCP address, and what listens there is up to the
server's owner. Usually it is a SOCKS5 proxy or SSH. For port forwarding this does not
matter, but for a full tunnel it does: the client must say **where** a connection goes.
So the exit needs a protocol that carries the destination address — SOCKS5 (`CONNECT`) or
SSH (`direct-tcpip`). dnstt itself has no client authentication: anyone who knows the
domain and the public key can use the tunnel. The server can be closed to strangers only
at the exit — with a SOCKS5 password or an SSH key.

---

## 2. How dnstt differs from qWDTT and CSQTT

| | qwdtt-cli | csqtt-cli | dnstt-cli |
|---|---|---|---|
| What the core carries | IP packets, WireGuard inside TURN | IP packets, own protocol inside TURN | **TCP streams** (smux), not packets |
| Where the app's TCP ends | on the server (WireGuard) | on the server | **on the client**: own TCP stack; on the server the proxy opens a new connection |
| App UDP | yes (`tun`) | yes (`tun`) | **no**: a SOCKS5 stream does not carry UDP |
| IPv6 | if the server assigns it | no | TCP to IPv6 addresses — if the server's exit supports them |
| Tunnel DNS | assigned by the server (WireGuard config) | assigned by the server (`TUNCONF`) | chosen by the client; queries go as **DNS over TCP** through a stream |
| Core | Go, a library in-process | Rust, a separate process | Go, **in-process** |
| Core's own sockets bypassing the tunnel | `Control` hook at 6 points | core patch, 8 points | `Control` hook, about 4 points (§3) |
| VK, captcha, calls | yes | yes | **no** |
| Secrets | password (also the WRAP key), hashes | password, hashes, VK token | the public key is not secret; secrets are only the exit login (SOCKS5, SSH) |
| Throughput | as a call | as a call | **narrow channel**: limited by the DNS response size and the resolver |
| Core license | GPL-3 | PolyForm NC | public domain (CC0) |

The main consequences:

- **In `tun` the client needs its own TCP stack.** In qwdtt-cli and csqtt-cli packets go
  into the tunnel as they are. Here packets are taken by gVisor netstack, every TCP
  connection becomes an smux stream, and inside the stream the client talks to the
  server's exit itself (`CONNECT address:port`). netstack is already in csqtt-cli
  (`datapath/netstack.go`) — there it sits on the IP channel to the core, here it
  terminates TCP.
- **App DNS has to be carried by us.** UDP :53 from the adapter is intercepted and sent as
  a DNS-over-TCP query through a stream to a resolver behind the server. The tunnel
  resolver is a profile field.
- **Other UDP is not carried at all.** It must be rejected immediately (ICMP "port
  unreachable") rather than silently dropped: then the browser's QUIC falls back to TCP
  without waiting.
- **The core is in-process — as in qwdtt-cli, not as in csqtt-cli.** The core's sockets are
  bound to the physical interface with one `Control` hook without patching upstream code
  (the idea of qwdtt-cli's R3), and WFP permits one program rather than two. There is no
  `coreproc`, no loopback packet channel, no TUN descriptor handover.
- **The narrow channel changes priorities.** A tunnel carrying all system traffic will
  also carry updates and cloud sync — and they will fill it up. Sending part of the
  traffic past the tunnel matters more here than for qWDTT and CSQTT.

---

## 3. dnstt core sockets that must bypass the tunnel

All of them live in the dnstt-cli process, and each is created through a standard
`net.Dialer` or `net.ListenUDP` — the `Control` hook fits every one.

| # | Where | What | How to bind |
|---|---|---|---|
| 1 | `main.go` · `net.ListenUDP` | UDP-mode queries to the resolver | `net.ListenConfig{Control}`; with a socket per query (§1, item 3) — on each |
| 2 | `utls.go` · `utlsDialContext` | TCP to DoH and DoT with uTLS | `net.Dialer{Control}` |
| 3 | `main.go` · `http.Transport` | DoH without uTLS (`-utls none`) | `DialContext` with the same `Dialer` |
| 4 | `main.go` · `tls.Dialer` | DoT without uTLS | `NetDialer` with the same `Dialer` |
| — | `net.ResolveUDPAddr`, resolving the DoH/DoT name | the resolver's name | own resolver with bound sockets, or an IP in the profile (§1, item 4) |

As in qwdtt-cli and csqtt-cli, the list needs a guard: a test that finds every call in
`core/` that creates a socket or resolves a name and checks it against the table. A new
call site after a core update must fail the test rather than loop traffic back through
the adapter.

---

## 4. What carries over

The base is csqtt-cli: it already separates the qwdtt-cli shell from the GPL code and
ships it under MIT. From qwdtt-cli we take decisions, not code: a Go core in-process (R1),
one `Control` hook (R3).

| Part | From | What changes |
|---|---|---|
| `netguard` — physical interface choice, the `Control` hook, network-change tracking | csqtt-cli | almost nothing; the hook goes on the core's sockets directly in-process |
| `linuxsys` — `/dev/net/tun`, routes via netlink, system DNS (systemd-resolved, resolvconf, `resolv.conf`), nftables table with `owner`, kill-switch, `rp_filter` check | csqtt-cli | almost nothing; the TUN descriptor is not handed to the core — the shell's netstack reads it |
| `winsys` — Wintun, `/1` routes, adapter DNS, WFP filters (DNS protection, kill-switch) | csqtt-cli | one program in WFP instead of two |
| `datapath/socks5.go`, `netstack.go`, `resolver.go` | csqtt-cli | netstack terminates TCP and hands connections to streams; the shell's SOCKS5 serves the `socks` mode |
| `datapath/tun*.go` — adapter per session, `Suspend`, `Close`, second-instance lock | csqtt-cli | an adapter ↔ netstack bridge instead of adapter ↔ core |
| `supervisor` — state machine, `1·2^n` backoff up to 64 s, `Restart` on network change, second Ctrl+C | csqtt-cli (`lifecycle.go`, `state.go`, `policy.go`) | events are Go API calls of the core, not stdout lines; states without credentials and captcha |
| `cli`, `config`, `lines` — commands, menu, profile wizard, hidden input, `profiles.toml` | csqtt-cli | own profile fields (§5); hash check → resolver check |
| `secretservice`, `sudo`, DPAPI | csqtt-cli | unchanged |
| `scripts/` — `wintun`, `release`, `notices`, `sha256sums`, `distcheck`, `checktunnel`, `ctrlbreak`, `releasenotes`; CI for Linux and Windows | csqtt-cli | names; `buildcore` is not needed — the core is Go |
| `scripts/vendorcore` — carrying the core over by rules with a mandatory match count, `-check` | csqtt-cli | simpler: the upstream is Go, no crates or Cargo |

**Not carried over:** `captcha` and everything about VK, `coreproc` (the core is not a
separate process), `datapath/link*.go` and `tunfd_linux.go` (channels to the core),
qwdtt-cli's `external` mode (WireGuard plays no part in dnstt), qwdtt-cli's GPL code —
`internal/core`, `supervisor/restart.go`, `captcha/token.go`, `captcha/solver.go`. dnstt-cli
needs none of it.

---

## 5. What has to be built anew

1. **Session reconnection.** The supervisor holds the KCP/Noise/smux session and recreates
   it on a break, on a network change and on a resolver change. It needs its own sign of
   a dead session, faster than the two-minute keepalive: for example a probe stream
   answered by the exit, or a counter of resolver responses. Open connections break when
   the session is recreated — new ones go into the new session.
2. **Talking to the server's exit.** A SOCKS5 client over a stream: `CONNECT`, optionally
   login and password if the owner wants them. SSH (`direct-tcpip`, one channel per
   connection) as a second option, if the server's exit is SSH.
3. **`socks` mode.** Own SOCKS5 on `127.0.0.1:1080`, no privileges. If the server's exit is
   SOCKS5, the app's connection can be passed into the stream as is, and names from
   `socks5h` are resolved on the server. If it is SSH, the shell parses SOCKS5 itself.
4. **DNS in `tun`.** UDP :53 queries from the adapter → DNS over TCP through a stream to a
   resolver behind the server. DNS protection and kill-switch — as in csqtt-cli: the
   system's port 53 is open only to the adapter's DNS address, dnstt-cli's own sockets pass
   by mark (Linux) or by program (Windows). In UDP mode dnstt-cli itself sends queries to
   port 53 — without the mark, DNS protection would kill the tunnel.
5. **IPv6.** By default — as in csqtt-cli: `::/1` and `8000::/1` routes into the adapter,
   IPv6 rejected immediately. An option — carry TCP to IPv6 addresses through the exit if
   it supports them.
6. **Resolvers.** A list with health checks and failover; a check command (the analogue of
   qwdtt-cli's `hashes`): which resolvers from the list carry the tunnel right now and with
   what latency. Later — a search for working resolvers over a list of candidates.
7. **Transport.** Core patches as `vendorcore` rules: a socket per query in UDP, DoT redial
   with a delay instead of giving up, the `Control` hook, own resolver for the DoH/DoT
   name, a newer uTLS. DoQ as a separate task.
8. **Past the tunnel.** Lists of domains and networks that go direct. The core is
   in-process and netstack sees both DNS queries and SNI, so the shell decides without
   patching the core — unlike csqtt-cli, where the CSQTT core does it. A separate stage.
9. **Status line.** Session, resolver, number of streams, received and sent, latency to
   the exit.

---

## 6. License: recommendation — MIT

All the shell code dnstt-cli needs is in csqtt-cli under MIT: it is the qwdtt-cli shell
without its GPL parts. Those parts (the qWDTT core, the restart sequence, captcha token
interception) are not needed by dnstt-cli. The dnstt core is public domain and compatible
with any license. So GPL gives nothing: the qwdtt-cli code that would justify it is not
needed here. The decision is the user's; until it is made, `AGENTS.md` keeps the strict
variant.

---

## 7. Questions for the architecture

1. **The server's exit.** SOCKS5, SSH or both? With a SOCKS5 password or without?
2. **The server.** Only the client, as in qwdtt-cli and csqtt-cli, or also instructions
   and a check for the server (`dnstt-server` + proxy)?
3. **Platforms.** Windows → Linux, as for the other two? macOS — only `socks`?
4. **Default transport.** DoH, DoT or UDP? A built-in list of public resolvers, or only
   each profile's own?
5. **Profile link.** An own format for import (domain, key, resolvers, exit) or one
   compatible with existing dnstt clients?
6. **Core.** The upstream Go module in `core/` as a whole, patches as `vendorcore` rules,
   version in `core/CORE_VERSION`. Upstream — `www.bamsoftware.com/git/dnstt.git` or the
   FreedomCats mirror?
7. **Stage order.** As in csqtt-cli: foundation → `socks` with reconnection → `tun` on
   Windows → Linux → menu and releases → past the tunnel?

---

## 8. Risks

| Risk | Likelihood | What we do |
|---|---|---|
| The DNS tunnel is recognised by query volume and shape, and the resolver gets blocked | high | several resolvers with failover; DoH and DoT with uTLS by default; a resolver check command |
| Public DoH and DoT are closed in the user's network | high | UDP mode through the ISP resolver stays; a socket per query |
| The narrow channel cannot carry all system traffic | high | `socks` as a full-fledged mode; past the tunnel as a separate stage |
| Without UDP some programs do not work (calls, games) | medium | said plainly in README; fast rejection so QUIC falls back to TCP |
| Anyone who knows the domain and key can use the server | medium | README advises a SOCKS5 password or SSH at the exit |
| A new upstream version creates a socket that bypasses the hook | low | a guard in tests and in `vendorcore -check` (§3) |
| Antivirus reacts to an unsigned exe that creates an adapter | medium | as in qwdtt-cli and csqtt-cli: a warning in README, release signing later |
