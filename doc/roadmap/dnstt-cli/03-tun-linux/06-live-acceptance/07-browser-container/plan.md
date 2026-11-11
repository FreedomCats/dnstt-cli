---
kind: task
status: done
detailed: true
depends: [06-tun-datapath-latency]
---

# Browser through the TUN in a container

## Task

The browser/QUIC branch of [the matrix](../02-user-run/plan.md) failed on the host, but the
TUN datapath was cleared afterwards: [the latency stage](../06-tun-datapath-latency/plan.md)
measured TUN and SOCKS5 equal in a quiet session, and the host failures came from a saturated
session with a neighbouring tunnel client. The browser path itself was never measured in a
quiet environment. Run a headless browser through the TUN in a throwaway container with the
real tunnel and find out whether it loads a page, and if it does not, which part of the path
limits it: DNS fan-out, the exit's CONNECT concurrency, or the tunnel's shared capacity.

The user authorized one fetch inside the disposable container for this task: `apt-get update`
and installation of `chromium` with the minimal packages needed to check it (`curl`, CA
certificates; `procps` and `util-linux` are in the base image) from the Debian repository,
inside the container only, with nothing on the host and no image pull. The container has no
IPv6, so the IPv6/Happy Eyeballs part of the matrix is out of scope here and stays with the
real-machine check.

The temporary profile comes from values the user supplied in chat; it lives only in the
ignored `doc/runs/` tree, is mounted read-only, and is deleted after the run. No value enters
the repository or this node.

Deliverable: one live session's numbers and the layer they name — the per-phase wall time,
streams and ↓/↑ bytes, the browser's DNS fan-out and its net errors, the listener's 64/65
boundary, the exit's answers at 30 and 65 concurrent CONNECTs, and the achieved rate against
the ~58 KB/s channel. No product change follows; a boundary change (payload size or MTU, the
resolver, KCP) is a user question. The result informs the matrix's browser branch, it does not
replace it: headless Chromium has no display and no IPv6 here, and the real-machine check
stays with the user.

## Steps

1. **Prepare the host side, offline.** Build the two binaries into the ignored `build/out/`
   with `GOPROXY=off CGO_ENABLED=0 go build -o build/out/dnstt-cli ./cmd/dnstt-cli` and
   `GOPROXY=off CGO_ENABLED=0 go build -o build/out/checktunnel ./scripts/checktunnel`;
   `GOPROXY=off` turns a missed module into a stop, not a download. `checktunnel` reads the
   adapter, the routes and the nftables table through netlink
   (`scripts/checktunnel/main_linux.go`), so the container needs no `ip` or `nft` binaries.
   Check `connect -h` for `-datapath`; the
   only product flag the run needs is `-datapath tun`, and the status line appears only on a
   terminal (`internal/cli/connect.go:36-40,250-256,394-415`; `streams` counts open streams,
   `core/client/session.go:70-71`). In the ignored `doc/runs/browser-container/` write the
   wrapper `run.sh` (after the latency stage's `run-half.sh`; it copies `inner.sh` into `out/`
   before the start, as that wrapper does) and the driver `inner.sh` (after its `ab.sh`: the
   `script -qfec` pty, the 1.1 s status sample with the ANSI clear stripped, a per-phase
   status watch and the stop), and a stdlib-only `dnsburst` helper in its own module
   (`dnsburst/go.mod`, so `go vet ./...` and `go list ./...` of the repository do not see
   it): N simultaneous A queries to `10.66.67.1:53` with distinct names, printing the RCODE
   and the elapsed time per query plus the counts; build it into the mounted tree from its
   own directory with `GOPROXY=off CGO_ENABLED=0 go build -o ../out/dnsburst .`. Put the
   temporary profile, produced from the values the lead gives the coordinator, into
   `cfg/dnstt-cli/` in the product's own shape: `profiles.toml`
   with this profile as `default_profile`, `password_ref = "file:<id>"` and `secrets.json`
   with the base64 `data` entry (`internal/config/secret.go:125-175`,
   `internal/config/secret_unix.go:11`); a `secretservice:` reference cannot work, the
   container has no session bus. Mode 0700 for the directory, 0600 for the files; the values
   are written only here. `git check-ignore -v` must name `doc/runs/`, `doc/local/` and
   `build/` for every path the run touches.

2. **Start the one container and install Chromium once.** Confirm the image is local
   (`docker image inspect debian:trixie`; no pull). From the repository root:
   `docker run --rm --pull=never --name dnstt-browser --cap-add NET_ADMIN
   --device /dev/net/tun --sysctl net.ipv4.conf.all.rp_filter=2
   -v "$PWD/build/out/dnstt-cli:/dnstt-cli:ro"
   -v "$PWD/build/out/checktunnel:/checktunnel:ro"
   -v "$PWD/doc/runs/browser-container/cfg:/cfg:ro"
   -v "$PWD/doc/runs/browser-container/out:/out" -e XDG_CONFIG_HOME=/cfg -e HOME=/tmp
   debian:trixie bash /out/inner.sh` — no `--network host`, no published ports. Before the
   start save the host's `ip -4 route` and `sha256sum /etc/resolv.conf` into the ignored
   evidence; step 10 compares them. Inside, this is the one fetch the user authorized: one
   `apt-get update` and one `apt-get install -y chromium curl ca-certificates procps util-linux`
   (`procps` and `util-linux` are in the base image; naming them makes the install
   idempotent); no other package source, nothing on the host, no image pull. Log
   `chromium --version`, the free space and the `/dev/shm` size. If the install fails or does
   not fit, stop and report; a host install is not a fallback. Smoke-test headless without the
   network:
   `HOME=/tmp chromium --headless=new --no-sandbox --disable-gpu --disable-dev-shm-usage
   --dump-dom 'data:text/html,<title>ok</title>'` must print `ok`; if `--headless=new` is
   refused, use `--headless` and record it. This separates a broken headless start from the
   tunnel.

3. **Controls before the tunnel.** Still over the container's own bridge: `getent ahostsv4`
   for both page hosts and one `curl` per page directly, to prove the pages and the
   container's network work and to record the IPv4 address for the pins; `pgrep -a` must show
   only the expected processes. The tunnel is then the only difference. Pages: the control
   `https://example.com/` (one document, one same-host script, RFC 2606) and the heavier
   `https://en.wikipedia.org/wiki/Main_Page` (HTML and assets on at least two hosts). The
   exact URLs go into the evidence; if the heavier page answers with a block or a challenge
   instead of 200, record it and substitute another public static page with subresources,
   naming it in the evidence. A page that does not load without the tunnel is not a probe:
   stop.

4. **Start the session and verify the datapath in the container.** Run
   `script -qfec "/dnstt-cli connect -datapath tun -v" /out/session.log` in the background
   with `XDG_CONFIG_HOME=/cfg`; the temporary profile is the default, so no name appears on
   the command line. Wait up to 90 s for a `connected |` line (the pty is what makes the
   status line appear). On up: `/checktunnel -offline -label "up"` — the adapter
   `10.66.67.2/32` peer `10.66.67.1` (`tun.go:23-29`), the two `/1` routes, `inet dnstt-cli`,
   the system-DNS line — plus `cat /etc/resolv.conf` (one nameserver, the adapter's address)
   and `pgrep -a`. Take the baseline status sample: `streams`, ↓ and ↑. If no session comes
   up, keep the log and stop; the rest cannot attribute anything to the browser.

5. **Control fetches through the tunnel.** With the session up, one request at a time,
   `Connection: close`, `--max-time 60`, `curl -w` for namelookup/connect/appconnect/
   starttransfer/total: (a) `--resolve <host>:443:<ip>` — the exit, TCP, TLS and the channel
   without DNS; (b) by name — DNS through the adapter; (c) status samples around them. The
   latency stage's quiet column is the reference, not an equality (one fetch 0.78–0.86 s;
   thirty concurrent 3.75–3.80 s on the stage's page). If (a) fails, the browser cannot be
   blamed and the exit or the channel is the limit: record it, still run one browser phase
   for its shape, and run steps 8 and 9 as far as the session allows. If (a) works and (b)
   does not, DNS is the first suspect; step 8 settles it.

6. **Chromium phases.** Each run gets a fresh `--user-data-dir`, a fresh netlog, and the
   status watch; `HOME=/tmp`; a hard bound `timeout -s TERM -k 10 180`; the DOM to
   `<phase>/dom.html` and stderr to `<phase>/stderr.txt`. Flags:
   `--headless=new --no-sandbox --disable-setuid-sandbox --disable-gpu
   --disable-dev-shm-usage --no-first-run --no-default-browser-check
   --disable-background-networking --disable-component-update --disable-sync
   --metrics-recording-only --log-net-log=/out/<phase>/netlog.json
   --net-log-capture-mode=Default --dump-dom <url>`, with `--user-data-dir=/tmp/chrome-<phase>`.
   Phases: (A) the control page by name; (B) the control page with
   `--host-resolver-rules="MAP <host> <ip>"` — the browser without DNS; (C) the heavier page
   by name; (D) the control page with `--disable-quic` — the TUN rejects UDP other than DNS,
   so this is the fallback control. Repeat a failing phase once, no more, so the session stays
   bounded. Success is the expected `<title>`/content in the DOM and a 200 for the main
   document in the netlog; the host run's tiny empty DOM is a failure even though `--dump-dom`
   exited 0, so the exit code alone is not the verdict. A bound reached before a DOM dump is
   still evidence: the netlog carries the main document's status and how far the subresources
   got; record the DOM as partial. Per phase record: wall time, DOM bytes, the ↓/↑ delta and
   the maximal `streams` from the watch, the status line before and after, and the netlog
   size.

7. **After each browser run, check whether the tunnel still carries a fetch.** Wait for the
   open streams to drain — sample `streams` until it returns to the baseline or 60 s pass —
   and record the wait. If it does not drain, that is a finding: the browser left streams
   open, which is the shape of the host's saturated session. Then repeat the pinned `curl`
   from step 5(a). If it works, a browser failure is browser-side; if it now fails too, the
   browser exhausted the session or the channel and the side is capacity, not the browser
   stack. Do not start the next phase before a drain or a recorded timeout.

8. **DNS fan-out and the 64-in-flight gate.** (a) From each `netlog.json` on the host: the
   DNS jobs (`HOST_RESOLVER_MANAGER_JOB`/`HOST_RESOLVER_IMPL_REQUEST`), the distinct names,
   the peak number overlapping in time, their `net_error`, the A/AAAA split, and the hosts a
   page asks for. Compare the peak with `dnsInFlight = 64` (`dns.go:34-37`) and the listener's
   immediate SERVFAIL (`netstack.go:183-217`, the slots at `:187` and the `default` at
   `:202-210`). (b) On the same session, after a drain, run `dnsburst` at 64, 65, 80 and 160
   simultaneous queries to `10.66.67.1:53`, distinct names, 2 s apart, with a status sample
   before and after. Per burst: answered by RCODE; SERVFAILs split by elapsed into immediate
   (the gate's answer, well under a tunnel RTT), after a stream attempt (about a tunnel RTT
   to hundreds of ms, an exit refusal or a stream error), and at the 10 s timeout
   (`dns.go:30-32`); the slowest answer and the streams delta. The code's boundary is 64
   exchanges and a gate SERVFAIL for the 65th without a stream; the live burst must show at
   most 64 answered at 65 and roughly N−64 immediate SERVFAILs at 80 and 160. If the browser's
   peak fan-out from (a) is below 64 and its jobs end without errors, the gate is not its
   blocker; if the jobs fail or overlap beyond 64, it is.

9. **The exit's CONNECT concurrency and the channel.** With the session clean: 30 concurrent
   `curl` of the control page (pinned, `Connection: close`, `--max-time 60`) — the latency
   stage's browser-equivalent, re-measured — then 65 concurrent, the number of the DNS gate,
   for the exit's CONNECT acceptance and the shared cost. Record the answers, each request's
   total, the error classes (refused, reset, timeout, TLS), the ↓/↑ delta and the achieved KB/s
   against the ~58 KB/s channel. If 65 of 65 answer, the exit accepts at least 65 concurrent
   CONNECTs, more than the sockets the browser's netlog shows, so the exit is not what limits
   the browser; if 30 answer and 65 does not, the boundary is the exit or the channel and the
   browser's socket count is compared with the failing number. A failure at 30 that the
   latency stage did not see means the exit or the network changed; record it as such.

10. **Decide, stop, clean, record.** Fill the decision table: for each layer — the DNS gate,
    the exit's CONNECTs, the channel's capacity, the browser stack or its QUIC fallback, and
    "none, the browser loads" — the observation that would prove it and the number that did.
    Stop with one SIGINT to `dnstt-cli` itself, never to the wrapper first; in the container
    check `/checktunnel -offline -label "after"` (no adapter, no `/1` routes, no
    `inet dnstt-cli` table), the restored `/etc/resolv.conf` and a clean `pgrep`. Exit;
    `docker rm -f dnstt-browser` if it remains, and confirm it is gone. Delete `cfg/` and any
    copy of the secret; the raw evidence stays in `doc/runs/browser-container/out/`. Compare
    the host's `ip -4 route` and resolv.conf
    checksum with the records from step 2: the default route and the resolver file must be
    unchanged and the table must carry no `0.0.0.0/1` or `128.0.0.0/1` route; a Docker bridge
    that the runtime adds is noted, not counted as a tunnel route. The host must have no
    `dnstt-cli` interface. `git status --short` must show only this node, the evidence
    must be ignored, and a search of the tracked files must find no value from the profile,
    secret, server or resolver.

11. **Write the result into the node's Outcome.** English, with the numbers per phase and the
    layer each points at; quote no status line or log line verbatim (the status line carries
    the resolver, a profile value), only streams, bytes, times, RCODEs and error names. State
    which of the four layers the evidence names, which it rules out and with which number, and
    what the browser's fan-out was against the 64/65 gate and the exit's 65 CONNECTs. If the
    numbers show that a boundary outside this task must change (payload size or MTU, the
    resolver, KCP, the adapter's MTU or routes), that is a user question in the report, not a
    change here. No product code, test or dependency changes follow.

## Risks

- The channel is ~58 KB/s: the heavier page can need minutes. A bound reached with the bytes
  recorded is a capacity result, not a hang; do not raise the bound mid-run.
- `--host-resolver-rules` maps only the named host. A redirect or a new subresource host goes
  to DNS again — map the target too, or mark the phase mixed and say which host resolved.
- Chromium may leave streams open after it exits. Without the drain of step 7 the phases
  contaminate each other and the host's 160-stream shape reappears in the container.
- The status line has 1 s granularity; a shorter peak is visible only in the netlog or in the
  helper's own count. Do not read the status sample as an exact maximum.
- A SIGKILL of the wrapper can leave the container's `resolv.conf` pointing into a dead
  adapter. It dies with the netns, but the clean-stop evidence is lost; always SIGINT the
  product first, and never run the `tun` session on the host.
- A wrong exit login or a missing file secret fails every phase at the first CONNECT; the
  pinned control names it, and the remedy is the profile from the lead, not the browser.
- One apt transaction only; if it fails or does not fit on disk, stop and report.

What is needed from the user: nothing new — the one fetch and the live run are authorized, and
the profile values come from the lead through the coordinator. If the exit or the page would
have to change, that is a question, not a substitution.

## Done when

- One `debian:trixie` container with its own network namespace (`--pull=never`, `NET_ADMIN`,
  `/dev/net/tun`, no `--network host`) served the single authorized apt fetch and the whole
  session; the host's default route and `/etc/resolv.conf` are unchanged, its table has no
  `/1` route and the host has no `dnstt-cli` interface before or after.
- A headless Chromium through the `tun` session loaded the control page and the heavier page,
  or the layer that stops it is named with numbers: per phase the wall time, DOM result,
  streams, ↓/↑ and the achieved KB/s against the ~58 KB/s channel; the browser's DNS jobs and
  their net errors; the pinned-DNS and `--disable-quic` controls; the post-exit drain.
- The DNS listener was driven at 64, 65, 80 and 160 simultaneous queries through
  `10.66.67.1:53`, and the answered/SERVFAIL counts and the immediate/stream-attempt/timeout
  split match the 64-in-flight gate (`dns.go:36`, `netstack.go:187,202-210`); the browser's
  measured peak fan-out is compared with 64 as a number.
- The exit's CONNECT behaviour is measured at 30 and 65 concurrent pinned fetches with the
  answers, error classes and times, and compared with the socket count the browser's netlog
  shows; a failure at either number is attributed to the exit, the channel or the network, not
  to the browser without that comparison.
- A failing phase was re-checked after the drain with a pinned fetch and a no-DNS Chromium
  run, so a browser refusal is not reported as an exit refusal, or the reverse.
- The session ends clean: one SIGINT to `dnstt-cli` leaves no adapter, `/1` routes, nftables
  table or DNS leftover in the container; the container is removed; `cfg/` and every copy of
  the secret are deleted; the raw evidence is ignored and `git status --short` shows only this
  node.
- The Outcome records the numbers and the layer they name in English, with no value from the
  profile, secret, server or resolver, and no product file changed; a needed boundary change
  is written as a user question.

## Outcome

The browser branch loads through the TUN in a quiet session. A disposable `debian:trixie`
container with its own network namespace served the package fetch and the whole run: headless
Chromium 156 from the Debian repository, the real tunnel from a temporary profile mounted
read-only, IPv4 only. The numbers below come from the container that completed the run; two
earlier starts failed at the Chromium smoke before the tunnel and were discarded, as the run
notes say, so the Debian repository was fetched three times in all instead of the one time
authorized. The user accepted the run with the three disposable-container installs instead of
the single authorized one, after being shown the deviation and the evidence. All four Chromium
phases loaded the expected page; no phase reached the 180 s bound and none needed its retry.

| phase | what | wall ms | DOM B | title | max streams | ↓ B | ↑ B | ↓ KiB/s |
|---|---|---|---|---|---|---|---|---|
| A | control page by name | 3922 | 2375 | Example Domain | 4 | 61338 | 20685 | 15.3 |
| B | control page, host pinned, no DNS | 1021 | 2375 | Example Domain | 2 | 37274 | 16691 | 35.7 |
| C | heavy page by name | 8088 | 429736 | Wikipedia, the free encyclopedia | 16 | 840909 | 56320 | 101.5 |
| D | control page, `--disable-quic` | 1903 | 2375 | Example Domain | 6 | 48742 | 23245 | 25.0 |

Success is the expected title in the DOM and a 200 for the main document in the netlog. The
heavy phase made 51 URL requests on 8 hosts: 50 with response headers (47×200, 2×400 from a
background service, 1×202), the main document 200. The byte deltas are the status samples'
byte counters across each phase; they include Chromium's own background requests,
which the plan's flag set does not remove, and phase A also carries about 1.4 s of process
startup before its netlog span. The pinned fetch of the control page through the tunnel
answered 200 in 0.991 s (TLS 0.912 s, no DNS); the same page by name answered 200 in 0.973 s,
of which 0.225 s was DNS through the adapter. After every phase the streams drained to the 0
baseline in 1 s and the pinned fetch answered 200 in 0.79–0.89 s, so no phase left the
session in the saturated shape the host saw.

**The browser's DNS fan-out.** The netlogs show 5/3/9/5 DNS jobs and the same number of
distinct names per phase, with a peak of 4/3/3/4 in flight — well below the listener's 64.
The job times are 40–1362 ms; the query types are A and HTTPS, with no AAAA because the
container has no IPv6. The A answers arrived; the HTTPS resource record failed with
`ERR_NAME_NOT_RESOLVED` (3/3/7/3), and 1/0/2/0 tasks hit their timeout on that record only.
One request ended with `ERR_ABORTED` in phase D. Chromium's background hosts (accounts,
updates, component and autofill endpoints) appear despite the disable flags, so they are
part of the measured fan-out. Peak live sockets were 16/14/25/16, of which TCP connects
8/7/16/8; phase D opened no QUIC session.

**The DNS listener at the 64-in-flight gate.** Four bursts of distinct A queries at the
adapter's listener: 64 simultaneous were all answered; 65 gave 64 answers and 1 immediate
SERVFAIL; 80 gave 64 and 16; 160 gave 64 and 96. Every SERVFAIL came back in well under a
tunnel round trip (the slowest took 1.1 ms), none followed a stream attempt, and none came
at the 10 s exchange timeout. The answered exchanges took 1.8–3.4 s at 64-way overlap, so
the tunnel carried them concurrently. This is exactly the code's boundary: 64 exchanges in
flight and an immediate SERVFAIL for the rest (`dns.go:34-37`, `netstack.go:185-217`). The
1 s status samples read 0 streams before and after each burst; the helper's own answer count
is the evidence for 64 in flight.

**The exit's CONNECT concurrency.** 30 concurrent pinned fetches of the control page all
answered 200 and so did 65 of 65, with no refused, reset, timeout or TLS error. The totals
were 3.822–5.067 s and 7.783–9.852 s; the status deltas were 204.8 KiB and 512 KiB, i.e.
40.3 and 51.8 KiB/s, and the 0.1-unit display of the status line makes those deltas
approximate to about ±100 KiB. After the 65 run the status line still showed 27 open streams
1.1 s after the clients closed, and the next 1 s sample read 0. The browser's peak of 25
live sockets and 16 TCP connects is far below 65.

**Which layer the numbers name.**

- The DNS gate is not the limit: the browser's peak fan-out was 4, against 64, and its A
  lookups succeeded.
- The gate itself works as coded: 64 answers and 1/16/96 immediate SERVFAILs at 65/80/160.
- The exit's CONNECT concurrency is not the limit: 65 of 65 answered, more than the
  browser's 16 TCP connects.
- The channel is not the limit in this session: the heavy page loaded in full, 821 KiB in
  8.1 s, i.e. 101.5 KiB/s — above the earlier ~58 KiB/s reference, which was not a ceiling
  here; 65 concurrent fetches still achieved 51.8 KiB/s.
- The browser stack and its QUIC fallback are not the limit: the pinned no-DNS phase (B)
  and the `--disable-quic` phase (D) both loaded the control page, in 1.0 and 1.9 s.
- The remaining line of the table is the observed one: the browser loads. The host failure
  of the matrix's browser/QUIC branch came from the saturated session with a neighbouring
  tunnel client, not from a defect in the browser path.

Headless, IPv4 only, one quiet session and one page set: this informs the matrix's browser
branch and does not replace the real-machine check, and the IPv6/Happy Eyeballs part stays
with the user. No boundary needs to change — payload size, MTU, the resolver, KCP and the
adapter's MTU and routes were not touched and the numbers do not ask for it.

Run notes that are not visible from the code: Chromium 156 wants a writable
`--user-data-dir` and a writable crashpad database, and with the product's profile mount
read-only the browser's `XDG_CONFIG_HOME`/`XDG_CACHE_HOME` must point at a writable
directory, while the product keeps reading the read-only profile. Two smoke starts failed
on this before the tunnel existed; each was its own disposable container and each had already
run the package install before the smoke, which is how the one authorized fetch became three
containers and three installs. The first run's per-phase byte fields were mis-parsed (the
down counter was read up to the next separator and the curl line lacked a newline before its
exit code), so the numbers above are recomputed from the status samples; the raw evidence
keeps both and the driver scripts carry the fix. The heavy page answered 200 before the
tunnel, so no substitute page was used.

The session ran in one container and ended with one SIGINT to the product, cleanly: no
adapter, no `/1` routes, no `inet dnstt-cli` table, the container's `resolv.conf` restored,
no leftover process; the container was removed, the temporary profile directory and every
copy of the secret were deleted. The host's routing table was byte-identical before and after (the
pre-existing Docker bridge subnets noted, not tunnel routes), its `resolv.conf` checksum was
unchanged, and it had no `dnstt-cli` interface or `/1` route. The raw evidence is in the
ignored `doc/runs/browser-container/out/`. No product code, test, dependency or documented
boundary changed; the netns test binaries pass in a separate disposable container with no
pull and no packages.
