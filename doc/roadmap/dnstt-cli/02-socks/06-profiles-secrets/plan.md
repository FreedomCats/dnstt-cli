---
kind: task
status: todo
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
