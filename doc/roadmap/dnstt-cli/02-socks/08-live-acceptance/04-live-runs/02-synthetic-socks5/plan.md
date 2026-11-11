---
kind: task
status: todo
depends: [01-runbook]
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
