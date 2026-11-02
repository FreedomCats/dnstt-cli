---
kind: task
status: done
detailed: true
---

# Packages carried over from csqtt-cli

## Task

The shell parts that are not specific to dnstt come from csqtt-cli (MIT): interface choice,
routes, system DNS, kill-switch, the secret stores, the script helpers. Everything visible
to the system is renamed and every constant by which the programs recognise their own
traces gets a new value, so that csqtt-cli and dnstt-cli can live on one machine. `E0`,
`F4`.

## Steps

1. `netguard` — everything: interface choice, `Control`, `watch.go`.
2. `linuxsys` — everything: adapter, routes, system DNS, `DNSGuard`, kill-switch, monitor,
   `rp_filter`.
3. `winsys` — everything; WFP "own program" rules with one app ID instead of two; drop the
   `csqtt-core.exe` app ID.
4. `sudo`, `secretservice`, `lines` — everything.
5. `config` — the config directory, file ownership under `sudo`, the secret stores (DPAPI,
   keyring, file). Not carried: the profile schema, validation and `csqtt://` links; they
   are rewritten for dnstt.
6. `datapath` — the interfaces (`Requirements`, `Persistent`, `Reserver`, `DNSGuard`,
   `IPv6Blocker`). Not carried: `link*`, `tunfd*` (channels to a core process); `socks*`,
   `netstack`, `tun*` come later.
7. `scripts` — `internal/script`, `internal/wintun`, `wintun`, `checktunnel`, `ctrlbreak`,
   `sha256sums`. Not carried: `buildcore`; `release`, `notices`, `releasenotes`,
   `distcheck` come with the release stage.
8. New values for the constants that identify our own traces: adapter GUID
   (`winsys.AdapterGUID`), WFP provider and sublayer GUIDs, `SO_MARK`. Test environment
   variables `CSQTT_TEST_*` → `DNSTT_CLI_TEST_*`.
9. The `tailscale/wf` fork keeps the same `replace` as in csqtt-cli.
10. Translate the carried comments into English where they are new or changed; comments
    left exactly as carried may stay (`AGENTS.md`, "Language").

## Done when

`go test ./...` passes on Linux and on Windows, `-race` too, and `git grep -i csqtt` finds
only the credits to the base project in the documentation.

## Outcome

The packages are carried with new GUIDs and `SO_MARK` 0x1076 and translated into English;
`go test ./...` passes on Linux and on Windows, `-race` too, and `git grep -i csqtt` finds
only the credits in the documentation. Nothing from the GPL parts of qwdtt-cli was opened
(`AGENTS.md`).
