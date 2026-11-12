---
kind: task
status: done
detailed: true
depends: [01-runbook]
---

# Resolvers, presets and fingerprints

## Task

The resolver-facing checks on the real server, from a temporary profile that needs only
the domain and public key (no exit credentials): `resolvers check` with a dead resolver in
the list and with only dead resolvers; the eight built-in presets; and every fingerprint
of `client.DefaultUTLS` through at least one DoH and one DoT preset.

The dead resolver is a documentation address (`udp:192.0.2.53:53`, TEST-NET-1) or another
address that does not answer; the mixed list also has one of the user's working resolvers.
The check uses the final 10 s handshake timeout. The presets are the four DoH and four DoT
entries of `internal/resolvers/presets.go`; a preset that fails is attributed to the
resolver or the network (for example, a blocked port), not to the product without
evidence. The fingerprints are `random`, `Chrome_131`, `Firefox_120`, `Safari_16_0` and
`iOS_14` (`DefaultUTLS`); for each, a profile sets `utls = "<label>"` and a resolver list
of one DoH and one DoT preset. The run records into the [runbook](../01-runbook/plan.md)
and follows the [stage design](../plan.md#run-design).

## Risks and easy mistakes

- **The dead resolver must fail.** `udp:192.0.2.53:53` is TEST-NET-1 (RFC 5737): nothing
  answers it. A pass of that entry means the check reached something else; that is a
  failing check, not a pass.
- **A resolver failure is not a product failure.** The side is named before anything is
  called a bug: the working entry passes in the same run, the other transport of the same
  class passes, a control outside the product says whether the port is open, and the error
  text says which layer failed.
- **The image has no CA store.** `debian:trixie` ships none (the real-server transports had
  to install `ca-certificates`); DoH and DoT verify the resolver's certificate against the
  system roots (`core/client/utls.go:72`, `core/client/transport.go:263`), and Go reads
  `SSL_CERT_FILE`. Without the read-only CA mount every preset fails with `x509:
  certificate signed by unknown authority` — the container's trust store, not the
  resolver; the `sha256sum` control tells them apart. Do not install the package, and do
  not fall back to a host run.
- **The exit code is not the verdict.** `resolvers check` exits 0 when one resolver of the
  profile passed (`internal/cli/resolvers.go:149-152`), so every check is read line by
  line, not by `rc` alone.
- **A resolver host name is resolved without the system.** The bootstrap asks the network
  DNS, and on Linux loopback nameservers are left out
  (`internal/linuxsys/networkdns_linux.go:19-44`); a default-bridge container's
  `/etc/resolv.conf` names Docker's `127.0.0.11`, so an unpinned host name fails with a
  bootstrap error — the environment, not the resolver. Presets carry pins and the dead
  entry is an address; the user's working entry is pinned as in step 3.
- **`utls` is not validated when a profile loads.** A misspelt label fails at the handshake
  with a parse error naming it: use the exact labels of `DefaultUTLS`
  (`core/client/session.go:33`) from the map in `core/client/utls.go:21-53`.
- **`random` is a new hello every time** (`utls.HelloRandomizedALPN`); one failure there is
  attributed by the diagnostic through the user's working resolver, and the first failure
  is kept, not retried until it passes.
- **The profile is a secret.** It holds the domain, the public key and the resolver entries;
  it lives only in the ignored run directory at 0600, is mounted read-only, is never passed
  in an argument, and is deleted at the end. `resolvers check` prints resolver names, so
  its output is ignored evidence only.
- **Nothing is built or installed in the container**, and the host's routes, DNS, firewall
  and interfaces are not touched. `timeout`'s exit 124 is a harness failure, not a resolver
  result. The datapath is `socks` with nothing listening: no `tun`, no `connect`.

## Steps

1. **Preflight: values, run id, rails, host baseline, permission.** Read the runbook's
   R7–R9 (`doc/local/runbook-socks-acceptance.md`), the stage's
   [run design](../plan.md#run-design) and the outcomes of `03-real-compare` and
   `04-real-transports`. Take from chat only `<domain>`, `<pubkey>`, the working
   `<working-resolver>` (a DoH entry) and, when supplied, the working
   `<working-dot-resolver>`; keep them out of arguments, the shell history, logs and every
   tracked file. Choose the next free `<run-id>` under `doc/runs/live-socks/` (`real-1` is
   the comparison, `real-2` the transports; `real-3` unless taken), `umask 077`, and create
   `doc/runs/live-socks/<run-id>/cfg/dnstt-cli/` (0700 each). `git check-ignore -v` must
   name `doc/runs/live-socks/<run-id>/`, `doc/local/runbook-socks-acceptance-filled.md` and
   `build/out/dnstt-cli`; never `git add -f`. Write `<run-id>/host-before.txt` with the
   commands of the stage design (`ip -4 route`, `ip -6 route`,
   `sha256sum /etc/resolv.conf`, `ip -br link`, `docker ps -a`, `docker network ls`,
   `docker image inspect debian:trixie` id and repo digests,
   `docker info --format '{{.ServerVersion}}'`, `git status --short`), the image id into
   `<run-id>/image-id.txt`, and `<run-id>/run-head.txt` with `git rev-parse HEAD` and
   `git status --short`; a change outside `doc/` and the ignored paths stops the task — the
   binary must come from the reviewed tree. No permission is requested for this run: it
   fetches and installs nothing — no browser, no `curl`, no `ca-certificates`, no `danted`,
   no image pull; the client container runs the binary built on the host and mounts the
   host's CA bundle read-only. Record that in `<run-id>/permission.txt`; a step that would
   need a fetch stops and is reported instead.

2. **Build offline, on the host.** `GOTOOLCHAIN=go1.26.2` with `GOSUMDB=off` failed before
   any fetch in `real-2`; use the module's required toolchain directly, its path from
   `go env GOMODCACHE` (no machine path in a tracked file):
   `GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off CGO_ENABLED=0 GOOS=linux GOARCH=amd64
   "$(go env GOMODCACHE)/golang.org/toolchain@v0.0.1-go1.26.2.linux-amd64/bin/go" build
   -mod=readonly -o build/out/dnstt-cli ./cmd/dnstt-cli`. Record the command, `rc`, that
   toolchain's `go version`, `file build/out/dnstt-cli` (ELF x86-64, statically linked)
   and `sha256sum` in `<run-id>/build.txt`. Only `dnstt-cli` is built: no `live`, no
   upstream `dnstt-client`, no control-fetch — this run has no target, no exit and no
   comparison. A missing toolchain or module stops the task; nothing is fetched, and
   nothing is built in the container.

3. **Temporary profiles for R7 and R8.** Write `<run-id>/cfg/dnstt-cli/profiles.toml`
   (0600) with `default_profile = "r7-mixed"`. Every profile carries `domain = "<domain>"`,
   `pubkey = "<pubkey>"` and `datapath = "socks"`, and no `[profile.*.exit]` section: a
   profile without an exit is valid (`internal/config/profiles.go:179-238`; an empty type
   counts as `socks5`), and `resolvers check` reads only the domain, the key, `utls`,
   `resolvers` and `bootstrap` (`internal/cli/resolvers.go:83-107`). There is no secret
   store, no password and no `socks.listen`: nothing listens and nothing authenticates.
   Profiles: `r7-mixed` with `resolvers = ["udp:192.0.2.53:53", "<working-resolver>"]` — the
   dead entry first, so one run shows that a failure does not stop the list; `r7-dead` with
   `resolvers = ["udp:192.0.2.53:53"]`; and one per preset, `r8-<preset>`, with
   `resolvers = ["<preset>"]` for the eight names of
   `internal/resolvers/presets.go:17-24` (`doh-cloudflare`, `doh-google`, `doh-quad9`,
   `doh-adguard`, `dot-cloudflare`, `dot-google`, `dot-quad9`, `dot-adguard`). The preset
   names are product constants; the file still lives only in the ignored run. The R9
   profiles are added in step 7, after R8 says which presets pass: the directory is bind-
   mounted read-only, and a host-side write to `profiles.toml` appears through the mount —
   nothing is rewritten inside the container. Check the form of `<working-resolver>`: it
   must be a literal address or carry an `@` pin (the presets carry pins, the dead entry is
   an address). An unpinned host name is resolved through the network DNS, and the tool
   leaves loopback nameservers out (`internal/linuxsys/networkdns_linux.go:19-44`) while
   the default-bridge container's `/etc/resolv.conf` names Docker's `127.0.0.11`: the
   bootstrap list would be empty and the entry would fail for the environment, not the
   resolver. If the supplied entry is unpinned, resolve the host on the host, write the
   entry with the `@<address>` pin into the ignored profile and record the adaptation in
   the `r7-mixed.txt` header.

4. **Client container and its controls.** Confirm `debian:trixie` is local (id equals
   `image-id.txt`), then `docker run -d --name dnstt-live-<run-id> --pull=never --user
   "$(id -u):$(id -g)" -v "<repo>/build/out/dnstt-cli:/work/dnstt-cli:ro" -v
   "<repo>/<run-id>/cfg:/cfg:ro" -v
   /etc/ssl/certs/ca-certificates.crt:/cacert.pem:ro debian:trixie sleep infinity`. No
   `--network host`, no `-p`, no `docker network create`, nothing else from the repository
   or the host's real config. Every `docker exec` uses `-e XDG_CONFIG_HOME=/cfg -e
   HOME=/tmp -e SSL_CERT_FILE=/cacert.pem -e SSL_CERT_DIR=`. Record `docker inspect` mounts
   and the image id in `<run-id>/container-inspect.txt`. Controls: `grep ' /cfg '
   /proc/mounts` shows `ro` and `touch /cfg/dnstt-cli/x` fails; `sha256sum /cacert.pem`
   inside the container equals `sha256sum /etc/ssl/certs/ca-certificates.crt` on the host
   (`r0-ca.txt`), and a mismatch stops the task before any check; `find / -path /proc
   -prune -o -path /sys -prune -o -path /dev -prune -o
   \( -name profiles.toml -o -name secrets.json \) -print` shows only
   `/cfg/dnstt-cli/profiles.toml` and no store (`r0-find.txt`); `profile list` exits 0 and
   names the profiles — it loads and validates every one of them
   (`internal/config/profiles.go:164`) — and is rerun after step 7. Nothing is installed:
   the image is used as it is.

5. **R7 — the dead resolver.** Write the helper `<run-id>/check.sh` (0600, ignored): it
   takes a profile name and an output name, records `start` (`date +%s%3N`), runs
   `docker exec -e XDG_CONFIG_HOME=/cfg -e HOME=/tmp -e SSL_CERT_FILE=/cacert.pem -e
   SSL_CERT_DIR= dnstt-live-<run-id> timeout -s TERM -k 5 120 /work/dnstt-cli resolvers
   check <profile>`, keeps stdout and stderr together, records the exit code and writes a
   header (`profile`, `elapsed_ms`, `rc`) above the output into `<run-id>/<output>`; it
   prints nothing to the terminal. Then `check.sh r7-mixed r7-mixed.txt`: expect
   `udp:192.0.2.53:53: FAILED (<error>)` and `<working-resolver>: OK (<time>)`, `rc` 0, and
   the dead entry's `elapsed_ms` at about the final 10 s handshake timeout
   (`internal/supervisor/tunnel.go:24`, used at `internal/cli/resolvers.go:134`) or a
   refusal at once if the container's network rejects the destination. Then
   `check.sh r7-dead r7-dead.txt`: every entry `FAILED`, `No resolver passed the check.`,
   `rc` 1, about 10 s. A pass of the dead entry is a failing check: stop and report. If
   `<working-resolver>` was not supplied, `r7-mixed` is `not seen — no working resolver
   supplied` with the side `prerequisite`, and `r7-dead` still runs; its criterion does not
   need a working entry. Fill R7 in the ignored filled checklist.

6. **R8 — the eight presets.** Run `check.sh r8-<preset> r8-<preset>.txt` for each of the
   eight; every `OK (<time>)` is a real Noise handshake through that resolver against the
   user's server. A `FAILED` line keeps the first failure and is attributed, not called a
   product defect: take the preset's own pinned address and port from
   `internal/resolvers/presets.go` (443 for DoH, 853 for DoT) and control the path outside
   the product with `docker exec ... bash -c 'timeout 5 bash -c "exec 3<>/dev/tcp/<pin>/<port>"'`,
   recording `open`, `refused` or `timeout` in `r8-<preset>-control.txt`. Open but the
   handshake failed — the resolver or its TLS/HTTP answer, with the error text naming the
   layer; refused or timeout — the network, and a blocked port looks exactly like that. The
   other presets of the same class passing, and the working entry passing in R7, close off
   the profile, the key and the server as causes. Write `r8-summary.txt`, one line per
   preset: `seen (<ms>)` or `failed — <side>: <control file>`. The exit code alone is not
   the verdict. Fill R8.

7. **R9 — the five `DefaultUTLS` fingerprints.** Pick the pair from R8: the first passing
   DoH preset and the first passing DoT preset, recorded in `r9-summary.txt`. A class with
   no passing preset makes R9 for that class `not seen — no passing <class> preset` with
   the side `network` or `resolver`; the user's working entry of that class may stand in
   only as the diagnostic below, recorded as such. Append to `profiles.toml` (still 0600,
   still ignored, host side): for each label, `r9-<label>` with `utls = "<label>"` and
   `resolvers = ["<doh preset>", "<dot preset>"]`, and `r9-<label>-user` with the same
   `utls` and `resolvers = ["<working-resolver>", "<working-dot-resolver>"]` (omit the
   second entry when no working DoT was supplied; both follow the address-or-`@`-pin rule
   of step 3). Labels: `random`, `Chrome_131`,
   `Firefox_120`, `Safari_16_0`, `iOS_14` — exact spelling; a bare label has weight 1
   (`core/client/weightedlist.go:15-24`), and the map matches case-insensitively
   (`core/client/utls.go:55-57`). Rerun `profile list` to prove the addition loads. Then
   `check.sh r9-<label> r9-<label>.txt` per label: two lines, one per preset, each `OK` a
   handshake with that fingerprint through a real DoT or DoH resolver. A label that fails a
   class gets one diagnostic, `check.sh r9-<label>-user r9-<label>-user.txt`; the first
   failure is kept. Attribution: both fail — the label's hello on that resolver class (the
   client's TLS shape, not the tunnel); the preset fails and the user's entry passes — the
   preset resolver or the network; every class fails with the same `x509 ... unknown
   authority` — the container's trust store, settled by the `r0-ca.txt` control of step 4,
   and then the class is `not seen — container trust store` and the task stops, not an
   attribution to the resolver.
   Write `r9-summary.txt` with `seen` or `attributed — <side>` per label and class, the
   pair used, and the list of labels seen and attributed; this file is what the filled form
   and the Outcome read. Fill R9, and in the ignored R9 section replace the `compare`
   command and the `r9-<label>-<doh|dot>.txt` names by `resolvers check` and
   `r9-<label>.txt` / `r9-<label>-user.txt`, with one line why: `compare` fetches the
   target through the exit and this run has no exit credentials, while the criterion is a
   handshake, which `resolvers check` performs with the profile's `utls`
   (`internal/cli/resolvers.go:124-144`).

8. **Clean stop, host check, evidence index, checklist.** Remove the container
   (`docker rm -f dnstt-live-<run-id>`); confirm no `dnstt-cli` process, no SOCKS5
   listener, no container, no published port and no new bridge from the run, and the image
   id equal to `image-id.txt` (no pull). Delete `<run-id>/cfg` — the profiles with the
   domain, the key and the resolver entries — and every copy made; confirm no
   `secrets.json` was ever created and that the host's real config directory was never
   mounted, opened or statted. Write `<run-id>/host-after.txt` with step 1's commands and
   compare: routes, `/etc/resolv.conf` checksum, links and Docker objects match;
   `git status --short` shows only the roadmap node and ignored paths, and external Docker
   churn from other projects is recorded as not the run's. Write `<run-id>/index.txt`: per
   check the evidence file, the verdict and the side, plus the build, the container inspect,
   the CA control and `permission.txt`. Fill the cleanup line and R7–R9 of
   `doc/local/runbook-socks-acceptance-filled.md`. No code, test, dependency or
   profile-schema change: `git diff` shows only this node. A `dnstt-cli` defect found here
   is not fixed in this task — name the check, the side and the evidence, report it, and it
   becomes a new child of the stage with a test; the first failure is kept. The Outcome of
   this node carries counts and verdicts only (`8/8` presets, or how many per class were
   attributed; which labels were seen and which attributed), never a resolver address, the
   domain, the key, a date or the set of public resolvers that worked.

## Done when

- The binary was built offline from the reviewed tree (the module's required toolchain,
  `GOPROXY=off`, `-mod=readonly`), its `sha256` and the `go version` are recorded, and the
  client container ran it with the temporary profile mounted read-only, with nothing
  installed and no image pull.
- `resolvers check` on the mixed profile named the dead resolver `FAILED` with its error and
  the working resolver `OK` with `rc` 0; on the dead-only profile every entry failed and the
  command exited 1 with `No resolver passed the check.`; the dead entry's time was the 10 s
  handshake timeout or an immediate refusal of the container's network, and a pass of the
  dead entry would have been a failing check. R7 is filled in the ignored checklist.
- Each of the eight presets either completed a Noise handshake against the real server
  (`OK` and time in `r8-<preset>.txt`) or is recorded as `failed` with its side, its error
  text and the `/dev/tcp` control; `r8-summary.txt` and R8 say which, and no verdict was
  read from the exit code alone.
- Each of the five `DefaultUTLS` labels completed a handshake through at least one DoH and
  one DoT preset (`OK` in `r9-<label>.txt`), or its failure is attributed with the first
  failure kept and the diagnostic recorded; `r9-summary.txt` and R9 say which labels were
  seen and which were attributed, and through which preset pair.
- The temporary profile is deleted and its values exist nowhere in a tracked file; the
  container is removed, nothing was fetched or installed, the host record (routes,
  `/etc/resolv.conf` checksum, links, Docker objects, image id, `git status`) matches the
  baseline, and no code, test, dependency or profile-schema change was made. No exit
  credentials or secret store were needed or written.

## Outcome

The resolver-facing checks were accepted in one `real-3` run against the real server, from
a temporary profile that carried only the domain and the public key: no exit credentials,
no secret store, no listener. One disposable client container from the local
`debian:trixie` image (`--pull=never`, default bridge, no published ports, nothing
installed) ran the binary built offline from the reviewed tree with the module's required
toolchain; the profile was mounted read-only and deleted after the run.

**R7.** The mixed list named the dead resolver first as `FAILED (context deadline
exceeded)` at the final 10 s handshake timeout and the working DoH entry as `OK (204 ms)`;
the command exited 0. The dead-only list failed every entry with the same error and
`No resolver passed the check.`, exit 1, about 10 s. The dead entry did not pass, so the
check is a pass; the dead-only exit code alone was not the verdict. Side: —.

**R8.** Seven of the eight presets completed a real Noise handshake against the real
server (DoH 3/4, DoT 4/4; 223 ms–3.164 s). The remaining DoH preset failed with `context
deadline exceeded` at the 10 s policy; a TCP control from the client container found both
of its pinned 443 paths open, and the same resolver's DoT preset, the other DoH presets
and the working DoH entry passed in the same run. The side is the resolver (its DoH
endpoint or the 443 path; the error text does not name the layer), not the product; the
first failure was kept and not retried. No product defect followed, and the check's exit
code alone was not the verdict.

**R9.** All five `client.DefaultUTLS` labels (`random`, `Chrome_131`, `Firefox_120`,
`Safari_16_0`, `iOS_14`) completed a handshake through both the first passing DoH preset
and the first passing DoT preset of R8: two `OK` lines per label, 112 ms–1.809 s. No label
was attributed; no diagnostic run was needed, and the diagnostic profiles only loaded.
`resolvers check` replaced the runbook's `compare` here, because `compare` fetches the
target through the exit and this run has no exit credentials, while the criterion is a
handshake, which `resolvers check` performs with the profile's `utls`.

**Controls and stop.** The profile and the binary were mounted read-only and a write under
`/cfg` failed; `sha256sum` of the mounted CA bundle matched the host's, so the trust-store
class was settled and never appeared; `find` showed only the mounted profile and no secret
store. Nothing was fetched or installed and no image was pulled; the temporary profile and
the `cfg` directory were deleted with every copy, the container was removed, and no
product process, listener, published port or bridge remained. The host record (routes,
`/etc/resolv.conf` checksum, links, Docker objects, image id, `git status`) is identical to
the baseline.

Deviations: the supplied working DoH and DoT entries were host names without `@`-pins; they
were resolved on the host and written with `@`-pins into the ignored profile, because a
default-bridge container's `/etc/resolv.conf` is Docker's loopback and the bootstrap leaves
loopback nameservers out. The first control attempt failed on a shell-quoting mistake and
was rerun; no product behaviour was involved, and the final control files are the reruns.
R10 (a clean stop of a real `connect` session) is not part of this node and remains with a
later real-server run.

No `dnstt-cli` defect was found; the one preset failure is attributed outside the product,
so no new child of the stage is opened. The browser transports R1–R4 and R10 are other
nodes or later runs and were not run.
