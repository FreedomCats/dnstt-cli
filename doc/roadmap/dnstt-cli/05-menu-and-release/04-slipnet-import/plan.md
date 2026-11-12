---
kind: stage
status: in-progress
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
- A real link given by the user imports and, for `dnstt`, connects; no value of it is in a
  tracked file.

## Children

1. [The parser and `profile import`](01-link-parser-and-import/plan.md) — the decoding and
   field mapping for both types, the command, the refusals, the secret store and synthetic
   tests. Task.
2. [A real link](02-live-link/plan.md) — the user's link imported and used once (connected for
   `dnstt`); values in chat, evidence ignored. Task; user-run.

The parser and the command come first, proven on links built inside the tests. The live link
is the user's and carries an exit password, so it is a separate result: it proves the real
generator's shape, which synthetic links cannot.

Decision recorded: password-based `dnstt_ssh` profiles are created and are not connectable
until `06-ssh-exit`; key-based links are refused until then, because `exit.key_ref` is that
stage's field and no key material is kept in the meantime (see the parser task).
