---
kind: task
status: done
detailed: true
---

# The menu and the command reuse

## Task

The interactive entry of `architecture.md` §10a: `dnstt-cli` without arguments in a terminal
opens the menu; without a terminal it prints the command list, as it does now; `menu` opens it
explicitly and `menu -connect <profile>` connects at once. The menu has no logic of its own:
each item calls the same function as the command behind it.

What the start has today: `run` prints the command list for no arguments
(`internal/cli/root.go:114-120`), the command list is `commands()` (`:70-78`), and the hidden
secret-service helper is dispatched before parsing (`:99-111`). `connect` already owns the
status line (`internal/cli/connect.go:250-256`, `:373-469`), the Ctrl+C ladder
(`:223-248`) and the root refusal (`:149-150`); its body has to become callable for one
connection without re-parsing arguments, because the menu supplies a profile name and no
flags. The wizard was built for this caller: it takes a `prompter`, the offered modes, the
`tun` refusal and the save function (`internal/cli/wizard.go:22-33`), and `profile add`
already computes those (`internal/cli/profile.go:243-282`). `internal/lines` keeps the one
reader of the process.

The shape, so that no second implementation appears: one `runConnection` body that both
`connect` and the menu's connect item call; one ordered item table whose position is the
number, so the editing items of child 2 drop in without touching the dispatch; one
`menuSession` holding the signals of a whole menu run; one `linePrompter` shared by the loop,
the wizard and the link question; the command bodies themselves (`runResolversCheck`,
`runProfileRm`, `runProfileList`, `importOne`, `wizard.add`) reached through fields of the
`menu` value, the way the wizard holds its `save`.

Out of scope: the edit items (child 2), the mid-session resolver offer (child 3), elevation
(child 5), and the link parser itself (the import stage), which this task only calls.

## Steps

1. **Split `connect` into a request and a body** — `internal/cli/connect.go`.

   Add `connectRequest{profileName, datapath string; verbose, noKillSwitch, allowLeak bool}`
   and `runConnection(ctx context.Context, e *env, req connectRequest, abort <-chan struct{}) error`.
   It takes today's body: the profile load and lookup (`:60-76`), the datapath choice
   (`:78-87`), `logf`/`notice` reading `req.verbose` (`:89-98`), the guard, the tunnel, the
   datapath, the preparation, netguard and protection (`:100-221`), the status line
   (`:250-256`) and the run with its wait (`:260-281`). The `<-ctx.Done()` branch selects on
   the `abort` parameter instead of its own `abortCh` (`:268-280`); `finishConnect`
   (`:333-349`) is unchanged and takes it as now.

   `runConnect` keeps the flag set and its validation (`:36-47`), the argument check
   (`:51-58`), the ladder (`:223-248`) and calls `runConnection` with the request and its
   `abortCh`. The ladder keeps the third press's `os.Exit(1)`; the menu has its own ladder in
   step 2.

   Add `writeStopNotice(w io.Writer)` — the one wording of `"\n<name>: stopping...\n"` — and
   call it from the ladder and from the menu session.

   Nothing else moves: no flag, message, drain or exit code changes. `connect_test.go`,
   `connect_prepare_test.go`, `connect_linux_test.go`, `connect_transitions_test.go` and
   `profile_test.go:720` are the guard and must pass without edits.

2. **The menu's signal state** — new `internal/cli/menu_signals.go`.

   `menuSession` holds what the §10a signals do to one menu run. Its fields under a mutex:
   the menu's `context.CancelFunc`, the running item's `cancel` and `abort chan struct{}`, and
   the press count of that item. One goroutine, `watch(sig <-chan os.Signal, done <-chan struct{})`,
   consumes a channel the caller creates: `runMenu` wires `signal.Notify(sigCh, os.Interrupt,
   syscall.SIGTERM, syscall.SIGHUP)` and stops it on return; tests inject their own channel.

   `running(cancel)` — called by the loop before an item — returns the item's abort channel and
   resets the press count; `idle()` clears the running item. The rules:

   - `os.Interrupt` with an item running: the first press prints `writeStopNotice` and cancels
     the item, the second closes the abort, the third does nothing;
   - `os.Interrupt` while the menu waits for a choice: cancel the menu;
   - `SIGTERM` and `SIGHUP`: cancel the running item and the menu.

   Nothing here calls `os.Exit`; the exit on the third signal stays in `connect`'s own ladder.

3. **The `menu` command, the start decision and the loop** — new `internal/cli/menu.go`,
   `internal/cli/root.go`.

   `menuCommand()`: `name: "menu"`, `args: "[-connect profile]"`,
   `summary: "interactive menu"`, and a description paragraph (no arguments in a terminal
   opens the menu, without a terminal the command list, `-connect` connects at once). Register
   it in `commands()` (`root.go:70-78`) right after `connect`: the list keeps its layout and
   gains one line, and `help menu` works through `lookup` (`:80-87`) with no extra code.

   `runMenu(ctx, e, args)` parses a `-connect` string flag. With it, the name is required
   (`usagef` on an empty value) and the run is one connection through the same session and the
   same body, returning its error; without it, the loop runs. In both cases `runMenu` creates
   the prompter, the store and the session and defers their teardown.

   `run` (`root.go:114-120`): with no arguments, a terminal on both sides opens the menu,
   otherwise `printUsage` as now. The terminal test goes through two package variables,
   `stdinIsTerminal` and `stdoutIsTerminal`, defaulting to `isTerminalReader`/`isTerminal`
   (`connect.go:351-367`) — the way `newSecretStore`/`saveProfiles` (`profile.go:181-184`) are
   indirected for tests. A test cannot fake a `*os.File` terminal, so the indirection is the
   only way to pin the decision.

   The loop: `ctx, cancel := context.WithCancel(ctx)`; each iteration loads the profiles and
   prints the header (the current profile and its datapath) and the numbered items; then
   `ask.Ask(ctx, "Choice [1]: ")`. An empty answer takes the first item, `0` returns nil, an
   unknown answer prints the range and asks again, `io.EOF` and `context.Canceled` return nil
   — the menu closes without writing. For an item the loop derives
   `itemCtx, itemCancel := context.WithCancel(ctx)`, calls `sess.running(itemCancel)` for the
   abort channel, runs the item, then `itemCancel()` and `sess.idle()`; the item's error goes to
   `reportItemError` (step 4), never out of the loop.

   The prompter is one `newLinePrompter(e.stdin, e.stdout)` (`prompt.go:32-34`) for the whole
   run, so the loop, the wizard and the link question share it and `internal/lines` stays the
   one reader (`lines.go:1-14`).

   `currentProfile(profiles)` returns `DefaultProfile`, or the only profile when no default is
   set — the state `profile rm` leaves (`profile.go:221-229`).

4. **The items over the command bodies** — `internal/cli/menu.go`.

   `menuItem{label string; run func(ctx context.Context, e *env, abort <-chan struct{}) error}`
   and `menuItems()` returning them in §10a order; the number is the item's 1-based position in
   the table and `0` is exit, so child 2 inserts its three items after "check resolvers"
   without touching the dispatch. Only the connect item uses `abort`.

   The `menu` value holds the seams, each defaulting to the command's own function, as the
   wizard holds `save` (`wizard.go:23-33`): `connect` = `runConnection`, `check` =
   `runResolversCheck` (`resolvers.go:57`), `remove` = `runProfileRm` (`profile.go:186`),
   `list` = `runProfileList` (`profile.go:76`), `save` = `saveProfiles`, `store` =
   `newSecretStore` (`profile.go:181-184`).

   The items of this task:

   - **connect**: load the profiles, take `currentProfile`, call
     `m.connect(ctx, e, connectRequest{profileName: name}, abort)` — the profile's datapath,
     the profile's kill-switch, no verbose. A `tun` profile without root returns the body's
     existing refusal (`connect.go:149-152`), which the menu prints and survives (elevation is
     child 5). The connect-time resolver offer of child 2 goes into this item's run, before the
     body, where the prompter is already at hand.
   - **check resolvers**: `m.check(ctx, e, []string{name})`. The `exitCodeError` it returns
     when nothing passed has already printed its line, so the menu does not report it again.
   - **another profile**: `m.list(ctx, e, nil)`, then ask `"Profile: "`; an empty answer
     cancels, an unknown name prints and re-asks, a known one sets `DefaultProfile` and calls
     `m.save`. A failed save is printed and the next iteration reloads the file, so the old
     default stands.
   - **delete profile**: ask `Delete profile %q and its exit password? (y/N): ` first; only
     `y`/`yes` calls `m.remove(ctx, e, []string{name})`, so the secret goes first and the last
     profile removes `profiles.toml` (`profile.go:206-241`). The next iteration reloads and,
     with no profile left, runs step 5's flow.

   `reportItemError(e, err)` prints nothing for nil, for `context.Canceled` (the user stopped
   it; the ladder said so) and for `exitCodeError` (already reported); everything else goes to
   `e.stderr` as `<name>: <err>`.

5. **The first profile: a link or the questions** — `internal/cli/menu.go`,
   `internal/cli/profile_import.go`, `internal/cli/profile.go`.

   `profile_import.go`: move the body of `importInteractive`'s loop (`:42-89`) into
   `importOne(ctx, e, p prompter, profiles *config.Profiles, store config.SecretStore, question string) (imported bool, err error)`:
   ask the question by hidden input, re-ask after a bad paste, ask the name when the link has
   none or it is taken (`:72-82`), `importLink` (`:135-172`), `reportImportedProfile`
   (`:176-181`); an empty answer returns `false, nil`. `importInteractive` keeps its signature
   and its two questions and calls it in a loop; its direct tests
   (`profile_import_test.go:428`, `:468`, `:500`) pass unchanged.

   `profile.go`: extract `newWizard(out io.Writer, ask prompter, store config.SecretStore) *wizard`
   computing `modes`/`tunUnsupported` from `(&datapath.Tun{}).Requires()` (`:266-273`), and
   make `runProfileAdd` use it. The defaults stay the wizard's: `home`
   (`wizard.go:126-146`), `network` (`:286-320`), `tun` first where supported (`:323-351`).

   The menu, when no profile is loaded: `importOne` with
   `"slipnet:// link (Enter to answer the questions): "`. Imported — the loop continues with
   the new profile. Empty — `newWizard(e.stdout, ask, store).add(ctx, profiles, "")` and the
   same line `profile add` prints (`:288`). `errCancelled` — the menu closes, nothing written.
   Any other error is printed and the loop asks again. Hidden input is the existing
   `linePrompter.AskSecret` (`prompt.go:48-65`): the terminal's echo is off, a pipe reads a
   line; no new code for it.

6. **The double-click pause** — new `internal/cli/menu_windows.go`,
   `internal/cli/menu_other.go`.

   §10a: a window opened by a double click does not close on an error, Enter does. On Windows,
   after the session returns an error, `runMenu` waits for Enter when the console belongs to
   one process — `GetConsoleProcessList` through `syscall.NewLazyDLL("kernel32.dll")`, the
   standard library, because `golang.org/x/sys/windows` does not export it. The pause asks
   through the menu's prompter, never a second reader of stdin. `menu_other.go` has the same
   function doing nothing, so the menu has no build tags.

7. **Tests** — new `internal/cli/menu_test.go`, plus `root_test.go` where the command list is
   checked.

   Harness: a `menu` with `scriptedPrompter` (`wizard_test.go:31-58`), `fakeSecretStore`
   (`:68-117`), a temporary configuration directory (`isolatedConnectConfig`,
   `connect_test.go:374-390`), fake command bodies recording their arguments, an injected
   signal channel and buffers for stdout and stderr.

   - the start decision: both terminal variables true → the menu; either false → `printUsage`
     as `TestNoArgumentsPrintsCommands` sees it now; `menu` in the list and `help menu` in
     `root_test.go`;
   - `menu -connect`: the fake body gets exactly that name and a zero request; an empty value
     and an extra argument are usage errors; an unknown profile is the ordinary error;
   - the loop: the header names the profile; a table-driven pass over `menuItems()` feeds each
     number and asserts its body ran; an empty answer takes the first item; `0` exits 0; an
     unknown answer re-asks; EOF exits 0 and writes nothing; `menu` with a redirected
     `printf '0\n'` closes cleanly;
   - the items: "another profile" lists, switches and saves, re-asks on an unknown name, and a
     failed save leaves the default; "delete profile" asks first and does not call `remove` on
     `n`; "check resolvers" passes the current name and its `exitCodeError` does not end the
     menu; a failed item is printed and the loop continues;
   - the no-profile flow: an empty link answer runs the wizard and the profile appears (store
     and save faked); a `dnstt` link built like `profile_import_test.go:22-70` imports one; a
     bad paste re-asks; EOF at the link question closes the menu with nothing written;
   - the signals: a fake connect blocks on `ctx.Done()` and reports `<-abort`; one
     `os.Interrupt` cancels it and the loop asks again; two close the abort; `os.Interrupt`
     while the menu waits for a choice returns nil; `SIGTERM` and `SIGHUP` cancel the running
     connect and end the loop; a third press does not kill the test process.

   No test opens a terminal, reads a real key or makes a network call.

8. **The local matrix** — on the final tree:

   - `gofmt` outside `core/` (it is never run through it, `doc/architecture.md` §02);
   - `go build ./...` and `CGO_ENABLED=0 go build ./...`;
   - the CI vet split, on Linux and with `GOOS=windows`:
     `go vet $(go list ./... | grep -v /core/)` and
     `go vet -testinggoroutine=false ./core/...`;
   - `go test -race -count=1 ./...` with `DNSTT_CLI_TEST_DBUS=1`, `DNSTT_CLI_TEST_NETNS=0`
     and
     `-skip '^(TestDualStackListener|TestLoopbackIndexBlocks|TestPhysicalIndexPasses|TestFamilyWithoutRouteRefuses)$'`
     — the four external `netguard` live tests, so no live call is made;
   - the Windows cross-build `GOOS=windows GOARCH=amd64 go build ./...` and
     `GOOS=windows go test -c` for every package with tests (19 at the import stage), which
     compiles `menu_windows.go` and the new tests;
   - `git diff --check`.

Easy to get wrong:

- the second Ctrl+C must not be taken for "the menu is idle": the running item is registered
  before its body starts and cleared after it returns, or a press between the status line's
  stop and the next prompt closes the menu instead of the connection;
- the menu must print nothing while a connection runs: the status line is one line at the
  bottom, and a line from the loop would scroll it away and leave `\r\033[K` on the wrong row;
- `abort` is per item: one channel reused across connections would abort the second one at
  once;
- the abort path closes the datapath and then waits for `errCh` (`connect.go:275-279`); taking
  it out of the body's defer chain leaves the SOCKS listener or the TUN adapter open;
- one `linePrompter` for the loop, the wizard and the link question: a second reader takes the
  next question's line (`lines.go:4-13`), and `AskSecret` bypasses the reader on purpose;
- `runConnection` must not call `signal.Notify`: two ladders would race, and `connect`'s
  `os.Exit` on the third press would kill the menu;
- the third Ctrl+C in the menu must not call `os.Exit`; if the abort path can still block after
  it, `signal.Reset` and re-sending the signal give the default action without leaving a stuck
  menu, which the rule allows;
- the loop must reload the profiles at the start of each iteration: `profile rm`, "another
  profile" and the wizard all change the file behind it.

## Done when

- No arguments: stdin and stdout a terminal → the menu; otherwise the command list with its
  layout unchanged; `menu` appears in the list and in `help menu`.
- `menu -connect <profile>` runs one connection and returns without the loop; the profile name
  is the only argument; an unknown profile is the ordinary `profile %q not found`; a missing
  name and an extra argument are usage errors.
- One connection goes through `runConnection`, the function `connect` itself calls: the flags'
  behaviour, the status line, the drain, the kill-switch lines and the exit codes are the
  command's, not a copy; the `connect` tests pass unchanged.
- The loop prints the numbered items in §10a order, reads with the shared line reader, takes an
  empty answer as connect (the first item), `0` exits, an unknown answer re-asks, and the end
  of input closes the menu without writing anything. Every item calls the command's own
  function: connect, `resolvers check` (the offer to drop the dead ones comes with child 2),
  another profile (list and switch the default), delete profile (confirmation first, then the
  `profile rm` rules: the secret goes with the profile, the last profile removes
  `profiles.toml`), exit.
- With no profile the wizard runs; its first question offers a `slipnet://` link by hidden
  input or the questions; a bad paste is re-asked; the defaults are `home`, `network` and `tun`
  where supported; after it the menu continues with the new profile; the end of input at any
  question cancels and writes nothing.
- Signals: while an item runs, the first Ctrl+C stops it and returns to the menu, the second
  does not wait for open connections, a third does not call `os.Exit`; while the menu waits for
  a choice Ctrl+C closes it; SIGTERM and SIGHUP stop the running item, close the menu and
  return. No `os.Exit` anywhere in the menu.
- A redirected stdout has no status line; while connected the status line is the command's one
  line at the bottom and the menu prints nothing over it; a menu started with redirected
  streams reads until the end of input and closes cleanly with exit code 0.
- Tests use buffers, a scripted prompter, fake command bodies and an injected signal channel;
  they cover the start decision, `-connect`, each item's dispatch, EOF, the wizard and link
  paths and the signals. No test opens a terminal or touches the network.
- The local matrix of step 8 passes. The real terminal — the keys, the hidden input, the status
  line, `kill -TERM`/`-HUP` — is a manual check recorded in the Outcome, and so is the
  double-click pause on Windows; Windows is cross-built and test-compiled only, `not seen`.
- No live run, no real profile, store or resolver, no network change, no new dependency;
  `core/`, `doc/architecture.md` and `README.md` are untouched.

## Outcome

Done: `dnstt-cli` with no arguments in a terminal opens the menu, without a terminal it prints
the command list as before, `menu` opens the menu explicitly and `menu -connect <profile>` runs
one connection and returns. The menu has no logic of its own: every item calls the function of
the command behind it.

- One body of a connection (`internal/cli/connect.go`): `connectRequest{profileName, datapath;
  verbose, noKillSwitch, allowLeak}` and `runConnection(ctx, e, req, abort)` hold what was
  `runConnect`'s body — the profile load and lookup, the datapath choice, the guard, the tunnel,
  the datapath, the preparation, netguard, the protection, the status line and the run with its
  wait, with the abort path still closing the datapath and then waiting for `errCh`.
  `runConnect` keeps the flag set, its validation, the argument check and the Ctrl+C ladder and
  calls the body; `writeStopNotice` is the one wording of the stop line. Flags, messages, drain
  and exit codes are the command's, and `connect_test.go`, `connect_prepare_test.go`,
  `connect_linux_test.go`, `connect_transitions_test.go` and `profile_test.go` pass unchanged.
- The start (`internal/cli/root.go`): with no arguments the decision goes through the
  `stdinIsTerminal`/`stdoutIsTerminal` package variables, defaulting to `isTerminalReader` and
  `isTerminal`, so a test can pin it without a `*os.File` terminal; both a terminal — the menu,
  otherwise `printUsage` as before. `menu` sits in `commands()` right after `connect`, so the
  list keeps its layout and gains one line, and `help menu` works through `lookup`.
- The loop (`internal/cli/menu.go`, new): the profiles are reloaded at the start of every
  iteration, the header names the current profile and its datapath, the items are numbered from
  the table's order with `0` as exit, an empty answer is the first item, an unknown answer
  prints the range and asks again, and `io.EOF`/`context.Canceled` close the menu without
  writing. The table is a method on `*menu` (`menuItems()`), because the bodies need the seams
  and the single prompter; the number is the 1-based position, so the three editing items of
  child 2 drop in after "check resolvers" without touching the dispatch. The item's error goes
  to `reportItemError`, which is silent for nil, `context.Canceled` and `exitCodeError`; an
  `errCancelled` from an item closes the menu. One `linePrompter` serves the loop, the wizard and
  the link question, so `internal/lines` stays the one reader.
- The items: connect loads the profiles and calls the body with the current profile's name and
  the profile's own datapath and kill-switch, so a `tun` refusal is the body's existing message
  and the menu survives it; check resolvers passes the current name and lets its `exitCodeError`
  go unreported; another profile lists the profiles, re-asks an unknown name and saves the
  choice as the default; delete profile asks `(y/N)` first and only then calls `profile rm`, so
  the secret goes first and the last profile removes `profiles.toml`. `currentProfile` is the
  default, or the only profile when no default is set.
- The first profile: `importOne` is extracted from `importInteractive` (same signature, same two
  questions, same end-of-input rules), and the menu asks the hidden link question first, the
  wizard's questions on an empty answer, reporting the same line as `profile add`;
  `newWizard(out, ask, store)` is extracted from `runProfileAdd`, so the defaults stay the
  wizard's (`home`, `network`, `tun` where supported).
- Signals (`internal/cli/menu_signals.go`, new): `menuSession` holds the menu's cancel, the
  running item's cancel and abort channel and that item's press count under a mutex; `running`
  registers an item before its body starts and makes a fresh abort channel, `idle` clears it, so
  a press between the status line's stop and the next prompt still belongs to the connection. The
  first Ctrl+C prints the stop line and cancels the item, the second closes the abort, the third
  does nothing; Ctrl+C while the menu waits for a choice closes the menu; SIGTERM and SIGHUP stop
  the item and close the menu. `runWith` takes the signal channel, so tests inject their own and
  `runMenu` wires `signal.Notify` and stops it on return. No `os.Exit` in the menu, and
  `runConnection` installs no `signal.Notify`.
- The Windows pause (`menu_windows.go`, `menu_other.go`, new): after the session returns an
  error, `runMenu` waits for Enter when the console belongs to one process
  (`GetConsoleProcessList` through `syscall.NewLazyDLL`), asking through the menu's prompter;
  elsewhere the same function does nothing, so the menu has no build tags.
- Tests (`internal/cli/menu_test.go`, new, 27 test functions; `root_test.go`): buffers, a
  scripted prompter, fake command bodies, a fake secret store, an injected signal channel and an
  isolated configuration directory. They cover the start decision, `-connect` and its usage
  errors, the dispatch of every item, the empty answer, `0`, an unknown answer, EOF, the
  wizard and link paths, a failed item, the signal rules including the third press, and the
  "profiles exist, no default" state. No test opens a terminal, reads a real key or touches the
  network.
- Verified: the full local matrix passed — `gofmt` outside `core/`, `go build ./...`,
  `CGO_ENABLED=0 go build ./...`, the vet split on Linux and on `GOOS=windows`, the full
  `go test -race -count=1 ./...` with `DNSTT_CLI_TEST_DBUS=1`, `DNSTT_CLI_TEST_NETNS=0` and the
  four external `netguard` live tests excluded by `-skip` (no live call), the Windows
  cross-build with `go test -c` of all 19 packages that have tests, and `git diff --check`.
  No new dependency.

Recorded limitations:

- The Ctrl+C ladder is installed before the body now, so a Ctrl+C or SIGTERM that arrives during
  the setup (profile load, netguard, protection) is caught: `connect` prints the stop line and
  ends cleanly instead of dying by the default action. This follows from step 1 — the ladder
  cannot live in `runConnection`, because the menu must not get `connect`'s `os.Exit` — and the
  first Ctrl+C is the command's clean stop everywhere else; the window is the setup only.
- The Windows pause stands in `runMenu` before the error reaches `report`, so a window opened by
  a double click shows the Enter prompt before the reason of the error. Moving it after the
  report means moving the pause into `mainWith`, outside this task; `not seen` anyway, Windows
  is cross-built and test-compiled only.
- A "profiles exist but no default" set (after deleting the default of three or more profiles)
  has no menu header to show: the loop asks for the profile first, and an empty answer closes
  the menu. There is no `0` at that question, because the menu cannot run without a profile.
- One run of the full suite failed in the vendored `core/client`
  `TestDefaultUTLSFingerprints` (a loopback HTTP/2 broken pipe) and passed on the rerun; the
  test and the package are untouched by this task.

Not seen, and not claimed: the real terminal — the keys, the hidden input, the status line,
`kill -TERM`/`-HUP` — is a manual check that was not made, and the double-click pause on
Windows is `not seen`.
