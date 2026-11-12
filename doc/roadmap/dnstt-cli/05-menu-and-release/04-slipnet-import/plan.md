---
kind: stage
status: done
base: 86f4aa7
depends: [01-p6-tcp-transport, 02-p7-payload-cap]
---

# Import of `slipnet://` links

Goal (`architecture.md` R9, §10): a `slipnet://` link imports a profile, so that a user of a
SlipGate-managed server or of the SlipNet app does not retype the domain, the key, the
resolvers and the exit password. `dnstt` links become a `socks5` exit. A password-based
`dnstt_ssh` link becomes an `ssh` profile that does not connect yet — `connect` refuses it
with the existing "SSH exit is not implemented yet" until `06-ssh-exit`, and the host key
arrives with that stage. The user chose to import such a profile now rather than refuse the
link, so the import says plainly that the profile is stored but cannot connect yet. A key-based
`dnstt_ssh` link is refused, because the private key has nowhere to go before that stage
defines its reference; nothing is written. `sayedns`/`sayedns_ssh` (NoizDNS) and
`slipnet-enc://` are refused with a clear message, because the core does not speak NoizDNS's
query encoding and the `slipnet-enc://` key is built into the SlipNet app.

The format is written from the description in `architecture.md` §10, not from the AGPL-3 code
of SlipNet and SlipGate (`AGENTS.md`): the field table, the base64 rules and the validation
are there.

## Done when

- A `slipnet://` link for a `dnstt` tunnel creates a profile: the parser takes it from a
  hidden input in a terminal and from stdin, and the profile carries the domain, the key, the
  resolvers with their transport, the exit type, the exit login and a password reference; the
  password itself is in the secret store, not in `profiles.toml`.
- A password-based `slipnet://` link for `dnstt_ssh` creates an `ssh` profile with its login
  and a password reference; the import states that it does not connect until `06-ssh-exit`,
  and `connect` refuses it with that stage's existing message. This is the user's choice: the
  profile is imported now, not deferred to that stage. A key-based `dnstt_ssh` link is refused
  with a message naming `06-ssh-exit`, and nothing is written (no profile, no key reference,
  no key material).
- Several links in one input import one by one, and a bad line is a warning, not a failure of
  the rest.
- `slipnet-enc://` is refused, naming the built-in key; `sayedns`/`sayedns_ssh` are refused,
  naming NoizDNS; other unrelated types are refused by name.
- The fields `tcp` (transport) and the payload size (`payload_size`) are imported as P6 and P7
  define them.
- A real link, if the user has one, imports and, for `dnstt`, connects; the user has none, so
  `02-live-link` records `not seen — no real slipnet:// link` and the stage rests on the
  synthetic parser tests; no value of a real link is in a tracked file.

## Children

1. [The parser and `profile import`](01-link-parser-and-import/plan.md) — the decoding and
   field mapping for both types, the command, the refusals, the secret store and synthetic
   tests. Task.
2. [A real link](02-live-link/plan.md) — the user's link imported and used once (connected for
   `dnstt`) if one exists; the user has none, so the node is `not seen — no real slipnet://
   link`. Task; user-run.

The parser and the command come first, proven on links built inside the tests. The live link
is the user's and carries an exit password, so it is a separate result: it proves the real
generator's shape, which synthetic links cannot. There is no such link here, so that result
is `not seen` and the synthetic tests stand alone.

Decision recorded: password-based `dnstt_ssh` profiles are created and are not connectable
until `06-ssh-exit`; key-based links are refused until then, because `exit.key_ref` is that
stage's field and no key material is kept in the meantime (see the parser task).

## Outcome

Done: a `slipnet://` link imports a profile, for both `dnstt` and password-based `dnstt_ssh`.
The stage rests on synthetic links: the user has no real link, so `02-live-link` records
`not seen — no real slipnet:// link`, and no generator shape, live connection or real value is
claimed.

- Parser (`internal/config/links.go`, new): `ParseLink` trims the input, matches the scheme
  case-insensitively, removes ASCII whitespace inside the payload (a soft-wrapped terminal
  line), refuses a second scheme in one token (a double paste), and decodes the standard
  alphabet with and without padding and the URL-safe one; a decode failure or a non-UTF-8
  payload is "the link is damaged", without the content. Fields map as §10: version 0, type 1,
  name 2, domain 3, key 11, SOCKS5 login and password 12/13, SSH login and password 15/16,
  auth kind 23, transport 22, resolvers from 4 or 37 with `doh` from 21, `payload_size` 39.
  `payload_size` is copied unchanged, as P7's unit. The profile has `kill_switch` explicitly
  on, an empty `datapath` (so `connect` defaults to `socks`), no `socks` block and no password
  reference. Resolver entries are normalized by `resolvers.Parse` with the port always
  written; `tcp` is P6's transport, `dot` maps port 53 to 853, and an absent or unknown
  transport is `udp`. `ParseLinks` reads one link per non-empty line and turns a bad line into
  a warning with its line number, keeping the good links in order.
- Refusals name the field and the rule, and never repeat the link, the password or the key:
  `slipnet-enc://` (the key is built into the SlipNet app), `sayedns`/`sayedns_ssh` (NoizDNS),
  an unknown type by name, a key-based `dnstt_ssh` (naming `06-ssh-exit`, nothing written), a
  password without a login, an SSH link without a login or password, a bad domain or key, no
  resolver unless the transport is `doh`, a non-https DoH URL, a negative `payload_size`, a bad
  version.
- Shared rules (`internal/config/validate.go`, new): `ValidProfileName` and `ValidDomainName`
  moved from `internal/cli/wizard.go` unchanged; the wizard and `profile add` call them, the
  local copies are gone and their tests moved to `internal/config/validate_test.go`. One set
  of rules, no behaviour change.
- The command (`internal/cli/profile_import.go`, new; `profile import` in
  `internal/cli/profile.go`): a hidden question in a terminal, repeated after a bad paste, then
  "Another link (Enter to finish)"; one link per line from stdin otherwise, with a bad line as
  a warning and a taken name as a skipped link. The name comes from field 2 when it is usable
  and free, otherwise from the wizard's question in a terminal or `home` on stdin. The
  password goes to the secret store under `<name>.password` before the save, a failed save
  deletes it and reports without its value, and the in-memory set is replaced after every save,
  so a second link with the same name meets the taken-name rule instead of overwriting. The
  first imported profile becomes the default. A password `dnstt_ssh` profile is stored and the
  command says it connects only after `06-ssh-exit`; `connect` refuses it there with that
  stage's existing message. The password, the key and the link never appear in the output or
  in an error.
- Tests are synthetic: a link builder in `internal/config/links_test.go` and
  `internal/cli/profile_import_test.go` with an isolated config directory and a fake secret
  store. No real link, address, key or password is in a tracked file.
- Verified: the full local matrix passed — `gofmt` outside `core/`, `go build ./...`,
  `CGO_ENABLED=0 go build ./...`, the vet split on Linux and on `GOOS=windows`, the full
  `go test -race -count=1 ./...` with `DNSTT_CLI_TEST_DBUS=1`, `DNSTT_CLI_TEST_NETNS=0` and the
  four external `netguard` live tests excluded by `-skip` (no live call), the Windows
  cross-build with `go test -c` of all 19 packages that have tests, and `git diff --check` on
  the tree and on the range. The netns suites were not run: no code of `internal/datapath` or
  `internal/linuxsys` changed here. No new dependency.

Limitations recorded:

- An IPv6 literal in field 4 is not expressible by the `host[:port[:flag]]` grammar of §10
  (the colons read as port separators), so such an entry is refused rather than silently
  stored as another resolver. The format is unchanged; §10 is not edited.
- A link broken by a hard newline is not joined: whitespace is removed inside one token, but
  the stdin path treats each line as one link. A soft wrap (no newline character) works.
- The parser does not apply the wizard's 255-byte limit on a SOCKS5 login or password: such a
  profile imports, and `connect` refuses it later with the exit's message.
- Stage acceptance added two tests for behaviour that was recorded but not covered: the
  refusal of an IPv6 literal in field 4, and the end of input at the name question after a
  profile was already imported, which ends cleanly instead of "nothing written".

Not seen, and not claimed: a real generator's link and its shape, a live connection through an
imported profile, the SSH exit with its `host_key` and `key_ref` (`06-ssh-exit`), and the menu's
link question (the menu stage).
