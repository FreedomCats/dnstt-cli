---
kind: task
status: done
detailed: true
depends: [05-menu]
---

# Elevation from the menu

## Task

`architecture.md` §10a, privileges: `tun` without root — the menu re-runs one connection
through `sudo` in the same terminal, and if that is impossible it uses `socks` for this time.
`connect` already refuses `tun` without root with a hint (`internal/cli/connect.go:210-217`,
the `Requires.Admin` check); the menu is where the hint becomes an action.

One connection, not the menu: the child is the same binary running
`connect -datapath tun <profile>` — the flags come before the profile name, because
`flag.Parse` stops at the first positional argument (`internal/cli/root.go:214-230`) — with
stdin, stdout, stderr and the working directory inherited, so `sudo` asks for its password in
the terminal and the `connect` status line and Ctrl+C work as on the command line. The
settings, the secrets and the keyring stay the invoking user's — that is already the
`internal/sudo`, `internal/config` and `internal/secretservice` contract, and the live TUN
matrix exercised it. The parent must not take the terminal's signals for itself while the
child runs: it ignores SIGINT and SIGTERM around `Wait` and restores them after, so the first
Ctrl+C reaches the child.

The decision is taken before the connect body, from the same requirements `connect` and the
wizard use: `(&datapath.Tun{}).Requires()` — `Admin` and `Unsupported`
(`internal/datapath/tun.go:147-149`) — is what `internal/cli/profile.go:280-286` already asks
for the wizard's modes, and the `Admin` refusal is `internal/cli/connect.go:210-217`. A `tun`
profile on a system where that datapath needs root and the process has none is the case for
`sudo`; a process already under `sudo` (`sudo.Invoker()`) and root itself run directly,
without a second `sudo`. On Windows the elevated run in a new window is part of
[`04-tun-windows`](../../04-tun-windows/plan.md) (blocked); here that branch reports that the
elevated run is unavailable, and the menu offers `socks`, cross-built only.

Out of scope: the Windows UAC window and `04-tun-windows`'s elevation, new flags for the
menu, elevation in `menu -connect` and in the `connect` command, and any password handling —
`sudo` prompts by itself.

## Steps

1. **The datapath rule in one place** — `internal/cli/connect.go`.

   Add

       // datapathOf is the datapath a connection of the profile runs: the profile's, then
       // socks, the product default.
       func datapathOf(prof *config.Profile) string

   and use it for `kind` in `runConnection` (`:142-151`), keeping the request's override
   (`req.datapath` first) and `p.Datapath = kind` as they are. The menu has to compute the
   same datapath the body will run: with a copy of the rule it would elevate a connection
   that then runs `socks`, or miss one that runs `tun`.

2. **The decision** — new `internal/cli/menu_elevate.go`.

   `elevationPlan{sudo bool; reason string}`: `sudo` — re-run one connection through `sudo`;
   `reason` — why the connection cannot be run as the profile asks (privileges that cannot be
   got, or a datapath that does not work here), and when it is set the menu offers `socks`
   instead of connecting.

   `elevationEnv{goos string; euid int; invoker func() (*sudo.User, error);
   lookPath func(string) (string, error); requires datapath.Requirements}` is what the
   decision reads, so a test can ask it about any platform without root or a real `sudo`;
   `systemElevation() elevationEnv` fills it from `runtime.GOOS`, `os.Geteuid`,
   `sudo.Invoker`, `exec.LookPath` and `(&datapath.Tun{}).Requires()`.

   `planElevation(kind string, env elevationEnv) (elevationPlan, error)` in this order — each
   earlier answer makes the later ones pointless:

   - `kind != "tun"` → nothing to decide;
   - `env.requires.Unsupported != nil` → `reason: the <kind> datapath cannot run on this
     system: <err>`. `Requirements` says the other fields are not checked when `Unsupported`
     is set (`internal/datapath/datapath.go:14-33`), and no privilege helps a datapath that
     does not work here. On Windows this is the branch that answers today
     (`internal/datapath/tun_other.go:12-17`);
   - `env.goos == "windows"` → `reason: the <kind> datapath needs root, and the elevated run
     is not available on this system yet`. It comes before the `sudo` lookup, or a Windows
     build would report "sudo was not found in PATH"; the UAC window is
     `04-tun-windows`'s. A guard for the day that datapath lands: today `Unsupported`
     answers first on Windows, and the unit test reaches it through the seam;
   - `!env.requires.Admin` → nothing to decide;
   - `invoker, err := env.invoker()`; an error is returned as it is (the settings cannot be
     told apart, and `config.LoadProfiles` would fail on the same call);
     `invoker != nil || env.euid == 0` → nothing to decide: root itself, or `sudo`, whose
     `Invoker` names the user the settings and the keyring belong to. A second `sudo` would
     be the same thing again, and a password prompt for nothing;
   - `env.lookPath("sudo")` failing → `reason: the <kind> datapath needs root, and the
     elevated run is not available: sudo was not found in PATH`;
   - otherwise `elevationPlan{sudo: true}`.

   The offer's question is a constant next to it:
   `socksOfferQuestion = "Connect with socks for this time? (y/N): "`.

3. **The child's argv and its streams** — `internal/cli/menu_elevate.go`.

       // elevatedCommand builds the child's command...
       func elevatedCommand(e *env, name string) (*exec.Cmd, error)

   `os.Executable()` for the binary (`os.Executable` failing is
   `cannot find the dnstt-cli binary: %w`), then

       exec.Command("sudo", "--", exe, "connect", "-datapath", "tun", name)

   — a list, never a shell string. `--` ends sudo's own options; the flags of `connect` come
   before the profile name. `cmd.Stdin`, `cmd.Stdout`, `cmd.Stderr` are `e.stdin`,
   `e.stdout`, `e.stderr`; `Dir` stays empty and `Env` stays nil, so the working directory
   and the environment are inherited and `sudo` asks for its password on the terminal. No
   `-E`, no `SUDO_ASKPASS`, no value of any kind is added to the argv or the environment: the
   exit password never leaves the secret store, and `sudo`'s own prompt is the only place a
   password is typed.

4. **Running it with the terminal's signals left to the child** — `internal/cli/menu_elevate.go`,
   `internal/cli/menu_signals.go`.

       // runElevated runs one connection as the same binary under sudo...
       func runElevated(ctx context.Context, e *env, name string, sess *menuSession) (int, error)

   `ctx.Err()` first (a stop that arrived before the child started must not start it); then
   `elevatedCommand`; then `sess.suspend()` and `defer sess.resume()`; `cmd.Start()` — a
   failure is `the elevated run could not be started: %w`; then `cmd.Wait()`:
   `*exec.ExitError` gives its code and no error, any other error is returned, `nil` is `0`.

   `exec.Command`, never `exec.CommandContext`: a cancelled context would SIGKILL the child
   and leave the routes, the system DNS and the kill-switch behind. The child's own ladder
   stops it — the same one the command line uses.

   `menuSession` (`internal/cli/menu_signals.go:22-37`, `:80-107`) gains `suspended bool` with
   `suspend()`/`resume()` under the existing mutex, and `handle` returns on every signal while
   suspended, before the press count: while a child runs, the terminal's signals are the
   child's, and it receives them from the kernel anyway, because both processes are in the
   foreground process group. Nothing else of the session changes: the rules for the other
   items, the stop line and the press ladder stay as they are, and `connect`'s `os.Exit` on
   the third press stays `connect`'s.

5. **The fallback and the item** — `internal/cli/menu_elevate.go`,
   `internal/cli/menu_round.go`, `internal/cli/menu.go`.

   `socksInstead(ctx, e, name, reason string, abort <-chan struct{}) error`: print `reason`
   when it is not empty, ask `socksOfferQuestion` through `m.ask` (read errors go through
   `readError`, so the end of input closes the menu), and on `isYes` run
   `m.connectSessions(ctx, e, name, "socks", abort)`; anything else returns nil and the loop
   shows the menu again.

   `elevatedConnection(ctx, e, name string, abort <-chan struct{}) error`:
   `code, err := m.elevate(...)`; a cancelled `ctx` (or a `context.Canceled` error) returns
   the error as it is — the person stopped the item and `reportItemError` is silent about it,
   as in the round offer (`menu_round.go:46-48`), so no question is asked. Otherwise an error
   means nothing ran — print `<app.Name>: <err>` and call `socksInstead` with no reason; a
   non-zero `code` prints `<app.Name>: the elevated connection exited with status <code>` and
   returns nil, so the menu stays. Zero prints nothing: the child said everything.

   `menu_round.go`: `connectWithResolvers` (`:18-64`) keeps its name and the round offer and
   gains the dispatch at the top — `plan, err := m.elevationPlan(name)`, then
   `plan.reason` → `socksInstead(ctx, e, name, plan.reason, abort)`, `plan.sudo` →
   `elevatedConnection(ctx, e, name, abort)`, otherwise the old loop. The loop body moves to

       // connectSessions runs the connection and the mid-session resolver offer...
       func (m *menu) connectSessions(ctx context.Context, e *env, name, datapath string,
           abort <-chan struct{}) error

   which puts `datapath` into `connectRequest` (`""` — the profile's, `"socks"` — the
   fallback) and otherwise changes nothing. The elevated run has no round offer: the child
   reports nothing back (there is no IPC), and that is recorded rather than built.

   `m.elevationPlan(name)` loads the profiles, takes `datapathOf(prof)` and calls
   `planElevation(kind, m.elevation())`; a missing profile returns the zero plan, so the body
   still answers `profile %q not found`.

   `menu` (`internal/cli/menu.go:43-54`) gains `elevation func() elevationEnv` and
   `elevate func(ctx context.Context, e *env, name string) (int, error)`; `newMenu`
   (`:57-66`) sets `elevation: systemElevation`; `runWith` (`:114-139`) binds `m.elevate` to
   `runElevated` with the session it has just made, unless a test supplied its own.

   `connectOnce` (`:143-152`) and `runConnect` are untouched: `menu -connect` and `connect`
   keep the body's refusal, and the question-free one-shot path stays question-free.

6. **Tests** — new `internal/cli/menu_elevate_test.go`, `internal/cli/menu_test.go`.

   `planElevation` as a table over `elevationEnv`, no process and no root: socks; `tun` with
   `Unsupported`; `tun` + admin + non-root + `sudo` found → `sudo`; `sudo` missing → the
   reason; `euid == 0` → nothing to decide (root directly); `euid == 0` with an invoker →
   nothing to decide (under `sudo`); an invoker without root → nothing to decide and
   `lookPath` never called; an invoker error → that error; `goos: "windows"` + admin → the
   reason, with `invoker` and `lookPath` fakes that fail the test if called.

   `elevatedCommand`: `Args` is exactly `["sudo", "--", <os.Executable()>, "connect",
   "-datapath", "tun", "home"]`, `Stdin`/`Stdout`/`Stderr` are the `env`'s own streams
   (`==`, not a copy), `Dir == ""` and `Env == nil`. Nothing is started.

   The item, over `menuHarness` (`internal/cli/menu_test.go:88-179`) with a `tun` profile
   (`menuTunProfile()` next to `menuProfile()`): the runner is called with the profile name
   and the in-process body is not; a non-zero code prints the status line, asks nothing and
   the loop asks for a choice again; a runner error prints it and offers socks — `y` connects
   with `datapath: "socks"`, `n` connects nothing and the loop goes on, the end of input
   closes the menu; a plan reason (no `sudo`) offers socks without calling the runner; an
   `euid == 0` environment calls the body and not the runner; a cancelled item context asks
   nothing and returns `context.Canceled`; `menu -connect` of a `tun` profile calls the body
   and not the runner.

   `menuSession`: while suspended an `os.Interrupt` prints nothing and cancels nothing, and
   neither does `SIGTERM`; after `resume` the rules are back — the first press prints the stop
   line and cancels the item.

   Harness: `elevated []string`, `elevateCode int`, `elevateErr error`; `newMenuHarness`
   replaces `h.menu.elevation` with an environment that needs nothing
   (`datapath.Requirements{}`), so no existing test looks for `sudo` on the machine, and
   `h.menu.elevate` with a recording fake. No test starts `sudo` or any process, opens a
   terminal, reads a real store or touches the network.

7. **The local matrix** — on the final tree:

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
     — the four external `netguard` live tests, so no live call is made;
   - `GOOS=windows GOARCH=amd64 go build ./...` and `GOOS=windows go test -c` for every
     package with tests, which compiles the Windows guard and its test;
   - `git diff --check`.

Easy to get wrong:

- the argv order: `flag.Parse` stops at the first positional argument, so
  `connect home -datapath tun` is three positional arguments and a usage error; the flags go
  first, and the unit test pins the exact list;
- `exec.CommandContext` would kill the child with SIGKILL on a cancelled context and leave
  the routes, the system DNS and the kill-switch behind: the child's own ladder stops it;
- `suspend` before `Start` and `resume` in a `defer` after `Wait`: a press between
  `sess.running()` and the child's ladder would otherwise print the stop line twice and
  cancel an item that is not the connection;
- the session's rules for the other items must not move: `suspend` is a flag, not a second
  ladder, and `connect`'s third-press `os.Exit` stays in `connect`;
- the parent prints nothing while the child runs: the status line is the child's and one line
  at the bottom; a menu line over it would leave `\r\033[K` on the wrong row;
- the parent's stdin reader is buffered (`internal/lines`): a line typed ahead of the `sudo`
  prompt stays in the parent's buffer and the child never sees it. The menu reads only when
  it asks and the child starts at once, so the window is a paste typed between the question
  and the child — worth a note in the Outcome, not a change of the reader;
- a missing profile must reach the body's `profile %q not found`, not the elevation decision;
- `sudo` resets the environment, so the child reads `<invoker home>/.config/dnstt-cli` and
  not a custom `XDG_CONFIG_HOME` (`internal/config/dir.go:62-85`): the same as the
  documented `sudo dnstt-cli connect`, and not something to fix here;
- the Windows guard before `lookPath` and `invoker`, and `Unsupported` before privileges:
  `Requirements` says the other fields mean nothing once `Unsupported` is set;
- the tests' `elevation` seam must default to "nothing needed", or every existing connect
  item test would look for `sudo` on the machine and depend on the host.

## Done when

- A `tun` profile in a non-root menu session runs one connection as `<the same binary>
  connect -datapath tun <profile>` under `sudo`: the argv is a list, stdin, stdout, stderr and
  the working directory are inherited, and no password or secret is in the argv or the
  environment. The password prompt, the status line and Ctrl+C are the child's; the parent
  takes no signal of the terminal while the child runs and prints nothing; the first Ctrl+C
  reaches the child.
- The child's exit returns to the menu, and a non-zero status is reported with its code
  without closing the menu.
- Root and an already-elevated session run `connect` in the process, with no second `sudo`
  and no prompt; `menu -connect` and the `connect` command keep their behaviour and their
  refusal.
- `sudo` missing, an unsupported `tun` datapath, the Windows branch, or a child that cannot
  start prints the reason and offers `socks` for this time; accepting runs the connection
  with `-datapath socks` (with the mid-session resolver offer as usual), declining returns to
  the menu, and the end of input at the question closes it.
- The elevated connection has no mid-session resolver offer — the child reports nothing back
  — and that is recorded, not built.
- Tests with an injected runner see the argv, the inherited streams and the `socks` fallback;
  the direct paths (root, under `sudo`), the Windows refusal and the unsupported datapath are
  covered through the decision seam; the session's suspend rule is covered. No test starts
  `sudo` or any process, opens a terminal or touches the network.
- The local matrix (build, vet split on Linux and `GOOS=windows`, race with the stated skips,
  Windows cross-build and test compilation, `git diff --check`) passes; no new dependency,
  `core/`, `doc/architecture.md` and `README.md` untouched.
- The real check — a terminal, the `sudo` prompt and a session through the tunnel — is a
  user-run step recorded in the Outcome: no agent-run host `tun` session is made without a
  fresh explicit request, and Windows is not run.

## Outcome

A `tun` profile in a non-root menu session runs one connection as the same binary under
`sudo`, and the menu offers `socks` for this time when that is impossible.

- New `internal/cli/menu_elevate.go`: `elevationPlan{sudo, reason}`, `elevationEnv{goos,
  euid, invoker, lookPath, requires}`, `systemElevation`, `planElevation` with the order of
  step 2 (not `tun` → `Unsupported` → `goos == "windows"` → `!Admin` → the `invoker` error
  → `invoker != nil || euid == 0` → `lookPath("sudo")` → the sudo plan),
  `(*menu).elevationPlan` (a missing profile gives the zero plan, so the body still answers
  `profile %q not found`), `socksOfferQuestion`, `elevatedCommand`, `runElevated`,
  `socksInstead` and `elevatedConnection`.
- `elevatedCommand` builds the argv as a list: `sudo -- <os.Executable()> connect -datapath
  tun <name>`. `exec.Command`, not `exec.CommandContext`; stdin, stdout and stderr are the
  env's own, `Dir` and `Env` are left zero. No `-E`, no askpass, nothing added to either: the
  exit password never leaves the secret store.
- `runElevated` returns on `ctx.Err()` before the command is built, suspends the session
  strictly before `Start` and resumes it in a `defer`. `*exec.ExitError` gives its code and
  no error; the child's own ladder stops it.
- `menuSession` gains `suspended` with `suspend()`/`resume()` under the existing mutex;
  `handle` returns on every signal while suspended, before the press count. The rules for
  the other items, the stop line and the press ladder did not move, and `connect`'s
  `os.Exit` stays in `connect`.
- `connectWithResolvers` keeps its name and the round offer and dispatches first: `reason`
  → `socksInstead`, `sudo` → `elevatedConnection`, otherwise the old loop, which moved to
  `connectSessions(ctx, e, name, datapath, abort)`. `menu` gains `elevation` and `elevate`;
  `newMenu` sets `systemElevation` and `runWith` binds `elevate` to `runElevated` with the
  session it has just made. `connectOnce`, `runConnect` and `runConnection`'s refusal are
  untouched: `connect` and `menu -connect` still do not elevate.
- `datapathOf(prof)` in `connect.go` is the one rule of the datapath of a connection: the
  body uses it for `kind` (the request's override still first, `p.Datapath = kind` kept) and
  the decision uses it to know what the body will run. `printMenu` keeps its own two lines
  for the header of the menu; the behaviour is identical today and moving it was not part of
  the step.
- Decisions taken inside the plan's freedom: the `reason` is printed to stdout before the
  question, as the round offer's failures are, and the runner's error and the status line go
  to stderr in `reportItemError`'s shape. A child killed by a signal has `ExitCode() == -1`
  and the status line would read `status -1`; the usual case is Ctrl+C at sudo's password
  prompt, where sudo dies by SIGINT. A negative code therefore prints `the elevated
  connection was stopped by a signal`, and only a positive one prints its code. The
  harness's default elevation environment needs nothing, so no existing test looks for
  `sudo` on the machine.
- Tests: `internal/cli/menu_elevate_test.go` — the `planElevation` table, including the
  Windows branch with fakes that fail the test if called, the exact argv, the identity of
  the streams and that nothing is started — and the item's cases in
  `internal/cli/menu_test.go` through `menuHarness`: a non-zero code, a signal stop, a
  runner error answered `y`, `n` and by the end of input, a plan reason without the runner,
  `euid == 0` → the body, a cancelled item → `context.Canceled` and no question, `menu
  -connect` → the body; and the suspend/resume rule of the session. No test starts `sudo` or
  any process, opens a terminal, reads a real store or touches the network.
- The elevated connection has no mid-session resolver offer: the child reports nothing back
  — there is no IPC — and the connection is the child's, as on the command line. Recorded in
  `elevatedConnection`'s comment, not built.
- `sudo` resets the environment, so the child reads the invoker's `~/.config/dnstt-cli` and
  not a custom `XDG_CONFIG_HOME`: the same as the documented `sudo dnstt-cli connect`, and
  not something this task changes.
- The menu's stdin reader is buffered (`internal/lines`): a line pasted between the socks
  question and the child's start stays in the parent's buffer and the child never sees it.
  The menu reads only when it asks, so the window is a paste at that moment; noted, the
  reader was not changed.
- The Windows branch answers through the decision seam only today: on a Windows build
  `Unsupported` answers first (`internal/datapath/tun_other.go`). The `goos` guard stays for
  the day `04-tun-windows` lands, and the elevated run in a new window is that task's.
- Checks on the final tree: `gofmt` clean outside `core/`; `go build ./...` and
  `CGO_ENABLED=0 go build ./...`; the CI vet split on Linux and with `GOOS=windows`; the
  full `go test -race -count=1 ./...` with `DNSTT_CLI_TEST_DBUS=1`, `DNSTT_CLI_TEST_NETNS=0`
  and the four live `netguard` tests excluded by `-skip`, so no live call was made; the
  Windows cross-build and `go test -c` for every package with tests; `git diff --check`. No
  new dependency; `core/`, `doc/architecture.md` and `README.md` untouched.
- The full `-race` matrix is flaky for a reason outside this task:
  `TestConnectWithInProcessServer` passes `t.Logf` into the tunnel and the core's goroutine
  logs after the test has returned (`internal/cli/connect_test.go`). Seen once in repeated
  runs of the package; the task neither touches nor worsens it.
- Not seen, and left to the person: the real check — a terminal, sudo's password prompt,
  Ctrl+C and a session through the tunnel. Windows was cross-built and its tests compiled,
  not run.
