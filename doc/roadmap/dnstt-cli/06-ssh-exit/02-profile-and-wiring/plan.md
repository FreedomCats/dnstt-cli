---
kind: task
status: todo
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

After this task an SSH profile connects; `profile import` still refuses a key-based
`dnstt_ssh` link and still prints "connects only after stage 06-ssh-exit", which stops being
true here — both lines go in step 6.

## Done when

- `internal/config` loads, saves and round-trips `exit.host_key` and `exit.key_ref`; every rule
  above is a load error with a message that names the field and never repeats a value, and the
  existing unknown-key, secret-in-TOML and `one_write` checks cover the new fields.
- `internal/app` builds an `exit.SSH` from the profile: the password comes from
  `password_ref` and the key from `key_ref` through the injected secret getter, a wrong
  passphrase or a damaged key fails the build with a message that names the field, and a
  missing secret does not leak the reference or the value.
- A host key mismatch on an application connection stops the tunnel the way a login refusal
  does, not just the connection.
- `profile show` prints the fingerprint and where the key lives, never the key; `profile rm`
  removes the stored key with the stored password.
- The "not implemented yet" wording is gone from the profile command's description and from
  the import report; §10 of `architecture.md` states the rules above.
- `go test ./internal/config/ ./internal/app/ ./internal/cli/`, `go vet ./...`, the Windows
  cross-build and `git diff --check` are clean; `socks5` and `http` behaviour is unchanged.
