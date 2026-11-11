---
kind: task
status: blocked
depends: [01-runbook]
detailed: true
---

# Authenticated comparison

## Task

The attribution gate on the user's real server, with the HTTP CONNECT exit and Basic
authentication. The ignored `doc/runs/_live/` tool's `compare` runs the upstream
`dnstt-client` through the tunnel with its independent HTTP CONNECT probe, and the
in-process product through its local SOCKS5 listener; over one working DoH resolver and
one working UDP resolver, every attempt on both sides must reach the target through the
authenticated exit. The result is what later browser failures are attributed against, and
it is not replaced by the browser runs.

`Connected` before the first real application CONNECT is tunnel readiness only; the
product verdict must record a successful first real application CONNECT, not readiness
alone. A 407 or another refusal fails the check and is not counted as a pass. An upstream
listener-ready or Noise-only result, a local selftest, or an attributed upstream failure
does not close this gate.

The user supplies the domain, public key, HTTP login and password, and the DoH and UDP
resolver entries in chat when the work starts; they live only in an ignored temporary
profile and file-backed store under `doc/runs/live-socks/`, mounted read-only into a
disposable client container, and are deleted after the real-server runs. The user's real
profile and secret store are not read. The upstream client is built from the sibling
checkout with cached dependencies (or a ready binary is used); the target is `<target>`
(default `https://example.com/`), resolved at the exit. The run records into the
[runbook](../01-runbook/plan.md) and follows the [stage design](../plan.md#run-design).

## Blocked

The user's real values are not in chat yet: the domain, the public key, the HTTP login and
password, one working DoH resolver and one working UDP resolver. The task resumes when the
user supplies them; the coordinator writes them only into the ignored temporary profile and
deletes them after the runs.

## Steps

1. **Preflight: run id, host baseline, privacy rails.** Read the runbook's R0, R5 and R6
   and the stage's [run design](../plan.md#run-design). Take the values from chat —
   `<domain>`, `<pubkey>`, HTTP Basic `<http-login>` and `<http-password>`, one working
   `<doh-resolver>` and one working `<udp-resolver>` — and keep them out of arguments,
   shell history and every tracked file. Choose `<run-id>` (`real-1`, the next free under
   `doc/runs/live-socks/`), `umask 077`, and create `doc/runs/live-socks/<run-id>/` with
   mode 0700. `git check-ignore -v` must name `doc/runs/live-socks/<run-id>/`,
   `doc/runs/logs/` and `doc/local/runbook-socks-acceptance-filled.md`; never
   `git add -f`. Write `<run-id>/host-before.txt` with `ip -4 route`, `ip -6 route`,
   `sha256sum /etc/resolv.conf`, `ip -br link`, `docker ps -a`, `docker network ls`,
   `docker image inspect debian:trixie` (id and repo digests),
   `docker info --format '{{.ServerVersion}}'`, `git status --short`; keep the image id in
   `<run-id>/image-id.txt`. This task installs nothing inside a container: the two user
   permissions (Chromium in the client container; the `danted` archive and build
   dependencies in the server container) are not used here, and a step that would need a
   fetch stops and is reported instead.

2. **Build the binaries on the host, offline.** From `<repo>`, with
   `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local CGO_ENABLED=0 GOOS=linux GOARCH=amd64` and
   `-mod=readonly`: `go build -o build/out/live ./doc/runs/_live`,
   `go build -o build/out/dnstt-cli ./cmd/dnstt-cli`, and the upstream client
   `(cd ../dnstt/dnstt-client && go build -mod=readonly -o <repo>/build/out/dnstt-client .)`.
   Check each with `file`: Linux amd64, statically linked. A missing cached module or
   toolchain is not a reason to fetch: stop and report. Record `git -C ../dnstt rev-parse
   HEAD` beside `core/CORE_VERSION` in `<run-id>/upstream-revision.txt` with a
   `match`/`differs` line: `-client-bin` skips the tool's own revision check
   (`doc/runs/_live/upstream.go:36`), so this host record stands in for it. The client
   container has no Go: nothing is built there.

3. **Control-fetch helper (run artifact, not product code).** Write
   `doc/runs/_evidence/real-compare/control-fetch.go` with the standard library only:
   `-url` and `-timeout` (default `30s`), follows redirects, reads and discards the body,
   prints `status=<code> bytes=<n>`, never echoes the URL. It performs the R0 control fetch
   on an image that has no `curl`, and needs no package fetch. Build it to
   `build/out/control-fetch` with the flags of step 2. Like
   `doc/runs/_evidence/synthetic-danted/target/target.go`, it is an ignored run artifact:
   no product code, test or dependency changes.

4. **Temporary profile and file-backed store.** Create `<run-id>/cfg/dnstt-cli/` (0700)
   with `profiles.toml` (0600): `default_profile = "real"`; `[profile.real]` with
   `domain = "<domain>"`, `pubkey = "<pubkey>"`, `datapath = "socks"`,
   `resolvers = ["<doh-resolver>"]`; `[profile.real.exit]` with `type = "http"` and
   `user = "<http-login>"`; no `password_ref` yet. Store the password from stdin, never as
   an argument:
   `env -u DBUS_SESSION_BUS_ADDRESS -u XDG_RUNTIME_DIR XDG_CONFIG_HOME=<run-id>/cfg <repo>/build/out/live secret -profile real`
   with the value piped in; clear any shell variable or temporary copy immediately. Put the
   printed `file:<id>` reference into `password_ref` and confirm the printed store kind is
   `secrets.json, file with mode 0600`, with the store file at 0600. The store must stay
   next to `profiles.toml` (`cfg/dnstt-cli/secrets.json`): `compare` resolves it from the
   config path. The password never enters an argument, a log, a console capture or evidence.

5. **Client container.** `docker run -d --name <client> --pull=never --user $(id -u):$(id -g)`
   from the local `debian:trixie`, no `--network host`, no published ports, no
   `docker network create`, command `sleep infinity`. Mounts:
   `<repo>/go.mod:/work/go.mod:ro` (the tool finds its repository root by `go.mod` and
   writes logs under it — `doc/runs/_live/main.go:201`),
   `build/out/live:/work/live:ro`, `build/out/dnstt-client:/work/dnstt-client:ro`,
   `build/out/dnstt-cli:/work/dnstt-cli:ro`,
   `build/out/control-fetch:/work/control-fetch:ro`, `<run-id>/cfg:/cfg:ro`,
   `/etc/ssl/certs/ca-certificates.crt:/cacert.pem:ro` (the image ships no CA store; the
   control fetch sets `SSL_CERT_FILE`), and
   `<repo>/doc/runs/logs:/work/doc/runs/logs` — the only writable host path, where the tool
   puts its private logs (`doc/runs/_live/log.go:95`); create that directory on the host
   first if it is missing, so Docker does not create a root-owned one. Every exec runs with
   `-w /work`.
   Record `docker inspect` mounts and the image id; confirm `/cfg` is read-only and that
   neither the repository tree nor the host's real config is mounted. If the image lacks a
   program these steps use, stop and report; do not install.

6. **R0 — profile load, read-only mount, control fetch.** With `XDG_CONFIG_HOME=/cfg` and
   `HOME=/tmp`: `/work/dnstt-cli profile list` exits 0 and lists `real` (loading validates
   every profile; the output is names only) → `<run-id>/r0-profile.txt`; `grep ' /cfg '
   /proc/mounts` shows `ro`, and `touch /cfg/dnstt-cli/x` fails →
   `<run-id>/r0-readonly.txt`. Without a proxy:
   `SSL_CERT_FILE=/cacert.pem /work/control-fetch -url '<target>'` →
   `<run-id>/r0-control.txt`; expect `status=200` (below 400) and a non-zero body. Run
   `find / -path /proc -prune -o -path /sys -prune -o -path /dev -prune -o \( -name
   profiles.toml -o -name secrets.json \) -print`; it must show only `/cfg/dnstt-cli/…`.
   Do not stat, open or mount the host's real config path. Fill R0's `Result:` in the
   filled runbook.

7. **R5 — comparison over DoH.** Put the command into `<run-id>/run-r5.sh` (0600; ignored)
   so the resolver and the target do not enter the host's shell history:
   `docker exec -w /work -e XDG_CONFIG_HOME=/cfg -e HOME=/tmp <client> timeout -s TERM -k 10 900 /work/live compare -profile real -resolver '<doh-resolver>' -url '<target>' -n 3 -timeout 90s -v -client-bin /work/dnstt-client > <run-id>/r5-compare.txt 2>&1`,
   and record the exit code in `<run-id>/r5-exit.txt`. Expect `upstream PASS 3/3` and
   `dnstt-cli PASS 3/3`, every attempt with the target's `200` and body bytes, and the
   verdict `OK`; the capture also shows the supervisor's `connected` transition *before*
   the first attempt, which is readiness only. The product's `200` through the
   authenticated HTTP exit is the first real application CONNECT; a 407 appears as `the
   HTTP exit refused authentication (status 407)` with a stopped supervisor and fails the
   check. The two expected notes (`-client-bin` revision unverifiable; different default
   uTLS distributions for DoH) are recorded, not treated as failures. Exit code 0 alone is
   not a pass: `not dnstt-cli` also exits 0 (`doc/runs/_live/compare.go:73`); require the
   `OK` verdict and both `PASS` lines. Keep the first failure; a diagnostic rerun with
   `-utls` may narrow a DoH difference but does not replace it. Fill R5.

8. **R6 — comparison over UDP.** The same command in `<run-id>/run-r6.sh`, with
   `-resolver '<udp-resolver>'` → `<run-id>/r6-compare.txt` and `r6-exit.txt`; the profile
   is unchanged between the runs, so the override is the only difference. Same
   expectations and record. Fill R6.

9. **Clean stop, evidence index, host check.** Stop and remove `<client>`; confirm no
   `live`, `dnstt-cli` or `dnstt-client` process, no container, no published port and no new
   bridge from the run. List the private logs created by these runs (`find doc/runs/logs
   -newer <run-id>/host-before.txt -type f`), their modes and the four transport/side
   names in `<run-id>/index.txt`; resolver entries stay only in those logs. Delete
   `<run-id>/cfg` (profile and `secrets.json`) and every copy of the password, and confirm
   that the host's real profile and store were never mounted, opened or statted. Write
   `<run-id>/host-after.txt` with step 1's commands and compare: routes, resolver checksum
   and links match; no container, network or image change; `git status --short` shows only
   the roadmap node and ignored paths. External Docker churn from other projects is
   recorded as not the run's, as in the synthetic run. Fill the cleanup line and R0, R5 and
   R6 in `doc/local/runbook-socks-acceptance-filled.md`; R10 stays with the later
   real-server run. A product defect found here is not fixed in this node: it becomes a new
   child of the stage, named after the check that found it.

## Done when

- `compare` ran in a disposable container with the temporary profile, over a working DoH
  and a working UDP resolver; every attempt on the upstream side and on the in-process
  product side passed and fetched the target through the authenticated HTTP exit.
- The temporary profile loaded from the read-only mount in the client container, and the
  control fetch from the container without the tunnel answered 200.
- The product verdict records a successful first real application CONNECT with Basic
  authentication, not `Connected` or readiness alone; a refusal or a 407 would be a
  failure, not a pass.
- A failure, if any, is attributed to the server, the exit, the resolver, the network or
  the product with the tool's output and a control; the side is named; the first failure
  is kept, not rerun until it passes.
- The temporary profile and store are deleted; the user's real profile and store were not
  read, statted or opened; no domain, key, address, resolver, login or date is in a
  tracked file; the clean stop and the host check pass.
