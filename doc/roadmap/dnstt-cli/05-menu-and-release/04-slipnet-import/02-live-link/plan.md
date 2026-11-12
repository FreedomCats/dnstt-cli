---
kind: task
status: todo
depends: [01-link-parser-and-import]
---

# A real link

## Task

The import of the parser task checked on a link the user actually has. Synthetic links cannot
prove the generator's shape — the version, the field count and the transports SlipGate writes —
so one real link is imported and used once, in a temporary configuration, and its outcome is
recorded.

For a `dnstt` link the profile is also connected and one fetch goes through its exit. For a
password-based `dnstt_ssh` link the accepted result is smaller: the profile and its secret are
created, and `connect` refuses with "SSH exit is not implemented yet" — that refusal is the
designed behaviour until `06-ssh-exit`, not a failure of the import. A key-based `dnstt_ssh`
link is refused by the import itself with the message naming that stage, and nothing is
written; that is also the expected result, not a failure.

The link carries the exit password. It is given in chat, never written into the repository,
into a tracked file or into the journal; the profile and the secret live in a temporary
directory, and both are deleted after the run, as in the earlier live checks. If the user's
resolver set is reachable from this machine, the agent runs it; otherwise the user runs the
same steps and the node records what was seen.

## Done when

- A real `slipnet://` link imports without a warning about its fields.
- `profile show` on the imported profile prints the domain, the key's presence, the resolvers
  with their transports, the exit type and the password reference — and no password value.
- For a `dnstt` link: `connect` with the imported profile brings a session up and one fetch
  through the exit returns its body; the datapath used is named. For a password-based
  `dnstt_ssh` link: the profile is stored with the `ssh` exit and `connect` refuses it with
  the `06-ssh-exit` message. For a key-based `dnstt_ssh` link: the import refuses it with that
  stage's message and writes nothing. Both refusals are recorded as the expected result.
- The temporary profile and its secret are deleted, and no value of the link is in a tracked
  file; the node's Outcome carries only the sanitized result.
