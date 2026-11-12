---
kind: task
status: done
detailed: true
---

# Fix the core track/close deadlock

## Task

A real self-deadlock in the session's socket bookkeeping, found once by the P7 matrix. The
P7 matrix saw a goroutine leak in `TestDoTSessionContext` with this stack; the source path is
real, although three repeats did not reproduce it:

- `sockets.track` (`scripts/vendorcore/_files/client/transport.go:76-90`) holds `s.mu` and,
  when the session is already closed, calls `c.Close()` while still holding it;
- `trackedConn.Close` (`:186-196`) calls `s.forget`, which takes the same `s.mu`.

If a session closes between a successful dial and its tracking and the closer is a
`*trackedConn`, the close re-enters the lock and hangs a redial or a teardown forever. The
window is narrow, but the result is a deadlock, not a slow path.

The fix is a core `_files` edit plus a regeneration, as in P2–P7: no behavior change besides
the deadlock, no new dependency, profile key or transport change. The test must be
deterministic and must fail on the old code.

## Steps

1. **Fix `sockets.track`** — `scripts/vendorcore/_files/client/transport.go`:
   - hold `s.mu` only while inspecting `closed` and inserting into `open`;
   - if the session is closed, unlock before calling `c.Close()`; return `net.ErrClosed`;
   - keep the successful path: insert and return nil;
   - leave `forget`, `closeAll` and `trackedConn.Close` as they are; `closeAll` already
     takes the map, clears it and unlocks before closing, and `forget` on a nil map after
     `closeAll` is safe. Do not add a second lock or a goroutine.
2. **Deterministic test** — `scripts/vendorcore/_files/client/transport_test.go` (new file;
   `CORE_VERSION` will count it):
   - build `s := newSockets(&Config{})`; a `net.Pipe` pair; `tc := &trackedConn{Conn: c1, s: s}`;
   - mark the session closed under `s.mu` (or use a small helper), then call `s.track(tc)` in
     a goroutine and wait with a bounded timeout (about 2 s); before the fix this blocks in
     `trackedConn.Close` → `forget`, after it returns `net.ErrClosed`;
   - assert the connection is closed (the peer reads EOF) and that `s.open` has no entry;
   - close the peer and the session; the test is in-package, loopback only, no live call.
   - If a `transport_test.go` already exists, extend it; otherwise create it. No production
     change outside `track`.
3. **Regeneration** — from the verified clone at `core/CORE_VERSION` (`0c5c52a`):
   `go run ./scripts/vendorcore -upstream ../dnstt`; then
   `go run ./scripts/vendorcore -check -upstream ../dnstt`; run the generation a second
   time and compare `core/` byte for byte. `core/CORE_VERSION` is written by the tool; do
   not hand-edit the counts.
4. **Checks** — `gofmt` outside `core/` (never inside), `go build ./...`,
   `CGO_ENABLED=0 go build ./...`, the CI vet split on Linux and Windows,
   `go test -race ./core/client`, the full `go test -race -count=1 ./...` with
   `DNSTT_CLI_TEST_DBUS=1`, `DNSTT_CLI_TEST_NETNS=0` and the exact `-skip` of the four
   external `netguard` live tests
   (`TestDualStackListener|TestLoopbackIndexBlocks|TestPhysicalIndexPasses|TestUnknownIndexRefuses`),
   the Windows cross-build with `go test -c` of every package with tests, and
   `git diff --check`. No live run, no profile, no network change.

Easy to get wrong:

- closing the connection while still holding `s.mu` is the bug; a `defer s.mu.Unlock()` keeps
  it;
- `closeAll` must not be changed to hold the lock while closing either; it already avoids
  that;
- the test must use a `*trackedConn` (a plain closer would not re-enter `forget`), and it must
  have a timeout so the old code fails rather than hangs the suite;
- `CORE_VERSION` counts change only because of the new test file; let the tool write them.

## Done when

- `sockets.track` never closes a socket while holding `s.mu`; a session already closed before
  `track` returns `net.ErrClosed` without blocking; a `*trackedConn` passed to `track` closes
  and leaves no entry in `open`; the successful path is unchanged.
- The deterministic test times out on the old code and passes on the fix; it uses loopback
  only and makes no live call; the test does not flake under `-race`.
- `core/` is generated from the recorded upstream commit with the new test file;
  `vendorcore -check` passes, a second generation is byte-identical, and `CORE_VERSION` is
  written by the tool.
- The full local matrix of step 4 passes; no new dependency, profile key, transport or user
  behavior; no values, dates or environment details in the changed files.

## Outcome

Done: `sockets.track` no longer closes a socket while holding `s.mu`, so a session that ends
between a successful dial and its tracking can no longer deadlock the redial or the
teardown.

- Core (`scripts/vendorcore/_files/client/transport.go`, generated into `core/client/`):
  `track` takes `s.mu` only to read `closed` and insert into `open`; on a closed session it
  unlocks, then closes the socket and returns `net.ErrClosed` — a `*trackedConn` closes
  through `forget`, which takes the same mutex. The successful path is unchanged: insert,
  unlock, return nil. `defer s.mu.Unlock()` is gone, since it would keep the bug.
  `forget`, `closeAll` and `trackedConn.Close` are untouched: `closeAll` already takes the
  map and unlocks before closing, and `forget` on the nil map after it is safe. No second
  lock, no goroutine, no signature change.
- Test (`scripts/vendorcore/_files/client/transport_test.go`, new, in-package):
  `TestSocketsTrackClosedSession` builds `newSockets(&Config{})`, a `net.Pipe` pair and a
  `*trackedConn`, marks the session closed, and calls `track` in a goroutine behind a 2 s
  bound. On the old code it fails by that timeout (`track of a closed session blocked`, the
  closer waits for `s.mu` held by `track`); on the fix it gets `net.ErrClosed`, the peer
  reads EOF and `s.open` is empty. The peer deadline is set before `track` closes its end:
  `net.Pipe` refuses a deadline once either end is closed. `t.Cleanup(s.closeAll)` is
  registered only after the bounded wait succeeds — on the old code cleanup would hang on
  the mutex the stuck goroutine holds. Loopback only, no live call, no flake under `-race`
  (`-count=100`).
- Regeneration: `go run ./scripts/vendorcore -upstream ../dnstt` from `0c5c52a`; the tool
  wrote `CORE_VERSION` (`files 35`, `added 14`, `rules 41` unchanged). `vendorcore -check`
  passes, and a second generation is byte-identical in `core/`.
- The full local matrix passed: `gofmt` outside `core/`, `go build ./...`,
  `CGO_ENABLED=0 go build ./...`, the vet split on Linux and Windows
  (`-testinggoroutine=false` for `core/`), `go test -race ./core/client`, the full
  `go test -race -count=1 ./...` with `DNSTT_CLI_TEST_DBUS=1`, `DNSTT_CLI_TEST_NETNS=0` and
  the `-skip` of the four external `netguard` live tests, the Windows cross-build with
  `go test -c` of all 19 packages that have tests, and `git diff --check`. No new
  dependency, profile key, transport or user behavior; no values, dates or environment
  details in the changed files.
