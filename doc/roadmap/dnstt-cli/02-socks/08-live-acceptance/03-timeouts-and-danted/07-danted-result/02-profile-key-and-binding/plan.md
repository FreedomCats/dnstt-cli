---
kind: task
status: done
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

## Outcome

The key is in `internal/config/profiles.go` as `Exit.OneWrite` with `toml:"one_write"` and no
`omitempty`, so a save writes it like the other exit fields (`one_write = false` included),
and `knownExitKeys` accepts it while a misspelt key still fails as an unknown exit key.
`validateProfile` rejects `one_write = true` with any type other than `socks5` or an empty
one, naming the key and the type; `false` stays valid with every type, because a saved `http`
or `ssh` profile now carries it. No other exit rule changed. `TestProfilesRoundTrip` needed
no change: it compares selected fields, and the new dedicated round-trip test covers the key
in both values, the visible file text and `Exit` equality.

The sole consumer is the SOCKS5 dialer built for the datapath in `internal/app/tunnel.go`;
the startup `checkExit` closure stays sequential, with a comment that it has no target and
never batches. An empty `exit.type` takes the same socks5 branch as before and gets the key.
`profile set`, `profile show`, the CLI wizard, the HTTP exit and its binding are unchanged,
and `rg OneWrite` finds no second consumer.

`internal/config/profiles_test.go` covers both round-trip values, the absent key as `false`,
the misspelt key in the exit section, and validation for `socks5`, an empty type, `http` and
`ssh`, including that a saved `http`/`ssh` profile with `one_write = false` reloads.
`internal/app/http_test.go` gains `startAppTunnelWithExit` (the old helper delegates to it)
and a loopback SOCKS5 exit fixture that tells the sequential startup check (`[5,2,0,2]`, a
login, no CONNECT) from the one-write batch (`[5,1,2]`), handles RFC 1929, echoes the target
and can drop a batch after its greeting. It counts every accepted stream, so a hypothetical
sequential retry cannot hide in an unread line, and bounds the stream-line reads in time.
`TestSOCKS5OneWriteBinding` proves the startup check is sequential and a connection through
the local proxy reaches the target with a login and `one_write = true`;
`TestSOCKS5OneWriteDroppedBatch` proves a dropped batch fails with an error naming
`exit.one_write` and not a login error, takes exactly one exit stream with no retry, and the
same session still reaches the target.

Review accepted one test-only correction (the all-stream counter and the bounded reads);
nothing was rejected. Focused `go test -race -count=1 ./internal/config ./internal/app`, the
full `go test -race -count=1 ./...` with `DNSTT_CLI_TEST_DBUS=1`, `DNSTT_CLI_TEST_NETNS=0` and
the stage's four network-dependent tests excluded, the CI vet split, `CGO_ENABLED=0 go
build ./...`, the Windows cross-build and Windows test compilation of both changed packages,
`gofmt` and `git diff --check` pass; the Secret Service tests ran without skips. No
dependency, network access or real value is involved.

Found while working, for the next child: the stage plan still lists the exit keys of a
profile as `type`, `user`, `password_ref` (`.../03-timeouts-and-danted/plan.md`); the line is
now stale and should be updated with R3, §10 and the reserved example by
[documentation and handoff](../03-documentation-and-handoff/plan.md).
