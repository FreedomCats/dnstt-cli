---
kind: task
status: done
detailed: true
depends: [01-menu-and-commands, 02-profile-editing]
---

# Resolvers run out mid-session

## Task

The §10a promise ([`architecture.md`](../../../../../architecture.md), §10a, "When resolvers
run out mid-session"): when the resolvers run out mid-session — none completes a handshake
for a full round — the menu offers to check and edit them and connects again.

The supervisor already detects exactly that case, but reports it only to its log and the
backoff: after a session has succeeded at least once (`heard`), a round of failed dials ends
with `cause := "no resolver of %d completed a handshake"` and a pause
(`internal/supervisor/tunnel.go:482-495`); the first connect instead stops with
`NeverConnectedError` (`:487-488`), which `connect` reports with its own four-way message and
the menu does not need to offer anything. The pool puts failed resolvers aside for a minute
and grows that time (`internal/resolvers/pool.go:16-23`, `:88-121`), so a full round means the
tunnel is down for a while, not a one-query blip.

The change is one narrow seam, not a state-machine rewrite: the supervisor gets an optional
callback (nil in the command path, so `connect` is unchanged) fired when the `heard` round
above fails, with the failed resolvers and their errors. The menu installs it for a
connection, offers once for that connection, and on a yes runs the child 2 editor and
reconnects with the saved list; on a no the menu returns, the connection having been stopped
before the question.

The question comes after the connection is stopped, not while it runs. The status line owns
the bottom row of the terminal and redraws it every second (§08, §10a "Status line"), so a
prompt printed under it would be wiped by the next redraw; the menu prints nothing while a
connection runs (child 1's rule, `internal/cli/connect.go:283-289`). The event is therefore
recorded by the callback and handled by the item after `runConnection` returned and the
status line cleared its row. The tunnel is down at that point: a yes brings it back at once
with the edited list, a no leaves it down and the person connects again from the menu, where
the first failed round is reported as `NeverConnectedError`, exactly as `connect` reports it.

Out of scope: the supervisor's retry and backoff policy, the resolvers' side-aside times,
`connect`'s output on the first connect, and any new line, status or exit code in `connect`.

## Steps

1. **The supervisor's seam** — `internal/supervisor/tunnel.go`.

   `Options` gains a field next to `OnTransition` (`:88-96`):

       // OnRoundFailed receives a full round of failed handshakes after the tunnel has been
       // up at least once: every resolver of the list, and every server of the network entry,
       // failed since the last session. It is called from Run's goroutine, once per round and
       // before the backoff delay, like OnTransition, and must not block. failures is a copy
       // the receiver owns. nil — silent. The first round of a connect does not report here:
       // it ends Run with NeverConnectedError.
       OnRoundFailed func(failures []ResolverFailure)

   In `Run`, the failed-dial branch (`:473-495`): after the `len(round) < t.roundSize()`
   check and the `!heard` return, take the round as the event, clear it and call the
   callback before the pause:

       failures := append([]ResolverFailure(nil), round...)
       round = nil
       cause := fmt.Errorf("no resolver of %d completed a handshake", pool.Len())
       if t.opts.OnRoundFailed != nil {
           t.opts.OnRoundFailed(failures)
       }
       if t.pause(ctx, name, cause, t.backoff.next()) {
           prev = -1
       }
       continue

   Nothing else in `Run` moves: the per-failure `t.logf("%s: %v", name, err)` (`:483`), the
   `no resolver of %d completed a handshake` cause of the pause (`:491-495`), the transitions
   and `roundSize` (`:646-655`) stay as they are. `Options` is read on Run's goroutine and
   never written after `New`, so no lock is needed and there is no setter: unlike
   `SetDatapath`/`SetDrain`/`SetKillSwitch` (`:221-263`), this is a callback, and the
   build path is one call.

   The round, which the doc comment states in one line and the tests pin:

   - one `ResolverFailure` per failed dial since the last session or restart (`:482`), with
     the name `dialOnce` gave — `network (<server>)` for the network entry (`:588-599`);
   - full at `roundSize()`: one per resolver, and for the `network` entry one per server the
     network gave at the last read (`:627-642`);
   - an event only when a handshake already completed in this `Run` (`heard`, `:457`).

   What is not the event: a single failed dial (it only appends to the round and logs,
   `:482-483`); a session that died by itself (it resets the round and pauses, `:501`,
   `:519`, `:533`); `StateDegraded`, which is a live session that heard nothing for
   `DegradedAfter` and fails no dial (`:723-733`); and the first connect, which returns
   `NeverConnectedError` before the callback (`:487-489`).

2. **Pass it through the build** — `internal/app/tunnel.go`.

   `TunnelOptions` (`:27-42`) gains `OnRoundFailed func([]supervisor.ResolverFailure)`,
   documented as "a full round of failed handshakes after a session has been up; nil —
   silent. See supervisor.Options", and `buildTunnel` passes it into `supervisor.New`
   (`:149-158`). Nothing else changes: the tests that build a tunnel keep compiling with the
   zero value (`internal/app/tunnel_test.go:49`, `internal/app/datapath_test.go:53`).

3. **The request carries it** — `internal/cli/connect.go`.

   `connectRequest` (`:104-110`) gains `onRoundFailed func([]supervisor.ResolverFailure)`,
   and `runConnection` passes `OnRoundFailed: req.onRoundFailed` in `app.TunnelOptions`
   (`:174-181`). `runConnect` (`:87-93`) leaves the field zero, so the supervisor's callback
   is nil on the command path: `connect`'s output, status line, transitions, drain and exit
   codes are exactly as before. The only line of this event that exists today,
   `no resolver of %d completed a handshake`, stays where it is — the `-v` log and the
   `reconnecting` state — and is not repeated.

4. **The item's loop** — new `internal/cli/menu_round.go`; `internal/cli/menu.go`.

   `roundOfferQuestion = "Check and edit resolvers? (y/N): "` and

       // connectWithResolvers connects and, when a full round of handshakes fails after the
       // tunnel has been up, stops the connection, offers to check and edit the resolvers and
       // connects again with the saved list.
       func (m *menu) connectWithResolvers(ctx context.Context, e *env, name string, abort <-chan struct{}) error {
           for {
               rounds := make(chan []supervisor.ResolverFailure, 1)
               connCtx, connCancel := context.WithCancel(ctx)
               done := make(chan error, 1)
               go func() {
                   done <- m.connect(connCtx, e, connectRequest{
                       profileName: name,
                       onRoundFailed: func(failures []supervisor.ResolverFailure) {
                           select {
                           case rounds <- failures:
                           default:
                           }
                       },
                   }, abort)
               }()

               select {
               case err := <-done:
                   connCancel()
                   return err
               case failures := <-rounds:
                   // The question must not be printed over the status line: the connection is
                   // stopped first, and the status line clears its row as it goes.
                   connCancel()
                   err := <-done
                   if ctx.Err() != nil {
                       return err // a stop by the person: the stop line has answered for it
                   }
                   yes, err := m.offerRoundFailed(ctx, e, failures)
                   if err != nil || !yes {
                       return err
                   }
                   if err := m.checkResolversItem(ctx, e, nil); err != nil {
                       var ec *exitCodeError
                       if !errors.As(err, &ec) {
                           return err
                       }
                   }
                   if err := m.editResolvers(ctx, e, name); err != nil {
                       return err
                   }
               }
           }
       }

   Why this shape, and not a goroutine that asks while the connection retries:

   - the callback runs on the supervisor's Run goroutine, so it only records: a buffered
     channel of one and a non-blocking send, the pattern of `notifyTransitions`
     (`connect.go:412-422`). A second round of the same connection cannot ask again, because
     the first event cancels the connection;
   - the item writes nothing while the connection runs: `runConnection` owns the terminal
     (notices, status line) until `done`, and the question is printed after
     `statusLine.stop` has cleared the row (`connect.go:441-457`, `:497-500`);
   - `connCancel` cancels the connection, not the item: the session keeps its item and its
     abort channel (`menu_signals.go`), so Ctrl+C and SIGTERM behave as for any other item,
     and a press during the question cancels the item and closes the menu;
   - the loop cannot spin without an answer from the person: each iteration either returns or
     follows a yes with a fresh connection, and a fresh connection has `heard == false`, so
     its first failed round ends with `NeverConnectedError` and not with an event. The event
     can fire again only after a session has come up and a full round has failed after it —
     a new failure, worth a new question, and a person who answers it.

   `offerRoundFailed(ctx, e, failures)` prints the heading and one line per failure, then
   asks:

       No resolver completed a handshake in a full round:
         network (192.0.2.53): no answer from the server within 10s
         udp:192.0.2.54:53: dial failed
       Check and edit resolvers? (y/N):

   Two spaces before a name, as `printResolvers` (`internal/cli/menu_edit.go:33-38`) and
   `NeverConnectedError` (`internal/supervisor/tunnel.go:123-131`) do; `isYes` decides the
   answer, and the end of input goes through `readError` (`internal/cli/wizard.go:420-431`)
   so it becomes `errCancelled` and closes the menu. The names come from the event —
   `ResolverFailure.Resolver`, `network (<server>)` for the network entry, the same shape the
   check prints through `resolverOutcome.name` (`internal/cli/resolvers.go:127-133`) — so the
   offer names the resolvers without running a second check.

   `connectItem` (`internal/cli/menu.go:319-330`) calls `m.connectWithResolvers` after
   `offerResolvers` instead of `m.connect`, and its comment says so. `connectOnce`
   (`:143-152`, `menu -connect`) keeps `m.connect` with a zero request: `menu -connect` stays
   question-free, as child 2 left it.

5. **The check and the editor, reused** — `internal/cli/menu_edit.go`, no new code there.

   - `m.checkResolversItem(ctx, e, nil)` (`:110-156`) is called as it is: the same body as
     `resolvers check`, the offer to drop the failed entries, the save, and the printed list.
     A check where nothing passed returns `&exitCodeError{code: exitError}` after printing
     `No resolver passed the check.`; here that is not the end — the person asked to check
     *and* edit — so an `exitCodeError` means "already reported, go on to the editor", and
     every other error ends the item.
   - `m.editResolvers(ctx, e, name)` (`:75-96`) is called as it is: the current list, the
     wizard's questions, `resolvers.Expand` on every entry, the save after the whole list is
     valid, and the saved list printed back. The reconnect uses it because `runConnection`
     loads the profile from the file.
   - Nothing is written when the person declines or the check fails before the editor; the
     profile file changes only through the two bodies above.

6. **Tests** — new `internal/supervisor/roundfailed_test.go`, new
   `internal/cli/menu_round_test.go`, `internal/cli/menu_test.go` for the harness.

   Supervisor, over `harness` (`internal/supervisor/fake_test.go:122-296`); the event is
   recorded in the `mutate func(*Options)` hook of `newHarness` (`:148-170`), under the
   harness mutex, and read with `h.wait`:

   - `TestRoundFailedFiresOncePerRound`: two resolvers, `h.dial` returns nil for the first
     dial and `errDial` for every later one; the first session is made to die by itself
     (`h.session(0).die`). Wait for one event: it carries exactly the two failures, in dial
     order, with their errors, and nothing came before the session died. With the harness's
     instant sleep the run goes on to the next round: a second event with two failures — one
     per round, not one per dial — and a `reconnecting` transition with the
     `no resolver of %d completed a handshake` cause between them.
   - `TestRoundFailedNotOnFirstConnect`: every dial fails from the start; `h.stop()` returns
     `*NeverConnectedError` and no event fired.
   - `TestRoundFailedStopIsClean`: the callback calls `h.cancel` (set by `h.run` before the
     goroutine starts); `h.stop()` returns nil — the menu's stop of the connection ends the
     supervisor cleanly, without a `NeverConnectedError` or a pause.
   - The nil-callback path is the command's and stays covered by the existing
     `tunnel_test.go`, which never sets the field.

   CLI, over `menuHarness` (`internal/cli/menu_test.go:88-171`), `scriptedPrompter`
   (`wizard_test.go:20-65`), `fakeSecretStore` (`:67-117`) and `isolatedConnectConfig`
   (`connect_test.go:374-390`):

   - the harness's `connect` fake (`menu_test.go:128-131`) gains `roundEvents
     [][]supervisor.ResolverFailure`, one entry per connection that reports a round, and
     `blockConnections bool`. The fake records the request as now; when the call has an entry
     it calls `req.onRoundFailed(entry)` and then waits for `ctx.Done()` and returns nil, and
     with `blockConnections` it only waits. A connection that reports a round is the one the
     item stops, and blocking is what makes the event, not the return, the first thing the
     item sees. A call with neither returns `h.connectErr` as now, so every existing menu test
     keeps its behaviour and its answers;
   - `TestMenuRoundFailedOffersAndReconnects`: answers `1`, `""` (connect with the saved
     resolvers), `y` (the round's question), `y` (the check's drop offer),
     `udp:192.0.2.54:53`, `""` (the editor), `0`; `h.checkOutcomes` has one passing entry and
     one failed one, so the check has something to keep and asks its question, and
     `h.menu.save = saveProfiles`, as the editor's tests do, so both writes reach the file.
     Assert the heading and the failure lines, the question asked once, the check called with
     `home`, two connections both of `home` with a non-nil `onRoundFailed`, the second one
     returning at once, the editor's entry in `readConfigFile` and the old one gone, and the
     loop asking for a choice again;
   - `TestMenuRoundFailedDeclined`: `n` — no check, no editor, no second connection, the file
     unchanged, the loop asks again;
   - `TestMenuRoundFailedAsksOncePerConnection`: the fake fires the callback twice into the
     channel of one; the question is asked once and the connection is cancelled once;
   - `TestMenuRoundFailedNothingPassedStillEdits`: `h.checkErr = &exitCodeError{code:
     exitError}` — the editor still opens, nothing is reported twice, the reconnect happens;
   - `TestMenuRoundFailedEOFClosesTheMenu`: the script ends at the question — the loop
     returns nil, the file is unchanged;
   - `TestMenuRoundFailedInterruptDoesNotOffer`: `h.start` with `blockConnections` and the
     answers `1`, `""`; send `os.Interrupt` on the injected channel once the connection runs,
     then `r.wait` — the question was never asked, the loop asked for a choice again and the
     end of the script closed it;
   - `TestMenuConnectFlagHasNoRoundOffer`: `h.runConnect(ctx, "home")` with a fake that
     reports a round — `h.connected[0].onRoundFailed == nil` and nothing was asked.

   No test dials, opens a terminal or touches a real store or resolver.

7. **The local matrix** — on the final tree, the same as children 1 and 2:

   - `gofmt -l $(git ls-files '*.go' | grep -v '^core/')` prints nothing (`core/` is never
     run through it, `doc/architecture.md` §02);
   - `go build ./...` and `CGO_ENABLED=0 go build ./...`;
   - the CI vet split (`.github/workflows/linux.yml:47-50`) on Linux and with `GOOS=windows`,
     the package list taken under the same `GOOS`:
     `go vet $(go list ./... | grep -v /core/)` and
     `go vet -testinggoroutine=false ./core/...`;
     `GOOS=windows go vet $(GOOS=windows go list ./... | grep -v /core/)` and
     `GOOS=windows go vet -testinggoroutine=false ./core/...`;
   - `go test -race -count=1 ./...` with `DNSTT_CLI_TEST_DBUS=1`, `DNSTT_CLI_TEST_NETNS=0`
     and
     `-skip '^(TestDualStackListener|TestLoopbackIndexBlocks|TestPhysicalIndexPasses|TestFamilyWithoutRouteRefuses)$'`
     — the four external `netguard` live tests (`internal/netguard/live_test.go:104`, `:129`,
     `:137`, `internal/netguard/netguard_linux_test.go:76`), so no live call is made;
   - `GOOS=windows GOARCH=amd64 go build ./...` and `GOOS=windows go test -c` for every
     package with tests — 19 of them, from
     `GOOS=windows go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./...`
     (20 on Linux; the difference is a Linux-only test package);
   - `git diff --check`.

Easy to get wrong:

- the callback runs on the supervisor's Run goroutine: printing or asking from it stops the
  tunnel, and the status line owns the row anyway — only a non-blocking send belongs there;
- the connection must be cancelled *before* the question: the status line redraws every
  second (`connect.go:441-457`) and would print over the prompt, leaving `\r\033[K` on the
  wrong row;
- after `connCancel` the item must wait for `done` before printing anything:
  `runConnection` closes the datapath and stops the status line in its defers, and a line
  printed earlier would be cleared with the row;
- `ctx.Err()` must be checked before the question: Ctrl+C arrives as a cancelled item
  context (`menu_signals.go:84-100`) and must not be answered with a prompt;
- the send into the event channel must be non-blocking, or a round whose event nobody reads
  would hold the supervisor inside the callback for good;
- the callback is installed per connection, not once per menu: one kept from a previous
  connection would report into a channel nobody reads;
- `connectOnce` and `runConnect` must leave the field zero: `menu -connect` stays
  question-free and `connect` must not change at all;
- an `exitCodeError` from the check is not a failure of the item: its line is printed, and
  the editor is exactly what the person asked for;
- the failures slice must be copied in the supervisor: the receiver keeps it after `Run`
  returned;
- the question's end of input must go through `readError`: a bare `io.EOF` out of the item
  is reported as a failure instead of closing the menu;
- the failure lines carry the names the supervisor shows (`network (<server>)`), not the
  profile entries: the event has no entries, and the check is not run again to get them.

## Done when

- The supervisor reports a full failed round after a success once per round, before the
  backoff pause, with the failed resolvers and their errors in dial order, and reports
  nothing on the first connect (which still ends with `NeverConnectedError`), nothing while
  a session is up or degraded, and nothing for a single failed dial or a dead session. What
  counts as a round is unchanged: `roundSize()` failed dials, one per resolver and one per
  network server.
- The event reaches the caller without races: the callback is called from Run's goroutine,
  the recorder is a non-blocking send, the failures are a copy the receiver owns, and nothing
  is written while the connection runs.
- `connect` is unchanged: no new line, no new status, no new exit code, the same status line
  and transitions, the same first-connect message; `menu -connect` asks nothing. The callback
  is nil on both paths, and the existing `connect`, `app` and supervisor tests pass
  unchanged.
- In the menu, a full failed round after a session shows the failed resolvers and asks
  `Check and edit resolvers? (y/N): `; the connection is stopped before the question, so
  nothing is printed over the status line, and a Ctrl+C during the connection is not answered
  with a question.
- `y` runs the same check as `resolvers check` for the current profile, with its offer to
  drop the dead entries, then the child 2 editor, then connects again with the saved list; a
  check where nothing passed still opens the editor and is not reported twice.
- `n` writes nothing and returns to the menu with the connection stopped; the end of input at
  the question closes the menu. The question is asked at most once per connection, and the
  item cannot spin without an answer, because a fresh connection ends its first failed round
  with `NeverConnectedError` rather than an event.
- Tests: the supervisor's with a pool whose every dial fails after one success (once per
  round, the payload, the clean stop, nothing on the first connect); the menu's with a fake
  connection that reports a round, `scriptedPrompter`, a temporary configuration directory
  and a fake store — the offer, the declined path, the once-only rule, the nothing-passed
  path, the end of input, the Ctrl+C path and `menu -connect` asking nothing. No terminal, no
  network, no live resolver.
- The local matrix of step 7 passes: `gofmt` outside `core/`, both builds, the vet split on
  Linux and on `GOOS=windows` with the package list taken under the same `GOOS`, the full
  `-race` suite with `DNSTT_CLI_TEST_DBUS=1`, `DNSTT_CLI_TEST_NETNS=0` and the four external
  `netguard` live tests excluded by `-skip`, the Windows cross-build with `go test -c`, and
  `git diff --check`.
- No live run, no real profile, store or resolver, no network, no new dependency; `core/`,
  `doc/architecture.md` and `README.md` are untouched; Windows is cross-built and
  test-compiled only, `not seen`.

## Outcome

Done as planned. `supervisor.Options.OnRoundFailed` fires once per full failed round after a
handshake has completed in this `Run`, before the backoff pause, with a copy of the round;
`app.TunnelOptions` and `cli.connectRequest` carry it; the menu installs it per connection in
`internal/cli/menu_round.go`. `connect` and `menu -connect` leave it nil, so their output,
status line, transitions and exit codes are unchanged and their tests pass unmodified.

What is not visible from the code:

- The item cancels the connection, waits for `runConnection` to return and only then prints.
  The status line redraws the bottom row every second, so a prompt printed while a connection
  runs would be wiped. A test pins the order: a connection that has not finished fails the
  round question.
- The bound on the loop is the supervisor's `heard`, not a counter in the item: a fresh
  connection starts with `heard == false`, ends its first failed round with
  `NeverConnectedError` and reports no event, so the offer cannot repeat without a session
  that came up and a round that failed after it.
- The offer names the resolvers as the supervisor does (`network (<server>)`, entries as the
  profile spells them), not as the profile entries: the event carries no entries, and the
  check is not run a second time just to name them.
- An `exitCodeError` from the check (`No resolver passed the check.`) is not the end of the
  item: the person asked to check *and* edit, so the editor still opens and the line is not
  printed twice.
- The end of input at the round question goes through `readError` and closes the menu; a
  Ctrl+C during the connection is not answered with a question, because the item checks its
  context before printing.

Nothing was postponed: the supervisor's retry policy, the resolvers' side-aside times and
`connect`'s first-connect message stay out of scope as stated.

Checks on the final tree: `gofmt` outside `core/`; `go build ./...` and
`CGO_ENABLED=0 go build ./...`; the CI vet split on Linux and with `GOOS=windows`, the package
list taken under the same `GOOS`; the full `go test -race -count=1 ./...` with
`DNSTT_CLI_TEST_DBUS=1`, `DNSTT_CLI_TEST_NETNS=0` and the four external `netguard` live tests
excluded by `-skip`; the Windows cross-build and `go test -c` for all 19 packages with tests;
`git diff --check`. No live run; Windows `not seen`.
