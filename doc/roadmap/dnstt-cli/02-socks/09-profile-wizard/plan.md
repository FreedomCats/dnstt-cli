---
kind: task
status: done
detailed: true
depends: [07-commands]
---

# Interactive profile wizard

## Task

Turn `profile add` into the interactive wizard described in `architecture.md` §10a, the item
[Commands](../07-commands/plan.md) deferred. The profile for the Linux live acceptance is
created from prompts, validated and saved in one go; the exit password goes to the secret
store and only its reference enters the profile. The wizard is a reusable component, so the
later menu (E4, [Interactive mode and releases](../../05-menu-and-release/plan.md)) calls the
same code instead of growing logic of its own.

This fixes the current failure of `profile add`: `internal/cli/profile.go:194-241` builds a
minimal profile with empty `domain`/`pubkey`, and `SaveProfilesTo`/`LoadProfiles`
(`internal/config/profiles.go:104-133,152-206`) reject it. `profile set` is a placeholder
(`internal/cli/profile.go:243-266`), so the product has no way to create a complete profile.
The fix creates it through the wizard; profile validation is not weakened. `profile set`
stays as it is; the wizard's question helpers are shaped so it can reuse them later.

The password is asked twice without echo, stored through `config.SecretStore`
(`internal/config/secret.go:31-42`), and only `exit.password_ref` reaches `profiles.toml`.
No new dependency: `golang.org/x/term` and `internal/lines` are already used
(`internal/cli/connect.go:13`, `internal/lines/lines.go`). No README rewrite: the `profile
add` help text is the only documentation.

## Steps

1. **Export the key check.** In `internal/config/profiles.go`, rename `isValidPubkey`
   (line 209) to the exported `IsValidPubkey` with a doc comment, and use it in
   `validateProfile`. The wizard lives in another package and must not duplicate the
   "exactly 64 hex digits" rule. No other config change, no change of behaviour.

2. **Add the prompter.** New `internal/cli/prompt.go`:
   - `prompter` interface: `Ask(ctx, question) (string, error)` and
     `AskSecret(ctx, question) (string, error)`.
   - `linePrompter` with `in io.Reader`, `out io.Writer` and one `*lines.Reader`;
     `newLinePrompter(in, out)`. `Ask` writes the question (the caller includes the
     default), then `lines.Read(ctx)`. `AskSecret` does the same but uses
     `term.ReadPassword(fd)` when `in` is an `*os.File` and `term.IsTerminal(fd)`; a
     non-terminal (a test buffer or a redirected file) reads an ordinary line from the same
     reader. Print a newline after the hidden read.
   - Never echo, log or include the answer in an error. The wizard prints the reasons, not
     the prompter.
   - One reader per process: the wizard owns this reader; the menu will pass its own later.
     `internal/lines` already documents why hidden input reads the terminal past the shared
     reader (`internal/lines/lines.go:1-14`).

3. **Write the wizard component.** New `internal/cli/wizard.go`:
   - `wizard{ask prompter; out io.Writer; store config.SecretStore; modes []string;
     tunUnsupported error; save func(*config.Profiles) error}` and
     `add(ctx context.Context, profiles *config.Profiles, name string) (string, error)`.
   - Questions in the §10a order:
     - **Name**: a positional `name` skips the question; otherwise ask `Profile name
       [home]:`, Enter is `home`. Accept non-empty ASCII letters, digits and `-`, not
       starting with `-`; a name already in `profiles` is re-asked (or, for a positional
       name, is an error before any question).
     - **Domain**: trim; `dns.ParseName` (core DNS limits) plus per-label checks: 1–63
       characters of `[A-Za-z0-9-]`, no leading or trailing `-`, no empty label; store the
       trimmed value.
     - **Server key**: trim; `config.IsValidPubkey`; the reason names the expected form,
       never the value.
     - **Exit type**: `socks5` (Enter) or `http`. `ssh` is refused with "the SSH exit is
       not implemented yet (stage E5); choose socks5 or http" and re-asked; any other
       answer is re-asked with the two names.
     - **Login and password**: for `socks5`, an empty login means no authentication: no
       password is asked, `exit.user` is empty and no secret is written. For `http`, an
       empty login is re-asked: Basic needs a login. A login is rejected for control
       characters, for `:` on HTTP, and for more than 255 bytes on SOCKS5. When a login is
       given, ask `Exit password:` and `Exit password again:` hidden; an empty or
       mismatched pair re-asks both; control characters and SOCKS5 passwords over 255
       bytes are re-asked. The password stays in memory until the save step.
     - **Resolvers**: one entry per line. The first prompt is `Resolver (Enter for
       "network"):`; an empty first answer gives `["network"]` and no further resolver
       questions. Later prompts are `Another resolver (Enter to finish):`; an empty answer
       finishes. Validate every line with `resolvers.Expand([]string{line})`
       (`internal/resolvers/presets.go:45`), accept a preset name, `network`, or a
       `doh:`/`dot:`/`udp:` entry, and drop a duplicate by `Resolver.String()`. Store the
       trimmed line as typed, so a preset name stays short.
     - **Mode**: offer `modes` in order, Enter takes the first. With `tun` not offered,
       answer `tun` is refused with `tunUnsupported` when there is a reason (a Linux kernel
       without `/dev/net/tun`) and with "not available on this system yet" otherwise; an
       unknown word is re-asked with the offered list.
   - Build `config.Profile{Domain, Pubkey, Resolvers, Datapath, KillSwitch: killSwitch,
     Exit: config.Exit{Type, User, PasswordRef}}`, with a local `killSwitch := true` as the
     current command does; leave `Bootstrap`, `UTLS`, `TunDNS` and `Socks` empty — their
     defaults are the product defaults (`internal/app/tunnel.go` reads an empty `tun_dns`
     as its default).
   - Cancel: EOF from any read (including a hidden read) becomes `errCancelled`; a
     cancelled context is returned as itself, so `run` reports "interrupted"; any other
     read error is returned. Nothing is written before the last question.
   - Save: only after all answers. If a password was entered, `store.Put(name+".password",
     password)` and set `PasswordRef`; then save a shallow copy of `Profiles` with the new
     profile, and set `DefaultProfile` when it was empty. On a `Put` error return without
     saving. On a save error call `store.Delete(ref)`, return the joined error and tell
     that the secret was removed (or that it could not be removed, without the value). Do
     not mutate the caller's `Profiles`; a failed save leaves no profile and no error text
     with the password.

4. **Wire `profile add`.** In `internal/cli/profile.go`:
   - Update the `profile` description: `add [name]` creates a profile with the wizard; a
     short paragraph gives the question order, the one-resolver-per-line input, the
     supported exits (`socks5`, `http`; the SSH exit is not implemented yet) and the mode
     (`tun` where supported, needs root to connect; otherwise `socks`), and says the exit
     password is stored in the secret store, not in `profiles.toml`.
   - `runProfileAdd` accepts zero or one argument. A positional name is checked with
     `validProfileName` and a bad one is a usage error; more than one is a usage error.
   - Load profiles; treat only `errors.Is(err, fs.ErrNotExist)` as "start a new file". Any
     other error, including an invalid existing profile, is returned: a corrupt or
     invalid `profiles.toml` must not be overwritten.
   - Create the store with `config.NewSecretStore()`. Compute the offered modes and the
     reason from `(&datapath.Tun{}).Requires()`: `tun` then `socks` when `Unsupported` is
     nil, otherwise `socks` only and the reason (`internal/datapath/tun.go:135`,
     `tun_other.go:14`). Build the wizard with `newLinePrompter(e.stdin, e.stdout)`,
     `out: e.stdout`, `save: config.SaveProfiles` and call `add`.
   - On `errCancelled`, return it so `run` prints "cancelled, nothing written" and exits 1;
     on success print `Profile %q added (exit %s, mode %s).` Do not change `runProfileSet`.

5. **Component tests.** New `internal/cli/wizard_test.go`:
   - `scriptedPrompter`: a queue of answers marked line or secret, records the questions,
     fails a test if the wrong method is used or the queue is empty (returns `io.EOF`), and
     can return an injected read error; `fakeSecretStore` (map, counts `Put`/`Delete`, can
     fail `Put`); a save function that records or fails.
   - Cases: a full SOCKS5 no-auth profile (empty login asks no password, no `Put`, mode
     Enter is `tun` when offered); a full HTTP profile (login and password required); a
     positional name skips the name question; the `home`/`network`/first-mode defaults; a
     name collision; every re-ask (bad name, bad domain, short or non-hex key, `ssh`,
     unknown exit type, empty HTTP login, empty and mismatched password, bad resolver,
     unknown mode, `tun` not offered); EOF at each question → `errCancelled`, no `Put`, no
     save; a context cancellation is not turned into `errCancelled`; an injected read error
     propagates; a failing save calls `Delete(ref)` and leaves the caller's profiles
     unchanged; no reason or error contains the password. Unit tests for
     `validProfileName`/`validDomainName`.

6. **Command and connect tests.** New `internal/cli/profile_test.go`:
   - Reuse `isolatedConnectConfig` (`internal/cli/connect_test.go:374`): it clears
     `SUDO_USER`, `DBUS_SESSION_BUS_ADDRESS`, `XDG_RUNTIME_DIR` and sets
     `XDG_CONFIG_HOME`/`APPDATA`, so no test reaches the real keyring or config. Add a
     `runCLIInput` helper that puts a string in `env.stdin` and captures the output.
   - `profile add` end to end with piped answers: the profile loads, its fields are
     complete, it becomes the default when it was empty, `config.NewSecretStoreFor(path)`
     returns the password by the profile's `password_ref`, and `profiles.toml` does not
     contain the password.
   - Cancel: an empty or partial input exits 1, prints "cancelled", writes nothing, and an
     existing `profiles.toml`/`secrets.json` stay byte-identical. A duplicate name, a
     corrupt `profiles.toml`, extra arguments and a bad name are refused.
   - The help output of `profile -h` names `add [name]`, the wizard, the supported exits
     and the secret store.
   - Synthetic `connect`: start `server.StartTest` with the test domain and a loopback HTTP
     CONNECT/Basic upstream (a small helper in this file, shaped like the upstream in
     `TestConnectHTTPProfile`); create the profile by the wizard with `t.example.com`, the
     server's hex key, `http`, a synthetic login and password twice, `udp:<server addr>`
     and `socks`; run `connect -v` and pass one request through the local SOCKS listener;
     assert the echo and that no diagnostic contains the password, login or reference. The
     loopback server replaces a live network; no real terminal, keyring or profile is used.
   - The terminal branch of `AskSecret` is not exercised by tests; the rest of the prompter
     is (see Risks).

7. **Verify.** `gofmt -l` empty, `go vet ./...`, `go test ./internal/cli ./internal/config`
   and the full suite with the existing external-network `netguard` live tests skipped as
   before; `GOOS=windows go build ./...` and `GOOS=windows go test -c` for `internal/cli`
   and `internal/config`; no new `go.mod`/`go.sum` lines; the diff has only synthetic
   values from `t.example.com`, documentation addresses and generated keys. No commit.

## Done when

- `profile add [name]` asks in the order of §10a: the name (a positional name skips the
  question; Enter — `home`), the tunnel domain, the server public key, the exit type
  (`socks5` or `http`, Enter — `socks5`; `ssh` refused as not implemented), the exit login
  and the password (hidden, twice; a SOCKS5 exit with an empty login is no-auth and asks no
  password; an HTTP exit needs both), the resolvers (one per line; Enter — `network`) and
  the mode (`tun` where the datapath reports support, Enter takes it; otherwise only
  `socks`, and `tun` is refused with the reason).
- Invalid name, domain, key, exit fields, resolver list or mode is re-asked with a short
  reason; the reason never repeats the domain, key or password; the password is never
  echoed, logged or written to `profiles.toml`; an empty or mismatched password is
  re-asked.
- End of input on any question cancels with exit code 1 and a "cancelled, nothing written"
  message; an existing `profiles.toml` and secret store are unchanged. A corrupt or invalid
  `profiles.toml` is an error, not overwritten.
- The completed profile passes the normal validation, is saved, becomes the default when
  there was none, and is loaded and used by `connect` in the synthetic test; the exit
  password is read back through `config.SecretStore` and `exit.password_ref`; a failed save
  rolls the secret back and leaves no profile.
- The prompts, defaults, validation, cancellation and save live in a reusable wizard
  component under `internal/cli` with an injected prompter and `config.SecretStore`;
  `profile add` is a thin caller; `profile set` is unchanged; no new dependency; no test
  needs a real terminal, keyring, profile or live network; the Windows build and test
  compile pass.
- `profile -h` describes the wizard; formatting, vet and the existing test suite pass; no
  real secret appears in the diff, the help or the logs.

## Risks and what is needed from the user

- The hidden-input branch cannot be covered by an automated test: it needs a real
  terminal. The user should run `profile add` once on a terminal with a throwaway config
  directory and check that the password is not echoed, the second entry works and EOF
  cancels. The live acceptance will exercise the real profile creation.
- An invalid `profiles.toml` (for example, a hand-edited entry or a leftover file from an
  older build) is not repaired: `profile add` refuses to load it rather than overwrite it.
  The user should fix or remove the offending entry before running `profile add` against
  the real config.
- EOF is the cancel path here; Ctrl+C and signal handling belong to the menu stage (E4).
- A SOCKS5 exit with a login but an empty password is not offered: an empty login means
  no auth, a non-empty login requires a password. The profile schema still allows such an
  exit in a hand-written file; the wizard does not create it.

## Depends on

[Commands](../07-commands/plan.md) and [Profiles and secrets](../06-profiles-secrets/plan.md).

## Outcome

Done as planned. `profile add [name]` now runs the `architecture.md` §10a wizard; `profile
set` is still the placeholder.

- `internal/cli/prompt.go` — the `prompter` interface and `linePrompter`: a question goes to
  `out`, the answer comes from one shared `lines.Reader`; `AskSecret` reads through
  `term.ReadPassword` when the input is a terminal and falls back to a plain line otherwise.
  Answers are never echoed, logged or put into an error.
- `internal/cli/wizard.go` — the reusable `wizard` component and `add`: the question order of
  §10a, the defaults (`home`, `network`, the first offered mode, `socks5`), every re-ask
  with a short reason that never repeats the domain, key or password, and `errCancelled` on
  the end of input. The password goes to `config.SecretStore`; a failed save deletes the
  secret and says whether the deletion worked; the caller's `Profiles` is not mutated. `add`
  returns the name and the built profile, so the caller reports the exit type and the mode
  without reaching into `Profiles`.
- `internal/cli/profile.go` — `runProfileAdd` is a thin caller: zero or one argument, only
  `fs.ErrNotExist` starts a new file (an invalid `profiles.toml` is refused, not
  overwritten), the offered modes and the reason come from `(&datapath.Tun{}).Requires()`.
  `profile -h` describes the wizard, the supported exits and the secret store.
- `internal/config/profiles.go` — `isValidPubkey` became the exported `IsValidPubkey`,
  behaviour unchanged; the wizard and `validateProfile` now share the one rule.
- Tests — `internal/cli/wizard_test.go` (a scripted prompter and a fake store: defaults,
  every re-ask, cancellation at every question, a failing save that rolls the secret back,
  the prompt helpers) and `internal/cli/profile_test.go` (`profile add` end to end with piped
  input, byte-identical files after a cancel, a duplicate name, a corrupt file, usage errors,
  the help, and a synthetic `connect` through a loopback HTTP CONNECT/Basic exit). The
  synthetic test sets `socks.listen` to `127.0.0.1:0` after the wizard, so it does not take
  the product's default port.
- Not covered by tests: the hidden-input branch of `AskSecret` needs a real terminal; that
  check stays with the user, as recorded in Risks.

`gofmt`, the CI split of `go vet`, both builds, the Windows vet and test compilation, and
the full `-race` suite pass. No new dependency, no `go.mod`/`go.sum` change.

One older gap came up during review and is not fixed here: `profile rm` removes the profile
but not its secret from the store. It belongs to a separate task.
