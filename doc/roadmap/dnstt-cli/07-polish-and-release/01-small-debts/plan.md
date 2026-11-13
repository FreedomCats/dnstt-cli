---
kind: task
status: done
detailed: true
---

# Small debts: text, behaviour and the two tests that hide a problem

## Task

Close the debts recorded at the stage acceptances that are still in the code, and say why the
rest stays. Each item below was read in the tree at the stage's `base` (`74e330d`); the line
numbers are of that tree. Nothing here is a feature: a hint that names three transports while
the parser names four, a confirmation that speaks of a password when a key is deleted, a `0`
that means "exit" everywhere except at one question, a link that is accepted and then refused
two questions later, a message that sends the person to a text editor, two literals that
duplicate the wizard's names, and two tests that are not honest about what they check.

The last part is the list that stays open. It is not a licence to skip work: every item there
is re-recorded with its reason in the report, so that the node's Outcome carries it and no
debt disappears silently.

## Steps

1. **The resolver hint names all four transports.** `internal/resolvers/presets.go:54`: the
   message becomes
   `resolver %q: not %s, no such preset (%s) and no transport (doh:, dot:, udp: or tcp:)`,
   matching `internal/resolvers/resolvers.go:133`. The third place the debt was recorded in is
   already right: `doc/architecture.md:271` names `DoH | DoT | UDP | TCP`, so that half of the
   record is closed and only the hint changes. In `internal/resolvers/resolvers_test.go:177`
   the assertion on the hint gains all four prefixes, so a hint that loses one fails.

2. **The delete confirmation names what is deleted.** `internal/cli/menu.go:394`: the word
   comes from the profile —
   `word := "exit password"; if prof := profiles.Profile[name]; prof != nil && prof.Exit.KeyRef != "" { word = "exit key" }`
   — and the question is `Delete profile %q and its %s? (y/N): `. The nil guard is there
   because a stale `default_profile` passes `validateProfiles`
   (`internal/config/profiles.go:175-187` does not check it) and `currentProfile` returns it
   anyway. The doc comment above the item (lines 381-383, "and its exit password") says
   "password or key". Tests: `internal/cli/menu_test.go:685` keeps the socks5 wording as it
   is; a new case with `oneSSHProfile("home", "file:home.key")` and the answer `n` asserts the
   question says `and its exit key?` and that nothing was removed.

3. **`0` at the `Profile: ` question.** `internal/cli/menu.go:363-378`: in the state "several
   profiles, no default" the answer `0` is taken as a name and answered with
   `profile "0" not found`, while `0` leaves the menu everywhere else (`askChoice`,
   line 281). Treat `0` as a cancel, like an empty answer — except when a profile is literally
   named `0`: `config.ValidProfileName` (`internal/cli/wizard.go:186-187`) allows digits, so
   such a profile can exist and must stay selectable. One condition:
   `if name == "" || (name == "0" && profiles.Profile[name] == nil) { return nil }`, and the
   doc comment above the item says it. Tests next to `TestMenuNoDefaultEmptyAnswerCloses`
   (`internal/cli/menu_test.go:800`): `0` alone closes the menu with no "not found" line and
   no second listing; a set holding `home` and `0` makes `0` the default and the menu continues
   with `Profile "0" (socks).`

4. **A `dnstt_ssh` link without a credential is refused by the parser.**
   `internal/config/links.go:175-194`, `linkSSHCredential`: with the type `password` an empty
   field 16 is an error naming field 16; with the type `key` `linkSSHKey` already names field
   24; with no type (SlipGate's older lines) an empty field 16 and an empty field 24 are an
   error naming both fields. The messages say what is missing and never repeat the link:
   `field 16: a password dnstt_ssh link carries no password`,
   `field 16 or field 24: a dnstt_ssh link carries a password or a private key`. Today such a
   line is accepted and fails later on the profile rule
   (`internal/config/profiles.go:261-262`, "needs exactly one of …") after the import has asked
   two questions. Tests in `internal/config/links_test.go`: two cases in
   `TestParseLinkRefusals` (line 320) — a password type with a key in field 24 and no password
   (wants `field 16`, must not mention `field 24`), and a login with neither field (wants
   `field 16` and `field 24`); the sub-test `a password type with no password` (line 513)
   becomes that refusal, keeping its point that the key of field 24 belongs to the other
   method. `internal/cli/profile_import_test.go` uses only links with a credential, so the
   import path needs no change; check that the error reaches the person through the import as
   it is.

5. **`profile set` stops sending people to a text editor.** `internal/cli/profile.go:350-351`:
   the two lines become one honest message — the command is not interactive yet, the profile is
   changed in the menu (the profile items, `another profile` among them) or created by
   `profile add`, which asks every field. The printed config path goes away with the advice, so
   `mustPath` (lines 355-361) has no caller left: delete it (and the import it used, if the
   compiler says it is unused). Interactive `profile set` stays out of scope and the message
   says so; the report repeats it as separate work for the Outcome. Test:
   `internal/cli/profile_test.go` with `runCLIInput` (line 31) — `profile set home` exits 0,
   names the menu and `profile add`, and prints no `Edit the file directly`;
   `profile set missing` still fails with `not found`.

6. **One definition of the secret suffixes.** `internal/cli/wizard.go:33-39`:
   `func (s exitSecret) secretSuffix() string` returns `.key` or `.password`, and
   `secretName(profile)` becomes `profile + s.secretSuffix()`.
   `internal/cli/menu_edit.go:238-240` calls it:
   `suffix, old := secret.secretSuffix(), prof.Exit.PasswordRef` with
   `if secret.key != "" { old = prof.Exit.KeyRef }`; the comment above `newSecretName`
   (lines 271-274) names the method instead of the two literals. No literal `.password` or
   `.key` is left in `menu_edit.go`. Test: a small unit check that `secretName("home")` is
   `home.password` and `home.key` for the two kinds and that `newSecretName` with the suffix
   gives `home.key-<hex>` — the existing menu tests already assert the stored names, this one
   pins the shared definition.

7. **`TestDialOneWriteCancel` becomes deterministic.** `internal/exit/socks5_test.go:844-875`
   races two things: `time.AfterFunc(100*time.Millisecond, cancel)` may fire before or after
   the batch of 26 bytes has been read, and the fake server's `defer server.Close()` closes the
   pipe at the same moment `handshake`'s `context.AfterFunc` sets a deadline in the past
   (`internal/exit/socks5.go:147-168`), so the read can end with the peer's close instead of
   the deadline. Under load the cause and the `stop()` branch then differ from run to run.
   Fix the test, not the assertion: the server signals on a channel once `io.ReadFull` has the
   batch; the test cancels only after that signal; the server is closed from the test after
   `Dial` returns (or `t.Cleanup`), not by a goroutine racing the deadline. Keep every
   assertion: `errors.Is(err, context.Canceled)`, the `exit.one_write` key, one open, one
   close. Show it: `go test -race -count=200 -run '^TestDialOneWriteCancel$' ./internal/exit`
   and `go test -race -count=50 ./internal/exit`. If the interleaving turns out to be in
   `socks5.go` rather than in the test, fix the code and say so in the report.

8. **The IPv6 skip is visible in CI.** `internal/netguard/netguard_test.go:147-170` skips when
   the kernel has no IPv6 socket or when `udp6` on `:0` comes out as `udp4`; the CI `test` job
   runs without `-v`, so nobody knows whether the check ran. Add `DNSTT_CLI_TEST_IPV6`, the same
   pattern as `DNSTT_CLI_TEST_DBUS` in `internal/secretservice/fake_test.go:26`: with `=1`
   both skips become `t.Fatal` ("no IPv6 socket, but DNSTT_CLI_TEST_IPV6=1" and "the socket is
   IPv4, but DNSTT_CLI_TEST_IPV6=1"). `.github/workflows/linux.yml`: the `go test -race` step
   of the `test` job gets `DNSTT_CLI_TEST_IPV6: '1'` beside `DNSTT_CLI_TEST_DBUS`. Run it here
   first: `DNSTT_CLI_TEST_IPV6=1 go test -race -count=1 -v -run '^TestApplyReadsV6Only$'
   ./internal/netguard/` and record whether it ran or failed. If the runner turns out to have no
   IPv6, the next node reports a red run: then the env line goes and the skip is recorded as an
   environment limit — never a silent skip.

9. **What stays open, with the reason.** No code: repeat this list in the report so that the
   node's Outcome carries it, each with the node that recorded it.

   - The pause after a double click on Windows: no Windows machine to check it on
     (`05-menu-and-release/05-menu/01-menu-and-commands/plan.md:371`).
   - The menu's buffered reader keeps a line pasted before the `sudo` child starts
     (`05-menu-and-release/plan.md:186`); the fix belongs to the elevation session, not here.
   - An addressed `SIGTERM` during the `sudo` child is dropped, not deferred
     (`05-menu-and-release/plan.md:172-178`): the parent takes no terminal signal while the
     child runs by design.
   - A deterministic test of `exit.SSH.Dial`'s single retry needs a hook in production code
     (`06-ssh-exit/plan.md:132-133`).
   - `scripts/notices` reads a module's license kind from the first license text and labels a
     file with several texts by the first (`scripts/notices/main.go:267-272`); the tool stops on
     an unknown kind instead of guessing further, which is the intended limit.
   - A `slipnet://` link broken by a hard newline is not joined, an IPv6 literal in field 4 is
     not expressible and is refused, and the 255-byte login limit is applied by `connect`
     rather than by the import (`05-menu-and-release/plan.md:194-196`).
   - `TestDefaultUTLSFingerprints` in the vendored `core/` flaked once (loopback HTTP/2) and
     passed on the rerun; `core/` stays verbatim (`05-menu-and-release/plan.md:198-200`).
   - `TestConnectWithInProcessServer` logs from the core after the test returns
     (`05-menu-and-release/plan.md:197-198`): outside the shell, and the core is not edited.
   - The README's macOS row was recorded as disagreeing with `doc/architecture.md` §09
     (`05-menu-and-release/plan.md:201-202`); the current text already agrees —
     `doc/architecture.md:550` says no CI job builds macOS, as the README does — so nothing is
     left to fix here.

10. **Checks**, from the repository root, on the final tree of this task:

    ```
    gofmt -l $(git ls-files '*.go' | grep -v '^core/')
    go build ./...
    CGO_ENABLED=0 go build ./...
    go vet $(go list ./... | grep -v /core/)
    go vet -testinggoroutine=false ./core/...
    GOOS=windows go vet $(GOOS=windows go list ./... | grep -v /core/)
    GOOS=windows go vet -testinggoroutine=false ./core/...
    GOOS=windows go build ./...
    GOOS=windows go test -c -o /tmp/opencode/cli.test.exe ./internal/cli
    GOOS=windows go test -c -o /tmp/opencode/config.test.exe ./internal/config
    GOOS=windows go test -c -o /tmp/opencode/exit.test.exe ./internal/exit
    GOOS=windows go test -c -o /tmp/opencode/netguard.test.exe ./internal/netguard
    DNSTT_CLI_TEST_DBUS=1 DNSTT_CLI_TEST_NETNS=0 \
      go test -race -count=1 ./... \
      -skip '^(TestDualStackListener|TestLoopbackIndexBlocks|TestPhysicalIndexPasses|TestFamilyWithoutRouteRefuses)$'
    git diff --check
    ```

    The four skipped tests are the live bindings that reach `1.1.1.1` or the IPv6 test
    address: `TestDualStackListener`, `TestLoopbackIndexBlocks` and `TestPhysicalIndexPasses`
    in `internal/netguard/live_test.go` and `TestFamilyWithoutRouteRefuses` in
    `internal/netguard/netguard_linux_test.go:76`, which dials
    `[2606:4700:4700::1111]:443`. `TestUnknownIndexRefuses` is not in the list on purpose: it
    fails in `Control` before a socket exists (`internal/netguard/netguard.go:248`) and makes
    no call, so it passes offline — the older lists that skipped it while letting
    `TestFamilyWithoutRouteRefuses` dial had the set the other way round. Nothing here opens a
    socket, reads a real profile, store or
    resolver, starts a tunnel or touches `go.mod`/`go.sum`; no live run, no new dependency.

## Done when

- `Expand`'s hint names `doh:`, `dot:`, `udp:` and `tcp:`, the parse error of
  `internal/resolvers/resolvers.go:133` and the hint agree, and `go test ./internal/resolvers/`
  passes with a test that fails if a prefix drops out.
- The menu's delete question says "exit password" for a socks5 or http profile and "exit key"
  for a key profile; both wordings are covered in `internal/cli/menu_test.go`.
- At the `Profile: ` question `0` cancels like an empty answer and a profile named `0` is still
  selectable; both are covered, and no `profile "0" not found` line is printed for `0`.
- `ParseLink` refuses a `dnstt_ssh` link with a login and neither a password nor a key, naming
  field 16 (or fields 16 and 24 for a line without a type), before any question is asked; the
  message carries no part of the link; `internal/config` tests cover both shapes.
- `profile set` names the menu and `profile add`, says the command is not interactive, prints no
  `Edit the file directly` and no path, and a test covers its output and the missing-profile
  error.
- `.password` and `.key` are built in one place, `exitSecret.secretSuffix`, used by both the
  wizard and the menu's secret item; a test pins `secretName` and the fresh `<name>.key-<hex>`
  shape.
- `TestDialOneWriteCancel` uses no timer and no peer close that can race the deadline, keeps
  every assertion, and passes `-race -count=200`; `internal/exit` passes `-race -count=50`.
- `TestApplyReadsV6Only` fails rather than skips under `DNSTT_CLI_TEST_IPV6=1`, the `test` job
  of `linux.yml` sets it, and the local run with it either passes or its failure is recorded
  with the evidence.
- The check matrix above is clean: `gofmt`, both builds, both vets, the Windows cross-build and
  the four `go test -c` binaries, the race suite with the exact `-skip`, `git diff --check`;
  `go.mod` and `go.sum` are unchanged, no socket was opened and no live run was made.
- The report repeats the list of step 9 — each item that stays open with its reason and the node
  that recorded it, and the one item already closed — for the node's Outcome.

## Outcome

Every item of the task is closed in the code, the IPv6 check is closed as far as the tree allows,
and the ninth list is re-recorded below.

- The resolver hint names `doh:`, `dot:`, `udp:` and `tcp:` and agrees with the parse error of
  `internal/resolvers/resolvers.go`; the test asserts the whole phrase, so a lost prefix fails.
- The menu's delete question says "exit password" for a socks5 or http profile and "exit key"
  for a key profile; both wordings are covered.
- At the `Profile: ` question `0` cancels like an empty answer and prints no "not found" line,
  while a profile literally named `0` stays selectable; both are covered.
- `ParseLink` refuses a `dnstt_ssh` link with a login and no credential before any import
  question, naming field 16, or fields 16 and 24 for a line without a type; the message carries
  no part of the link.
- `profile set` prints one honest message: the command is not interactive, a profile is changed
  in the menu or created by `profile add`. The config path and `Edit the file directly` are gone,
  `mustPath` is deleted, and the command's help line no longer promises interactivity — it said
  `(interactive)` and contradicted the new message.
- `.key` and `.password` are built in `exitSecret.secretSuffix` alone, used by the wizard and by
  the menu's secret item; a test pins `secretName` and the fresh `<name>.key-<hex>` shape.
- `TestDialOneWriteCancel` no longer races a timer or the peer's close against the deadline: the
  fake server signals once the batch is read, the test cancels only then, and the server's end is
  closed from `t.Cleanup` after `Dial` returns. The interleaving was in the test, not in
  `internal/exit/socks5.go`, which is unchanged. Every assertion stays; `-race -count=200` is
  clean and `internal/exit` passes `-race -count=50`.
- `DNSTT_CLI_TEST_IPV6=1` turns both IPv6 skips of `TestApplyReadsV6Only` into failures, and the
  `test` job of `linux.yml` sets it. The run with the variable passed where the tree was
  checked — the kernel has an IPv6 socket — so the first CI run is the one that says whether the
  runner has IPv6. If that run is red only for this reason, the variable goes and the skip is
  recorded as an environment limit.
- The check matrix of step 10 is clean on the final tree: `gofmt` outside `core/`, both builds,
  both vets, the Windows cross-build and the four `go test -c` binaries, the race suite with the
  exact `-skip`, `git diff --check`. `go.mod`, `go.sum` and `core/` are untouched, and no live
  run was made.

Open, with the reason — each was recorded at an earlier acceptance and stays out of this task:

- The pause after a double click on Windows: no Windows machine to check it on
  (`05-menu-and-release/05-menu/01-menu-and-commands/plan.md:371`).
- The menu's buffered reader keeps a line pasted before the `sudo` child starts
  (`05-menu-and-release/plan.md:186`); the fix belongs to the elevation session.
- An addressed `SIGTERM` during the `sudo` child is dropped, not deferred
  (`05-menu-and-release/plan.md:172-178`): the parent takes no terminal signal while the child
  runs, by design.
- A deterministic test of `exit.SSH.Dial`'s single retry needs a hook in production code
  (`06-ssh-exit/plan.md:132-133`).
- `scripts/notices` reads a module's license kind from the first license text
  (`scripts/notices/main.go:267-272`); the tool stops on an unknown kind instead of guessing
  further, which is the intended limit.
- A `slipnet://` link broken by a hard newline is not joined, an IPv6 literal in field 4 is not
  expressible, and the 255-byte login limit is applied by `connect` rather than by the import
  (`05-menu-and-release/plan.md:194-196`).
- `TestDefaultUTLSFingerprints` in the vendored `core/` flaked once (loopback HTTP/2) and passed
  on the rerun; `core/` stays verbatim (`05-menu-and-release/plan.md:198-200`).
- `TestConnectWithInProcessServer` logs from the core after the test returns
  (`05-menu-and-release/plan.md:197-198`): outside the shell, and the core is not edited.
- Closed here: the README's macOS row recorded as disagreeing with `doc/architecture.md` §09
  (`05-menu-and-release/plan.md:201-202`) — the current text already agrees, so nothing was left
  to fix.

Separate work, not a debt: an interactive `profile set`. The command only says how a profile is
changed today; making it interactive is a feature, and the message states the current limit.
