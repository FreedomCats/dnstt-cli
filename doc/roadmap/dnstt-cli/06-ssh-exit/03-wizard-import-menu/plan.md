---
kind: task
status: done
detailed: true
depends: [02-profile-and-wiring]
---

# Wizard, import and menu

## Task

Every way a user creates or edits a profile must know the SSH exit, so that no SSH profile
needs a hand-edited `profiles.toml`: the wizard's exit type `ssh`, the import of `dnstt_ssh`
links, and the menu items that replace the exit secret and the pinned fingerprint. The rules
and the connect path are [Profile and connect wiring](../02-profile-and-wiring/plan.md); this
task adds no new profile rule and no new exit behaviour.

What each path does:

- **Wizard** (`internal/cli/wizard.go`). `ssh` becomes an offered exit type
  (`askExitType`, `internal/cli/wizard.go:256-275`) instead of the refusal at line 270. After
  the login it asks the authentication method — `password` or `key` — and then either the
  password twice, through the existing `askExitPassword`, or the private key: a path to read,
  `exit.ParseKey` to check it, and, when the key needs a passphrase, a hidden passphrase
  question with a retry. Then the pinned fingerprint as a plain question (it is public, not a
  secret), validated with `exit.ParseFingerprint`. The key PEM goes into the store under
  `<name>.key`, the password under `<name>.password` as today, with the same rollback: nothing
  is written before the last question and a failed save removes what was put.
- **Import** (`internal/config/links.go`, `internal/cli/profile_import.go`). The `dnstt_ssh`
  refusal (`internal/config/links.go:77-78`), which replaced both the key-based one and the
  "needs a password, because key-based links are not imported yet" rule, goes away. For
  `dnstt_ssh` the parser reads field 15 (login) and either field 16 (password) or fields 23–25
  of §10 (auth type, private key in base64 PEM, passphrase in base64); the passphrase decrypts
  the key and is then dropped, as in the wizard. A link carries no host key — §10's fields 17
  and 19 are the SSH port and host and are not used — so `Link` carries an empty
  `Exit.HostKey` and the importer decides: the interactive path asks for the fingerprint with
  the same validator, the stdin path refuses that line with a warning that names
  `exit.host_key` and keeps importing the other lines.
- **Menu** (`internal/cli/menu.go`, `internal/cli/menu_edit.go`). `new exit password`
  (`newExitPasswordItem`, `internal/cli/menu_edit.go:196-258`) becomes the exit secret item:
  a password profile asks the password as today, a key profile asks for the key and its
  passphrase and stores it under a fresh `<name>.key-<random>` name with the same
  put-save-delete ordering and rollback. A new item changes the pinned fingerprint of the
  current profile, with the current value as the default answer. §10a's item list in
  `architecture.md` gains it; that file is the lead's, so the wording is handed over in the
  report of this node, not edited here.
- `internal/cli/profile.go`: the import paragraph of the profile command's description
  (`internal/cli/profile.go:35-39`) now says that a `dnstt_ssh` link is imported with its
  password or key and that the fingerprint is asked.

## Steps

1. `internal/exit/ssh.go`: the one place that turns a link's encrypted key into the value the
   store keeps. Two additions next to `ParseKey` and `ParseFingerprint`:
   - `var ErrKeyNeedsPassphrase = errors.New("the private key needs a passphrase")`, and
     `ParseKey` returns it where it builds that message today. The text does not change; the
     sentinel only lets a caller tell "this key needs a passphrase, ask for one" from "this
     key is damaged, ask for another" with `errors.Is`.
   - `DecryptKey(pemBytes []byte, passphrase string) ([]byte, error)` — the key in PEM without
     a passphrase, which is what §10's `key_ref` holds. Without a passphrase:
     `ssh.ParseRawPrivateKey`; a `*ssh.PassphraseMissingError` becomes `ErrKeyNeedsPassphrase`,
     any other failure `errors.New("the private key cannot be parsed")`, success returns the
     input bytes unchanged. With a passphrase: `ssh.ParseRawPrivateKeyWithPassphrase` (a wrong
     passphrase or a damaged key is "the passphrase is wrong or the key is damaged"), then
     `ssh.MarshalPrivateKey(key, "")` and `pem.EncodeToMemory` — the OpenSSH PEM form, which
     `ParseKey(pem, "")` and therefore `internal/app`'s build accept. A key type the
     marshaller cannot write (DSA) is "the private key type is not supported".
   - No message of either function carries key material, a passphrase or a file path. The
     rule lives here rather than in the three CLI files because §10 has no passphrase field:
     every authoring path must store a decrypted key, and the build path already refuses an
     encrypted one through `ParseKey`.

2. `internal/exit/ssh_test.go`: rows for the two additions, with generated keys only — the
   file already has `generateEd25519` and the two `ssh.Marshal…` calls at
   `internal/exit/ssh_test.go:335` and `:341`, so no new helper: an encrypted key with the right
   passphrase returns a PEM that `ParseKey(pem, "")` accepts and that signs with the same
   public key; a wrong passphrase gives the message and no key material; an encrypted key
   without a passphrase gives `ErrKeyNeedsPassphrase`; an unencrypted key comes back
   byte-identical; junk is "cannot be parsed"; every message is checked for the passphrase and
   for a marker put into the PEM.

3. `internal/config/links.go`: the parser reads a `dnstt_ssh` line.
   - `Link` gains `Key string`: the SSH exit's private key in PEM, decrypted, the value for
     the secret store; empty for a password link and for `dnstt`. The comment says it is never
     printed, as the one on `Password` does.
   - `case "dnstt_ssh": exitType = "ssh"` replaces the refusal, and the login and password
     fields become per type: 15 and 16 for `ssh`, 12 and 13 for `dnstt`. Every message that
     names a field uses the variables — the control-character checks, "a password without a
     login is not accepted" included — or the numbers would lie for half the links.
   - After the login and password checks, for `ssh`:
     - field 15 is the login and is required: `field 15: an SSH exit needs a login`. It must
       not contain control characters, as field 12 must not.
     - the authentication type comes from field 23, trimmed and lower-cased. `key`: the key is
       field 24 and the passphrase field 25. `password` or empty: the password is field 16 —
       and when field 23 is empty, field 16 is empty and field 24 is not, the key is read
       anyway, because SlipGate's older lines may carry no type. Anything else:
       `field 23: the SSH authentication type must be "password" or "key"`.
     - fields 24 and 25 are base64 of a UTF-8 string: decode them with the existing
       `decodeLinkPayload`, and a failure is `field 24: the private key is not valid base64`
       (or field 25). Then `exit.DecryptKey(pem, passphrase)`; `ErrKeyNeedsPassphrase` becomes
       `field 25: the private key needs a passphrase, and the link does not carry one`, any
       other error `field 24: <the reason>`. The passphrase is dropped here: nothing returns
       it.
     - a key link with no field 24 is `field 24: a key-based dnstt_ssh link carries no private
       key`. A value in the field the authentication type does not use is ignored — the type
       decides — with a comment saying so.
   - `prof.Exit = Exit{Type: exitType, User: user}`: `HostKey` stays empty for `ssh`, because
     a link carries no fingerprint. `Link.Password` is set for a password link, `Link.Key` for
     a key link.
   - Errors keep naming fields and never the values: the link, the password, the key and the
     passphrase appear in no message.

4. `internal/config/links_test.go`:
   - The two `dnstt_ssh` rows leave `TestParseLinkRefusals`. It gains rows for a missing login
     (field 15), a key link without field 24, an unknown field 23, field 24 that is not base64,
     an encrypted key without field 25 and an encrypted key with a wrong passphrase — each
     naming its field and repeating no value. The existing "no value from the link" assertion
     gains the key and the passphrase as markers.
   - New `TestParseLinkSSH`: a password link gives `Exit{Type: "ssh", User}` with an empty
     `HostKey` and `Link.Password`; a key link (23 `key`, 24 the base64 of a generated
     unencrypted PEM) gives `Link.Key` equal to that PEM and no password; an encrypted key with
     the passphrase in field 25 gives a `Link.Key` that `exit.ParseKey(pem, "")` accepts and
     that signs with the same public key; field 23 absent with only field 16 gives a password,
     absent with only field 24 a key; the login comes from field 15 and not from 12 (a link
     with both set proves it).

5. `internal/cli/wizard.go`: the shared questions and the `ssh` exit type.
   - `exitSecret` — `Password`, `Key` (PEM, decrypted) — and its three small methods:
     `secretName(profile)` (`<profile>.password` / `<profile>.key`), `kind()` ("the exit
     password" / "the exit key") and `apply(prof, ref)` (sets `PasswordRef` or `KeyRef`). One
     place for the store name, the word of the messages and the field, shared by the wizard,
     the import and the menu.
   - `askExitType`: `ssh` returns `"ssh"`; the hint becomes
     `the exit type must be socks5, http or ssh`.
   - `askExit` returns `(config.Exit, exitSecret, error)`. An empty login is refused for `ssh`
     as for `http` (`an HTTP or SSH exit needs a login`); the `:` and 255-byte checks stay
     SOCKS5 and HTTP only. For `ssh`, after the login:
     - `askSSHAuth`: `SSH authentication (password, key) [password]: ` — an empty answer or
       `password` asks the password twice through `askExitPassword(ctx, ask, out, "ssh")`,
       `key` calls `askExitKey`, anything else repeats with the two names.
     - then `askFingerprint(ctx, w.ask, w.out, "")`, whose normalized answer becomes
       `Exit.HostKey`.
   - `askExitKey(ctx, ask, out) (string, error)`: `Private key file: `; a leading `~/` (or a
     bare `~`) is joined with `os.UserHomeDir`; `os.ReadFile`; a read failure prints `the
     private key file cannot be read` and **no** path — an `*os.PathError` would carry it;
     `exit.DecryptKey(pem, "")`; on `exit.ErrKeyNeedsPassphrase` a hidden `Key passphrase: `
     and `exit.DecryptKey(pem, passphrase)`; on any failure the reason is printed and the path
     is asked again. One loop, so a typo, a damaged key and a wrong passphrase all lead back to
     the path question instead of dead-ending the wizard. Returns the decrypted PEM; the key
     and the passphrase are never printed, echoed or put into a question.
   - `askFingerprint(ctx, ask, out, current string) (string, error)`: a plain question —
     `Pinned host key fingerprint (SHA256:…, as ssh-keygen -lf prints it): `, with `[current]`
     in front of the colon when there is one — the answer through `exit.ParseFingerprint`, its
     error printed and the question repeated, and an empty answer with a current value keeping
     it. The menu's fingerprint item and the import reuse it; the value is public, so it is
     shown in full, not masked.
   - `add`: the store step becomes one `w.store.Put(secret.secretName(name), value)` and
     `secret.apply`, with `secret.kind()` in the three messages (cannot store / could not be
     removed from the secret store / was removed from the secret store). The order is
     untouched: every question, then the Put, then the save, then the rollback.

6. `internal/cli/wizard_test.go`:
   - `baseWizardAnswers` gains an `ssh-password` and an `ssh-key` script (the method question,
     the password twice or a path in `t.TempDir()`, the fingerprint); a helper writes a
     generated ed25519 key in PEM, unencrypted and encrypted, into `t.TempDir()`. No fixture,
     no real key, no path of the machine.
   - `TestWizardSSHPassword`: `Exit{Type: "ssh", User, HostKey: "SHA256:…"}` with
     `PasswordRef`, the store holding `<name>.password`, the saved set loading through
     `config.LoadProfilesFrom` — the proof that the wizard builds a loadable profile — and
     neither the output nor a question carrying the password.
   - `TestWizardSSHKey`: `KeyRef`, the stored value under `<name>.key` parsing through
     `exit.ParseKey(pem, "")`; the passphrase question only for an encrypted key; a wrong
     passphrase reasks and then accepts; an unencrypted key asks no passphrase; the key is in
     neither the output, a question nor `profiles.toml`.
   - A key file that does not exist: the reason is printed, the path is asked again, and the
     message contains no path (a recognisable marker is put into the path for that assertion).
   - The fingerprint: an MD5 answer reasks and the accepted form is printed; a bare
     43-character answer is stored as `SHA256:…`.
   - `TestWizardReasks`: the "ssh exit" row goes — `ssh` is no longer refused — and the
     "unknown exit type" row expects `must be socks5, http or ssh`; new rows for an unknown
     SSH method and an empty SSH login.
   - `TestWizardEOFAtEveryQuestion` also runs over the `ssh-password` script: a cut at any
     question leaves `puts == 0`, no delete and no save.
   - The rollback tests: a key variant of `TestWizardFailedSaveRollsBackSecret` (the key is
     removed, no password is mentioned), and a Put failure that saves nothing.

7. `internal/cli/profile_import.go`:
   - `importOne` (interactive): after `ParseLink` and before the name question an `ssh` link
     gets its fingerprint — `askFingerprint(ctx, p, e.stdout, "")` — and the normalized value
     goes into `link.Profile.Exit.HostKey`. A cancelled or ended question returns without
     writing, as the other questions of this path do. The menu's first-run path
     (`firstProfile`) calls the same function, so a pasted `dnstt_ssh` link asks there too,
     without a line of its own.
   - `importFromInput` (stdin): an `ssh` link is a warning on stderr that names
     `exit.host_key` and says what to do — `a dnstt_ssh link carries no exit.host_key; import
     it in a terminal, where the fingerprint is asked — the link was skipped` — and the other
     lines still import. A run where nothing else was imported keeps returning "no profile was
     imported".
   - `importLink` stores `link.Password` or `link.Key` through the same `exitSecret` names and
     words (`<name>.password` / `<name>.key`, "the exit password" / "the exit key") with the
     same rollback after a failed save. The key is stored in PEM, decrypted, so the profile's
     `exit.key_ref` passes the build's `exit.ParseKey(pem, "")`.

8. `internal/cli/profile_import_test.go`: `importSSHLink` gains a key form;
   `TestProfileImportStdinSSHRefused` becomes "an ssh line from stdin is a warning naming
   `exit.host_key`, the other lines import and the store is untouched by it"; the interactive
   test becomes "the fingerprint is asked, a bad answer reasks, the profile is saved with it,
   and the credential is in the store under `<name>.password` or `<name>.key`". New rows: a
   key link imports and the saved profile loads and its key parses with
   `exit.ParseKey(pem, "")`; the end of input at the fingerprint question writes nothing; a
   failed save removes the stored secret; a Put failure saves nothing; the warning and the
   output carry neither the link, the password, the key nor the passphrase.

9. `internal/cli/menu.go`: the item table — `new exit password` becomes
   `new exit password or key` (body `newExitSecretItem`), and `change pinned fingerprint`
   (body `changePinnedFingerprintItem`) follows it, so `change domain or key`, `another
   profile` and `delete profile` become 6, 7 and 8. The comment pointing at §10a stays.

10. `internal/cli/menu_edit.go`: the two items.
    - `newExitPasswordItem` becomes `newExitSecretItem`. After the profile is loaded the shape
      follows the exit type and the reference that is there: `ssh` with `KeyRef` asks the key
      (`askExitKey`), everything else asks the password (`askExitPassword`). The "no exit
      login" refusal stays first and its wording becomes secret-neutral (`so the exit secret
      is not used`). An `ssh` profile with neither reference does not load, so it is not
      reachable; the password shape covers it and leaves a valid profile rather than a broken
      one. Only the field of the shape is set: a key profile never gains `password_ref`, a
      password profile never gains `key_ref`.
    - `newPasswordName(profile, oldRef)` becomes `newSecretName(profile, suffix, oldRef)` with
      the same fresh-name rule; the suffix comes from the shape (`.password` / `.key`).
    - The ordering stays exactly as it is: Put under the fresh name, save, then delete the old
      reference; a failed save deletes the new entry and leaves the old reference and value; a
      failed delete of the old one is reported on stderr and the run succeeds. A passphrase is
      asked here and dropped, because the stored key is decrypted.
    - `changePinnedFingerprintItem`: a profile that is not `ssh` prints
      `profile %q has no pinned host key: exit.type is not ssh` and writes nothing; otherwise
      `askFingerprint(ctx, m.ask, e.stdout, prof.Exit.HostKey)`, an unchanged answer prints
      `nothing changed`, and a new one is stored with one save.

11. `internal/cli/menu_edit_test.go` and `internal/cli/menu_test.go`:
    - The item table of `TestMenuItemsCallTheCommandBodies` gains the new row, and every test
      that picks an item by number shifts with it: `change domain or key` 5 → 6, `another
      profile` 6 → 7, `delete profile` 7 → 8. That is `internal/cli/menu_test.go`
      (`TestMenuItemsCallTheCommandBodies`, `TestMenuItemsCallTheRealCommandBodies`, the
      another-profile tests and the delete-profile table) and `internal/cli/menu_edit_test.go`
      (the `TestMenuChangeServer*` table). `TestMenuUnknownAnswerReasks` reads the count from
      `menuItems()` and its out-of-range answer `9` stays above eight items, so it needs
      nothing.
    - New rows for the secret item of a key profile: `<name>.key-<random>` in the store, the
      save before the delete of the old key, and a saved profile with `key_ref` set,
      `password_ref` empty, that loads; the rollback rows of the password item (a failed save
      deletes the new key, a failed delete still succeeds); an `ssh` profile without a login is
      refused before any question (`puts == 0`); the key is in neither `profiles.toml`, the
      output nor a question.
    - The fingerprint item: a new value is normalized and saved; an empty answer keeps the
      current one and writes nothing; a bad value reasks; a profile that is not `ssh` says
      there is nothing to change; the end of input writes nothing; a failed save leaves the
      file as it was.

12. `internal/cli/profile.go`: the two description paragraphs. The wizard line becomes
    `the exit type (socks5, http or ssh; for ssh the authentication method — the password
    twice, or the path of a private key with its passphrase when it has one — and the pinned
    fingerprint)`. The import paragraph becomes: `A dnstt_ssh link becomes an ssh exit: the
    login and the password or the private key come from the link, a passphrase only decrypts
    the key, and the pinned fingerprint is asked in a terminal. From stdin such a link is a
    warning, because the fingerprint cannot be asked there.` The last paragraph already
    mentions the key; it stays.

13. Checks, all from the repository root:

    ```
    gofmt -l $(git ls-files '*.go' | grep -v '^core/')
    go build ./...
    CGO_ENABLED=0 go build ./...
    go vet $(go list ./... | grep -v /core/)
    go vet -testinggoroutine=false ./core/...
    GOOS=windows go vet $(GOOS=windows go list ./... | grep -v /core/)
    GOOS=windows go vet -testinggoroutine=false ./core/...
    GOOS=windows go build ./...
    GOOS=windows go test -c -o /tmp/opencode/config.test.exe ./internal/config
    GOOS=windows go test -c -o /tmp/opencode/exit.test.exe ./internal/exit
    GOOS=windows go test -c -o /tmp/opencode/cli.test.exe ./internal/cli
    DNSTT_CLI_TEST_DBUS=1 DNSTT_CLI_TEST_NETNS=0 \
      go test -race -count=1 ./... \
      -skip '^(TestDualStackListener|TestLoopbackIndexBlocks|TestPhysicalIndexPasses|TestUnknownIndexRefuses)$'
    git diff --check
    rg -n "06-ssh-exit" internal/     # must print nothing
    ```

    The four skipped tests are the live bindings of `internal/netguard/live_test.go`: they
    reach `1.1.1.1` and skip without a network, so they are named exactly rather than left to
    the environment. Nothing here opens a socket, reads a real profile, store or resolver,
    starts a tunnel or reads a key of the machine; `core/` and the dependency list are not
    touched.

## Done when

- `internal/exit` has `DecryptKey` and `ErrKeyNeedsPassphrase`, and a key decrypted by the
  first parses through `ParseKey(pem, "")` and signs with the same public key; no message of
  either carries key material, a passphrase or a path.
- The wizard offers `ssh`, asks the method, the login, the password twice or a key path with
  its passphrase when the key needs one, and the fingerprint plainly; the profile it saves
  loads through `internal/config`; the end of input at any question — the passphrase and the
  fingerprint ones included — writes nothing, and a failed save removes the credential that
  was put.
- A password `dnstt_ssh` link and a key `dnstt_ssh` link both import: `exit.password_ref` or
  `exit.key_ref` points at the stored value, the stored key is a PEM that
  `exit.ParseKey(pem, "")` accepts, `exit.host_key` is the answer to the fingerprint question,
  and the passphrase is in no file, no message and no output.
- From stdin an `ssh` link is a warning that names `exit.host_key` and says to import it in a
  terminal, the other lines still import, and that line leaves no profile and no secret
  behind.
- The menu's secret item replaces the password of a password profile and the key of a key
  profile under a fresh `<name>.key-<random>`, the old entry is deleted only after the file
  points at the new one, a failed save keeps the old reference and value, and a key profile
  never gains `password_ref`.
- The new menu item changes the pinned fingerprint with the current value as the default
  answer, refuses a profile that is not `ssh` with a line, and saves once.
- `profile`'s description says a `dnstt_ssh` link imports with its password or key and that
  the fingerprint is asked; no message says an SSH exit is written by hand;
  `rg -n "06-ssh-exit" internal/` is empty.
- The tests of steps 2, 4, 6, 8 and 11 pass with `-race`, use synthetic values and generated
  keys in `t.TempDir()` only, and no test opens a socket or reads a real profile or store; the
  checks of step 13 are clean; `doc/architecture.md` and `README.md` are not edited here.

## Risks and what is needed from the user

- Reading a private key from a path is the first time the tool touches a user's file: the
  error must name the reason and never the path or the key, and the key is never printed,
  logged or echoed — the same rule as the exit password. `os.ReadFile` returns an
  `*os.PathError`, which prints the path: it must not be wrapped or joined into a message.
- A `dnstt_ssh` link from the app has no fingerprint, so the same link imports interactively
  and is refused from stdin. That is a deliberate consequence of pinning; the message must
  say what to do instead of only what failed.
- The menu's secret item now has two shapes. A profile with a key but an empty `key_ref` or a
  password without a login must be refused before anything is written, not built and left
  broken. The item replaces the credential of the shape the profile already has; switching a
  profile from a password to a key or back stays out of scope and is a candidate for a later
  node.
- `internal/exit` gains two exported names for the authoring paths. The alternative is a copy
  of the passphrase rule in three CLI files, which §10 forbids by keeping one rule in one
  place; the package has no project imports, so no cycle appears.
- A decrypted key is re-marshalled by `ssh.MarshalPrivateKey` into the OpenSSH PEM form, so
  the stored value is not byte-identical to the user's file; `ParseKey` reads that form, which
  the connect path proves. A DSA key cannot be marshalled and is refused with a reason.
- The menu's item numbers shift by one: user-visible, and every test that picks an item by
  number is updated in the same change.
- `doc/architecture.md` §10a and §10 are the lead's: this task changes what the menu and the
  wizard do, so §10a's item list and wizard paragraph and §10's `dnstt_ssh` paragraph need
  the wording handed over in the report of this node. Nothing in `README.md` is needed beyond
  what stage 06 already says.
- Nothing is needed from the user: the tests are synthetic and no live run is part of this
  task.

## Outcome

Every path that authors a profile knows the SSH exit: the wizard offers `ssh`, a `dnstt_ssh`
link imports with its password or its key, and the menu replaces the exit secret of either
shape and the pinned fingerprint. No SSH profile needs a hand-edited `profiles.toml` any
more.

- `internal/exit`: `ErrKeyNeedsPassphrase`, returned by `ParseKey` where it built that
  message before, and `DecryptKey(pem, passphrase)` — the one place that turns an encrypted
  key into the PEM the store keeps. Without a passphrase the input comes back
  byte-identical; with one the key is re-marshalled by `ssh.MarshalPrivateKey`, into the form
  `ParseKey(pem, "")` and therefore the build accept; a type the marshaller cannot write is
  refused with its reason. No message carries key material, a passphrase or a path.
- `internal/config`: `Link.Key`, the `dnstt_ssh` refusal gone, and the login and password
  fields per type (15 and 16 for `ssh`, 12 and 13 for `dnstt`) with every message naming
  them through the variables. The authentication type of field 23 decides; only an absent
  type — SlipGate's older lines — falls back to whichever of fields 16 and 24 carries a
  value, and a value in the field the type does not use is ignored. The passphrase of field
  25 decrypts the key and is dropped. `Exit.HostKey` stays empty: a link carries no
  fingerprint.
- `internal/cli`: `exitSecret` — the store name, the words of the messages, the profile
  field and the value — is the one place the wizard, the import and the menu agree on
  `<name>.password` and `<name>.key`. The wizard asks the method, the password twice or the
  key path with its passphrase, then the fingerprint plainly, and keeps the
  put-save-delete order with the rollback. The interactive import asks the fingerprint after
  the link and before the name question, so the menu's first-run path gets it without a line
  of its own; from stdin an `ssh` line is a warning naming `exit.host_key` and the other
  lines still import. The menu's item 4 is `new exit password or key`, item 5 is
  `change pinned fingerprint`, and `change domain or key`, `another profile` and
  `delete profile` moved to 6, 7 and 8.
- What the code turned out to be, beyond the plan's assumptions:
  - `exitSecret` gained a fourth method, `value()`, so the value reaches `Put` without
    opening the fields. `kind()` returns "exit password" / "exit key" without an article, so
    it fits both "the %s …" and "The %s of profile %q was updated."; the menu's success line
    therefore reads `The exit password of profile "home" was updated.`
  - `baseWizardAnswers` takes the test's `*testing.T`: the `ssh-key` script needs a key file
    in `t.TempDir()`. `TestWizardEOFAtEveryQuestion` runs the `ssh-key` script too, so the
    passphrase question is covered by the cut-at-any-question rule.
  - step 11's "an `ssh` profile without a login is refused before any question" cannot be
    built as a fixture: `internal/config` does not load an `ssh` profile without a login, so
    such a profile never reaches the menu. The refusal is tested on a socks5 profile, and the
    login is checked before the shape, so the `ssh` case takes the same path.
  - an unencrypted key with a non-empty field 25 is refused as "the passphrase is wrong or
    the key is damaged". That follows from the rule that a passphrase decrypts rather than
    from the code: an older SlipGate line that carries a stale passphrase next to an open key
    does not import.
- Left for others:
  - `README.md` still lists the old menu items ("a new exit password", "change the domain or
    the key") and says nothing about `ssh` in the wizard. §10 and §10a were updated before
    this task; the README's lines are the lead's to change.
  - the delete-profile confirmation still asks about "its exit password" although a key
    profile loses its key, and `profile set` still answers "Edit the file directly" for every
    profile. Both are older code that this task neither changes nor worsens; the first now
    reads wrong for the profiles this task creates.
  - switching a profile from a password to a key or back from the menu stays out of scope, as
    the plan says.
