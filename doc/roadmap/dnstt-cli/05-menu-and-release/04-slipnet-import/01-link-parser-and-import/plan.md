---
kind: task
status: done
detailed: true
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
(the menu stage) reuses the parser later.

Field 39 is copied into `payload_size` unchanged: `architecture.md` §10 calls it "payload size
per query", which is the unit P7 defines, and the live link is where a generator's real value
shows. Nothing is scaled or guessed.

Out of scope: the menu's link question (menu stage), the SSH exit itself and its `host_key` /
`key_ref` fields (`06-ssh-exit`), any live value, and a change of the link format itself — if
the implementation finds §10 wrong, that is reported, not edited here.

## Steps

1. **The shared name and domain rules** — new `internal/config/validate.go`,
   `internal/cli/wizard.go`, `internal/cli/profile.go`:
   - move `validProfileName` (`internal/cli/wizard.go:382-395`) and `validDomainName`
     (`:397-427`) into `config` as `ValidProfileName(s string) bool` and
     `ValidDomainName(s string) error`, unchanged: ASCII letters, digits and `-`, not starting
     with `-`; the core's DNS limits and label characters for the domain; the errors never
     repeat the value;
   - update the callers: `wizard.go:44`, `:137`, `:157` and `profile.go:240` call the config
     functions; the two local functions go away; no behaviour change;
   - move `TestValidProfileName` and `TestValidDomainName` (`internal/cli/wizard_test.go:695-745`)
     to `internal/config/validate_test.go` unchanged. The parser and the wizard must enforce one
     set of rules, not two copies that drift.
2. **One link** — `internal/config/links.go` (new): `Link` and `ParseLink`:
   - `type Link struct { Name string; Profile *Profile; Password string }` — `Name` empty means
     the field 2 name was absent or unusable and the caller has to ask; `Password` is the exit
     password for the secret store and is never printed; `Profile.Exit.PasswordRef` stays empty
     until the command has stored it;
   - input handling: trim, match the scheme case-insensitively, remove ASCII whitespace inside
     the payload (a terminal's soft wrap), refuse a second scheme in the same token (a link
     pasted twice), then base64-decode: the standard alphabet with padding first, then without
     padding, then the URL-safe alphabet; a decode error or a non-UTF-8 result is "the link is
     damaged; copy it again in full", without the content;
   - fields: split on `|`; field 0 the version as a decimal (missing or non-numeric is an
     error); field 1 the type (`dnstt` → exit `socks5`, `dnstt_ssh` → `ssh`;
     `sayedns`/`sayedns_ssh` refused naming NoizDNS; any other type refused by name); field 2
     the name, kept only when `ValidProfileName` accepts it; field 3 the domain through
     `ValidDomainName`; field 11 the key through `IsValidPubkey`; fields 12/13 the SOCKS5 login
     and password; fields 15/16 the SSH login and password; field 23 the SSH auth kind, where
     `key` is refused with the `06-ssh-exit` message; field 39 the payload size as a decimal
     (absent or empty 0, negative refused); other fields ignored;
   - a password without a login is refused for both types; a `dnstt_ssh` link needs a login
     and a password, because key links are refused;
   - resolvers: field 22 lowercased, absent `udp`, unknown `udp` (§10); `doh` takes field 21,
     which must be an `https://` URL and is validated by `resolvers.Parse("doh:" + url)`;
     `udp`, `tcp` and `dot` take field 4, or field 37 when 4 is empty, split on `,`, each entry
     `host[:port[:flag]]` where the third part is Slipstream's flag and is ignored, the default
     port 53, and `dot` maps 53 to 853; every entry is validated by `resolvers.Parse` and
     stored as `r.String()` (normalized, port always written); at least one resolver unless the
     transport is `doh`;
   - the profile: `Domain`, `Pubkey`, `Resolvers`, `PayloadSize`, `Exit{Type, User}`, and
     `KillSwitch` pointing at true, as the wizard writes it (`internal/cli/wizard.go:78-89`);
     `Datapath` stays empty because a link carries no mode (`connect` then defaults to
     `socks`), and `Socks` stays empty;
   - every error names the field (`field 3`, `field 11`) and the rule; none repeats the link,
     the password or the key. The type name may appear, it is not secret.
3. **Several links** — `internal/config/links.go`: `ParseLinks(text string) ([]*Link, []error)`:
   one link per non-empty line; a line without a scheme, a decode failure, a refusal or a bad
   field become a warning that names the line number and the reason, never the content, while
   the good lines are returned; order kept; no de-duplication (a second copy of a link meets
   the taken-name rule instead).
4. **The command** — `internal/cli/profile.go`:
   - `import` in the `profile` description (`:17-33`) and in the usage list of `runProfile`
     (`:45`), plus `case "import": return runProfileImport(ctx, e, subArgs)`; the subcommand
     takes no arguments (an extra one is a usage error, a link in an argument is not accepted);
   - a reader terminal check next to `isTerminal` (`internal/cli/connect.go:351-357`):
     `isTerminalReader(r io.Reader) bool` for `*os.File` and `term.IsTerminal`;
   - `runProfileImport`: load profiles, open the store through the existing `newSecretStore`
     seam (`profile.go:169-172`), then the terminal path or the stdin path;
   - terminal path: `newLinePrompter(e.stdin, e.stdout)`; ask the link by `AskSecret`; an empty
     answer or EOF with nothing imported yet is `errCancelled` (the wizard's "cancelled,
     nothing written"); a parse error or a refusal is printed and the question repeats; after
     each profile ask "Another link (Enter to finish)" by hidden input; the name comes from
     field 2 when it is usable and free, otherwise from `(&wizard{ask: p, out: e.stdout}).askName`
     (`wizard.go:127-147`, which re-asks and defaults to `home`); a profile imported in this run
     counts as taken for the next one;
   - stdin path: `io.ReadAll`, `config.ParseLinks`, every warning to stderr, every link
     imported; `ask` is nil there, so an absent or unusable name falls back to `home`, and a
     taken name is a warning and that link is skipped; when nothing was imported, return
     `no profile was imported` (the warnings are already printed), or `no slipnet:// link in
     the input` when there was no link at all;
   - one link: build the new `*config.Profiles` as a copy (`wizard.go:101-112`); if the exit
     has a password, `store.Put(name+".password", password)` first and set
     `Exit.PasswordRef`; make the first imported profile the default when there is none; save
     with `saveProfiles` (`profile.go:169-172`); a failed save deletes the stored secret and
     reports without its value (`wizard.go:113-122`); replace the in-memory set with the saved
     one so the next link of the same run sees it;
   - output: `Profile %q imported (exit %s).`; for a `dnstt_ssh` profile one more line that it
     connects only after `06-ssh-exit`; the password, the link and the key never appear in the
     output or in an error.
5. **The parser tests** — `internal/config/links_test.go`:
   - a synthetic builder in the test: `makeLink(version int, fields map[int]string) string` fills
     a `|`-joined line with empty fields up to the highest index used and base64-encodes it;
     every value is made up (`t.example.com`, `192.0.2.53`, a 64-hex key from a repeated
     pattern), never a real link or server;
   - decoding: standard alphabet with padding, without padding, URL-safe, whitespace inside,
     CRLF; a double paste refused; damaged base64 refused; a non-UTF-8 payload refused;
   - mapping: the two types; `udp` default, `tcp`, `dot` with 53 → 853, `doh` from field 21;
     field 37 when 4 is empty; several resolvers; normalized entries with the port written;
     `payload_size` absent, zero, a value, a negative; kill-switch true; datapath empty; the
     password returned separately and no reference in the profile;
   - refusals: `slipnet-enc://`; `sayedns`/`sayedns_ssh`; an unknown type; a key-based
     `dnstt_ssh`; a password without a login; `dnstt_ssh` without a login or without a
     password; a bad domain; a bad key; no resolver with `udp`; a non-https DoH URL; a bad
     version; a missing type;
   - `ParseLinks`: several lines, a bad line among good ones gives a warning with its line
     number and the good links survive;
   - one test asserts that a distinctive password and key value from a link never appear in
     any error text.
6. **The command tests** — new `internal/cli/profile_import_test.go`:
   - isolate the config with `t.Setenv("XDG_CONFIG_HOME", t.TempDir())` and `APPDATA` as the
     other CLI tests do (`connect_test.go:377-380`); a fake `config.SecretStore` recording
     `Put`/`Get`/`Delete`, assigned to `newSecretStore` and restored in `t.Cleanup`;
   - stdin path: one `dnstt` link imports a profile; `config.LoadProfiles` shows the domain,
     the key, the resolvers, `payload_size`, `exit.type`/`exit.user` and `password_ref`; the
     file holds no password; the fake store got it under `<name>.password`; `runProfileShow`
     prints the reference and no value; the profile becomes the default when it was the first;
   - several links with a bad line: the good one imports, the warning is printed;
   - a password `dnstt_ssh` link: `exit.type` is `ssh`, the reference is set, the output names
     `06-ssh-exit`; a key-based one: nothing is written and the message names `06-ssh-exit`;
   - a taken name with stdin: a warning, no overwrite; the same with the terminal path through
     a fake prompter: the name question is asked;
   - a failing `Put`: nothing is saved; a failing `saveProfiles`: the fake store saw `Delete`,
     the error does not contain the value, and no profile is in the file;
   - an empty input and an input without a link are errors and write nothing; an extra argument
     is a usage error;
   - the terminal path with a fake prompter: a link then an empty answer ends cleanly; a parse
     error repeats the question; EOF on the first question returns `errCancelled`.
7. **Help and checks** — the `profile` description and usage list in
   `internal/cli/profile.go`; then `gofmt` outside `core/`, `go build ./...` and
   `CGO_ENABLED=0 go build ./...`, the CI vet split on Linux and Windows,
   `go test -race ./internal/config ./internal/cli`, the full `go test -race -count=1 ./...`
   with `DNSTT_CLI_TEST_DBUS=1`, `DNSTT_CLI_TEST_NETNS=0` and the four external `netguard`
   live tests excluded by `-skip` (no live call), the Windows cross-build with `go test -c`
   of every package with tests, and `git diff --check`. No `doc/architecture.md` edit: §10
   already describes the format; a mismatch is reported, not changed here. No README edit: the
   release task rewrites it.

Easy to get wrong:

- the parser must not echo the link, the password or the key in an error; the link is the
  secret, and a wrapped error from base64 can otherwise carry the payload;
- a password in field 13 or 16 without a login is not "no authentication": it is refused,
  because the profile validation cannot tell the two apart later;
- `dot` with port 53 in the link means 853; the entry is normalized by `resolvers.Parse`, and
  the stored string must parse back to the same resolver;
- `doh` takes its URL from field 21, not from the hosts of field 4;
- `payload_size` is copied, not scaled: the unit is P7's bytes of DNS payload;
- the in-memory profiles set must be replaced after each saved link, or two links with the
  same name in one run both "succeed" and the second silently overwrites the first;
- a failed save after a successful `Put` must delete the secret, and the error must not name
  the value.

## Done when

- `ParseLink` decodes the standard alphabet with and without padding and the URL-safe one,
  removes whitespace inside a soft-wrapped line, and refuses a damaged link or a double paste
  without repeating its content.
- The fields map as §10: type, name, domain, key, SOCKS5 login/password, SSH login/password,
  auth kind, resolvers from 4 or 37 with the transport of 22, DoH from 21, `payload_size` from
  39; `tcp:` and `payload_size` behave as P6 and P7 define them; the profile has the
  kill-switch explicitly on, no datapath and no password reference.
- `ParseLinks` splits several lines and turns each bad line into a warning with its line
  number, keeping the good links.
- `slipnet-enc://` is refused naming the key built into the SlipNet app; `sayedns`/`sayedns_ssh`
  are refused naming NoizDNS; an unknown type, a key-based `dnstt_ssh`, a password without a
  login, an SSH link without login or password, a bad domain or key, a missing resolver unless
  DoH, a non-https DoH URL, a negative payload size and a bad version are refused with
  messages naming the field and the rule.
- `profile import` reads one link at a time by hidden input in a terminal, re-asks after a bad
  paste and asks the name with the wizard's rule, and reads lines from stdin otherwise; a
  password-based `dnstt_ssh` profile is created and the command says it connects only after
  `06-ssh-exit`; a key-based link writes nothing; several links import one by one; a taken name
  is asked in a terminal and skipped with a warning from stdin; end of input cancels without
  writing.
- The password goes to the secret store, `profiles.toml` holds only the reference, a failed
  save removes the secret and reports without its value, and neither output nor an error ever
  contains the password, the key or the link.
- The tests of steps 5 and 6 pass, and the local matrix of step 7 is green; no new dependency,
  no live run.

## Outcome

- The parser is in `internal/config/links.go`: `ParseLink`/`ParseLinks` decode the standard
  alphabet with and without padding and the URL-safe one, drop whitespace inside a
  soft-wrapped line, refuse a double paste and a damaged link without repeating the content,
  and map the fields of §10 — type, name, domain, key, SOCKS5 and SSH credentials, auth
  kind, transport and resolvers (field 4 or 37, `doh` from 21), `payload_size` — into a
  profile with the kill-switch on, an empty datapath and no password reference. Resolver
  entries are normalized by `resolvers.Parse`; `tcp` and `dot` (53 becomes 853) work as P6
  and the §10 table define them. `slipnet-enc://`, NoizDNS, unknown types, key-based
  `dnstt_ssh`, a password without a login, an SSH link without a login or password, a bad
  domain or key, a missing resolver, a non-https DoH URL, a negative payload size and a bad
  version are refused with the field and the rule in the message.
- `ValidProfileName` and `ValidDomainName` live in `internal/config/validate.go`; the wizard
  and `profile add` call them, and the tests moved with them. One set of rules, no drift.
- `profile import` is in `internal/cli/profile_import.go`: a hidden question in a terminal
  (re-asked after a bad paste, then "Another link"), lines from stdin otherwise. The name
  comes from field 2 when it is usable and free, otherwise from the wizard's question in a
  terminal or `home` on stdin; a taken name is skipped with a warning there. The password
  goes to the secret store under `<name>.password` before the save, a failed save deletes it
  and reports without the value, and the in-memory set is replaced after every save. A
  password `dnstt_ssh` profile is stored and the command says it connects only after
  `06-ssh-exit`; a key-based link is refused and writes nothing. The password, the key and
  the link never appear in the output or in an error.
- Tests are synthetic: a link builder in `internal/config/links_test.go`, and
  `internal/cli/profile_import_test.go` with an isolated config directory and a fake secret
  store. No real link, address, key or password is in the repository.
- Review added one rule the wizard already had: a login or password with a control character
  is refused, because a link from an untrusted server could otherwise inject terminal
  escapes into `profile show`. Errors from `resolvers.Parse` are replaced by own messages so
  an entry of the link cannot leak; the end of input on the name question after profiles
  were imported ends cleanly instead of "nothing written".
- An IPv6 literal in field 4 is not expressible by the `host[:port[:flag]]` grammar of §10
  (the colons are read as port separators), so such an entry is refused rather than
  silently stored as another resolver. The format is unchanged; §10 is not edited.
- Local matrix green: `gofmt` outside `core/`, `go build ./...`, `CGO_ENABLED=0 go build
  ./...`, the CI vet split on Linux and Windows, `go test -race ./internal/config
  ./internal/cli`, the full `go test -race -count=1 ./...` with `DNSTT_CLI_TEST_DBUS=1`,
  `DNSTT_CLI_TEST_NETNS=0` and the four external `netguard` live tests excluded by `-skip`,
  the Windows cross-build and `go test -c` of all 19 packages with tests, and `git diff
  --check`. No new dependency.

Not checked here: a real generator's link — that is `02-live-link`; the SSH exit and its
`host_key`/`key_ref` — `06-ssh-exit`; the menu's link question — the menu stage.
