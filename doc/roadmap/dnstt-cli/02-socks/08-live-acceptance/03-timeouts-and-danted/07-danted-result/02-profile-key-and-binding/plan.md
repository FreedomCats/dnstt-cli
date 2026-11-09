---
kind: task
status: todo
depends: [01-one-write-exchange]
---

# Profile key and binding

## Task

The accepted contract of the [parent stage](../plan.md#accepted-contract) makes the mode an
enduring profile key: `[profile.<name>.exit] one_write` (bool, default `false`), applying to
`exit.type = "socks5"` only, with an empty type counting as socks5 and `http` or `ssh`
rejected by validation. This task adds the key, its validation and its sole consumer — the
SOCKS5 dialer built for the datapath — as one product result, because a field without a
consumer and a consumer without a field are each unusable. The exchange itself comes from
[one-write exchange](../01-one-write-exchange/plan.md); nothing here changes the sequential
path or the HTTP exit.

What changes:

- `internal/config/profiles.go`: `OneWrite bool` with `toml:"one_write"` in `Exit`
  (`internal/config/profiles.go:45`), `"one_write"` in `knownExitKeys`
  (`internal/config/profiles.go:319`), and a rule in `validateProfile`
  (`internal/config/profiles.go:210`) that rejects the key with any type other than
  `socks5` or an empty one, naming the key and the type. The saved file keeps the key
  visible (`one_write = false`) like the other fields; the round-trip expectation in
  `TestProfilesRoundTrip` (`internal/config/profiles_test.go:16`) follows.
- `internal/app/tunnel.go`: the datapath dialer (`internal/app/tunnel.go:170`) receives
  `OneWrite: prof.Exit.OneWrite`; the startup `checkExit` closure
  (`internal/app/tunnel.go:123`) does not, with a comment that it stays sequential — it has
  no target. `profile set` is not interactive and `profile show` is not part of the accepted
  contract, so no new CLI surface is added.
- Tests: `internal/config/profiles_test.go` for load/save/round-trip of both values, the
  default, the unknown-key rejection of a misspelt key, and validation with `socks5`, the
  empty type, `http` and `ssh`; `internal/app/http_test.go` for the binding, extending the
  generated in-process tunnel helpers (`startAppTunnel`,
  `internal/app/http_test.go:546`) and the existing startup-check pattern
  (`TestSOCKS5StartupCheckRemainsSessionLocal`, `internal/app/http_test.go:171`).

## Done when

- A profile with `one_write = true` loads, saves and round-trips; an absent key means
  `false`; `one_write` is a known exit key and a misspelt one is rejected as an unknown
  exit key.
- Validation rejects `one_write = true` with `type = "http"` and `type = "ssh"`, naming the
  key and the type, and accepts it with `type = "socks5"` and with an empty type; no other
  exit rule changes.
- The datapath `*exit.SOCKS5` gets its `OneWrite` from the profile; through the generated
  in-process tunnel with a login and `one_write = true`, a connection reaches the target,
  while the startup `Check` is observable as sequential: the greeting offers both methods
  and no `CONNECT` follows.
- With an exit fixture that drops the batch, the connection fails with the error naming
  `exit.one_write`; the fixture accepts one exit stream for it with no sequential retry, and
  a later connection through the same session still reaches the target, so the supervisor
  keeps running.
- The HTTP exit, its binding and its tests are unchanged, and no second consumer of the key
  appears.
- Focused `go test -race -count=1 ./internal/config ./internal/app` pass, and the stage's
  [local validation matrix](../../plan.md#local-validation-for-code-children) for code
  children passes (vet split, Windows cross-build and test compilation, formatting,
  `git diff --check`); no dependency, network access or real value is involved.
