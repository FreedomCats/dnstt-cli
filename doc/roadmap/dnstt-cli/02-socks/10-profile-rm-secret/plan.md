---
kind: task
status: done
detailed: true
depends: [06-profiles-secrets, 09-profile-wizard]
---

# Delete a profile's secret with the profile

## Task

`profile rm` removes the profile from `profiles.toml` and leaves the entry that
`exit.password_ref` points at in the secret store (`internal/cli/profile.go:167-203`). The
[interactive profile wizard](../09-profile-wizard/plan.md) writes a secret for every
authenticated profile, so deleting such a profile leaves its password in `secrets.json` or
the keyring with nothing pointing at it. `SecretStore.Delete`
(`internal/config/secret.go:38-41,264-291`) already names `profile rm` in its contract and
treats an absent entry as success: the command must carry the removal through, and must not
check `Get` first or repeat that rule.

The reference comes from the profile, never from recomputing `<name>.password`: the store
name is only a label, the reference is the contract. The order is fixed: the secret goes
first, then `profiles.toml` is saved. A secret whose profile is gone would stay in the store
with nothing pointing at it and no command able to find it; a profile left without its
secret is visible in the file, and the command can say so. So a real `Delete` error leaves
the profile in place and returns the store's error, and a failed save after a successful
delete reports both facts. `profile rm` never reads the value (`Get` is not called), so no
message of its own can contain the secret.

The last profile is part of the same fix. `SaveProfiles` today refuses an empty set
(`validateProfiles`, `internal/config/profiles.go:138-140`), so removing the only profile
fails and, with the secret first, would lose its password. The no-profile state is made
explicit instead: a missing `profiles.toml` means "no profiles". `LoadProfilesFrom` returns
an empty set for a missing file, and `SaveProfilesTo` removes the file when the set is
empty instead of writing it. The write path stays one call: `profile rm` deletes the map
entry and calls `saveProfiles`; no branch for the last profile and no second test seam. The
choice over a separate `RemoveProfiles` API keeps one invariant in one place — "no profiles
equals no file" — on both the read and the write side, and the existing save-failure path
covers a failed removal of the file as well. `validateProfiles` is not relaxed: an existing
empty or invalid `profiles.toml` is still an error and is not overwritten; only the missing
file is the empty set.

With the missing file as the empty set, `profile list` prints its existing "No profiles
configured." message, `profile add` starts from empty (the `fs.ErrNotExist` special case at
`internal/cli/profile.go:218-225` goes away with the `io/fs` import), and `connect` and
`resolvers check` keep their own clear error: with no profiles they report `no default
profile set`, exactly as they already do for a file without a default. `profile show` and
`profile set` report `profile %q not found`, as they already do for a name that is not in
the set. No new UX. After the last profile the store file may remain with an empty entry
map; it holds no secrets and the next `profile add` reuses it.

A profile without `exit.password_ref` (no-auth SOCKS5, an HTTP profile before a password was
set) is removed without opening the store. A missing profile fails before any store access.
The keyring branch of `Delete` is covered in `internal/config/secret_keyring_test.go`
(`TestKeyringDelete`); the CLI has one scheme-agnostic call and does not repeat it.

Scope: `internal/config/profiles.go` (the missing-file and empty-set handling only),
`internal/cli/profile.go` and their tests, plus one field on the test fake in
`internal/cli/wizard_test.go`. The wizard, `profile set` and `connect` keep their
behaviour; only the `rm` line of `profile -h` changes. No new dependency, no download, no
live access, no real keyring or config.

## Steps

1. **Make the no-profile state explicit in `internal/config/profiles.go`.**
   - `LoadProfilesFrom`: before wrapping a read error, check
     `errors.Is(err, os.ErrNotExist)` and return `&Profiles{Profile: map[string]*Profile{}}`
     with no error. `errors` and `os` are already imported. Document on `LoadProfiles` and
     `LoadProfilesFrom` that a missing file is the empty set while an existing empty or
     invalid file is an error.
   - `SaveProfilesTo`: at the top, when `p == nil || len(p.Profile) == 0`, remove the file
     instead of validating and writing. A small unexported helper does `os.Remove(path)`,
     treats `os.ErrNotExist` as success and wraps any other error as `profiles %s: %w`.
     Document on `SaveProfiles` and `SaveProfilesTo` that the file is never written empty,
     because an existing empty file is invalid; the empty set is the absence of the file.
   - Leave `validateProfiles` itself unchanged. No other config change.

2. **Add the test seams.** In `internal/cli/profile.go`, above `runProfileRm`:

   ```go
   // Indirections for the tests: a failing store and a failing save are not reachable
   // from an isolated temporary config otherwise.
   var (
       newSecretStore = config.NewSecretStore
       saveProfiles   = config.SaveProfiles
   )
   ```

   Unexported, no behaviour change. `runProfileAdd` keeps its direct calls: the wizard
   already takes its store and its save function as fields.

3. **Rewrite `runProfileRm`** (`internal/cli/profile.go:167-203`) and simplify
   `runProfileAdd`:
   - Keep the argument checks, `config.LoadProfiles` and the `not found` error in that
     order: a missing profile returns `profile %q not found` before the store is touched.
   - Keep the profile pointer from the map and take `ref := prof.Exit.PasswordRef`. When
     `ref != ""`, open the store with `newSecretStore()` and delete the reference before
     anything is written:
     - constructor error → `profile %q was not removed: cannot open the secret store:
       <cause>`, wrapped with `%w`;
     - `Delete` error → `profile %q was not removed: the exit password could not be
       deleted from the secret store: <cause>`, wrapped with `%w`.
     Nothing is written to `profiles.toml` on either path. Add a comment naming the order
     and the reason: an orphan secret is invisible and permanent, a profile without its
     secret is visible in the file.
   - Then delete the map entry and fix `DefaultProfile` exactly as now (lines 186-195), and
     call `saveProfiles(profiles)` — for the last profile this removes `profiles.toml`. On
     error, return it as before when `ref == ""`; otherwise join it with `profile %q was
     not removed from profiles.toml, but its exit password was removed from the secret
     store` via `errors.Join` and `fmt.Errorf` with the name. `errors` is already
     imported.
   - Keep the success line `Profile %q removed.`; in the `rm` line of the command help
     (`internal/cli/profile.go:26`) say that the stored exit password is removed too, with
     the column spacing of the neighbouring lines.
   - Do not call `Get`; do not put the reference or a secret value into the new messages.
   - In `runProfileAdd`, replace the `errors.Is(err, fs.ErrNotExist)` branch with a plain
     `config.LoadProfiles()` and drop the `io/fs` import; the loaders now hand back the
     empty set. The comment about a corrupt file moves to the loader's doc.

4. **Teach the fake store to fail `Delete`.** In `internal/cli/wizard_test.go:67-113` add
   `deleteErr error` to `fakeSecretStore` and return it from `Delete` after recording the
   call. With `deleteErr == nil` the wizard tests behave as before.

5. **Tests.**
   - `internal/config/profiles_test.go`:
     - a missing `profiles.toml` loads as an empty set: no error, `len(Profile) == 0`, the
       map is not nil;
     - saving one profile and then the empty set removes the file (no error, no
       `profiles.toml.tmp`), the same save with the file already missing is not an error,
       and reloading gives the empty set again.
   - `internal/cli/profile_test.go` (package `cli`; `isolatedConnectConfig`, `runCLIInput`;
     no `t.Parallel` — the package has none, so the swapped package variables do not race
     under `-race`; every swapped seam is restored with `t.Cleanup`). Add a fixture helper
     that saves two valid profiles — `home` with the exit under test and a no-auth `work` —
     for the tests that need a set which stays non-empty; the secret is
     `config.NewSecretStoreFor(path).Put("home.password", "synthetic-exit-password")`, and
     the reference is taken from there, never spelled out (the scheme is `file` on unix,
     `dpapi` on Windows).
     - `TestProfileRmDeletesSecret`: `home` is authenticated (SOCKS5 login) and the default;
       exit 0, stdout `Profile "home" removed.`, the reloaded file has no `home`, the
       default moved to `work`, `Get(ref)` fails and `secrets.json` does not contain
       `home.password`.
     - `TestProfileRmWithoutPasswordRefLeavesStoreClosed`: swap `newSecretStore` for a
       constructor that records the call; exit 0, the profile is gone, the constructor was
       not called and `secrets.json` does not exist.
     - `TestProfileRmMissingProfileFailsBeforeStore`: same recording constructor; exit 1
       with `not found`, the constructor was not called, `profiles.toml` is byte-identical
       and `Get(ref)` still returns the password.
     - `TestProfileRmKeepsProfileWhenSecretDeleteFails`: two subtests — the constructor
       returns an error, and a fake store has `deleteErr` set. Both exit 1, the message says
       the profile was not removed, `profiles.toml` is byte-identical and no
       `profiles.toml.tmp` is left; the second subtest also checks that the fake saw
       exactly `ref` and one `Delete`.
     - `TestProfileRmReportsPartialStateWhenSaveFails`: real store; after the fixture is
       written, swap `saveProfiles` for one returning
       `errors.New("synthetic save failure")`. Exit 1, stderr contains that cause and
       `was not removed from profiles.toml, but its exit password was removed from the
       secret store`; the reloaded file still has `home` with `ref`, and `Get(ref)` fails.
     - `TestProfileRmToleratesMissingSecret`: `home` carries a reference whose entry was
       never stored; exit 0 and the profile is removed — the command must not pre-check.
     - `TestProfileRmLastProfileRemovesFile`: one authenticated profile with a secret, no
       `work`; `profile rm home` exits 0, `profiles.toml` does not exist, `Get(ref)` fails
       and `secrets.json` does not contain `home.password`; `profile list` exits 0 and
       prints `No profiles configured.`; `connect` exits 1 with `no default profile set`;
       then `profile add` with piped answers (name Enter, `t.example.com`,
       `wizardTestPubkey`, `socks5`, an empty login, an empty resolver, `socks`) writes
       `home` again, which loads and is the default.
     - The error tests also assert that stderr does not contain the password.

6. **Verify.** `gofmt -l` on the changed files is empty;
   `go vet $(go list ./... | grep -v /core/)` and
   `go vet -testinggoroutine=false ./core/...`; `CGO_ENABLED=0 go build ./...`;
   `go test -race -count=1 ./...`; `GOOS=windows go build ./...` and
   `GOOS=windows go test -c -o build/out/cli.test ./internal/cli` (the same with
   `build/out/config.test ./internal/config`). No new `go.mod`/`go.sum` line, no live
   access.

## Done when

- `profile rm <name>` for a profile with a non-empty `exit.password_ref` passes exactly that
  reference to `SecretStore.Delete`, removes the profile and keeps the default-profile
  handling; the password is gone from an isolated temporary file store and no
  `secrets.json` entry with the profile's store name remains. The keyring branch of `Delete`
  stays covered by `internal/config/secret_keyring_test.go`.
- Removing the only profile works: the secret is deleted, the profile goes, and
  `profiles.toml` is removed rather than written as an empty set; afterwards `profile list`
  prints `No profiles configured.`, `connect` reports its existing `no default profile set`,
  and `profile add` creates a profile again from the empty state.
- `LoadProfiles` treats a missing `profiles.toml` as an empty set; an empty set passed to
  `SaveProfilesTo` removes the file and a missing file is not an error; an existing empty or
  invalid `profiles.toml` is still an error and is not overwritten.
- A profile without `password_ref` is removed with the store constructor never called and no
  `secrets.json` created.
- A missing profile exits 1 with `not found` before the store constructor is called, and the
  profile file and the secret stay as they were.
- A store constructor or `Delete` error exits 1, leaves `profiles.toml` byte-identical with
  no temporary file, and says the profile was not removed; an entry already gone from the
  store does not stop the removal.
- A failed save after a successful delete exits 1, the file still holds the profile, the
  secret is gone, and the message says both: the profile was not removed from
  `profiles.toml` and its exit password was removed from the secret store.
- No stdout or stderr of any path contains the stored password. `profile add`, `show`, `set`,
  `list`, `connect` and the wizard keep their behaviour except the no-profile state above;
  `profile -h` mentions that `rm` removes the stored exit password too.
- `gofmt` is clean, the CI vet split, `CGO_ENABLED=0 go build ./...`,
  `go test -race -count=1 ./...`, `GOOS=windows go build ./...` and the Windows test
  compilation of `internal/cli` and `internal/config` pass; no new dependency, `go.mod` and
  `go.sum` unchanged.

## Risks and what is needed from the user

- A `password_ref` in another platform's scheme (`dpapi:` on Linux, `file:` on Windows) is
  refused by `Delete` as "not available on this system", so such a profile cannot be removed
  by `profile rm` and has to be deleted from `profiles.toml` by hand. This follows the
  store's existing contract; relaxing it for `Delete` would be a decision of its own.
- Removing the last profile deletes `profiles.toml`, so hand-written comments or ordering in
  it go with the file. The file is generated by the wizard and `profile rm`, and it holds no
  secrets, so this is accepted; the store file may stay behind with an empty entry map.
- Nothing is needed from the user for this task: the whole path is local, no keyring, no
  live run and no real config are involved.

## Depends on

[Profiles and secrets](../06-profiles-secrets/plan.md) and the [interactive profile
wizard](../09-profile-wizard/plan.md).

## Outcome

Done as planned.

- `internal/config/profiles.go` — a missing `profiles.toml` is the empty set:
  `LoadProfilesFrom` returns an empty, non-nil profile map. `SaveProfilesTo` removes the
  file for an empty or nil set instead of writing it, and a file that is already gone is
  not an error; the file is never written empty because an existing empty file is invalid.
  `validateProfiles` is unchanged, so an existing empty or invalid file is still an error
  and is not overwritten.
- `internal/cli/profile.go` — `runProfileRm` deletes the secret that
  `exit.password_ref` points at before anything is written: a store constructor or
  `Delete` error leaves the profile in place and returns the cause; a failed save after a
  successful delete reports both facts, joined so that the save cause is preserved for
  `errors.Is`. `Get` is never called, the reference and the value appear in no message.
  Removing the last profile removes `profiles.toml`. The `rm` line of `profile -h` says
  the stored exit password is removed too. `runProfileAdd` lost its `fs.ErrNotExist`
  branch: the loader now hands back the empty set itself.
- Tests — `internal/config/profiles_test.go`: the missing file as the empty set, an
  existing empty file still an error, the empty save removing the file with no `.tmp`
  left, and a repeated empty save over a missing file. `internal/cli/profile_test.go`: a
  two-profile fixture whose secret reference comes from `Put`; the secret is deleted with
  the profile and the default moves; no store is opened without a `password_ref`; a
  missing profile fails before the store; constructor and `Delete` failures leave
  `profiles.toml` byte-identical with no temporary file; a failed save reports the partial
  state; an entry already gone from the store does not stop the removal; removing the
  last profile removes the file and leaves `profile list`, `connect` and a new
  `profile add` working. The fake store in `internal/cli/wizard_test.go` learned
  `deleteErr`.

What came up that is not visible from the code: the Windows-vet recipe of the CI cannot
be repeated verbatim from a unix host — the `go list` in the command substitution expands
to the unix package set, where `internal/secretservice` is excluded by build constraints,
so `go vet` fails before it looks at any code. The check was run with the Windows package
list instead; on the Windows runner the CI form is the same thing.

Not changed, by design: a `password_ref` of another platform's scheme (`dpapi:` on unix,
`file:` on Windows) is still refused by `Delete`, so such a profile has to be removed from
`profiles.toml` by hand. The keyring branch of `Delete` stays covered by
`internal/config/secret_keyring_test.go`.

`gofmt`, the CI vet split, `CGO_ENABLED=0 go build ./...`, `GOOS=windows go build ./...`,
the Windows vet and the Windows test compilation of `internal/cli` and `internal/config`,
and the full `-race` suite pass. No new dependency; `go.mod` and `go.sum` unchanged.
