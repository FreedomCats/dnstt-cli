---
kind: task
status: todo
depends: [02-user-run]
---

# Recording the results

## Task

The results of the user-run matrix written where they are published, without anything that
identifies the user or the machine, and this stage's `Outcome` filled in. `architecture.md`
§05, §05a, §09; `AGENTS.md`, "Personal data" and "Documents in `doc/`".

Input: the filled `doc/local/runbook-tun-linux.md` of
[the user-run matrix](../02-user-run/plan.md), read through its `Result` fields only. The raw
outputs in `doc/runs/tun-matrix/`, the real profile, the secret store and the keyring are not
read: they carry addresses, resolvers, the domain, the key and dates. If a result field contains
a real value, it is reported back and not copied.

Detailed when its turn comes. It will:

- **README.** A "Leak protection" or "What leaks and what does not" section for `tun`, in terms a
  user can check: all TCP through the exit; DNS from the adapter to `tun_dns` as DNS over TCP;
  other UDP refused at once with ICMP, so QUIC falls back to TCP; IPv6 closed outward while the
  local IPv6 network stays; browser DoH carried by the routes, not leaked; the kill-switch on by
  default and its §05a exceptions; `-no-kill-switch`, `kill_switch = false`, `-allow-leak`; a
  `kill -9` leaves no adapter, routes or table, while a DNS leftover is recovered by the next
  start; `socks` mode still leaks DNS unless the application or its proxy resolves names, with a
  pointer to `tun`. The `socks` record task
  ([`02-socks/08-live-acceptance/05-record-results`](../../../02-socks/08-live-acceptance/05-record-results/plan.md))
  is still open: extend its section if it exists rather than duplicate it.
- **architecture.md.** The values the runs settled: §05 if the adapter address, MTU or route set
  differs from the text, §05a if an observed behavior differs, §09 if the `sudo`/keyring
  behavior of the run says more than the current paragraph. A difference is a finding with its
  own task, not a quiet rewrite; if nothing changed, the stage `Outcome` says so.
- **This stage's `Outcome`.** Per P id: what was seen, what was not exercised and why, what
  failed and on which side (server, exit, network, `dnstt-cli`), and where a failure was fixed or
  left. The system-DNS method and whether the `kill -9` leftover was recovered. No dates, no
  domain, key, resolver, address, profile or interface name that identifies the machine; method
  names (`systemd-resolved`, `resolvconf`, an ordinary file) are substance and may stay.
- **Comments and cross-references** that still call the live behavior preliminary or point at
  the matrix as not run, if any; the stage's links checked.

## Done when

- `README.md` says what leaks in `tun` and what does not, in terms a user can check; a `socks`
  section is not duplicated — the existing one is extended if the SOCKS record task has added
  it.
- `architecture.md` carries the settled values, or the stage `Outcome` says that the runs settled
  none.
- The stage `Outcome` covers every P id and every line of this stage's `Done when`; no
  "preliminary" or "to be checked live" is left about the behavior this stage accepted.
- No domain, key, address, resolver, profile name, interface name, date or detail of the
  environment is in the changed files; the raw evidence stays in `doc/local/` and `doc/runs/`;
  links resolve and the files are in English.
