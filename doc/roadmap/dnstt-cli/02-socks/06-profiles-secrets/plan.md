---
kind: task
status: done
---

# Profiles and secrets

## Task

`internal/config` — `profiles.toml` with the schema of `architecture.md` §10 and its
validation, and the exit password in the secret store rather than in the profile file.
`E1`, `S6`; `architecture.md` R8, §10.

## Steps

1. The profile schema of §10: name, domain, key, exit type and login, resolvers, mode,
   `bootstrap`, `kill_switch`, `tun_dns`. Unknown keys are an error, not a warning: a
   misspelt key that is ignored is a setting that silently does nothing.
2. Validation: `resolvers` through `resolvers.Expand` with `["network"]` when empty;
   `bootstrap` through `resolvers.ParseBootstrap`; a secret written into the TOML is an
   error.
3. The exit password in the secret store (DPAPI on Windows, the keyring on Linux, a `0600`
   file without a keyring); the profile holds a reference, not the value.
4. `Save` with its ownership tests under `sudo` — the file belongs to the calling user, not
   to root.
5. Tests for load, save, validation and the secret stores.

## Done when

Load, save, validation and secret tests pass on Linux and on Windows; a profile with a
misspelt key and a profile with a password in it are both refused with a message naming
the offending key.

## Depends on

[Resolvers](../03-resolvers/plan.md) — done; the schema refers to its types.

## Outcome

Done as planned. All steps completed:

1. Profile schema from `architecture.md` §10 implemented: `domain`, `pubkey`, `resolvers`, `bootstrap`, `utls`, `datapath`, `kill_switch`, `tun_dns`, with `exit` and `socks` subsections. Unknown keys are detected and reported with the key name.

2. Validation: `resolvers` through `resolvers.Expand` (empty list becomes `["network"]`), `bootstrap` through `resolvers.ParseBootstrap`, secrets written directly into TOML rejected, `pubkey` validated as exactly 64 hex digits.

3. Exit password in the secret store: DPAPI on Windows, Secret Service keyring on Linux (falls back to `secrets.json` with mode 0600 without a keyring). Profile holds only a reference (`<scheme>:<id>`), not the value.

4. Save with ownership tests: files belong to the invoking user under sudo, using existing `giveToInvoker` from `owner.go`.

5. Tests: 12 new tests for profiles (round-trip, validation of unknown keys, misspelt keys, password values, invalid resolvers/bootstrap/pubkey, required fields, ownership under sudo, integration with secrets), all pass alongside 17 existing tests of `internal/config`.

Added `dnstt-cli.example.toml` with commented examples using documentation addresses (RFC 2606). Dependency: `github.com/pelletier/go-toml/v2 v2.4.3`.

SSH exit (`exit.type = "ssh"`) is recognized by validation but not implemented — that is stage E5.
