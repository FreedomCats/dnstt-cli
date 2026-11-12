---
kind: task
status: done
detailed: true
depends: [01-menu-and-commands]
---

# Editing the profile in the menu

## Task

The §10a items that change the profile, and the connect-time offer that keeps the resolvers
visible: before a connection the menu prints the profile's resolvers and offers to connect
with them (Enter) or change them first; the changed list is saved to the profile. The items
are: check resolvers with the offer to drop the dead ones, edit resolvers, a new exit
password, a new domain or key.

The pieces exist and are reused, not copied: the `resolvers check` body
(`internal/cli/resolvers.go`), the shared validators (`config.ValidDomainName`,
`config.IsValidPubkey`, `resolvers.Expand`) and its secret-store pattern: the secret is
written first, the profile second, a failed save removes the secret without printing it, and a
failed delete names the error without the value (`internal/cli/wizard.go:91-122`). `profile
set` stays the placeholder it is; the menu edits the profile directly through the same
`SaveProfilesTo`/`SaveProfiles` calls.

The editing code is a method of `*menu`, not a branch of the loop, because the mid-session
offer of child 3 reuses the same editor: it has the prompter, the store and the save seam in
one place. `menu -connect` and the `connect` command keep their behaviour: only the loop's
connect item asks, so nothing is asked on a command line.

Out of scope: the menu loop and the start (child 1), the mid-session offer (child 3), and any
editing from the command line beyond what exists.

## Steps

1. **The check body returns its outcomes** — `internal/cli/resolvers.go`.

   The menu cannot offer to drop the dead ones from the printed lines: it needs the outcome
   per profile entry. `resolverCheck` (`:160-165`) gains `entry string` — the profile entry
   the check came from: `r.Name()` for an entry of the list (a preset stays its preset name, a
   hand-written entry comes back in the normalized form `Expand` produced), the constant
   `resolvers.NetworkEntry` for the network expansion (`:194-197`), and `""` for the bootstrap
   parse error (`:171-174`), which is not a resolver of the profile.

   `runResolverChecks` (`:114-158`) keeps its signature, because `resolvers_test.go:107`,
   `:153`, `:188` and `:242` call it with a fake dial and assert its error and its output;
   it becomes `_, err := checkResolvers(...); return err`. The new

       func checkResolvers(ctx context.Context, e *env, profileName string, prof *config.Profile,
           domain dns.Name, pubkey []byte, expanded []resolvers.Resolver,
           networkDNS func() ([]netip.Addr, error), dial resolverDialFunc) ([]resolverOutcome, error)

   holds the loop: the same `Checking %d resolver(s)...` line, the same `FAILED (%v)` and
   `OK (%v)` lines, the same `session.Close()` and the same `\nNo resolver passed the
   check.\n` on stderr with `&exitCodeError{code: exitError}` when nothing passed — the error
   path returns no outcomes, so an all-failed run has nothing to drop. Every check appends

       // resolverOutcome is one checked entry: the profile entry it came from, the name that
       // was printed, and whether the handshake passed.
       type resolverOutcome struct {
           entry string
           name  string
           ok    bool
       }

   Add `checkProfileResolvers(ctx context.Context, e *env, profileName string) ([]resolverOutcome, error)`
   holding what `runResolversCheck` does around the loop (`:68-109`): the load, the default
   profile when the name is empty (`no default profile set`), the lookup, the domain and key
   parse, `resolvers.Expand` with the empty-list-becomes-`["network"]` rule, and the call with
   `app.SystemNetworkDNS` and `client.Dial`. `runResolversCheck` keeps its argument parsing
   (`0` or `1` name, the usage error) and returns `checkProfileResolvers`' error, so the
   command's output, exit code and messages stay exactly as they are.

2. **The wizard's questions as functions** — `internal/cli/wizard.go`.

   The menu needs the same four questions with a current value offered and without the wizard
   value. Extract them as free functions over the prompter, so the wording and the rules stay
   in one place; the methods become one-line delegates and `wizard_test.go` keeps passing:

   - `askResolverList(ctx, ask, out) ([]string, error)` from `askResolvers` (`:286-320`): the
     questions `Resolver (Enter for "network"): ` and `Another resolver (Enter to finish): `,
     `resolvers.Expand` on every entry, a rejected entry printed and asked again, duplicates
     dropped by their normalized form, an empty first answer returning `[]string{"network"}`,
     an empty later answer finishing.
   - `askDomainValue(ctx, ask, out, current string) (string, error)` from `askDomain`
     (`:149-162`) and `askPubkeyValue` from `askPubkey` (`:165-178`): with `current == ""` the
     questions and the messages are the wizard's today; with a value the question carries it
     as the default, `Domain [<masked>]: ` / `Server public key [<masked>]: `, and an empty
     answer returns `current`. Mask with `maskSensitive` (`internal/cli/profile.go:173-178`),
     as `profile show` does.
   - `askExitPassword(ctx, ask, out, exitType string) (string, error)` from `askPassword`
     (`:252-281`): hidden input twice, non-empty, no control characters, the SOCKS5 255-byte
     cap, the mismatch re-asked, and never the value in a message.

   Keep `readLine`/`readSecret`'s `readError` (`:355-379`) in the extracted functions: an EOF
   must still become `errCancelled` and a cancelled context itself.

3. **The table, the connect item and the check seam** — `internal/cli/menu.go`.

   `menuItems()` (`:234-241`) gains three items after "check resolvers", in §10a order:
   `connect`, `check resolvers`, `edit resolvers`, `new exit password`, `change domain or
   key`, `another profile`, `delete profile`; `0` stays exit. The number is the position, so
   nothing else in the dispatch changes.

   `connectItem` (`:312-318`) asks the resolver offer before the body:

       profiles, err := config.LoadProfiles()
       ...
       name := currentProfile(profiles)
       if err := m.offerResolvers(ctx, e, profiles, name); err != nil {
           return err
       }
       return m.connect(ctx, e, connectRequest{profileName: name}, abort)

   The offer must run before the body because the body loads the profile from the file: an
   edited list is used by this connect exactly because it was saved first. `connectOnce`
   (`:141-150`, `menu -connect`) is not touched.

   The `check` seam (`:45`, `:58`) becomes
   `check func(ctx context.Context, e *env, name string) ([]resolverOutcome, error)`,
   defaulting to `checkProfileResolvers`: the menu only ever checks the current profile, and
   it needs the outcomes. `checkResolversItem` moves out of `menu.go` (step 5).

   Extract `isYes(answer string) bool` from the delete item's switch (`:377-382`) and use it
   in the delete item and in the two confirmations of this task.

4. **The resolver editor and the connect offer** — new `internal/cli/menu_edit.go`.

   Shared helpers, used by the offer, the editor and the check item:

   - `profileResolvers(prof *config.Profile) []string` — the list as a connect uses it: an
     empty list is `["network"]`, the §10 default.
   - `printResolvers(w io.Writer, name string, list []string)` — `Resolvers of profile
     "<name>":` and one entry per line, two spaces in front.

   `offerResolvers(ctx, e, profiles, name)`:

       Resolvers of profile "home":
         network
       Connect with these resolvers? [Enter to connect, e to edit, n for the menu]:

   An empty answer connects, `e` calls `editResolvers(ctx, e, name)` and then connects with
   what was saved, `n` returns nil and the loop shows the menu again. The answer is trimmed
   and lower-cased; an unknown answer prints the three keys and asks again; the end of input
   returns `errCancelled`, which the
   loop turns into "the menu closes" (`menu.go:223-226`). A cancelled context comes back as
   itself. `m.ask` is the only reader: no new `linePrompter`.

   `editResolvers(ctx, e, name) error` is the editor child 3 reuses: load the profiles, print
   the current list with `printResolvers`, ask with `askResolverList` (step 2) — so every
   entry goes through `resolvers.Expand`, a rejected entry prints its reason and is asked
   again, an empty first answer is `network`, an empty later answer finishes — then set
   `prof.Resolvers = list`, call `m.save(profiles)` and print `Resolvers of profile "<name>"
   saved.` Nothing is written before the whole list is valid, and a read error
   (`errCancelled`, `context.Canceled`) returns before the save.

   `editResolversItem` is the table entry: load, `currentProfile`, `editResolvers`.

5. **The check item with the drop offer** — `internal/cli/menu_edit.go`.

   `checkResolversItem` loads the profiles, takes `currentProfile`, calls `m.check(ctx, e,
   name)` and returns its error as it is: an `exitCodeError` means nothing passed, the body
   has printed the line, and `reportItemError` stays silent — there is no offer, because
   removing every resolver is not offered. Otherwise:

   - `failedEntries(outcomes)` — the entries with no passing check, in the order of the
     checks, without duplicates; `keptEntries(outcomes)` — the entries with at least one
     passing check, in the same order, without duplicates (the network entry expands to one
     check per system server and must stay once). An entry stays if any of its checks passed.
   - Nothing failed: return nil, no question.
   - Some failed: print `%d of %d resolvers failed: %s` with the failed entries, then ask
     `Remove them and save the profile? (y/N): `. `isYes` — reload the profiles (the check
     took a while), set `prof.Resolvers = keptEntries(outcomes)`, `m.save`, then
     `printResolvers` with the saved list. Anything else returns nil and writes nothing. Guard
     `len(kept) == 0` with a return: it cannot happen, and it must not write an empty list.

6. **A new exit password** — `internal/cli/menu_edit.go`.

   `newExitPasswordItem` loads the profiles and takes the current profile. Then:

   - No exit login (`prof.Exit.User == ""`): print `profile "<name>" has no exit login, so an
     exit password is not used` and return nil, nothing asked and nothing written — a
     `password_ref` without a user is refused at connect time
     (`internal/app/tunnel.go:102-113`), and the menu must not build that profile.
   - Ask with `askExitPassword` (step 2), the exit type normalized the way the connect does
     it (`""` is `socks5`).
   - `store, err := m.store()` — the seam `runWith` already uses; a store error is returned.
   - `ref, err := store.Put(newPasswordName(name, prof.Exit.PasswordRef), password)`: an error
     is `cannot store the exit password: %w` and the profile is untouched.
   - `old := prof.Exit.PasswordRef; prof.Exit.PasswordRef = ref; m.save(profiles)`. A failed
     save must leave the old reference and the old value: delete the *new* entry and return
     the save error joined with the wizard's wording (`internal/cli/wizard.go:112-121`) —
     `the exit password could not be removed from the secret store: %w`, or `the exit
     password was removed from the secret store` — never the value.
   - After a successful save, and only then, delete `old` when it is not empty. A failed
     delete is a line on `e.stderr`, `the previous exit password could not be removed from the
     secret store: %v`, and the item still succeeds: the profile already points at the new
     entry.
   - Print `Exit password of profile "<name>" updated.`

   `newPasswordName(profile, oldRef string) (string, error)`: `profile + ".password"` when
   there is no reference yet — the wizard's name — and `profile + ".password-" +
   hex(random)` when there is. A fresh name is what makes the order possible: `Put` under an
   existing name overwrites the value under the same id
   (`internal/config/secret.go:31-42`, `:143-176`), so a failed save would leave the profile
   pointing at the new password with the old one already gone. Use `crypto/rand` and
   `encoding/hex`, both standard library.

7. **A new domain or key** — `internal/cli/menu_edit.go`.

   `changeServerItem` loads the profiles, takes the current profile, asks
   `askDomainValue(ctx, m.ask, e.stdout, prof.Domain)` and then
   `askPubkeyValue(ctx, m.ask, e.stdout, prof.Pubkey)`: the current value is the default in
   the question, masked, and Enter keeps it; an invalid answer prints the reason and is asked
   again. Both answers are collected before anything is written. Neither changed: print
   `nothing changed` and return nil without a save. Otherwise set both fields, `m.save`, and
   print `Profile "<name>" updated: domain <masked>, key <masked>.` — the next connect reads
   the file, so the next connection uses them.

8. **Tests** — new `internal/cli/menu_edit_test.go`; `internal/cli/menu_test.go` updated.

   Reuse the harness: `menuHarness` (`menu_test.go:88-144`), `scriptedPrompter`
   (`wizard_test.go:31-58`), `fakeSecretStore` (`:68-117`), `isolatedConnectConfig`
   (`connect_test.go:374-390`), `writeMenuProfiles`, `runLoop`.

   `menu_test.go` must change for the new table and the offer; every one of these is
   mechanical:

   - the `check` fake becomes the new signature and records the name; add
     `checkOutcomes []resolverOutcome`;
   - `menuProfile()` gains an exit with a login (`Exit: config.Exit{Type: "socks5", User:
     "user"}`), or the password item refuses in the table test;
   - `TestMenuItemsCallTheCommandBodies` (`:316-367`): seven cases, the labels of step 3, and
     the answers of the new items — connect: the choice, the offer's empty answer, then `0`;
     edit resolvers: the choice, one entry, an empty answer, then `0`; new exit password: the
     choice, the two hidden answers, then `0`; domain or key: the choice, a new domain, an
     empty answer for the key, then `0`; "another profile" and "delete profile" move to `6`
     and `7`;
   - the connect item's answer list in `TestMenuEmptyAnswerConnects` (`:370-384`),
     `TestMenuFailedItemIsPrintedAndTheLoopContinues` (`:660-675`) and the four signal tests
     (`:899-1021`) gains the offer's answer, and `TestMenuThirdInterruptDoesNotExit` reads the
     offer's question from `waitingPrompter.asks` before the next `Choice [1]: ` — without it
     the scripted prompter hits EOF at the offer and the test waits for a connection that
     never starts;
   - `TestMenuCheckResolversExitsAndSurvives` (`:640-658`) asserts the name, not a slice.

   `menu_edit_test.go`:

   - the offer: the resolvers are printed; Enter connects and writes nothing; `e` opens the
     editor, the saved list is on disk and the connect runs; `n` returns to the menu without
     a connection; an unknown answer re-asks; EOF at the offer closes the menu with nothing
     written; `runConnect` (`menu -connect`) asks nothing (the existing
     `TestMenuConnectFlagRunsOneConnection` already proves it with no answers at all);
   - the editor: the current list is shown; a valid list is saved and read back from the
     file; an invalid entry prints the reason and is asked again with nothing written; an
     immediate empty answer saves `["network"]`; EOF saves nothing and closes the menu; a
     failed save is reported;
   - the check: a mixed run names the failed entries and `y` writes the list without them (a
     network entry with one passing server stays once); `n` writes nothing; nothing failed —
     no question; nothing passed (`&exitCodeError{code: exitError}`) — no question, nothing
     written, no reprint on stderr, the loop continues; a failed save is reported;
   - the password: asked twice with `AskSecret`, stored, `password_ref` changes on disk, the
     old reference is deleted after the save and not before; a failed save keeps the old
     reference on disk, removes the new entry (`store.deleted` has the new reference) and
     leaves the old value in `store.values`; a failed delete prints the line and succeeds; an
     empty, a control-character and a mismatched answer are asked again; no exit login —
     nothing asked, nothing written; a `Put` failure and EOF at either question write
     nothing; the value appears neither in `profiles.toml` nor in stdout/stderr (read the
     file with `os.ReadFile`);
   - the domain or key: both changed are saved and `config.LoadProfiles` returns them; an
     empty pair keeps both with no save; an invalid domain and an invalid key are asked again;
     a failed save is reported; EOF writes nothing.

   No test dials, opens a terminal or touches a real store.

9. **The local matrix** — on the final tree, the same as child 1:

   - `gofmt` outside `core/` (never run through it, `doc/architecture.md` §02);
   - `go build ./...` and `CGO_ENABLED=0 go build ./...`;
   - the CI vet split on Linux and with `GOOS=windows`:
     `go vet $(go list ./... | grep -v /core/)` and
     `go vet -testinggoroutine=false ./core/...`;
   - `go test -race -count=1 ./...` with `DNSTT_CLI_TEST_DBUS=1`, `DNSTT_CLI_TEST_NETNS=0`
     and
     `-skip '^(TestDualStackListener|TestLoopbackIndexBlocks|TestPhysicalIndexPasses|TestFamilyWithoutRouteRefuses)$'`
     — the four external `netguard` live tests, so no live call is made;
   - the Windows cross-build `GOOS=windows GOARCH=amd64 go build ./...` and
     `GOOS=windows go test -c` for every package with tests, which compiles the new files and
     tests;
   - `git diff --check`.

Easy to get wrong:

- the offer's answer is read with `m.ask`, and its EOF must go through `readError`: a bare
  `io.EOF` returned from an item is reported as a failure instead of closing the menu;
- the offer must be before `m.connect` and the edited list must be saved first: the body loads
  the profile from the file, so an unsaved list would not be used by "this connect";
- `menu -connect` must stay question-free, and `connect` must not change at all;
- `resolverCheck.entry` for the network expansion must be `resolvers.NetworkEntry`, not the
  per-server resolver's name: the per-server resolver is not in the profile, and dropping it
  would remove the wrong thing — or nothing;
- the kept list must be deduplicated (the network entry produces one check per system server)
  and must never be empty;
- the all-failed run returns `exitCodeError` with no outcomes: do not offer, and do not report
  the error again;
- `Put` under the profile's old name overwrites the old value under the same id — the whole
  rollback of step 6 depends on the new name;
- the old entry goes only after `m.save` returned nil, and no message ever carries a secret
  value: `Put`/`Delete` errors name the reference, the wizard's wording is the model;
- `m.save` writes the whole file: reload before mutating, or the edit would write back the
  copy loaded before a long check;
- `maskSensitive` in the questions, as `profile show` does: an unmasked domain or key in a
  question ends up in a screen recording and in the test log;
- the existing menu tests that answer the connect item will hang on `<-body.started` until
  they answer the offer, and `TestMenuItemsCallTheCommandBodies` fails on `len(items) !=
  len(tests)` until the three new cases are there;
- `resolvers_test.go` must pass unchanged: `runResolverChecks` and `resolverChecks` keep their
  signatures.

## Done when

- Connect prints the current profile's resolvers (an empty list shown as `network`), then
  asks: Enter connects with them, `e` opens the resolver editor and the saved list is what the
  connection uses, `n` returns to the menu without connecting; an unknown answer re-asks; the
  end of input closes the menu; `menu -connect` asks nothing and connects at once, and the
  `connect` command is unchanged.
- The resolver editor shows the current list and asks one entry per line with the wizard's
  questions — an empty first answer is `network`, any later empty answer finishes; every
  entry goes through `resolvers.Expand`, a rejected entry prints the reason and is asked
  again; an empty result is saved as `["network"]`, the §10 default; `profiles.toml` is
  written only after a valid list, and the saved list is printed back.
- The check item runs the same body as `resolvers check` for the current profile; when some
  entries failed it names them and offers to remove and save; `y` writes the list without them
  (an entry with any passing check stays, once), a refusal writes nothing, and a run where
  nothing passed offers nothing — removing every resolver is not offered — and its
  `exitCodeError` is not reported twice.
- A new exit password is asked twice by hidden input, non-empty, without control characters,
  and is refused when the profile's exit has no login; it goes to the store under a name of
  its own, `exit.password_ref` is updated, a failed save removes the new entry and leaves the
  old reference and the old secret in place, and the old entry is deleted only after a
  successful save (a failed delete is printed without the value).
- A new domain or key is asked with the wizard's validators and the current value as the
  masked default (Enter keeps it); an invalid answer is asked again; neither changed writes
  nothing; the file is written once after both questions and the next connect uses it.
- End of input at any question of any item cancels the edit and writes nothing; in the loop it
  closes the menu.
- The items sit in §10a order after "check resolvers", numbered by their position, with `0`
  as exit; the dispatch and the signals are untouched.
- Tests use a temporary configuration directory, a fake store, `scriptedPrompter` and fake
  command bodies: each edit's save, each failure path, the empty result and EOF; no secret
  appears in the TOML or in the output; the existing menu tests pass with the offer's answer.
- The local matrix of step 9 passes: `gofmt` outside `core/`, both builds, the vet split on
  Linux and on `GOOS=windows`, the full `-race` suite with `DNSTT_CLI_TEST_DBUS=1`,
  `DNSTT_CLI_TEST_NETNS=0` and the four external `netguard` live tests excluded by `-skip`,
  the Windows cross-build with `go test -c`, and `git diff --check`.
- No live run, no real profile, store or resolver, no network, no new dependency; `core/`,
  `doc/architecture.md` and `README.md` are untouched; Windows is cross-built only, `not
  seen`.

## Outcome

Done: the menu edits the profile in place, and the connect item keeps the resolvers in sight
before a connection.

- The check body returns its outcomes (`internal/cli/resolvers.go`): `resolverCheck` carries
  `entry` — the profile entry the check came from, `resolvers.NetworkEntry` for the network
  expansion and `""` for the bootstrap parse error — and `checkResolvers` holds the loop,
  returning one `resolverOutcome` per check. `checkProfileResolvers` holds what
  `runResolversCheck` did around the loop, so the command keeps its arguments, output, messages
  and exit code; `runResolverChecks` and `resolverChecks` keep their signatures, and
  `resolvers_test.go` passes unchanged. An all-failed run returns the `exitCodeError` with no
  outcomes: there is nothing to drop.
- The wizard's questions are free functions over the prompter (`internal/cli/wizard.go`):
  `askResolverList`, `askDomainValue`, `askPubkeyValue`, `askExitPassword`. With a current
  value the domain and key questions carry it masked as the default and an empty answer keeps
  it; with none the wording and the messages are the wizard's. The methods are one-line
  delegates, so `wizard_test.go` passes unchanged, and `readError` still turns the end of input
  into `errCancelled` and a cancelled context into itself.
- The table (`internal/cli/menu.go`) is seven items in §10a order — connect, check resolvers,
  edit resolvers, new exit password, change domain or key, another profile, delete profile —
  numbered by position with `0` as exit, so the dispatch and the signals are untouched. The
  `check` seam takes the profile name and returns the outcomes; `isYes` is shared by the delete
  item and the two confirmations.
- The connect item asks before the body (`internal/cli/menu_edit.go`): the resolvers of the
  current profile are printed (an empty list as `network`), Enter connects, `e` opens the
  editor and connects with what was saved, `n` returns to the menu, an unknown answer prints
  the three keys and asks again, and the end of input closes the menu. The offer runs first
  because the body loads the profile from the file: an edited list is used by this connect
  exactly because it was saved first. `menu -connect` and the `connect` command are untouched
  and ask nothing.
- The editor shows the current list and asks one entry per line with the wizard's questions;
  every entry goes through `resolvers.Expand`, a rejected entry prints its reason and is asked
  again, an empty first answer is `network`, an empty later answer finishes. The file is
  written only after the whole list is valid, and the saved list is printed back.
- The check item names the failed entries and offers to remove and save them; `y` reloads the
  profiles (the check took a while), writes the list without them and prints it, anything else
  writes nothing. An entry stays if any of its checks passed and appears once, so the network
  entry that expanded to several servers is not duplicated; an empty kept list is refused
  before the save. A run where nothing passed offers nothing and its `exitCodeError` is not
  reported twice.
- A new exit password is asked twice by hidden input, non-empty, without control characters,
  with the SOCKS5 cap applied to an absent exit type as the connect applies it, and is refused
  when the profile has no exit login — a `password_ref` without a user is refused at connect
  time, and the menu must not build that profile. The secret goes to the store under a name of
  its own (`<profile>.password`, or `<profile>.password-<hex>` when a reference exists), the
  profile is saved, and only then is the old entry deleted; a failed save removes the new entry
  and leaves the old reference and the old value in place, and a failed delete is a line on
  stderr. No message carries the value.
- A new domain or key is asked with the wizard's validators and the current value as the masked
  default; both answers are collected before anything is written, neither changed prints
  `nothing changed` without a save, and otherwise the file is written once and the next connect
  reads it.
- Tests: `internal/cli/menu_edit_test.go` (new) covers the offer, the editor, the check item,
  the password and the domain or key, including every failure path, the empty result and the
  end of input, on buffers, a scripted prompter, a fake store and a temporary configuration
  directory; `internal/cli/menu_test.go` gained the offer's answers, the seven-item table and
  the new `check` signature. No test dials, opens a terminal or touches a real store.
- Verified: the full local matrix passed — `gofmt` outside `core/`, `go build ./...`,
  `CGO_ENABLED=0 go build ./...`, the vet split on Linux and on `GOOS=windows`, the full
  `go test -race -count=1 ./...` with `DNSTT_CLI_TEST_DBUS=1`, `DNSTT_CLI_TEST_NETNS=0` and the
  four external `netguard` live tests excluded by `-skip` (no live call), the Windows
  cross-build with `go test -c` of all 19 packages that have tests, and `git diff --check`. No
  new dependency.

Decisions taken while working:

- `offerResolvers` returns `(bool, error)`, not the error alone as the step's snippet shows:
  with one error the item cannot tell "connect" from "back to the menu" without a sentinel, and
  the loop's `errCancelled` rule must stay the only way an item closes the menu.
- The editor prints the saved list and the `saved.` line: the step asks for the line, the
  acceptance for the printed list, and both are what the person needs to see.
- The editor shows the current list through `profileResolvers`, so an empty list is shown as
  `network` — the same list the connect uses.
- The failed and kept lists are aggregated by profile entry, and the count in the question is
  the number of distinct entries, not of checks: the network entry expands to one check per
  system server.

Recorded limitations:

- `resolverOutcome.name` was filled but never read: the printed name is the body's, and the
  field was meant for the mid-session offer of child 3, which names a resolver without
  re-running the check. Child 3 takes the names from the supervisor's event instead, so the
  field was removed when the stage was accepted.
- The real terminal — the hidden input, the masked defaults, the offer's keys — is a manual
  check that was not made; Windows is cross-built and test-compiled only, `not seen`.
