---
kind: task
status: todo
---

# The parser and `profile import`

## Task

The `slipnet://` format of `architecture.md` §10, parsed in `internal/config/links.go` (new)
and exposed as the `profile import` subcommand. The parser is written from the description in
§10, not from the AGPL-3 code (`AGENTS.md`): a `slipnet://` prefix and base64 of a UTF-8 line
of `|`-separated fields, a version in field 0, positions that keep their meaning across
versions, defaults for missing fields and extra fields ignored.

Both tunnel types have a profile shape, but only password links are imported now: `dnstt` a
`socks5` exit, `dnstt_ssh` an `ssh` exit that does not connect until `06-ssh-exit` — the
user's choice, recorded in the parent stage. The profile is saved like any other; `connect`
then refuses it with the existing "SSH exit is not implemented yet" message
(`internal/app/tunnel.go:142-143`), and the import says one line about that. A key-based
`dnstt_ssh` link is not imported: the private key has nowhere to go before `06-ssh-exit`
defines `exit.key_ref`, so the import refuses it with a message naming that stage and writes
nothing — no profile, no key, no reference.

The link carries the exit password in plain text. It is read by hidden input in a terminal or
from stdin, never taken from an argument, and its values go to the secret store; `profile
show` prints the reference, not the value. The command is the first caller; the menu's wizard
(the menu stage) reuses the same function later.

Field 39 is copied into `payload_size` unchanged: `architecture.md` §10 calls it "payload size
per query", which is the unit P7 defines, and the live link is where a generator's real value
shows. Nothing is scaled or guessed.

Out of scope: the menu's link question (menu stage), the SSH exit itself and its `host_key` /
`key_ref` fields (`06-ssh-exit`), and any live value.

## Done when

- `slipnet://` decoding accepts the standard alphabet with padding, trims the input, drops
  whitespace inside a wrapped line, accepts missing padding and falls back to the URL-safe
  alphabet.
- The fields map as §10 says: field 1 the type (`dnstt` → `socks5`, `dnstt_ssh` → `ssh`;
  `sayedns`/`sayedns_ssh` and other unrelated types refused by name), field 2 the name (latin
  letters, digits and `-`, otherwise asked), field 3 the domain, field 4 and 37 the resolvers
  with field 22's transport (`udp` default, `tcp` from P6, `doh:` from field 21, `dot`, a
  `dot` port 53 means 853), field 11 the key, fields 12 and 13 the SOCKS5 login and password,
  fields 15 and 16 the SSH login and password, fields 23–25 the SSH auth type, key and
  passphrase, field 39 `payload_size` from P7 (`0` is the domain's maximum). Other fields are
  ignored.
- Validation follows §10: a non-blank domain and name, exactly 64 hex digits for the key, at
  least one resolver unless the transport is `doh`, a DoH URL starting with `https://`, and an
  SSH login plus a password or a key for `dnstt_ssh`.
- A password-based `dnstt_ssh` profile is created with `exit.type = "ssh"`, its login and a
  password reference; the import prints that it connects only after `06-ssh-exit`, and
  `connect` refuses it with that message. A key-based `dnstt_ssh` link is refused with a
  message naming `06-ssh-exit`: no profile, no `key_ref` and no key material is written,
  because that field does not exist before the stage.
- `slipnet-enc://` is refused with a message naming the key built into the SlipNet app; a
  `sayedns`/`sayedns_ssh` link is refused naming NoizDNS.
- `profile import` reads hidden input in a terminal and lines from stdin otherwise; several
  links import one by one and a bad line is a warning; a name already taken is re-asked like
  the wizard does, never overwritten silently; the profile is saved only after the secret has
  been stored, and a failed save removes the stored secret without printing it (the wizard's
  pattern, `internal/cli/wizard.go:91-122`).
- Tests build links inside the test from synthetic fields — no real value — and cover a
  wrapped link, missing padding, the URL-safe alphabet, a bad line among good ones, every
  refusal, the `dnstt_ssh` mapping including a key link's refusal, the store, and a
  `profiles.toml` that holds a reference and no secret.
