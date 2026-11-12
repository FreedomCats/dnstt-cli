---
kind: task
status: done
detailed: true
depends: [01-runbook, 03-real-compare]
---

# Real-server transports

## Task

A headless browser through the local SOCKS5 listener and the user's real HTTP CONNECT exit
with Basic authentication, once with only a DoH resolver, once with only DoT, once with
only UDP. The browser and the product share a disposable client container; the product
listens on `127.0.0.1:<local-port>`; Chromium starts with
`--proxy-server="socks5://127.0.0.1:<local-port>"`, and the target `<target>` (default
`https://example.com/`) is resolved at the exit. Each transport is a temporary profile
built from the values supplied in chat and stored in the ignored profile; the user's real
profile and store are not read.

The first real application CONNECT performs Basic authentication; `Connected` before it is
tunnel readiness only. With a wrong password the exit answers 407, the supervisor stops
and does not retry it as a resolver failure; this is recorded as the final exit-login
result and is not read as a resolver problem. A transport that cannot be tested (no
working resolver supplied, the container cannot reach it) is recorded as
`not seen — <reason>`, not silently skipped. The run records into the
[runbook](../01-runbook/plan.md) and follows the [stage design](../plan.md#run-design).

## Steps

1. **Preflight: run id, host baseline, permission.** Read the runbook's R0–R4 and the
   stage's [run design](../plan.md#run-design). Take the values from the chat message —
   `<domain>`, `<pubkey>`, HTTP Basic `<http-login>` and `<http-password>`, the working
   `<doh-resolver>`, `<dot-resolver>` and `<udp-resolver>`, and the user's working
   `<target>` (it replaces the default `https://example.com/`; record the substitution).
   Choose the next free `<run-id>` under `doc/runs/live-socks/` (`real-2` after the
   comparison's `real-1`; never reuse or overwrite `real-1`), `umask 077`, and create
   `doc/runs/live-socks/<run-id>/` with `cfg/` and `out/` (0700). `git check-ignore -v`
   must name `doc/runs/live-socks/<run-id>/`, `doc/runs/logs/`,
   `doc/local/runbook-socks-acceptance-filled.md` and `build/out/`; never `git add -f`.
   Write `<run-id>/host-before.txt`: `ip -4 route`, `ip -6 route`,
   `sha256sum /etc/resolv.conf`, `ip -br link`, `docker ps -a`, `docker network ls`,
   `docker image inspect debian:trixie` (id and repo digests),
   `docker info --format '{{.ServerVersion}}'`, `git status --short`; save the image id in
   `<run-id>/image-id.txt`. The Chromium fetch in the client container is a new fetch: ask
   the user for an explicit permission in chat, naming one `apt-get update` and one
   `apt-get install -y chromium curl ca-certificates` from the image's Debian repository,
   no other source, `--pull=never`, nothing on the host. The synthetic task's permission
   does not carry over. Record the request and the answer in `<run-id>/permission.txt`; a
   refusal stops the task and is reported, with no host install and no fallback.

2. **Build offline.** With `GOTOOLCHAIN=go1.26.2 GOPROXY=off GOSUMDB=off CGO_ENABLED=0
   GOOS=linux GOARCH=amd64` and `-mod=readonly`: `go build -o build/out/dnstt-cli
   ./cmd/dnstt-cli` and `go build -o build/out/live ./doc/runs/_live`. Check both with
   `file`: Linux amd64, statically linked. Use the module's required toolchain through
   `GOTOOLCHAIN` (no machine path in a tracked file). A missing module or toolchain is not
   a reason to fetch: stop and report. The container has no Go; nothing is built there.

3. **Temporary profiles and file-backed store.** Create `<run-id>/cfg/dnstt-cli/` (0700)
   with `profiles.toml` (0600): `default_profile = "real-doh"`; four profiles `real-doh`,
   `real-dot`, `real-udp` and `real-407`, each with `domain = "<domain>"`,
   `pubkey = "<pubkey>"`, `datapath = "socks"`, `socks.listen = "127.0.0.1:1080"` and
   `resolvers` = only the matching entry (`<doh-resolver>`, `<dot-resolver>`,
   `<udp-resolver>`, and `<doh-resolver>` again for `real-407`); `[profile.*.exit]` with
   `type = "http"` and `user = "<http-login>"`; leave `password_ref` empty until the store
   is written. Store the correct password from stdin, never as an argument: pipe it from a
   0600 temporary file under `<run-id>/` (created for this write, deleted at once) into
   `env -u DBUS_SESSION_BUS_ADDRESS -u XDG_RUNTIME_DIR XDG_CONFIG_HOME=<run-id>/cfg
   <repo>/build/out/live secret -profile real-doh`; clear every shell variable and
   temporary copy immediately. Put the printed `file:<id>` reference into the
   `password_ref` of `real-doh`, `real-dot` and `real-udp`; confirm the printed store kind
   is `secrets.json, file with mode 0600`, with the store at 0600 next to `profiles.toml`.
   Generate a wrong password for `real-407` (`head -c 16 /dev/urandom | base64`), store it
   the same way with `-profile real-407`, and put its reference only into `real-407`. The
   values and both passwords never enter an argument, a log, a console capture, the host's
   shell history or a tracked file; the run scripts are mode 0600.

4. **Client container and the one install.** Confirm the image is local; run
   `docker run -d --name <client> --pull=never --user $(id -u):$(id -g)` from the local
   `debian:trixie`, command `sleep infinity`, no `--network host`, no published ports, no
   `docker network create`; the default bridge only. Mount
   `build/out/dnstt-cli:/work/dnstt-cli:ro`, `<run-id>/cfg:/cfg:ro` and
   `<run-id>/out:/out`; nothing else from the repository and nothing from the host's real
   config. Record `docker inspect` mounts and the image id. Run the one authorized install
   as root: `docker exec -u 0 <client> apt-get update` and
   `docker exec -u 0 <client> apt-get install -y chromium curl ca-certificates`; no other
   package source, no host install. Record `chromium --version`, `curl --version`,
   `dpkg -s ca-certificates`, `test -s /etc/ssl/certs/ca-certificates.crt` (the image ships
   no CA store; the install must provide it — if the file is missing, stop and report, do
   not assume the product or the browser can verify TLS), the free space and the `/dev/shm`
   size. Smoke-test headless without the network:
   `HOME=/tmp chromium --headless=new --no-sandbox --disable-gpu --disable-dev-shm-usage
   --dump-dom 'data:text/html,<title>ok</title>'` must print `ok`; if `--headless=new` is
   refused, use `--headless` and record it. If the install or the smoke fails, stop and
   report; no host fallback. The profile's datapath is `socks`: no `tun` session and no host
   interface are part of this task.

5. **R0 — profile load, read-only mount, control fetch.** With `XDG_CONFIG_HOME=/cfg` and
   `HOME=/tmp`: `/work/dnstt-cli profile list` exits 0 and lists the four profiles →
   `<run-id>/r0-profile.txt`; `grep ' /cfg ' /proc/mounts` shows `ro`; `touch
   /cfg/dnstt-cli/x` fails → `<run-id>/r0-readonly.txt`; a `find / -path /proc -prune -o
   -path /sys -prune -o -path /dev -prune -o \( -name profiles.toml -o -name secrets.json
   \) -print` shows only `/cfg/dnstt-cli/…`. Do not stat, open or mount the host's real
   config path. Without the tunnel:
   `curl -sS -o /out/r0-control.html -w 'status=%{http_code} bytes=%{size_download}
   total=%{time_total}\n' '<target>' > <run-id>/r0-control.txt 2>&1`; expect `200` (below
   400) and a non-zero body. Extract the expected `<title>` from `r0-control.html` into
   `<run-id>/r0-title.txt`; if the target redirects, record the final URL and take the
   final page. If the control fetch fails, record the side (target, container network) and
   stop: the transports cannot be tested without the target. Fill R0's `Result:` line with
   this run's evidence; it stays `seen` and the comparison run's line is not weakened.

6. **R1 — DoH, the browser phase.** Write `<run-id>/r1-run.sh` (0600) with the real target
   and commands so they do not enter the host's shell history. Start the session detached:
   `docker exec -d -e XDG_CONFIG_HOME=/cfg -e HOME=/tmp <client> bash -c 'script -qefc
   "/work/dnstt-cli connect -v real-doh" /out/r1-session.log; echo $? > /out/r1-exit.txt'`.
   Record `phase_start`; poll `tr '\r' '\n' < <run-id>/r1-session.log | rg -q 'connected \|'`
   up to 90 s and record `connected_at`. If no `connected` comes, stop the session, run
   `/work/dnstt-cli resolvers check real-doh` → `<run-id>/r1-resolvers.txt`, fill R1 as
   `not seen — <reason>` with the side, and go to the next transport without the browser.
   Then run Chromium with the accepted synthetic flags (fresh `--user-data-dir=/tmp/chrome-r1`,
   `--headless=new --no-sandbox --disable-setuid-sandbox --disable-gpu
   --disable-dev-shm-usage --no-first-run --no-default-browser-check
   --disable-background-networking --disable-component-update --disable-sync
   --metrics-recording-only --log-net-log=/out/r1-netlog.json
   --net-log-capture-mode=Default --proxy-server="socks5://127.0.0.1:1080" --dump-dom
   '<target>'`), `HOME=/tmp`, `timeout -s TERM -k 10 180`; DOM to `<run-id>/r1-dom.html`,
   stderr to `<run-id>/r1-stderr.txt`; record `fetch_start`, `fetch_end`, `wall_ms`,
   `dom_bytes` and `netlog_bytes` in `<run-id>/r1-phase.txt`. Success: the expected title
   and body in the DOM (the exit code alone is not the verdict; an empty or tiny DOM is a
   failure), and the main document's status in the netlog
   (`python3 -I <run-id>/netlog.py -o <run-id>/r1-netlog.txt -m '<target>'
   <run-id>/r1-netlog.json`, using a copy of the ignored
   `doc/runs/browser-container/netlog.py`); the browser's background requests are noise,
   not the page result, and the main document's 200 through the authenticated exit is a real
   application CONNECT; the first application CONNECT, target or background, authenticates.
   R4 shows the same exit returns 407 for a wrong password, so the 200 is not anonymous.
   Extract the status lines with `tr '\r' '\n' <
   <run-id>/r1-session.log | rg -o '(connected|degraded) \|.*' >
   <run-id>/r1-status.txt`; record the first and last byte counters, their delta, the
   maximum `streams`, and the netlog's socket fan-out. The 1 s status sample may show 0
   streams for a short fetch; say so and use the netlog and the byte delta as the primary.
   The status line names the resolver; it stays in the ignored evidence and is quoted with
   the placeholder elsewhere. If the browser fails, run the control fetch through the
   listener (`curl --socks5-hostname 127.0.0.1:1080 -o /out/r1-control.html` with the same
   `-w` fields and `<target>`); if it passes, the side is the browser; if it fails, run
   `resolvers check real-doh`: a passing check names the exit or the product, a failing one
   the resolver or the network. Recheck the failed phase once with `--disable-quic` and
   record it; keep the first failure. Wait for the streams to drain (up to 60 s), stop the
   session with `docker exec <client> pkill -INT -x dnstt-cli` (never `docker stop` first),
   wait for `r1-exit.txt`, and confirm `docker top` shows no `dnstt-cli` before the next
   phase. Fill R1.

7. **R2 — DoT.** The same as step 6 with the profile `real-dot`, the files `r2-*`, and the
   control `resolvers check real-dot`. DoT has not been tested before: the comparison
   baseline exists only for DoH and UDP. If the control fetch passes and the browser fails,
   the side is the browser; if the control fetch fails while `resolvers check real-dot`
   passes, the side is the exit or the product's DoT path (a product defect becomes a child
   of the stage with a test; do not fix it here); if the resolver check fails, the side is
   the resolver or the network. Keep the first failure. Fill R2.

8. **R3 — UDP.** The same as step 6 with the profile `real-udp`, the files `r3-*`, and the
   control `resolvers check real-udp`. The comparison passed over UDP, so a failure is read
   against that baseline with the same controls; keep the first failure and fill R3.

9. **R4 — first-CONNECT authentication and the wrong-password 407.** Use `real-407` with
   the first transport that came up (the DoH entry if R1 passed; otherwise rewrite its
   `resolvers` to the first passing transport before starting and record it), the correct
   login and the generated wrong password. If no transport came up, R4 is
   `not seen — no working transport` and the side is recorded. Start the session with
   `script` as in step 6 (files `r4-*`), wait for `connected` (readiness) and record
   `connected_at`; run Chromium once with the proxy and `--dump-dom '<target>'`, timeout
   120 s. The first real application CONNECT, whether the target or a background request,
   gets 407; the product prints `dnstt-cli connect: the HTTP exit refused authentication
   (status 407)`, stops the supervisor and exits. Record the exit code in `r4-exit.txt`,
   the session log, the status lines, and the line in `r4-wrong.txt`; expect the login
   error, not a resolver failure: no `restarting:` or a new `dialing` after the 407, one
   session only, the listener gone. The `Connected` line before that CONNECT is readiness
   only; with the passing transport's 200 through the same exit, the pair shows the exit
   authenticates and the 200 is not anonymous. If no application CONNECT appears in the log,
   record `not seen — no application CONNECT` and the side. Fill R4.

10. **Clean stop, evidence index, host check, checklist.** Stop and remove `<client>`;
    confirm no `dnstt-cli` process, no SOCKS5 listener, no container, no published port and
    no new bridge from the run. Delete `<run-id>/cfg` (profiles and `secrets.json`) and
    every copy of both passwords this run made; confirm the host's real profile and store
    were never mounted, opened or statted. List the evidence in `<run-id>/index.txt` (per
    phase the DOM, netlog, netlog summary, session, status, exit, control and resolver-check
    files, plus the host files and the permission) and write `host-after.txt` with step 1's
    commands; compare: routes, `/etc/resolv.conf` checksum and links match; no container,
    network or image change; `git status --short` shows only the roadmap node and ignored
    paths. External Docker churn from other projects is recorded as not the run's. Fill the
    cleanup line and R1–R4 in `doc/local/runbook-socks-acceptance-filled.md`, appending this
    run to R0; R10 is filled at the stage level from this run. A product
    defect found here is not fixed in this node: it becomes a new child of the stage, named
    after the check that found it, with a test; the first failure is kept. No tracked file
    gains a domain, key, address, resolver, login, target or date; the Outcome uses the
    placeholders.

## Done when

- Over each of DoH, DoT and UDP, a headless Chromium through the local proxy loaded the
  target page through the HTTP exit with Basic authentication, or the transport is recorded
  as `not seen — <reason>` with the side; per phase the DOM and title, the netlog status,
  the wall time, streams and bytes are recorded, and R1–R4 are filled in the ignored filled
  checklist.
- The Chromium install in the client container happened only after the permission was
  requested and recorded, with its source named; a refusal would have stopped the task;
  no host install and no image pull.
- The session evidence shows that `Connected` before the first real application CONNECT is
  readiness only and that the CONNECT authenticates: the same exit returns 200 with the
  correct password and 407 with the wrong one; the 407 stopped the supervisor and was not
  retried as a resolver failure.
- A failing transport is attributed to the resolver, the network, the exit or the product
  with a control, not called a product defect without it; a product defect becomes a child
  of the stage with a test.
- The clean stop and the host check pass; the temporary profile and store are deleted; no
  domain, key, address, resolver, login, target or date is in a tracked file.

## Outcome

The three real-server transports were accepted in one `real-2` run: a headless Chromium
through the local SOCKS5 listener and the user's real HTTP CONNECT exit with Basic
authentication loaded the target over DoH, DoT and UDP, and the wrong-password phase showed
the same exit answering `407` and the product stopping on the login error instead of
retrying it as a resolver failure. One disposable client container from the local
`debian:trixie` image (`--pull=never`, default bridge, no published ports) ran the product
and Chromium against four temporary profiles mounted read-only; the values came from chat
into the ignored profile, which was deleted after the run. The user's working example target
replaced the task's default `https://example.com/`, and the control fetch confirmed it
outside the tunnel.

**R0.** The four temporary profiles loaded from the read-only mount (`profile list` exited
0 and named them); a write under `/cfg` failed with `Read-only file system`; the control
fetch from the container without the tunnel answered `200` with a non-zero body (13 B); a
`find` for `profiles.toml` and `secrets.json` showed only `/cfg/dnstt-cli/…`. The correct
and the wrong password both went to the file-backed store (`secrets.json, file with mode
0600`) through stdin, never an argument; the host's real profile and store were never
mounted, opened or statted. The Chromium/curl/CA install in the client container happened
once, after the user's explicit permission recorded in the run's `permission.txt`, from the
image's Debian repository, with no other source and nothing installed on the host.

**R1 (DoH).** The browser loaded the target through the authenticated exit: the main
document `200` in the netlog, the DOM 166 B (the plain-text target echoes the exit address,
so it has no title), wall 1.95 s; the readiness `connected` preceded the fetch. The status
samples peaked at 3 streams, the byte counters moved from 50 B/50 B to 60.7 KB down/18.3 KB
up, and the netlog showed 10 TCP and 2 UDP connects, 3 HTTP/2 sessions and no QUIC. The
browser's background requests were noise, not the page result.

**R2 (DoT).** The first browser fetch failed: an empty DOM and the main document
`request without response headers` after 16.3 s, the target's stream closed without headers
(`ERR_CONNECTION_CLOSED` in the netlog), with the status samples peaking at 6 streams. The
control fetch through the same listener passed (`200`, 14 B, 1.27 s) and `resolvers check`
passed (254 ms), so the side of the first failure is the browser. One recheck of the same
browser phase with `--disable-quic` passed: the main document `200` and the DOM 166 B in
13.1 s, so the transport is recorded as seen; the first failure is kept in the evidence and
was not overwritten, and the later phases used the accepted flags. No product defect was
found.

**R3 (UDP).** The browser loaded the target over the supplied public UDP resolver (the
first one tried; no fallback was needed): the main document `200`, the DOM 166 B, wall
1.95 s, the status samples peaking at 3 streams and 31.8 KB down/15.9 KB up over 12 sockets,
no QUIC.

**R4 (wrong password).** With the wrong password in the temporary profile's store, the
readiness `connected` came before any application CONNECT; the first real CONNECT (a
background request) got `407`, the product printed
`dnstt-cli connect: the HTTP exit refused authentication (status 407)` and exited 1, with no
`restarting:` and no new `dialing` after the 407, one session only and the listener gone;
the browser's page fetch failed as expected. With the correct password the same exit
returned `200` in R1–R3, so those pages were not anonymous.

**Stop, cleanup and host check.** The R1–R3 sessions were stopped with SIGINT through
`/proc` (the image has no `pkill`), waited for their exit files and showed no `dnstt-cli`
process and no listener before the next phase. The client container was stopped and
removed; the temporary profiles, their file store and every copy of both passwords were
deleted; the host record matches the baseline on the routes, the `/etc/resolv.conf`
checksum, the links, the Docker networks and the image id, and `git status --short` showed
only this roadmap node, the only difference being another project's container age. `R10`
is filled at the stage level from this run.

Deviations and run notes: the module's required toolchain was used offline as in the
comparison; `GOTOOLCHAIN=go1.26.2` with `GOSUMDB=off` failed before any fetch, and the
build record keeps the working command.
`pkill` is absent from the image, so a SIGINT through `/proc` stopped the sessions. R2's
recheck is the one planned `--disable-quic` retry. The target is plain text, so the title
check is recorded as not applicable and the DOM is the page result. No values, resolvers,
logins or exit addresses are in this Outcome; they stay only in the ignored evidence, and
the temporary profiles were deleted with the run.
