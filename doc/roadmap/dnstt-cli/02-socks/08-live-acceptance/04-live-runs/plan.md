---
kind: stage
status: done
base: a787cd7
depends: [02-live-run-script, 03-timeouts-and-danted]
---

# Live runs

The acceptance of the SOCKS5 datapath, moved from the user's checklist to the agent and
disposable containers on this machine, with the final timeouts in place. The user's server
has no SOCKS5 exit: the SOCKS5 datapath is accepted in a synthetic local setup, and the
real server serves the HTTP CONNECT exit with Basic authentication. Every result says
which of the two it comes from; a synthetic result never stands for the user's server,
exit or network.

Scope:

- **Synthetic SOCKS5.** A local `dnstt-server` built from the vendored `core/server`, with
  `t.example.com` and a generated key; a `danted` 1.4.4 built from source with one login; a
  fixed-body local target; the product's local SOCKS5 listener and a headless Chromium.
  The local server is restarted and the client container's network is changed in the middle
  of a session. This is not the user's exit, server or network.
- **Real server, HTTP exit.** The user's server and its HTTP CONNECT exit with Basic
  authentication, over working DoH, DoT and UDP resolvers: the browser through the local
  proxy, the authenticated comparison with the upstream client and the independent HTTP
  CONNECT probe, the dead-resolver check, the eight presets and the uTLS fingerprints. The
  values are supplied in chat when the real-server work starts.
- **Windows.** Deferred until a Windows machine is available; a `blocked` child. No Windows
  item is counted as seen.

The timeouts are final: keepalive 30 s, handshake 10 s, degraded 25 s
(`internal/supervisor/tunnel.go`); the restart and network-change recovery times are read
against them. The comparison is the accepted `doc/runs/_live/` tool. The synthetic setup
and its launcher follow the
[synthetic `danted` check](../03-timeouts-and-danted/08-synthetic-danted/plan.md); the
browser container follows the
[TUN browser task](../../../03-tun-linux/06-live-acceptance/07-browser-container/plan.md).

## Run design

These decisions are shared by the children; a child may add detail, not contradict them.

- **Containers.** `debian:trixie` (local; `--pull=never`), no `--network host`, no
  published ports, no `docker network create`. The default bridge only. The host's routes,
  DNS, firewall and interfaces are not changed; the only host-side writes are builds into
  the ignored `build/out/`, the ignored profile and evidence files under `doc/runs/`, and
  the ignored checklist under `doc/local/`.
- **Browser and proxy.** The browser and the product share one client container. The
  product listens on `127.0.0.1:<local-port>`; Chromium starts with
  `--proxy-server="socks5://127.0.0.1:<local-port>"`. The browser resolves the target at
  the exit (SOCKS5 remote DNS); no host proxy and no published port. The same holds for
  the synthetic and the real-server runs.
- **Synthetic topology.** Two containers on the default bridge. Server container:
  `dnstt-server`, `danted`, the target. Client container: the product and Chromium. The
  profile's only resolver is `udp:<server-ip>:<port>`, a direct local resolver; no local
  DoH or DoT servers are built, because the three transports are the real-server task,
  where the user's resolvers exist.
- **Restart.** The local `dnstt-server` is stopped and started again with the same key,
  domain, port and upstream while `connect` stays up. The session dies and is redialed by
  the supervisor; a new fetch is the evidence.
- **Network change.** The client container's address changes inside its own network
  namespace, on the same bridge and with the same server address in the profile. The old
  session's source address disappears and the next dial uses the new one. This is the
  closest local equivalent of a Wi-Fi-to-cable change for `socks` mode, which has no
  network tracking and recovers through the keepalive; it is not a physical link change
  and says nothing about the user's networks. Moving the client to a second bridge was
  rejected: the server address would change with it, which would replace the network
  change with a server-address change.
- **Real-server runs.** One client container with the product, Chromium and the comparison
  tool as the task needs; the temporary profile is mounted read-only. The only network
  actions are the resolvers, the tunnel and the target `<target>` (default
  `https://example.com/`), which the exit resolves.
- **Values.** The user's domain, public key, HTTP login and password, and DoH and DoT
  resolver entries are supplied in chat and written only to an ignored temporary profile
  under `doc/runs/live-socks/` (mode 0700 for the directory, 0600 for the files), mounted
  read-only and deleted after the real-server runs. A public UDP resolver stands in when
  the user has none of their own. They are never passed in an argument, never logged, and
  never written to the roadmap, the journal or a tracked file. The user's real profile and
  secret store are not read.
- **Permissions.** Every fetch inside a disposable container is requested explicitly
  before it happens, with its source named: Chromium and its minimal packages in the
  client container; the `danted` source archive, its build dependencies and the account
  tooling in the server container. `--pull=never`; no other package source; nothing on the
  host. A refused permission or a missing container runtime stops the task and is
  reported; it does not fall back to a host run or a fixture result for the user's exit.
- **Host unchanged.** Before the first container and after the last one, record the IPv4
  and IPv6 routes, the `sha256sum` of `/etc/resolv.conf`, `ip -br link`, `docker ps -a`,
  `docker network ls`, the image ids and `git status --short`. The default route and the
  resolver file must match; no container, bridge, `dnstt-cli` process or interface from
  the runs may remain; the image ids must be unchanged; `git status` may show only the
  roadmap node and ignored paths. Docker's own veth pairs exist only while the containers
  run; the pre-existing Docker objects are recorded and not touched.
- **Evidence.** The checklist is `doc/local/runbook-socks-acceptance.md` (ignored), filled
  as `doc/local/runbook-socks-acceptance-filled.md`. Each check keeps its raw log or dump
  under `doc/runs/live-socks/<run-id>/` and one line in the filled checklist: `seen`,
  `not seen — <reason>` or `failed — <what happened>`, with the side (server, exit,
  resolver, network, product). Neither the line nor a tracked file carries a value,
  address, resolver, login or date.

## Children

1. [Runbook and evidence form](01-runbook/plan.md) — the checklist with placeholders and the
   result form in ignored `doc/local/`. Task; documentation only, detailed.
2. [Synthetic SOCKS5 exit](02-synthetic-socks5/plan.md) — the local server, `danted`, target
   and browser; the restart and the container network change; the clean stop and the
   host-unchanged proof. Task.
3. [Authenticated comparison](03-real-compare/plan.md) — `compare` against the real server
   over a working DoH and a working UDP resolver: the upstream tunnel plus the independent
   HTTP CONNECT probe and the in-process product, with the first real application CONNECT
   authenticated. Task.
4. [Real-server transports](04-real-transports/plan.md) — a headless browser through the
   local proxy and the real HTTP exit over DoH, DoT and UDP; the wrong-password 407. Task.
5. [Resolvers, presets and fingerprints](05-resolver-matrix/plan.md) — the dead-resolver
   check, the eight presets and the `DefaultUTLS` fingerprints through DoH and DoT. Task.
6. [Windows](06-windows/plan.md) — the same matrix on Windows when a machine is available.
   Stage, `blocked` by the user's decision.

The checklist comes first because every run records into it. The synthetic setup is
self-contained and comes before the real-server runs: it proves the container and browser
tooling and the reconnection path without the user's values. The comparison comes before
the browser transports so that a browser failure has an attributable baseline; the
resolver matrix needs only the domain and key and can run once the real server is
reachable. The Windows child is not a prerequisite of the Linux evidence.

A defect found by any child becomes a new task of this stage, named after the check that
found it; it is fixed with a test when a test can express it, and the check is rerun. An
unfixed defect keeps the stage open; none is worked around silently.

## Done when

- The checklist exists in ignored `doc/local/` and is filled from the runs: every item
  below is `seen`, `not seen — <reason>` or `failed — <what happened>`, with its side.
- **Synthetic SOCKS5.** In the disposable local setup a headless Chromium through the
  local SOCKS5 listener loads the target page; the connection recovers after a restart of
  the local `dnstt-server` and after a network change of the client container, new
  requests work without restarting `connect`, and the recovery times are recorded against
  the 30 s keepalive and the 25 s degraded threshold. The clean stop leaves no process,
  adapter, container, temporary profile or secret; the host's routes, `/etc/resolv.conf`,
  links, Docker objects and `git status` match the pre-run record, with no new bridge,
  published port or host process. The published text states that this is a synthetic
  setup, not the user's exit, server or network.
- **Real server, HTTP exit.** Over each of DoH, DoT and UDP a headless Chromium through
  the local proxy loads the target page through the server-side HTTP CONNECT exit with
  Basic authentication. `Connected` before the first real application CONNECT is tunnel
  readiness only, and that CONNECT authenticates; a wrong password gives 407, stops the
  supervisor and is not retried as a resolver failure. A missing working DoH or DoT
  resolver supplied by the user is an unmet prerequisite, recorded as such, and keeps the
  stage open. UDP is different: when the user has no resolver of their own, a public
  resolver is an acceptable substitute for the transport check, and the Outcome must say
  that the user's own UDP path was not checked.
- **Authenticated comparison.** Over a working DoH and a working UDP resolver the upstream
  tunnel plus the independent HTTP CONNECT probe and the in-process product both reach the
  target with Basic authentication, and all attempts pass; the product verdict records a
  successful first real application CONNECT, not readiness alone. An upstream
  listener-ready or Noise-only result, a local selftest, or an attributed upstream failure
  does not close this gate.
- **Dead resolver.** `resolvers check` on a list with a resolver that does not answer
  names it with its error and the others pass; on a list of dead resolvers only, the exit
  code is 1.
- **Presets and fingerprints.** Each of the eight presets completes a handshake against the
  real service or its failure is attributed to the resolver or the network; each
  fingerprint of `client.DefaultUTLS` completes a handshake through at least one DoH and
  one DoT preset, or its failure is attributed.
- **Windows.** The `blocked` child records the deferral; no Windows item is counted as
  seen, and the Linux acceptance is not described as both platforms.
- **Defects.** Every `dnstt-cli` defect found in the runs is fixed and committed with a
  test; a defect that cannot be expressed in-process is named with the live check as its
  regression. No defect is left unfixed without an open child of this stage.
- **Privacy.** The filled checklist, the raw logs and the values are only in ignored
  `doc/local/` and `doc/runs/`; the temporary profile and the stored secret are deleted;
  no domain, key, address, resolver, login, date or environment detail is in a tracked
  file.

## Risks and what is needed from the user

- **Scope.** The SOCKS5 result is synthetic, and the restart and the network change happen
  on the local server and inside the client container, not on the user's server or
  network. The synthetic setup is the user-chosen substitute for the SOCKS5 part of the
  parent's goal; the published text must say that it is not the user's exit, server or
  network, and no wording may turn it into one.
- **Values.** The user supplies the domain, public key, HTTP login and password, and the
  working DoH and DoT resolver entries in chat when the real-server work starts. They live
  only in the ignored temporary profile, are deleted after the runs, and never enter an
  argument, a log or a tracked file. If a DoH or DoT value is missing or refused, the task
  records the item as `not seen — <reason>` and stops; there is no substitute for the
  user's own DoH or DoT path. For UDP, a public resolver is an acceptable substitute when
  the user has none of their own: it proves the transport, and the Outcome states that the
  user's own UDP path was not checked.
- **Permissions.** Chromium and its packages in the client container, and the `danted`
  source archive, its build dependencies and account tooling in the server container, are
  fetched only inside disposable containers under an explicit permission recorded before
  the fetch. `--pull=never`; no host install; a refusal stops the task.
- **The user's server.** It has an HTTP proxy, not a SOCKS5 service. The HTTP scenarios
  depend on its CONNECT support, its Basic credentials and its egress to the target; a
  refusal is attributed to the exit, not to the product without the comparison.
- **Resolvers.** A DoH, DoT or UDP resolver that does not work in the user's network is an
  unmet prerequisite, not a pass. The presets use public resolvers and need container
  egress; a preset failure is attributed to the resolver or the network with its output.
- **Flakiness and attribution.** The comparison requires all attempts to pass; the first
  failure is kept, not rerun until it passes. A failure is called a bug only after the
  independent probe or a control names the side.
- **The browser.** Headless Chromium has no display and no IPv6; the result is about this
  browser and page set, not the user's browser. A browser failure is checked with a pinned
  fetch and a no-DNS control.
- **Windows.** Deferred; the `blocked` child carries it and no Windows claim is made. The
  parent stage's Windows requirement stays open until that child runs.
- **Defects.** A fix is a new child of this stage with a test; a test that cannot be
  expressed in-process is named and the live check is rerun. No silent workaround and no
  partial fix left without a node.
- **Host and containers.** Docker's veth pairs appear while a container runs; the
  after-check runs once the containers are removed. A leftover container, bridge, image
  pull or host process is a failure to record and clean, not a pass.

## Outcome

The stage was accepted on the final tree. The SOCKS5 datapath is synthetic and scoped to
the local `dnstt-server`, the `danted` built from source, the fixed-body target and the
container network; it is not the user's exit, server or network. The real-server runs used
the user's server and its HTTP CONNECT exit with Basic authentication. The acceptance is
Linux only: the Windows child is deferred and no Windows item is counted as seen.

The checklist is `doc/local/runbook-socks-acceptance.md` (ignored), filled as
`doc/local/runbook-socks-acceptance-filled.md`; S0–S4 and R0–R10 are `seen`, W1 is
`not seen` by the user's deferral. The runbook and the done children are at `77796e9`,
`bd835a5`, `c2c7db4`, `a07e33e` and `7ef8092`.

**Synthetic SOCKS5 (S1–S4).** A headless Chromium through the local SOCKS5 listener loaded
the target page. The local server restart and the client container's address change did not
require restarting `connect`, and new fetches worked. The restart recovery was 52.8 s: the
degraded state at the 25 s threshold, the unanswered keepalive at the 30 s keepalive, then
the stop/start and the redial. The address change recovered in 0 s because the SOCKS5
transport opens a UDP socket per query, so the session is not tied to the source address.
The clean stop left no product process, listener, container, profile or secret; the host
matched its pre-run record apart from external Docker churn recorded as not the run's.

**Real server, HTTP exit (R0–R10).** The temporary profile loaded from the read-only mount
and the control fetch passed. The browser loaded the target over DoH and UDP, and over DoT
after the first fetch failed on the browser side (an empty DOM and no response headers; the
control fetch through the same listener and the resolver check passed) and the planned
`--disable-quic` recheck passed, with the first failure kept. The wrong password gave 407;
the product printed the login error, stopped the supervisor and did not retry it as a
resolver failure, and with the correct password the same exit returned 200. The comparison
passed over DoH and UDP: upstream and in-process `dnstt-cli` both `PASS 3/3`, verdict `OK`,
and the product verdict records the first real application CONNECT, not readiness. The dead
resolver was named with its error in a mixed list, and a dead-only list exited 1. Seven of
the eight presets completed a real handshake; the one failure is attributed to the resolver
(both pinned 443 paths open, the same resolver's DoT preset and the other presets passing).
All five `DefaultUTLS` labels completed a handshake through a DoH and a DoT preset. R10 is
filled from the real-server transports run: the sessions stopped with SIGINT, exit 0, no
process or listener, and the container, profiles and store removed.

The UDP transport was checked over a public resolver: the user has no UDP resolver of their
own and allowed a public one as the substitute for the transport check. The user's own UDP
path was not checked; the DoH and DoT results are over the user's entries. The stage's
`Done when` and `Risks` state this split.

**Windows (W1).** Not seen: the user deferred the part until a Windows machine exists. The
`blocked` child `06-windows` records the deferral, and the Linux acceptance is not described
as both platforms.

**Defects.** No `dnstt-cli` defect was found in the runs; the kept failures (the DoT browser
fetch and the preset) are attributed to the browser and the resolver, and no child of the
stage was opened for a defect. The recorded limits are the indirect source-address proof
(the image has no `ip`/`ss`; the accepted address change did not break the session), the
SIGINT reaching the `script` wrapper rather than the product (the product exited 0, its stop
line is missing), and the external Docker churn in the synthetic host comparison.

**Privacy.** The filled checklist and the raw logs are only in ignored `doc/local/` and
`doc/runs/`; the temporary profiles and the stored secret were deleted; no domain, key,
address, resolver, login, target, date or environment detail is in a tracked file.
