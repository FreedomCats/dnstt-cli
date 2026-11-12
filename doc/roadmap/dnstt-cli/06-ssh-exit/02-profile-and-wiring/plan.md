---
kind: task
status: done
detailed: true
depends: [01-stream-client]
---

# Profile and connect wiring

## Task

Make an SSH profile a first-class profile: the two fields of `architecture.md` §10 —
`exit.host_key` and `exit.key_ref` — with their rules, and the connect path that builds the
exit from them, so a profile written by hand connects. The protocol itself is
[SSH client over the tunnel stream](../01-stream-client/plan.md); the questions that create
such a profile are [Wizard, import and menu](../03-wizard-import-menu/plan.md).

The rules, decided here and written into §10:

- `exit.type = "ssh"` requires `exit.user` and `exit.host_key`; `host_key` is the OpenSSH
  SHA-256 fingerprint (`SHA256:<base64>`, as `ssh-keygen -lf` prints it, the prefix may be
  omitted) and is normalized through `exit.ParseFingerprint`, so the rule lives once.
- Exactly one credential: `exit.password_ref` or `exit.key_ref`. Both or neither is a profile
  error, not a runtime one.
- `exit.key_ref` holds a private key in PEM, decrypted. §10 has no passphrase field, and a
  passphrase stored next to its key protects nothing: the store's own protection (DPAPI, the
  keyring, mode 0600) is the boundary, exactly as for the exit password. A passphrase is
  therefore used once, when the key is put into the store, and not stored.
- `host_key` and `key_ref` on any other exit type are errors, like every unknown or misplaced
  key in this project.

After this task an SSH profile connects. `profile import` stops taking `dnstt_ssh` links
altogether: a link carries no fingerprint, and a profile with `exit.type = "ssh"` and no
`exit.host_key` no longer loads at all, so storing one would leave a `profiles.toml` that no
command can read. The fingerprint question at import belongs to
[Wizard, import and menu](../03-wizard-import-menu/plan.md); here the line is refused with a
warning naming `exit.host_key`, and the "connects only after stage 06-ssh-exit" line goes.

## Steps

1. `internal/config/profiles.go`: the two fields of `Exit`, after the existing ones.

   ```go
   // KeyRef is the reference of the SSH exit's private key in the secret store; exactly
   // one of KeyRef and PasswordRef is set for exit.type "ssh".
   KeyRef string `toml:"key_ref,omitempty"`
   // HostKey is the pinned fingerprint of the SSH exit's host key, SHA256:…; required for
   // exit.type "ssh", refused with any other type. Normalized by exit.ParseFingerprint.
   HostKey string `toml:"host_key,omitempty"`
   ```

   `omitempty` on purpose: an empty value is not a value here (it means absent), so an
   existing `socks5` profile is not rewritten with two meaningless keys on its next save,
   unlike `user = ""`, which is a real setting of the exit. `KillSwitch` is the precedent for
   an optional key.

2. `internal/config/profiles.go`, `checkProfileKeys`: add `"key_ref"` and `"host_key"` to
   `knownExitKeys`. Without this they stay unknown keys, and the rules below would never be
   reached.

3. `internal/config/profiles.go`, `validateProfile`: the rules, all as load and save errors.
   Take the effective exit type first (`""` is `socks5`), so the messages do not print an
   empty type.

   - Any type but `ssh`: `exit.host_key` and `exit.key_ref` set is an error each, worded like
     the `one_write` one — `exit.host_key applies to exit.type "ssh" only, not "socks5"`.
   - `ssh`: `exit.user` is required and must not contain control characters; `exit.host_key`
     is required, and its value is checked by `exit.ParseFingerprint` (the error already
     names `exit.host_key` and the accepted form, and repeats no value); exactly one of
     `exit.password_ref` and `exit.key_ref` — `needs exactly one of exit.password_ref and
     exit.key_ref` and `takes exit.password_ref or exit.key_ref, not both`.
   - `exit.one_write` with `ssh` stays an error through the existing check; add a row to its
     table so it is proven.
   - `internal/config` gains the `internal/exit` import for `ParseFingerprint` only. There is
     no cycle: `internal/exit` imports nothing from the project. Do not write a second
     fingerprint check here — one rule, one place.

4. `internal/config/profiles.go`, `checkForSecretValues`: pull the reference test out into
   `checkRefValue(prefix, field, value string) error` and call it for `exit.password_ref` and
   `exit.key_ref`. The messages name the field and never repeat the value: a `key_ref` holding
   PEM has no `:` and fails on the first test; the length-and-space heuristic stays for a
   one-line secret. Return both errors, not the first: `validateProfile` already joins.

5. `internal/config/profiles_test.go`:
   - The save-and-load round trip gains `HostKey` and `KeyRef` on a second, `ssh`, profile of
     the same set, asserted after `LoadProfilesFrom`; a `socks5` profile saved without them
     must not gain an empty key (that is what `omitempty` is for).
   - A table over `validateProfiles`, one row per rule: `ssh` without a user, without
     `host_key`, with an MD5 `aa:bb:…` fingerprint, with junk, with both references, with
     neither, with a `key_ref` that is a PEM, with a `key_ref` without a scheme;
     `host_key`/`key_ref` on `socks5` and on `http`; `one_write = true` on `ssh`. Assert the
     field name in the message and that the value does not appear in it.
   - A load-from-TOML test for `host_key` on a `socks5` exit: the error is the rule's, not
     `unknown key`.

6. `internal/app/tunnel.go`, the `case "ssh":` of `buildTunnel`, which today returns "SSH
   exit is not implemented yet (stage E5)". It builds the same values twice — once for
   `checkExit` and once for the dialer — so the profile's rules are checked again here (a
   profile built in code, as the tests and `menu` do, has not been through
   `internal/config`); the messages name the field:
   - `exit.user` empty → `exit.user is required for exit.type "ssh"`.
   - `exit.ParseFingerprint(prof.Exit.HostKey)` → the normalized `SHA256:…`; its error goes
     up as is.
   - both or neither of `password_ref`/`key_ref` → the same two messages as in step 3.
   - `password_ref`: `getSecret`, then refuse an empty value
     (`exit.password_ref: the stored password is empty`). A store error becomes a fixed
     message naming the field — `exit.password_ref: cannot read the password from the secret
     store` — and is not wrapped: store errors carry the reference, paths and payload
     (`internal/config/secret.go`, `splitRef`). The `http` case is the precedent.
   - `key_ref`: `getSecret`, refuse an empty value, then `exit.ParseKey([]byte(value), "")`
     — the empty passphrase is the rule, not an omission: §10 stores no passphrase, so an
     encrypted key is refused here with `exit.key_ref: the private key needs a passphrase`,
     and a damaged one with `exit.key_ref: the private key cannot be parsed`.
   - `checkExit` builds its own `&exit.SSH{…}` and calls `Check`; it must not share the
     instance of the dialer, for the reason the SOCKS5 case gives (the check opens its own
     stream and caches nothing). `Signer` is shared between the two: signing is stateless.

7. `internal/app/tunnel.go`: the exit wrapper. Rename `httpExit` to `fatalExit` and let it
   stop the tunnel on `exit.IsLogin(err) || exit.IsHostKeyMismatch(err)`; the comment says
   which failures are the tunnel's (a refused login, a host key that is not the pinned one)
   and which belong to the application connection. Build it for `http` and for `ssh` in a
   `switch` where the `if exitType == "http"` stands today; `socks5` stays unwrapped, so its
   behaviour does not change. Rename the three `*httpExit` assertions in
   `internal/app/http_test.go` (the type assertion at the top of the file, the wrapper test,
   the shutdown test) — nothing else there changes.

8. `internal/app/ssh_test.go`, new, with a fake getter `func(string) (string, error)` and a
   generated ed25519 key (`ssh.MarshalPrivateKey`, `ssh.MarshalPrivateKeyWithPassphrase` for
   the encrypted case) — no fixture and no real key:
   - a password profile: `tun.Exit` is `*fatalExit` over `*exit.SSH` with `User`, `Password`,
     a nil `Signer` and the normalized `HostKey` (`SHA256:` added to a bare 43-character
     value);
   - a key profile: `Signer` set, `Password` empty;
   - the getter is asked for `password_ref` or `key_ref` and for nothing else;
   - a getter error, a damaged PEM, an encrypted PEM and an empty value: each error names
     `exit.key_ref` (or `exit.password_ref`) and contains neither the reference nor the
     value — pass a reference with a recognisable marker and a value with another;
   - a profile without `host_key`, with both references, and with an MD5 fingerprint: the
     build refuses it before the getter is called;
   - the wrapper: a dialer returning `*exit.HostKeyError` and one returning `*exit.LoginError`
     each signal `fatal` exactly once, and the error reaches the caller unchanged (the shape
     of `TestHTTPExitSignalsOnce`). `internal/app` gets no SSH server of its own: the
     synthetic one lives in `internal/exit/ssh_test.go` and is not reachable from another
     package; the chain "mismatch → stop" is covered here, by the supervisor row of the
     previous task and by step 10.
   - replace the "SSH exit is not implemented yet" assertion at the end of
     `internal/app/http_test.go` with a call that builds a valid SSH profile and asserts the
     `*fatalExit`/`*exit.SSH` pair, or move it here.

9. `internal/cli/profile.go`:
   - `profile show`: after `Exit User`, `Exit Host Key: <fingerprint>` when set, and
     `Exit Key: (stored in <ref>)` when `key_ref` is set — the shape of the password line, so
     the key itself is never printed. The fingerprint is a public value and is printed in
     full, with and without `-full`, like the reference.
   - `profile rm`: delete the stored key with the stored password — the secrets first, then
     the file, as today. Collect the references (`exit.password_ref`, `exit.key_ref`), open
     the store once if any is set (so a profile with neither still leaves it closed), delete
     each with its own failure message (`the exit key could not be deleted from the secret
     store`), and make the join after a failed save say which of them went.
   - The command description: `rm` removes "its stored exit password or key"; the wizard line
     no longer says the SSH exit is not implemented (the wizard still offers `socks5` and
     `http` only, and the line says so); the import line says a `dnstt_ssh` link is refused
     because it carries no `exit.host_key`; the last line mentions the key next to the
     password and names the four fields of a hand-written SSH profile.

10. `internal/cli/connect.go`, `finishConnect`: the SSH fatal case joins the HTTP one —
    `(exitType == "http" && exit.IsLogin(err)) || (exitType == "ssh" && (exit.IsLogin(err) ||
    exit.IsHostKeyMismatch(err)))` — so a fatal exit failure during an application connection
    drains the datapath instead of waiting for a SOCKS5 reply that never comes. Leave the
    `http` half exactly as it is: its test table then needs only the new `ssh` rows
    (`TestFinishConnectOnlyDrainsHTTPLogin`, extended with `ssh` and `*exit.HostKeyError`).

11. `internal/config/links.go`: `dnstt_ssh` is refused where the type is resolved, for
    password and key links alike, with a message naming `exit.host_key` and saying the
    profile is not stored: a link has no fingerprint, and the profile it would make does not
    load (step 3). The two refusals this replaces — the key one ("until stage
    06-ssh-exit") and the "needs a password, because key-based links are not imported yet" —
    go, together with the now unreachable `loginField, passwordField = 15, 16` branch.
    `internal/config/links_test.go`: the key-link and password-link cases become this one
    refusal; the other `dnstt_ssh` rows (no login, no password) go.
    `internal/cli/profile_import.go`: drop the "connects only after stage 06-ssh-exit" print
    from `reportImportedProfile` (no imported profile is `ssh` any more, so the whole branch
    goes). `internal/cli/profile_import_test.go`: the ssh import rows become "the link is a
    warning, the other lines still import" on stdin and "the error is printed and the
    question repeats" interactively.

12. `internal/cli/connect_test.go`: `TestConnectSSHDeferred` becomes the load refusal it now
    is — an `ssh` profile without `exit.host_key` fails before the tunnel is built, with
    `exit.host_key` in the message — and the name says so. A second row keeps a valid `ssh`
    profile from being refused by the command itself (it reaches the secret store and the
    dial; no live run).

13. `internal/cli/profile_show_test.go` and `internal/cli/profile_test.go`: an `ssh` profile
    whose key sits in a temporary store prints the fingerprint and the reference and no part
    of the key; `profile rm` of that profile removes the key from the store (the store file no
    longer names it) and leaves the other profile alone. Reuse `isolatedConnectConfig`,
    `newFakeSecretStore` and `twoProfileFixture`; the key is a generated one, never a value.

14. `dnstt-cli.example.toml`: `# socks5 | http | ssh (ssh is stage E5)` loses the
    parenthesis, and the `[profile.home.exit]` comment block says which fields an `ssh` exit
    needs (`user`, `host_key`, `password_ref` or `key_ref`). Keep it comments only: the example
    profile stays `socks5`.

15. Checks, all from the repository root:

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
    GOOS=windows go test -c -o /tmp/opencode/app.test.exe ./internal/app
    GOOS=windows go test -c -o /tmp/opencode/cli.test.exe ./internal/cli
    DNSTT_CLI_TEST_DBUS=1 DNSTT_CLI_TEST_NETNS=0 \
      go test -race -count=1 ./... \
      -skip '^(TestDualStackListener|TestLoopbackIndexBlocks|TestPhysicalIndexPasses|TestUnknownIndexRefuses)$'
    git diff --check
    ```

    The four skipped tests are the live bindings of `internal/netguard/live_test.go`: they
    reach `1.1.1.1` and skip without a network, so they are named exactly rather than left to
    the environment. Nothing here opens a socket, reads a real profile or starts a tunnel.

## Done when

- `internal/config` loads, saves and round-trips `exit.host_key` and `exit.key_ref`, and
  `exit.ParseFingerprint` is the only copy of the fingerprint rule. Every rule of the task is
  a load error that names the field and repeats no value: `exit.user` and `exit.host_key`
  required for `ssh`, the fingerprint in the accepted form, exactly one of `exit.password_ref`
  and `exit.key_ref`, both fields refused with any other exit type, a `key_ref` that is a
  value rather than a reference. `key_ref` is a known exit key, is covered by the
  secret-in-TOML check, and `one_write = true` with `ssh` is still refused.
- `internal/app` builds an `exit.SSH` from a profile: the password from `password_ref` or the
  key from `key_ref` through the injected getter, the key through `exit.ParseKey(pem, "")`. A
  damaged key, an encrypted key (no passphrase is stored) and an empty stored value fail the
  build with a message naming `exit.key_ref`; a store error names the field and repeats
  neither the reference nor the value.
- A host key mismatch or a refused login on an application connection stops the tunnel: the
  exit wrapper calls `FatalExit` once, and `connect` drains the datapath the way it does for
  an HTTP login refusal. `socks5` and `http` behaviour is otherwise unchanged, and their tests
  pass with no edit but the wrapper's name.
- `profile show` prints `Exit Host Key:` and `Exit Key: (stored in …)` and never the key;
  `profile rm` removes the stored key or password before the profile, and a failure of either
  step leaves a message that says what was and was not removed.
- No `dnstt_ssh` link is imported, for a password or a key, and the warning names
  `exit.host_key`; the "connects only after stage 06-ssh-exit" wording is gone from the code,
  from the `profile` command's description and from the example config.
- The checks of step 15 are clean; `core/` and the dependency list are untouched;
  `doc/architecture.md` is not edited here (its §10 text is with the lead).

## Risks and what is needed from the user

- Refusing every `dnstt_ssh` link at import contradicts the decision recorded for the import
  stage ("password-based `dnstt_ssh` profiles are created and are not connectable until
  `06-ssh-exit`"). That decision assumed `host_key` would arrive as a connect-time
  requirement; as a load-time one it makes the stored profile unloadable, and an unloadable
  `profiles.toml` breaks every command, not one profile. The alternative — keeping the import
  and moving the `host_key` check to `connect` — would contradict the rule this task is
  detailed from. The import comes back in
  [Wizard, import and menu](../03-wizard-import-menu/plan.md) with the fingerprint question.
- `internal/app` has no synthetic SSH server and must not get one: the one in
  `internal/exit/ssh_test.go` is package-private, so the app tests prove the wiring and the
  wrapper with a fake dialer, and the end-to-end mismatch rests on the previous task's
  supervisor row and on the live acceptance node.
- A wrong passphrase cannot reach the build: no passphrase is stored. The build therefore
  refuses an encrypted key ("needs a passphrase") and a damaged one; a wrong passphrase is
  refused when the key is put into the store, which is
  [Wizard, import and menu](../03-wizard-import-menu/plan.md).
- `internal/cli/menu_edit.go` still asks for an exit password whatever the type, so editing
  the password of an `ssh` profile that has a `key_ref` would set both and fail at save with
  "not both". That is a loud failure, not a corrupted profile, and the menu is the next
  node's business — not touched here.
- `internal/config` starts importing `internal/exit`, which brings `x/crypto/ssh` into the
  config package's graph. No cycle and no new module, but it is a dependency direction worth
  a look in review; the alternative is a second copy of the fingerprint rule.
- Nothing is needed from the user: every test is synthetic, and no live run, real profile,
  resolver or store is touched.

## Outcome

An SSH profile written by hand connects, and no `dnstt_ssh` link is imported any more.

- `internal/config`: `Exit.KeyRef` and `Exit.HostKey` with `omitempty`, both in
  `knownExitKeys`; `validateProfile` takes the effective exit type and applies the rules of
  §10 — `user` and `host_key` required for `ssh`, the fingerprint through
  `exit.ParseFingerprint`, exactly one of `password_ref` and `key_ref`, both fields refused
  with every other type. `checkForSecretValues` moved onto `checkRefValue(prefix, field,
  value)`, covers `key_ref` and returns both errors. `internal/config` now imports
  `internal/exit` for the fingerprint rule only; no cycle.
- `internal/app`: `case "ssh"` re-checks the profile's rules (a profile built in code has not
  been through `internal/config`), reads the password or the key through the injected
  getter, parses the key with `exit.ParseKey(pem, "")`, and builds a separate `*exit.SSH` for
  the supervisor's check with the dialer's signer shared. `httpExit` became `fatalExit`,
  firing once on `exit.IsLogin` or `exit.IsHostKeyMismatch`; `socks5` stays unwrapped.
  `finishConnect` drains the datapath for a fatal ssh failure as it does for an HTTP login
  refusal.
- `internal/cli`: `profile show` prints `Exit Host Key:` and `Exit Key: (stored in …)` and
  never the key; `profile rm` deletes the stored key with the stored password, one store open,
  each failure naming its secret and the join after a failed save naming what went; the
  command description, the wizard's ssh line and the example config lost the "stage E5" and
  "connects only after stage 06-ssh-exit" wording. `ParseLink` refuses `dnstt_ssh` for
  password and key links alike, naming `exit.host_key`, before anything is stored.
- What the code turned out to be, beyond the plan's assumptions:
  - two existing rows of the validation tables stopped being valid under the new rules — an
    `ssh` exit with an empty `Exit` and the load row that expected an `ssh` profile to load
    without `host_key`. Both are consequences of the rules, not of a test edit.
  - the wizard's own line carried the removed wording although no numbered step named it;
    it now says the wizard offers `socks5` and `http` and an SSH exit is written by hand.
  - `checkRefValue` speaks of a "secret value" rather than a "password value", because one
    message now serves both fields.
  - `internal/app` refuses control characters in `exit.user` as well: the plan named only the
    empty user there, but `internal/config` has the rule and the HTTP case checks its user the
    same way.
  - a profile with both references cannot load, so `profile rm` deletes at most one secret;
    the loop over the two references stays as the shape that covers whichever one is there.
  - step 12's "reaches the secret store and the dial" is a store read and the build, not a
    dial: no SSH server may live in `internal/app`. The test proves the command does not
    refuse a valid `ssh` profile — the run stops at the datapath, before any exit dial.
- Left for the next children: the wizard, import and menu items of
  [Wizard, import and menu](../03-wizard-import-menu/plan.md), and the live acceptance. §10's
  link table still says `dnstt_ssh` becomes an `ssh` exit, which the import refuses until
  that stage gives it a fingerprint question.
