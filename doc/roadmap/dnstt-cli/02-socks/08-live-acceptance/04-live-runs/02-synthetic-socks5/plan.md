---
kind: task
status: done
depends: [01-runbook]
detailed: true
---

# Synthetic SOCKS5 exit

## Task

Accept the SOCKS5 datapath in a disposable local setup, because the user's server has no
SOCKS5 exit. The result is synthetic and scoped: it speaks about this local `dnstt-server`,
this `danted`, this target and this container network, not about the user's exit, server or
network. The published text must say so.

The task reuses the approach and launcher of the
[synthetic `danted` check](../../03-timeouts-and-danted/08-synthetic-danted/plan.md) and
the browser container of the
[TUN browser task](../../../../03-tun-linux/06-live-acceptance/07-browser-container/plan.md).
It records into the [runbook](../01-runbook/plan.md) and follows the
[stage's run design](../plan.md#run-design).

The user granted the one permission for both container-internal fetches. Nothing is
installed on the host, the `debian:trixie` image stays local, and no other source is used.

## Design

- **Topology.** Two containers from the local `debian:trixie` image on the default bridge,
  `--pull=never`, no `--network host`, no published ports, no `docker network create`.
  Server container: `dnstt-server` built from the vendored `core/server` with the reserved
  `t.example.com` and a key generated with `-gen-key -privkey-file` into an ignored private
  directory; `danted` 1.4.4 built from source with one login and its destination limited
  to the target; a fixed-body HTTP target on `127.0.0.1` with `target.example` mapped in
  the server container's hosts file. Client container: the product and headless Chromium.
- **Proxy access.** The product and Chromium share the client container. The product
  listens on `127.0.0.1:<local-port>`; Chromium starts with
  `--proxy-server="socks5://127.0.0.1:<local-port>"`. The browser sends the target name to
  the proxy, and `danted` resolves it; no host proxy, no published port and no hosts entry
  for the target in the client container. The synthetic page uses an explicit port, not
  443, so an HTTPS-first navigation cannot substitute a TLS attempt for the checked path.
- **Transport.** The profile has one resolver, `udp:<server-ip>:<port>`, reaching the local
  `dnstt-server` directly. No local DoH or DoT resolver is built: the three transports are
  the real-server task, where the user's resolvers exist.
- **Restart.** `dnstt-server` is stopped and started again with the same key, domain, port
  and upstream; `connect` is not restarted. A new fetch after the session is redialed is
  the evidence; the recovery time is read against the 30 s keepalive and the 25 s degraded
  threshold.
- **Network change.** The client container's address changes inside its own network
  namespace, on the same bridge and with the same server address in the profile. The old
  session's source address disappears; the next dial uses the new one. This is not a
  physical link change, and the text must not call it a Wi-Fi-to-cable test.
- **Product setting.** `exit.one_write` stays at its default `false`; the one-write mode is
  not part of this acceptance.
- **Permissions.** Before anything is fetched, one explicit permission names both
  container-internal fetches: Chromium and its minimal packages in the client container;
  the `danted` source archive, its build dependencies and the account tooling in the server
  container. `--pull=never`; no host install; a refusal stops the task.
- **Host unchanged.** Before the first container and after the last one are removed,
  record the IPv4 and IPv6 routes, the `sha256sum` of `/etc/resolv.conf`, `ip -br link`,
  `docker ps -a`, `docker network ls`, the image ids and `git status --short`. The required
  result: the default route and the resolver file match; no container, bridge, published
  port, `dnstt-cli` process or interface from the run remains; no image was pulled and the
  image ids are unchanged; `git status` shows only the roadmap node and ignored paths. The
  temporary profile and every copy of the secret are deleted.
- **Browser noise.** `danted` permits only the target; Chromium's background requests are
  refused and counted as noise, not taken for the page result. A page that fails is checked
  with a pinned fetch before it is attributed to the exit.

## Steps

1. **S0 — environment and permission.** Read the runbook's S0 and the design's
   "Permissions" bullet. On the host, write the pre-run record into
   `doc/runs/live-socks/<run-id>/host-before.txt`: `ip -4 route`, `ip -6 route`,
   `sha256sum /etc/resolv.conf`, `ip -br link`, `docker ps -a`, `docker network ls`,
   `docker image inspect debian:trixie`, `git status --short`, plus
   `docker info --format '{{.ServerVersion}}'`; save the image id. Confirm with
   `git check-ignore -v` that `doc/runs/live-socks/` and
   `doc/local/runbook-socks-acceptance-filled.md` are ignored. Request the one explicit
   permission for both container-internal fetches: the Chromium packages in the client
   container; the `dante-1.4.4.tar.gz` archive (SHA-256
   `1973c7732f1f9f0a4c0ccf2c1ce462c7c25060b25643ea90f9b98f53a813faec`), its build
   dependencies and the account tooling in the server container. Record the request and
   the image id in the same file. A refusal stops the task; there is no host install and no
   fallback. Build offline with `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local CGO_ENABLED=0 GOOS=linux
   GOARCH=amd64`: `go build -o build/out/dnstt-cli ./cmd/dnstt-cli`,
   `go build -o build/out/synthetic-target ./doc/runs/_evidence/synthetic-danted/target`,
   and the `dnstt-server` from the vendored `core/server` (`go build -o build/out/dnstt-server
   ./core/server`). Check `connect -h` for the `-datapath` flag; the profile's `datapath =
   "socks"` is the accepted setting. The key and the private directory are created with
   `-gen-key -privkey-file` inside the ignored `doc/runs/live-socks/<run-id>/` directory.

2. **Server container: `danted`, target and `dnstt-server`.** From the local
   `debian:trixie` image, `docker run -d --name <server> --pull=never` with no
   `--network host`, no published ports and no `docker network create`; the default bridge
   only. The container's command is `bash /out/server.sh`; the script starts
   `dnstt-server`, `synthetic-target` and `sockd -f /etc/sockd.conf` in the foreground and
   waits, so `docker stop`/`docker start` restarts all three with the same files. Inside the container, install the `danted` build and account packages from the
   launcher's list, copy the `dante-1.4.4.tar.gz` archive, verify its SHA-256, run
   `./configure --prefix=/usr/local --with-pam --with-libwrap`, `make`, `make install`, and
   write the PAM service files `/etc/pam.d/danted`, `/etc/pam.d/dante` and
   `/etc/pam.d/sockd` with `auth required pam_unix.so` and `account required pam_unix.so`.
   Create the one login `<exit-login>` with the password in the container's shadow file and
   the temporary profile; never in an argument or the evidence. Copy the `sockd.conf` from
   the launcher: `internal: 0.0.0.0 port = <danted-port>`, `external: 127.0.0.1`,
   `clientmethod: none`, `socksmethod: username`, `user.privileged: root`,
   `user.unprivileged: nobody`, `client pass` from `0.0.0.0/0`, `socks pass` only to
   `127.0.0.1:<target-port>`, `command: connect`, `protocol: tcp`, `socksmethod: username`,
   `logoutput: stderr`. Start `synthetic-target -addr 127.0.0.1:<target-port> -dir
   /srv/body` with the fixed body; add `127.0.0.1 target.example` to the container's
   `/etc/hosts`. Generate the key with `dnstt-server -gen-key -privkey-file <key>
   -pubkey-file <pubkey>` into the ignored private directory; start `dnstt-server
   -udp 0.0.0.0:<server-port> -privkey-file <key> t.example.com <server-ip>:<danted-port>`
   and `sockd -f /etc/sockd.conf` in the foreground. Check the greeting from inside the
   container: `05 01 02` answers `05 02`. Record the `danted` version, the image id, the
   `sockd.log` and the `target.log`; the server's address is the container's bridge address.

3. **S1 — the session and the browser fetch.** Start the client container from the same
   image with `--pull=never`, no `--network host` and no published ports; mount the
   temporary profile read-only at `/cfg` and set `XDG_CONFIG_HOME=/cfg`. Write the
   temporary profile `synthetic` in `doc/runs/live-socks/<run-id>/` (mode 0700, files
   0600) with `domain = "t.example.com"`, the generated public key, `datapath = "socks"`,
   `exit.type = "socks5"`, `exit.user = "<exit-login>"` and `socks.listen =
   "127.0.0.1:<local-port>"` and one resolver `udp:<server-ip>:<server-port>`; leave
   `exit.password_ref` empty until the secret is stored. On the host, run
   `env -u DBUS_SESSION_BUS_ADDRESS -u XDG_RUNTIME_DIR XDG_CONFIG_HOME=<temporary>
   go run ./doc/runs/_live secret -profile synthetic` with the password on stdin; write the
   printed `password_ref` into `profiles.toml`'s `exit.password_ref`; then mount the
   profile read-only into the client container. Start the session in a pty with
   `script -qfec "dnstt-cli connect -v synthetic" /out/<run-id>/session.log`; wait up to
   90 s for the `connected` line and the local listener. Then run headless Chromium with
   `--proxy-server="socks5://127.0.0.1:<local-port>"`, `--dump-dom
   http://target.example:<target-port>/body.txt`, `--log-net-log=/out/<run-id>/netlog.json`,
   `--net-log-capture-mode=Default`, `--headless=new`, `--no-sandbox`,
   `--disable-setuid-sandbox`, `--disable-dev-shm-usage`, `--no-first-run`,
   `--no-default-browser-check`, `--disable-background-networking`,
   `--disable-component-update`, `--disable-sync`, `--metrics-recording-only`,
   `HOME=/tmp`, a writable `--user-data-dir`, and `timeout -s TERM -k 10 180`. The DOM
   must show the expected title and body; the `sockd.log` must show the authenticated
   `CONNECT`; the browser's background requests are refused and counted as noise. Record
   the per-phase wall time, DOM, streams and bytes; fill the S1 `Result:` line. The
   browser's `--host-resolver-rules` and `--disable-quic` are not used: the target's name
   goes to the proxy and `danted` resolves it, and the page uses an explicit port, not 443.

4. **S2 — restart the local server.** With the session up, run `docker stop <server>` and
   `docker start <server>`; the same key, domain, port and upstream start again; the
   client's `connect` is not restarted. Record the time from the stop to the `connected`
   line and the status line's `tunnel reconnecting` then `connected`; run the browser fetch
   again and check the same body. Compare the recovery time against the 30 s keepalive and
   the 25 s degraded threshold; fill the S2 `Result:` line.

5. **S3 — the client's network change.** With the session up, read `ip -br addr show eth0`
   in the client container, then `ip addr del <old-addr> dev eth0` and
   `ip addr add <new-addr>/16 dev eth0`; the server address in the profile stays unchanged.
   Run `ss -uanp` on the client and on the server to show the new source; run the browser
   fetch again and record the recovery time. Fill the S3 `Result:` line; the text must say
   this is a container address change, not a physical link change and not a Wi-Fi-to-cable
   test.

6. **S4 — clean stop and host comparison.** Send `kill -INT` to the client's `connect`
   process (not `docker stop`), wait for it to exit, then stop and remove both containers.
   Confirm the client's `socks` listener and the server's `dnstt-server`, `sockd` and
   `synthetic-target` are gone. Delete the temporary profile, the key and every copy of the
   secret. Write the host's after record into `host-after.txt` with the same commands as
   S0 and compare it with `host-before.txt`: the default route, the IPv6 routes and the
   `/etc/resolv.conf` checksum match; `ip -br link` shows no `dnstt-cli` interface; no
   container, bridge or image change; `git status --short` shows only the roadmap node and
   ignored paths. Fill the S4 `Result:` line.

7. **Fill the checklist and the evidence.** Fill the `Result:` lines of S0–S4 in
   `doc/local/runbook-socks-acceptance-filled.md` with `seen`, `not seen — <reason>` or
   `failed — <what happened>` and the side. Keep the raw evidence in
   `doc/runs/live-socks/<run-id>/`: the DOM, netlog, streams, bytes, `sockd.log`,
   `target.log`, `session.log`, `host-before.txt` and `host-after.txt`. Check the
   `git check-ignore -v` on every tracked path and never use `git add -f`. The coordinator's
   `Outcome` carries the sanitized text: the setup is synthetic and scoped to this local
   `dnstt-server`, the recorded `danted` version, the target and the container network; no
   value, address, resolver, login or date is in a tracked file.

## Done when

- A disposable two-container setup on the default bridge (local `debian:trixie`,
  `--pull=never`, no `--network host`, no published ports, no `docker network create`)
  served the authorized fetches and the whole run; the server container ran `dnstt-server`
  from `core/server` with `t.example.com` and a generated key, `danted` 1.4.4 with one
  login, and the fixed-body target; the client container ran the product and headless
  Chromium.
- A headless Chromium through the local SOCKS5 listener at `127.0.0.1:<local-port>` loaded
  the target page (expected title and body in the DOM); per phase the wall time, DOM,
  streams and bytes are recorded.
- The local `dnstt-server` restart was survived without restarting `connect`: the old
  session ended, a new one came up and a new fetch worked; the recovery time is recorded
  against the 30 s keepalive and the 25 s degraded threshold.
- The client container's address change was survived the same way, with the server address
  unchanged; the recovery time is recorded, and the text states that this is a container
  address change, not a physical link change.
- The clean stop left no product process, adapter, container, temporary profile or secret;
  the host's routes, `/etc/resolv.conf` checksum, links, Docker images and networks and
  `git status` match the pre-run record; no new bridge, published port or host process
  exists.
- The published text says the setup is synthetic and scoped to this local `dnstt-server`,
  the recorded `danted` version, the target and the container network; it is not the
  user's exit, server or network; no value, address, resolver, login or date is in a
  tracked file.

## Outcome

A disposable synthetic setup accepted the SOCKS5 datapath. Two containers from the local
`debian:trixie` image ran on the default bridge with `--pull=never`, no `--network host`,
no published ports and no `docker network create`. The server container ran `danted` 1.4.4
built from source with one login, the fixed-body target and `dnstt-server` built from the
vendored `core/server` with `t.example.com` and a generated key; the client container ran
the product and headless Chromium 156. The profile had one `udp:` resolver reaching the
local server directly, `datapath = "socks"`, and `exit.one_write` at its default false. The
result is synthetic and scoped to this local `dnstt-server`, this `danted`, this target and
this container network; it is not the user's exit, server or network. No product code,
test or dependency changed.

**S1.** A headless Chromium through the product's local SOCKS5 listener loaded the target
page: 367 ms, DOM 164 B with the expected title and body, the main document 200 in the
netlog, and the authenticated CONNECT in the exit's log; the browser's background requests
were refused by the exit and counted as noise. The status sample read 3 streams and moved
the byte counters from 426 B/477 B to 1.9 KB/2.6 KB.

**S2.** The local `dnstt-server` was stopped and started with the same key, domain, port
and upstream while `connect` stayed up. The old session ended, the status showed `degraded`
and then `connected`, and the new `connected` came 52.8 s after the stop: the degraded
state began at the 25 s threshold, the unanswered keepalive timed out at the core's 30 s,
and the rest is the stop/start and the redial. The new fetch answered 200 with the same
title and body: 347 ms, DOM 164 B, byte counters 244 B/295 B to 1.8 KB/2.5 KB; the 1.1 s
status samples read 0 streams because the fetch was shorter than their step.

**S3.** The client's address changed inside its own network namespace: the old address was
removed and a new one in the same subnet was added, while the server address in the profile
stayed unchanged. The session stayed `connected` and no redial happened, so the recovery was
0 s: the SOCKS5 transport opens a UDP socket per query and closes it, and the session is not
tied to the source address. The new fetch answered 200 with the title and body: 344 ms, DOM
164 B, and the exit logged the authenticated CONNECT. The idle `ss` and `/proc/net/udp`
were empty; during the fetch the sockets were bound to `0.0.0.0`, the kernel choosing the
source, and the new address was the only one on the interface. This is a container address
change, not a physical link change and not a Wi-Fi-to-cable test.

The image has no `ip` or `ss`, so the plan's commands ran in the client's network namespace
from the host through `nsenter`; the host's network namespace was not changed and no
package, container or capability was added. A first attempt to set an address with
`docker network connect --ip` on the default bridge was rejected by the daemon (a
user-specified address is supported on user-defined networks only); it is kept in the
ignored evidence. That first attempt, a disconnect and reconnect with the same address, did
break the session, which then recovered without restarting `connect`; the accepted address
change did not break it. The address deletion also dropped the client's default route; the
resolver is on-link and the fetch worked, and the container was removed afterwards.

**S4.** One SIGINT was sent to the `script` wrapper that owns the pty (the first `/proc`
match); the product exited 0 (`COMMAND_EXIT_CODE="0"`), and `docker top` after showed only
the container's `sleep`. Both containers were stopped and removed; the client's listener
and the server's `dnstt-server`, `sockd` and target are gone; the profile, the file store,
the private key and every copy of the secret were deleted. The host's default route, the
IPv6 routes apart from the pre-existing Docker veth pairs, `/etc/resolv.conf` checksum,
image id, network list and `git status` match the baseline; no container, veth, published
port or host process from the run remains, and `docker0` returned to its DOWN state. The comparison recorded external Docker changes that
are not the run's: a pre-existing container was restarted by its own environment, another
was recreated by its own compose project, and two transient tool containers from that
project came and went; they are in the ignored evidence and were not touched by the run.

Deviations and run notes: the target page is `page.html` with the title and the same fixed
body, not `body.txt`, because the DOM needs a title; the client mounts only its own evidence
directory, not the whole run directory, so the private key is not visible to it;
`dnstt-server` was built from a copy of `core/server/main.go` with the package clause
changed to main, because the vendored package is a library; the system Go is older than
`go.mod` requires, so the cached 1.26.2 toolchain was used offline. The steps' build command
for `dnstt-server` and `GOTOOLCHAIN=local` with the system Go do not produce the server
binary; the run used the workaround above. No `dnstt-cli` defect was found: the 52.8 s
restart recovery is the keepalive path, recorded against the 30 s keepalive and 25 s
degraded threshold rather than treated as a defect. The real-server checks R0–R10 and the
Windows child W1 are other nodes and were not run; the one-write mode is not part of this
acceptance.
