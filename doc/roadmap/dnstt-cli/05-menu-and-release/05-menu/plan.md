---
kind: stage
status: done
base: 9ce7e2f
depends: [04-slipnet-import]
---

# The menu and the wizard

Goal (`architecture.md` §10a): `dnstt-cli` without arguments in a terminal opens a menu that
calls the same code as the commands; without a terminal it prints the command list. With no
profile yet the wizard runs, with a `slipnet://` link or the questions; the connect flow keeps
the resolvers visible and offers to change them; when they run out mid-session the menu offers
to check and edit them and connects again.

Most pieces exist and are meant for this menu: `connect` with its status line and Ctrl+C
handling (`internal/cli/connect.go`), the wizard with its `prompter` seam
(`internal/cli/wizard.go:22-33`, `internal/cli/prompt.go:14-21`), `resolvers check`,
`profile rm`, and the one shared line reader (`internal/lines`). The menu adds the loop, the
items, the reuse of the command bodies and the signals of §10a.

## Done when

- No arguments in a terminal opens the menu; without a terminal, no arguments print the
  command list; `menu` opens it and `menu -connect <profile>` connects at once.
- The items call the same code as the commands: connect, check resolvers, edit resolvers, new
  exit password, change domain or key, another profile, delete profile, exit (`0`).
- With no profile the wizard runs: a link by hidden input or the questions, the defaults
  `home`, `network` and `tun` where supported.
- Connect shows the saved resolvers and offers to connect with them or change them first; the
  changed list is saved to the profile.
- Ctrl+C while connected returns to the menu and the second one does not wait; in the menu it
  closes the menu; SIGTERM and SIGHUP remove the tunnel and close the menu; the status line
  sits at the bottom and is absent with redirected output.
- When no resolver completes a handshake for a full round mid-session, the menu offers to
  check and edit them and connects again.
- `connect` on the command line behaves exactly as before: the same flags, messages, status
  line, drain and exit codes. A signal that arrives while the tunnel is still being set up now
  stops cleanly with the same `stopping...` line instead of killing the process, because the
  stop ladder is armed before the connection body; that window used to leave the routes and the
  system DNS behind.

## Children

1. [The menu and the command reuse](01-menu-and-commands/plan.md) — the start, the loop, the
   items over the existing command bodies (connect, check resolvers, another profile, delete
   profile, exit), the wizard, the status line and the signals. Task.
2. [Editing the profile in the menu](02-profile-editing/plan.md) — the connect-time resolver
   offer, edit resolvers, check and drop dead ones, a new exit password, a new domain or key.
   Task.
3. [Resolvers run out mid-session](03-resolvers-exhausted/plan.md) — the supervisor's event
   for a full failed round and the menu's offer to check, edit and reconnect. Task.

The loop and the reuse come first: every later item is a branch of the same loop. Editing
comes next, because the mid-session offer reuses the edit path. The exhausted-resolvers event
is last: it needs a small supervisor seam and is the only part that touches the state machine.

Limits: the Windows double-click behaviour (the window does not close on an error) and a
console's signals are cross-built and compiled only, because `04-tun-windows` is blocked and
no Windows machine is available; a real terminal check of the keys and the hidden input is a
manual check on Linux.

## Outcome

Done: `dnstt-cli` without arguments in a terminal opens the menu, without a terminal it prints
the command list, and the menu has no logic of its own — every item calls the function of the
command behind it. The three children carry the details; this section records the acceptance of
them together.

- Start and the loop (`internal/cli/menu.go`, `root.go`): the no-argument decision reads the
  `stdinIsTerminal`/`stdoutIsTerminal` indirections, so a test pins it without a `*os.File`
  terminal; `menu` sits in `commands()` right after `connect`, and `menu -connect <profile>`
  runs one connection through the same session and body and returns. The loop reloads the
  profiles every iteration, numbers the seven items by the table's order with `0` as exit, takes
  an empty answer as connect, re-asks an unknown answer and closes on the end of input without
  writing.
- One connection for both callers (`internal/cli/connect.go`): `runConnection` with
  `connectRequest` is what `connect`, `menu -connect` and the menu's connect item call; the
  flags, the messages, the status line, the drain and the exit codes are the command's, and the
  `connect` tests pass unmodified. `writeStopNotice` is the one wording of the stop line.
- The first profile (`internal/cli/profile_import.go`, `profile.go`): `importOne` and
  `newWizard` are shared with `profile import` and `profile add`, so the menu's first question
  is the hidden `slipnet://` link and an empty answer is the wizard's questions, with the
  defaults `home`, `network` and `tun` where supported.
- Editing (`internal/cli/menu_edit.go`): the connect item prints the saved resolvers and offers
  Enter to connect, `e` to edit first and `n` for the menu; an edited list is saved before the
  body loads the profile, so this connect uses it. The check item drops the failed entries on
  request and never writes an empty list; a new exit password goes to the store under a fresh
  name, the profile is saved, and only then is the old entry deleted, so a failed save rolls
  back; a new domain or key is asked with the wizard's validators and the current value as a
  masked default, and the file is written once.
- The resolvers running out (`internal/supervisor/tunnel.go`, `internal/app/tunnel.go`,
  `internal/cli/menu_round.go`): `OnRoundFailed` fires once per full failed round after a
  session was up, before the backoff pause, with a copy of the failures. The menu installs it
  per connection, stops the connection before printing, names the resolvers from the event,
  and on `y` runs the same check and the same editor as the items and connects again; `n`
  writes nothing, and `connect` and `menu -connect` leave the callback nil.
- Signals and output (`internal/cli/menu_signals.go`, `menu_windows.go`, `menu_other.go`): one
  `menuSession` per menu run; the first Ctrl+C stops the item, the second closes the abort, the
  third does nothing, Ctrl+C while the menu waits closes it, SIGTERM and SIGHUP stop the item
  and close the menu. There is no `os.Exit` in the menu and no `signal.Notify` in the connection
  body. The status line stays the command's, at the bottom, absent with redirected output. On
  Windows a window opened by a double click waits for Enter after an error, through the same
  prompter.
- Tests: `menu_test.go`, `menu_edit_test.go`, `menu_round_test.go` and
  `supervisor/roundfailed_test.go` — buffers, a scripted prompter, fake command bodies, a fake
  secret store, an injected signal channel and an isolated configuration directory. No test
  opens a terminal, reads a real key or touches the network.

What the acceptance found and fixed:

- The leftover `resolverOutcome.name` of child 2 was written and never read: the mid-session
  offer names the resolvers from the supervisor's event. The field and its initialisations are
  removed.
- The round test did not pin "the event comes before the pause": moving the callback below
  `t.pause` left it green. It now compares the transitions and the backoff sleeps the callback
  sees with the counts at the round's first failed dial.
- The round test waited for an exact number of events. The harness's sleep is instant, so the
  supervisor ran several rounds before the count was observed and `go test -count=1 ./...`
  without `-race` — the form the Windows job runs — timed out. The two waits now accept more
  rounds than asked for; the ordering check is unchanged.
- Nothing tested that `newMenu` binds the items to the command's own functions, because the
  harness replaces every seam: two seams of the same signature could be swapped. A test now
  runs the menu's own `list`, `remove` and `save` against an isolated configuration directory.

Decisions and notes:

- `menu -connect` asks nothing: the plans of children 1 and 2 settled it, so the connect-time
  resolver offer runs in the menu's connect item only. `architecture.md` §10a's wording
  "connects at once with the same questions" is ambiguous; the accepted behaviour is the
  question-free one, and that document was not edited here.
- The Ctrl+C ladder of `connect` is armed before the connection body, so a signal during the
  setup ends cleanly instead of killing the process; the menu cannot get the ladder, because
  its third press must not call `os.Exit`.
- The Windows pause stands in `runMenu` before the error is reported; moving it after the
  report would put it outside the menu.
- A set with profiles but no default asks which profile to work with; an empty answer closes
  the menu, because the menu cannot run without a profile.

Verified on the final tree: `gofmt` outside `core/`; `go build ./...` and
`CGO_ENABLED=0 go build ./...`; the CI vet split on Linux and with `GOOS=windows`, the package
list taken under the same `GOOS`; the full `go test -race -count=1 ./...` with
`DNSTT_CLI_TEST_DBUS=1`, `DNSTT_CLI_TEST_NETNS=0` and the four external `netguard` live tests
excluded by `-skip`; `go test -count=1 ./internal/supervisor/` without `-race`, five times;
the Windows cross-build with `go test -c` of all 19 packages that have tests; `git diff --check`
on the tree and on the range. No new dependency; `core/`, `doc/architecture.md` and `README.md`
are untouched.

Not seen, and not claimed: the real terminal — the keys, the hidden input, the masked defaults,
the status line, `kill -TERM`/`-HUP` — is a manual check that was not made; the double-click
pause on Windows is `not seen` and Windows is cross-built and test-compiled only. No live run,
no real profile, store or resolver.
