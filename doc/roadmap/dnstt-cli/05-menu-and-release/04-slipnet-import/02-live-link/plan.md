---
kind: task
status: done
detailed: true
depends: [01-link-parser-and-import]
---

# A real link

## Task

The import of the parser task checked on a link the user actually has. Synthetic links cannot
prove the generator's shape — the version, the field count and the transports SlipGate or the
SlipNet app writes — so one real link is imported in a temporary configuration, and its outcome
is recorded. The parser and the command are done ([`01-link-parser-and-import`][parser]); this
node adds only what a real generator shows. §10 of
[`architecture.md`](../../../../../architecture.md) is the format: a mismatch is reported, and
neither the link, the parser nor §10 is edited here.

The link carries the exit password in plain text. It is given in chat, never written into the
repository, a tracked file or the journal; the link, the profile and the secret live in one
directory under ignored `doc/runs/live-link/`, mode 0700 for the directory and 0600 for the
files, and the whole directory is deleted after the run. The user's paste in the chat is the
permission for the run: nothing is fetched, installed or built in a container, and the host is
not changed.

A `dnstt` link imports as a `socks5` exit (the parent stage's decision). The user's server has
been exercised with an HTTP CONNECT exit (`02-socks`), which a SOCKS5 exit cannot authenticate
against; unless the link's server serves a SOCKS5 exit, `connect` is therefore
`not seen — exit type` — recorded and never presented as a live connection. A password-based
`dnstt_ssh` link stores an `ssh` profile that does not connect until `06-ssh-exit`; the refusal
is the designed result. A key-based `dnstt_ssh` link is refused by the import with the message
naming that stage and nothing is written; also the designed result. A link of any other type is
refused by name, and the refusal is recorded, not counted as a failure.

If the user has no real link, the node is `not seen — no real slipnet:// link`: nothing is
invented, no run directory is created, and the synthetic tests of the parser task remain the
only evidence for the format.

[parser]: ../01-link-parser-and-import/plan.md

## Steps

1. **The link in chat.** Ask the user for one real `slipnet://` link of each kind they have — a
   `dnstt` one, a password-based `dnstt_ssh` one, a key-based one — and say what will happen to
   it: it is written to the ignored run directory as a 0600 file, the import stores its profile
   and secret there, and the directory is deleted afterwards. The link is pasted into the chat
   only: never into a tracked file, the journal or a commit, and never as a command argument
   (`profile import` takes none). No link, or a refusal, ends the node here with
   `not seen — no real slipnet:// link`; a kind the user does not have is
   `not seen — no link of this kind`. If the user prefers to run the steps themselves, they do
   3–7 exactly and report the observed lines, and the sanitized result comes back the same way.
   The request and the answer are recorded in `<run-id>/permission.txt` (step 2 creates the
   directory); a refusal stops the task and nothing is run.

2. **Preflight: run id, build, isolation rails.** Read the parser task's Outcome. Choose the
   next free `<run-id>` under `doc/runs/live-link/` (`real-1` unless taken), `umask 077`, and
   create `doc/runs/live-link/<run-id>/` with mode 0700. `git check-ignore -v` must name
   `doc/runs/live-link/<run-id>/` and `build/out/dnstt-cli`; never `git add -f`. Build the
   binary from the reviewed tree, offline:
   `GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off CGO_ENABLED=0 go build -mod=readonly
   -o build/out/dnstt-cli ./cmd/dnstt-cli`; save the command, its exit code, `go version`,
   `git rev-parse HEAD`, `git status --short` and `sha256sum build/out/dnstt-cli` in
   `<run-id>/build.txt`. A tracked change outside `doc/` and the ignored paths stops the task:
   the binary must come from the reviewed tree. A missing toolchain or module is not a reason to
   fetch: stop and report. Save `sha256sum` of the user's real
   `profiles.toml` and `secrets.json` — or the fact that one is absent — in
   `<run-id>/real-config-before.txt`; they are compared in step 7.

3. **The link file and its shape.** For each kind, create `<run-id>/<kind>/` with `cfg/` and
   `out/` (0700 each) and write `<kind>/link.txt` (0600) from the chat value — with a hidden
   `read -rs` in an interactive shell or the executor's file-writing tool; never with `echo`, a
   here-document in an interactive shell or an argument; `chmod 600`; clear the variable. A line
   wrapped by the chat or the terminal is fine: the parser removes ASCII whitespace inside the
   payload, and this step does the same. Record the shape, and only the shape: strip the scheme
   (case-insensitively), base64-decode the payload and print `version=<field 0>
   fields=<field count>` into `<kind>/shape.txt`; decode with the standard alphabet first, and
   on failure translate `-`/`_` to `+`/`/` and add the padding, as the parser does; if it still
   does not decode, record that instead. §10 names the two
   generator shapes (version 22 with 60 fields and version 28 with 79); the version tells which
   one wrote the link, and a different count is what this node exists to find. No field value is
   printed by this step.

4. **Import into one isolated config per kind.** Point the platform's config variable at the
   kind's `cfg` (`XDG_CONFIG_HOME` on Linux, `APPDATA` on Windows) and, on Linux, also unset
   `DBUS_SESSION_BUS_ADDRESS` and `XDG_RUNTIME_DIR`: with a session keyring, `NewSecretStore`
   would put the secret into the user's real keyring instead of the run directory, and the run
   must leave no trace there. No `sudo`: under it the config path switches to the invoking
   user's and ignores `XDG_CONFIG_HOME` (`internal/config/dir.go:59-84`), and the `socks`
   datapath needs no root either. Before the import, `profile list` must print
   `No profiles configured.`; anything else means the isolation failed, and the run stops before
   a value is written. Then `<bin> profile import < link.txt > import.out 2> import.err` and
   `echo $?`: stdin is not a terminal, so the links come from the file, and nothing is asked.
   Expected: `Profile "<name>" imported (exit socks5).`, or `(exit ssh)` with the line about
   `06-ssh-exit`, and exit 0. For a `dnstt` or a password-based `dnstt_ssh` link, a warning
   naming a field or a non-zero exit is recorded and that kind stops there — the link and the
   code are not edited here; the key-based link's refusal is step 6's expected result.

5. **The result without values.** For each imported kind, `profile list` (the name and
   `(default)`, because the config was empty) and then `profile show <name>` **without
   `-full`**: the default form prints the domain and the key masked, the resolvers in full, an
   empty datapath, the kill-switch, the payload size when it is not zero, the exit type and
   user, and `Exit Password: (stored in <ref>)`. Capture it into `<kind>/show.out`. Compare the
   mapped values with the decoded link inside the run directory and record only `match` or
   `mismatch` per item: `domain` with field 3, `pubkey` with field 11, the number of resolvers
   and their transport prefix with fields 4 (or 37) and 22, `payload_size` with field 39,
   `exit.type` and `exit.user` with the type and fields 12 or 15. Check the modes: `cfg` 0700,
   `cfg/dnstt-cli/secrets.json` 0600, and `profiles.toml` 0644 as the writer makes it
   (`internal/config/profiles.go:143`) — it holds no secret, and the 0700 directory is what
   protects it. When the link carries a password, its reference must be `file:<id>`, not
   `secretservice:`, and `secrets.json` must hold one entry named `<name>.password`; a keyring
   reference means the run wrote into the real keyring, and it is stopped and reported.
   `profiles.toml` must not contain the password from field 13 or 16; a link without a password
   has no secret and no reference. Neither file is copied out of the run directory.

6. **The kinds.**
   - **`dnstt`** — no `connect`. The imported exit is `socks5`, the user's server serves an HTTP
     CONNECT exit, and a SOCKS5 greeting cannot be served by it: the result is
     `not seen — exit type`, with that reason, not a failure. Only if the user says the link's
     server serves a SOCKS5 exit is `connect <name>` run for a bounded time, one fetch goes
     through the local proxy, and the datapath is named as `socks` in the record; a timeout is a
     harness failure, not a result. The import is never described as a live connection.
   - **`dnstt_ssh`, password** — `connect <name>` must print
     `dnstt-cli connect: SSH exit is not implemented yet (stage E5)` on stderr and exit 1,
     before any listener is opened and before any dial: the exit is built before the datapath
     (`internal/cli/connect.go:114-131`, `internal/app/tunnel.go:142-143`), and E5 is
     `06-ssh-exit`. `profiles.toml` and `secrets.json` are byte-identical before and after the
     command. The refusal is the expected result, not a failure of the import.
   - **`dnstt_ssh`, key** — expect `line 1: a key-based dnstt_ssh link is not imported until
     stage 06-ssh-exit: the profile is not stored and no key is kept` on stderr, then
     `dnstt-cli profile: no profile was imported` and exit 1, with no profile and no secret in
     that kind's config directory (`cfg/dnstt-cli` absent or empty). This is the designed
     result; a profile or a secret written for this link fails the node.
   - **Another type** (`sayedns`, `sayedns_ssh`, `slipnet-enc://`, an unknown one) — the
     refusal names it and nothing is written; that kind is `not seen — link of another type`,
     and the refusal is recorded as the result of that link.

7. **Cleanup and the sanitized result.** Before deleting anything, scan for the values: from
   the decoded fields put the domain, the key and the passwords into a 0600 patterns file in the
   run directory and run `git grep -F -f <patterns>` over the tracked files and
   `grep -R -F -f <patterns>` over `doc/roadmap` and `doc/local` (the journal is the lead's and
   is outside git); a hit stops the node and is reported, never edited around. Then write
   `<run-id>/result.txt` with the sanitized lines: which kinds ran, the version and field count,
   the transport, the payload size, the modes, the reference scheme, the refusal lines quoted
   above, the connection outcome, and whether the real config changed (compare the hashes of
   step 2). Copy that into the coordinator's report, and delete the whole
   `doc/runs/live-link/<run-id>/` directory: it is the only copy of the link, the password and
   the profile. `git status --short` must show nothing new or changed in tracked files; no host
   state was touched, and the real profile and store are byte-identical.

Easy to get wrong:

- the link is a password in plain text: one copy, 0600, inside the run directory, deleted at the
  end; never in an argument, in `echo` or a here-document of an interactive shell, in the shell
  history, in the journal or in a commit; the chat message to the user is the only other place;
- on Linux a session keyring would take the secret outside the run directory and leave it in the
  user's real keyring; unsetting `DBUS_SESSION_BUS_ADDRESS` and `XDG_RUNTIME_DIR` makes the store
  fall back to the `file` scheme — verify `password_ref = "file:…"` before trusting the cleanup
  of the directory;
- under `sudo` the config path ignores `XDG_CONFIG_HOME` and switches to the invoking user's
  (`internal/config/dir.go:59-84`); the run takes no root;
- `profile show` prints the resolvers in full and the domain and key masked; the output belongs
  in the ignored run directory only, never with `-full`, and never in the Outcome — the
  resolvers are the ISP's and reveal the region;
- a warning about a field is the finding, not a reason to change the link or the parser: §10 is
  the format, and a new parser task is the lead's call;
- the import is not a connection: for `dnstt` nothing dials unless the user's exit speaks
  SOCKS5, so the record must not say "connected";
- `git add -f` is never used; the run directory is ignored, read once and deleted;
- the real profile and store are never written by the run: the config variable points at the
  run directory, and the `file` reference proves which store was used.

## Done when

- A real `slipnet://` link imports in an isolated config with no warning about its fields and no
  value in a tracked file: `Profile "<name>" imported (exit …)` and exit 0 — or the node
  records `not seen — no real slipnet:// link` and invents nothing.
- The generator's shape is recorded (version and field count), and the profile matches the link:
  the domain, the key, the resolvers with their transport, `payload_size` from field 39, the exit
  type and user, and the password reference; `profile show` prints the masked domain and key,
  the resolvers, the exit type and the reference, and no password value.
- `profiles.toml` holds no password; when the link carries one, `secrets.json` is 0600 with one
  `<name>.password` entry under the `file` scheme; the config directory is 0700; the real
  profile and store are unchanged.
- For a `dnstt` link: `connect` is `not seen — exit type` (or `seen` with one fetch, if the
  server's exit speaks SOCKS5), and the import is not presented as a live connection. For a
  password-based `dnstt_ssh` link: `connect` refuses with
  `SSH exit is not implemented yet (stage E5)` and exits 1, writing nothing. For a key-based
  `dnstt_ssh` link: the import refuses it with the `06-ssh-exit` message and writes nothing.
  All three are recorded as the expected results.
- The run directory with the link, the profile and the secret is deleted; the real config was
  compared before and after and did not change; `git status --short` shows nothing in tracked
  files; the node's Outcome carries only the sanitized result.
- A mismatch with §10, a link of another type or a kind the user did not supply is recorded as a
  finding or as `not seen — <reason>`, never as a pass and never patched here.

## Outcome

The user has no real `slipnet://` link (asked in chat). The node is
`not seen — no real slipnet:// link`: no run directory was created, no link, profile or secret
was written, and nothing was built or fetched. The synthetic links of
[the parser task](../01-link-parser-and-import/plan.md) remain the only evidence for the
format and the import command; no generator shape, live connection or real link is claimed.
The real profile and store were not touched.
