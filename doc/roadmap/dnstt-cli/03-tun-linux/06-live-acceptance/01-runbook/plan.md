---
kind: task
status: done
detailed: true
---

# Runbook and evidence form

## Task

Write the checklist the user will follow for the Linux `tun` acceptance, with placeholders instead
of the user's values, and settle the evidence form before the user spends a session on the matrix.
The checklist covers every line of this stage's `What the user runs` and every line of the parent
stage's `## Done when`, and it is runnable against the current binary: only flags and subcommands
that exist in the code are used.

The matrix is user-run; this task runs nothing that changes the network, reads no real profile,
secret, keyring or shell history, and opens no file the user wrote under `doc/local/` or
`doc/runs/`. It may build the two binaries and ask them for `-h`.

Deliverable: `doc/local/runbook-tun-linux.md` (outside git, `AGENTS.md`, "Documents in `doc/`")
and an empty `doc/runs/tun-matrix/` for the raw outputs. Nodes in the repository stay free of the
user's values; the result fields the user fills in carry verdicts, not addresses.

The ground truth for commands and observations:

- `internal/cli/connect.go:36-60` — `connect [profile]`, `-v`, `-datapath socks|tun`,
  `-no-kill-switch`, `-allow-leak`. The `-config` and `-resolver` flags of `architecture.md`
  §10 do not exist yet; do not use them.
- `internal/cli/profile.go:230-277` (add), `:67-157` (show) and `internal/cli/wizard.go:126-352`
  — the questions, the final line and what `profile show` prints, masked without `-full`.
- `internal/sudo/sudo.go:29-89`, `internal/sudo/env.go`, `internal/config/dir.go:29-85`,
  `internal/secretservice/helper.go:24-64` — under plain `sudo` the settings are the invoking
  user's `~/.config/dnstt-cli/profiles.toml` (taken from the user database, not
  `XDG_CONFIG_HOME`), and the Secret Service helper runs as that user.
- `scripts/checktunnel/main_linux.go` — the sections and the flags `-label`, `-name`,
  `-timeout`, `-offline`; `common.go` — the external address and resolver sections.
- `internal/netguard/netguard_linux.go:50-69` — the strict `rp_filter` refusal and hint;
  `internal/netguard/watch.go:18-22` — the 2 s settle and the 10 s maximum wait.
- `internal/datapath/tun.go:22-29` — `10.66.67.1` in the netstack, `10.66.67.2` on the host,
  MTU 1500, the two `/1` routes; `tun_linux.go:28-46` — `@dnstt-cli-tun`, the unsupported
  system message; `:181-184` — the foreign-interface refusal; `tun.go:112-114` — the busy-lock
  error.
- `internal/linuxsys/dns_linux.go` — the three system-DNS methods and the recovery messages;
  `dnsguard_linux.go` — `inet dnstt-cli` with the `owner` flag and the EEXIST refusal;
  `killswitch_linux.go:9-55` — the "closed" set, the §05a exceptions, and the drop that makes
  a UDP send fail with `EPERM` while a dropped TCP connect times out.
- `internal/app/protection.go:70-108` — the kill-switch notices; `internal/supervisor/tunnel.go`
  — the reconnecting and "up again" lines.
- `doc/architecture.md` §05, §05a, §09; `AGENTS.md`, "Personal data" and "Documents in `doc/`".

## Steps

1. **Build once into the ignored `build/` and confirm the vocabulary.** With the existing module
   cache and no download:
   `go build -o build/dnstt-cli ./cmd/dnstt-cli` and
   `go build -o build/checktunnel ./scripts/checktunnel`. Read the `-h` output of `connect`,
   `profile add`, `profile show` and `checktunnel`; every flag the runbook uses must appear
   there. If Go tries to fetch a module, stop: no dependency is added here.
2. **Write the runbook's frame.** In `doc/local/runbook-tun-linux.md`: purpose and scope; a
   user-run notice (agents do not run it); a coverage table mapping every bullet of this
   stage's `What the user runs` and of the parent's `## Done when` to a check id P0–P8; a
   placeholders table (`<profile>`, `<domain>`, `<server-key>`, `<exit-login>`,
   `<exit-password>`, `<resolver>`, `<server>`, `<site>`, `<iface>`, `<gateway>`,
   `<router-ipv6>`, `<local-port>`, `<tun-dns>`); preconditions (Linux with `/dev/net/tun`,
   root through plain `sudo` from the user's session, the profile in the default
   `~/.config/dnstt-cli`, the server up and stoppable, one physical link plus a way to change
   it, `curl`, `python3`, `ping`, `getent`, `script` (or `2>&1 | tee` without the status line
   as the fallback); `nft` only for the P3 setup; `resolvectl`
   or `resolvconf` depending on the method); and the evidence and privacy rules (step 11).
3. **P0 — build, profile, baseline, `sudo` ownership.** The runbook says: build as in step 1;
   create the profile as the user, not under `sudo`, with `./build/dnstt-cli profile add
   <profile>` and the answers: tunnel domain; server public key; exit type `http` (the user's
   exit is a proxy that supports it; `socks5` is the equivalent choice if their proxy needs
   that; an HTTP exit needs a login, so a proxy without authentication takes the `socks5`
   type with Enter for no login); exit login; exit password twice; resolvers, one per line or
   Enter for `network`; mode `tun`. Expect `Profile "<profile>" added (exit http, mode tun).`
   Then
   `./build/dnstt-cli profile show <profile>`: `Datapath: tun`, `Kill Switch: true`,
   `Exit Type: http`, an exit user, `Exit Password: (stored in secretservice:…)` or
   `(stored in file:…)`, and no `Tun DNS` line (empty means the default `1.1.1.1`). Note: the
   default config path is required because under `sudo` the code reads `~/.config/dnstt-cli`
   of the invoking user and ignores `XDG_CONFIG_HOME`; the secret store and the profile files
   must belong to the user, and `ls -l ~/.config/dnstt-cli/` as the user is the check.
   Baseline: `sudo ./build/checktunnel -label "P0 baseline"` — no `dnstt-cli` process, lock
   `free`, no tunnel adapter, no `/0` or `/1` routes into one, no `inet dnstt-cli` table, the
   network's DNS, the home external address, IPv6 present or absent (record which), and the
   `rp_filter` values (record them). Also, without root, `./build/dnstt-cli connect -v
   <profile>` must refuse with `the tun datapath needs root: run sudo dnstt-cli connect
   "<profile>", or connect with -datapath socks` and change nothing; the profile and the exit
   password are read before that check, so an unavailable keyring would fail earlier and is a
   different branch to record. A dirty baseline or a foreign `dnstt-cli` interface (the
   `ip link del` hint) is a failure to record, not to clean blindly.
4. **P1 — first session, traffic, clean stop.** Terminal A:
   `script -q -e -c "sudo ./build/dnstt-cli connect -v <profile>" doc/runs/tun-matrix/01-connect.log`;
   expected lines: `kill-switch: system traffic is blocked while the tunnel is down`, then the
   `[TUN]` lines with `-v` (`system DNS is set by: …`, `DNS protection: port 53 only to
   10.66.67.1 …`, `adapter "dnstt-cli": 10.66.67.2/32 peer 10.66.67.1/32, MTU 1500, routes …`,
   `system DNS (…)`), and the status line if the terminal is a pty. In terminal B:
   `curl -4 -sS -o /dev/null -w '%{http_code}\n' https://example.com` → `200`; the browser
   opens `<site>` → the page loads; no proxy settings are touched. Then
   `sudo ./build/checktunnel -label "P1 up"`: adapter up with `10.66.67.2/32`, MTU 1500; the two
   `/1` routes into it; `inet dnstt-cli` with `owner: netlink port …`; the system DNS is the
   adapter's — via `cat /etc/resolv.conf`/the checktunnel "nameserver" line for `resolvconf`
   and the plain file, via `resolvectl status dnstt-cli` for `systemd-resolved`; the core's
   interface is the physical one; lock `held`; external address and the
   `o-o.myaddr.l.google.com` answer are the exit's, not the home ones. The exit password is
   read before the first dial: reaching the dial proves the store is the user's — if it is
   `secretservice:`, the helper ran as that user; a `keyring as <user>` error is recorded. One
   Ctrl+C in A:
   `dnstt-cli: stopping...`; the exit status from `script -e` is 0. `checktunnel -label
   "P1 after"` equals P0: no adapter, routes, table, lock or DNS leftover, and the external
   address is home again.
5. **P2 — `kill -9` and recovery.** Start as in P1 (log `04-connect-kill9.log`), wait until the
   session is up; `pgrep -x dnstt-cli` names the PID; `sudo kill -9 <pid>`. Immediately
   `sudo ./build/checktunnel -label "P2 after kill -9" -offline`: no process, lock `free`, no
   adapter, no `/1` routes, no `inet dnstt-cli` table. Record the DNS method's leftover: for
   `resolvconf` a `tun.dnstt-cli` record, for the plain file the marker and the backup, for
   `systemd-resolved` nothing (the link is gone). Start again with the server up and `-v`:
   expect `[TUN] record tun.dnstt-cli of the previous dnstt-cli run removed` or
   `[TUN] previous resolv.conf restored from …` (only with `-v`), then a working session;
   Ctrl+C; `checktunnel` clean. A surviving adapter, route or table is a failure; a leftover not
   recovered by the next start is a failure.
6. **P3 — kill-switch.** P3a, first dial and server down: stop `<server>`; start `connect -v`
   (log `06-connect-server-down.log`); expect the blocked line before the first attempt and
   retries afterwards. In terminal B, while down: `curl --max-time 3 -sS http://192.0.2.1/` ends
   in a timeout (exit 28), not a success; a UDP send to `192.0.2.1:443` fails at once with
   `EPERM` (the runbook gives a `python3` snippet that catches both the send and the receive;
   the same snippet is used in P4 with a different expected error; the socket is connected,
   `settimeout(3)`, and both `send` and `recv` are inside the `try`); `timeout 3 getent hosts
   does-not-exist.invalid` ends by timeout
   unless a cache answers; `sudo ./build/checktunnel -offline -label "P3 locked"` shows no
   adapter, the lock held and the `inet dnstt-cli` table with rules (it exists before the first
   session). Start `<server>`: `tunnel up again — system traffic goes through it`; `curl`
   works. P3b, reconnection with the server still down: with a session up, stop `<server>` and
   change the network (P5); the watcher restarts, the dial fails, the reconnecting line
   `tunnel reconnecting — system traffic is blocked (kill-switch)` stays, and the blocked
   checks above repeat; start `<server>` and expect the up line. P3c, `-no-kill-switch` with
   the server down: `kill-switch is off (-no-kill-switch): system traffic goes past the tunnel
   while it is down`, `curl` works, `checktunnel` shows the empty `inet dnstt-cli` chains and
   no rules. P3d, the profile: change `kill_switch = true` to `false` in
   `~/.config/dnstt-cli/profiles.toml` as the user, run without the flag, expect
   `kill-switch is off (kill_switch = false in the profile): …`, restore `true`, confirm with
   `profile show`. P3e, `-allow-leak`: `sudo nft add table inet dnstt-cli`, run `connect` — it
   refuses with `nftables: table inet dnstt-cli already exists and was not created by
   dnstt-cli; if it is not needed, delete it: sudo nft delete table inet dnstt-cli`; the same
   run with `-allow-leak` warns `DNS protection could not be set: … — with -allow-leak the
   tunnel runs without it: DNS queries and system traffic may go past the tunnel` and works;
   `checktunnel` shows the foreign table without an owner and no rules, and IPv6 is not blocked
   (the documented fail-open consequence); `sudo nft delete table inet dnstt-cli` restores the
   normal start. P3f, the §05a exceptions while locked: loopback works (`python3 -m http.server
   <local-port> --bind 127.0.0.1` and `curl http://127.0.0.1:<local-port>/` → 200); the
   local network works (`ping -c 1 <gateway>`, `ping -6 -c 3 ff02::1%<iface>`); port 53 to the
   local network is closed (a UDP send to `<gateway>:53` fails at once with `EPERM`, a TCP
   connect to it times out); the lease, ND and RA stay alive, and P5 is the practical proof
   that DHCP works with the kill-switch on.
7. **P4 — DNS, UDP, QUIC, IPv6, with the session up.** System resolver and `tun_dns`: the
   method's check above shows `10.66.67.1`; `getent hosts example.com` resolves;
   `checktunnel`'s resolver section shows the exit's answer, not the home one. Other UDP:
   the `python3` send to `192.0.2.1:443` now ends in `ConnectionRefusedError` (the netstack's
   ICMP port unreachable) in well under a second, not a timeout; a UDP send to `<gateway>:53`
   fails at once with `EPERM` (DNS protection), and a TCP connect to it times out. QUIC: open
   `<site>` in the browser; the page loads, and where the browser shows the protocol it says
   `h2`/`http/1.1`, not `h3`; the UDP :443 refusal above is the mechanism. IPv6: if P0 had an
   external IPv6 address, `checktunnel` shows `IPv6: none` now and `curl -6 --max-time 3`
   fails, while `ping -6 -c 3 ff02::1%<iface>` gets local replies (or the router's link-local
   or ULA address answers if multicast echo is filtered); with no IPv6 in the network, mark the
   check not exercised and why. If names do not resolve, the branch names the exit:
   the exit proxy must allow a TCP stream to `<tun-dns>:53`; the default is `1.1.1.1`, and
   `tun_dns` can be changed in `profiles.toml` to an address the exit reaches. A wrong exit
   password ends the run with the HTTP login error and is not a resolver failure.
8. **P5 — network change.** With the session up, note `<iface>` from `checktunnel`; switch
   Wi-Fi and Ethernet, or unplug and replug the cable. Expect within about 2–10 s the
   reconnecting line, then the up line; `checktunnel` shows the core's interface as the new
   one; `curl` works without restarting `connect`; the adapter keeps its name and index. A
   change that leaves no network keeps the blocked line and blocks traffic — that is correct,
   and the run records how the session comes back when the link returns.
9. **P6 — strict `rp_filter`.** Record the values from P0. If the effective value is 2 or 0,
   set both to 1: `sudo sysctl -w net.ipv4.conf.all.rp_filter=1
   net.ipv4.conf.<iface>.rp_filter=1`; run `connect -v <profile>` and expect the refusal
   `netguard: strict IPv4 rp_filter on <iface> drops the tunnel's replies: loosen it with sudo
   sysctl -w net.ipv4.conf.all.rp_filter=2 net.ipv4.conf.<iface>.rp_filter=2, or connect with
   -datapath socks`, exit 1, no adapter and no table. If it is already 1, record that refusal
   and then loosen to 2 as the documented remedy to continue the matrix. Restore the original
   values with `sysctl -w` and confirm with `checktunnel`.
10. **P7 — second instance and the lock.** With a session up, `sudo ./build/dnstt-cli connect
    -v <profile>` in another terminal: `dnstt-cli connect: tun: the adapter is already busy:
    another dnstt-cli with the tun datapath is running on this machine; stop it or connect
    with -datapath socks`, exit 1; `checktunnel` shows the lock `held`. After the first stops,
    the second starts. The stale lock after P2 must be `free`.
11. **P8 — final state and evidence form.** Clean exit; the final `checktunnel` equals P0;
    `git status --short` is clean. The runbook's evidence rules: every raw output goes to
    `doc/runs/tun-matrix/` with the fixed names (`00-baseline.txt`, `01-connect.log`,
    `02-checktunnel-up.txt`, …); the filled checklist stays in `doc/local/`; both are gitignored
    and are never `git add -f`-ed. Raw files may contain the domain, the key, resolvers,
    addresses, the profile name and dates — none of that is pasted into the checklist's
    `Result` field, which carries a verdict and one sentence; the record task reads only those
    fields. Each check ends with `Result: seen / not seen / failed — one line`.
12. **Review the whole file against the code.** Every `Run` line uses only flags from `-h`;
    every `Expect` line is either a fixed string quoted from the code or an observable; every
    scenario has an `If not` branch that says what was not seen and which side (server, exit,
    network, product) it points at; the coverage table has no empty cell.
13. **Finish without changing the tree.** `mkdir -p doc/runs/tun-matrix`;
    `git check-ignore -v doc/local/runbook-tun-linux.md doc/runs/tun-matrix` names the two
    ignore rules; `git status --short` is clean; the only new files are ignored (`build/`,
    `doc/local/runbook-tun-linux.md`, `doc/runs/tun-matrix/`); no commit, no live run, no
    profile or keyring read.

## Done when

- `doc/local/runbook-tun-linux.md` exists and `doc/runs/tun-matrix/` exists; both are ignored by
  git, and the runbook is not tracked.
- The runbook has the frame, the placeholders, the P0–P8 checks with Run / Expect / If not /
  Result, and a coverage table with every bullet of this stage's `What the user runs` and of the
  parent stage's `## Done when` mapped to a check.
- Every command and flag in the runbook appears in the built binaries' `-h` output; `-config`
  and `-resolver` are absent because they are not implemented.
- The privacy rule is explicit: raw outputs stay in `doc/runs/tun-matrix/`, result fields carry
  verdicts and no values, nothing is force-added, and the checklist itself uses placeholders.
- `go build` succeeded from the cache with no download; `git status --short` is clean; no
  repository file changed; no live run, no real profile or keyring read, no commit.

## Outcome

The runbook is written at `doc/local/runbook-tun-linux.md` (ignored) and the evidence
directory `doc/runs/tun-matrix/` exists. It has the placeholders, preconditions, the
coverage table, the P0–P8 checks with Run / Expect / If not / Result, and the evidence and
privacy rules. Every flag used was checked against the `-h` output of the built
`dnstt-cli` and `checktunnel`; `-config` and `-resolver` are not used because they do not
exist. Both local paths are ignored by git; `go build` used the module cache and no
download; no live run, real profile, secret, keyring or network access was made, and no
tracked file holds a user value.
