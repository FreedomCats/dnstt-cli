---
kind: task
status: done
detailed: true
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

## Steps

1. **README.** Add a "What leaks and what does not" section for `tun`, in terms a user can
   check: all TCP through the exit; DNS from the adapter to `tun_dns` as DNS over TCP; other
   UDP refused at once with ICMP, so QUIC falls back to TCP; IPv6 closed outward while the
   local IPv6 network stays; browser DoH carried by the routes, not leaked; the kill-switch
   on by default and its §05a exceptions; `-no-kill-switch`, `kill_switch = false`,
   `-allow-leak`; a `kill -9` leaves no adapter, routes or table, while a DNS leftover is
   recovered by the next start; `socks` still leaks DNS unless the application or its proxy
   resolves names, with a pointer to `tun`. A `socks` section is not duplicated — the SOCKS
   record task extends the same section if it has not written one yet.
2. **architecture.md.** The values the runs settled: §05 carries the point-to-point adapter
   pair (`10.66.67.2/32` with peer `10.66.67.1/32`, MTU 1500) and the recovery order (the
   leftover is removed right after the adapter is reserved, before the first session); §09
   says how the `sudo` keyring is reached; §05a needs no change if the runs match it. A
   difference is a finding with its own task, not a quiet rewrite; if nothing changed, the
   stage `Outcome` says so.
3. **This stage's `Outcome`.** Per P id: what was seen, what was not exercised and why, what
   failed and on which side, and where a failure was fixed or left. The system-DNS method and
   whether the `kill -9` leftover was recovered; the user's acceptance of the P5 gap and the
   live IPv6 gap. No dates, domain, key, resolver, address, profile or interface name that
   identifies the machine; method names (`systemd-resolved`, `resolvconf`, an ordinary file)
   are substance and may stay.
4. **Comments and cross-references** that still call the live behavior preliminary or point at
   the matrix as not run, if any; the stage's links checked; English and whitespace checks;
   no value in the diff.

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

## Outcome

`README.md` now has a "What leaks and what does not" section for `tun`, in terms a user can
check, and a short `socks` paragraph that points to `tun`; the SOCKS record task extends that
section rather than adding a second one. `architecture.md` §05 carries the settled
point-to-point adapter pair and the recovery order, §09 the `sudo` keyring helper; §05a needed
no change — the runs matched it. This stage's `Outcome` covers every P id, the P5 and live
IPv6 gaps and the user's acceptance. Links and English pass, `git diff --check` is clean, and
the diff has no value, domain, key, resolver, environment detail or date.
