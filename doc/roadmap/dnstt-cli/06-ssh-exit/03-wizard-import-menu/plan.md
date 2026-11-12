---
kind: task
status: todo
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
- **Import** (`internal/config/links.go`, `internal/cli/profile_import.go`). The key-based
  `dnstt_ssh` refusal (`internal/config/links.go:86-88`) and the "needs a password, because
  key-based links are not imported yet" rule (`internal/config/links.go:120-126`) go away.
  For `dnstt_ssh` the parser reads field 15 (login) and either field 16 (password) or fields
  23–25 of §10 (auth type, private key in base64 PEM, passphrase in base64); the passphrase
  decrypts the key and is then dropped, as in the wizard. A link carries no host key — §10's
  fields 17 and 19 are the SSH port and host and are not used — so `Link` carries an empty
  `Exit.HostKey` and the importer decides: the interactive path asks for the fingerprint with
  the same validator, the stdin path refuses that line with a warning that names
  `exit.host_key` and keeps importing the other lines.
- **Menu** (`internal/cli/menu.go`, `internal/cli/menu_edit.go`). `new exit password`
  (`newExitPasswordItem`, `internal/cli/menu_edit.go:196-258`) becomes the exit secret item:
  a password profile asks the password as today, a key profile asks for the key and its
  passphrase and stores it under a fresh `<name>.key-<random>` name with the same
  put-save-delete ordering and rollback. A new item changes the pinned fingerprint of the
  current profile, with the current value as the default answer. §10a's item list in
  `architecture.md` gains it.
- `internal/cli/profile.go`: the import paragraph of the profile command's description
  (`internal/cli/profile.go:35-39`) now says that a `dnstt_ssh` link is imported with its
  password or key and that the fingerprint is asked.

## Done when

- The wizard offers `ssh`, asks the method, the login, the password or the key with its
  passphrase, and the fingerprint; a cancelled or invalid answer writes nothing, and the saved
  profile loads through `internal/config` without an error.
- A password `dnstt_ssh` link and a key `dnstt_ssh` link both import: the password or the
  decrypted key is in the store, `exit.key_ref` or `exit.password_ref` points at it, and
  `exit.host_key` is the answer to the fingerprint question. From stdin a `dnstt_ssh` line
  without a fingerprint is a warning naming `exit.host_key`, the other lines still import, and
  no half-written profile or orphaned secret is left behind.
- `profile import` and `profile` no longer print "until stage 06-ssh-exit" or "connects only
  after"; `rg -n "06-ssh-exit" internal/` is empty.
- The menu replaces the key of a key profile and the password of a password profile, changes
  the fingerprint, and leaves the old secret removed only after the file points at the new
  one; a failed save keeps the old reference and value.
- Tests cover each path (`internal/cli/wizard_test.go`, `internal/config/links_test.go`,
  `internal/cli/profile_import_test.go`, `internal/cli/menu_edit_test.go`), with synthetic
  values only; `go test ./...`, `go vet ./...`, the Windows cross-build and
  `git diff --check` are clean.

## Risks and what is needed from the user

- Reading a private key from a path is the first time the tool touches a user's file: the
  error must name the reason and never the path or the key, and the key is never printed,
  logged or echoed — the same rule as the exit password.
- A `dnstt_ssh` link from the app has no fingerprint, so the same link imports interactively
  and is refused from stdin. That is a deliberate consequence of pinning; the message must
  say what to do instead of only what failed.
- The menu's secret item now has two shapes. A profile with a key but an empty `key_ref` or a
  password without a login must be refused before anything is written, not built and left
  broken.
- Nothing is needed from the user; the tests are synthetic.
