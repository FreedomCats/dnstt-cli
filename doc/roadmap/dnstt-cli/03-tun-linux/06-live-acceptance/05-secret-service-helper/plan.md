---
kind: task
status: done
detailed: true
---

# Dispatch the secret-service helper

## Task

Under `sudo`, a `secretservice:` reference is read through a hidden dnstt-cli command that
runs as the invoking user and talks to that user's keyring
(`internal/secretservice/helper.go`: `HelperCommand`, `RunHelper`). The command is never
dispatched: `internal/cli/root.go` parses it as an ordinary command and answers `unknown
command "__secret-service"`, so the keyring cannot be read under `sudo` at all. The P0
keyring branch of the [live matrix](../02-user-run/plan.md) found it; a file-backed secret
works. This task wires the hidden command into the CLI without changing the helper protocol
or the normal commands.

## Steps

1. Add a small build-tagged dispatch in `internal/cli`: a Linux file with
   `runSecretHelper(args []string, in io.Reader, out, errW io.Writer) (int, bool)` that
   returns `(0, false)` unless `args[0] == secretservice.HelperCommand`, and otherwise
   `(secretservice.RunHelper(in, out, errW), true)`; a `!linux` file returns `(0, false)`.
   Call it at the start of `Main` with `os.Stdin`, `os.Stdout`, `os.Stderr`, before `run`,
   and return its exit code when handled. Do not import the package from files that build on
   Windows.
2. Keep the helper invisible to the normal interface: no usage line, no help entry, no flag
   parsing before it, and an empty argument list still prints usage. The helper's request
   and response format stays exactly as it is.
3. Tests in `internal/cli`: a request on the injected stdin produces the helper's JSON
   response and its exit code, `args` with another command or empty is not handled, and a
   malformed request still returns the helper's non-zero code without printing usage.
4. Check that the package graph has no cycle (`internal/cli` may import
   `internal/secretservice`; `internal/config` already does), that Windows builds and tests
   compile, and that the existing `secretservice` tests are untouched.
5. Run the local validation matrix of the parent stage: focused race on `internal/cli` and
   `internal/secretservice` with the isolated D-Bus setup where applicable, the project
   race suite with the stated exclusions, the CI vet split, the static build, the Windows
   cross-build and test compilation, `gofmt` and `git diff --check`.

## Done when

- `dnstt-cli __secret-service` reaches `secretservice.RunHelper` on Linux and its exit code
  and streams are passed through; on other platforms the name stays an ordinary unknown
  command (Windows still builds and compiles its tests).
- No normal command, usage text or flag parsing changes; the helper protocol is byte-for-byte
  unchanged.
- Tests prove the dispatch, the not-handled cases and the malformed request; the focused and
  project race suites, the CI vet split, the static build, the Windows cross-build and test
  compilation, `gofmt` and `git diff --check` pass.
- Handoff: the live matrix re-checks the keyring branch under `sudo` with a `secretservice:`
  reference (P0); a file-backed reference keeps working either way. No dependency, profile
  field or user-visible interface is added.

## Risks and what is needed from the user

- The dispatch must not become a way to run the helper as root against the user's keyring:
  it only runs in the process that was already started, with the same user's session, as the
  protocol assumes.
- A wrong placement after `run` would make the name look like a command again; the test pins
  the order.
- The live keyring check needs the user's session bus and is done by lead after this task;
  nothing here needs the real keyring.

## Outcome

The hidden helper is now dispatched before the command list is consulted, so
`dnstt-cli __secret-service` runs as the invoking user's keyring bridge again. Under `sudo`
a `secretservice:` reference therefore has a reader, which is what P0 of the matrix needs.

- `internal/cli/secrethelper_linux.go` holds `runSecretHelper(args, in, out, errW) (int,
  bool)`: it handles only `args[0] == secretservice.HelperCommand`, otherwise it answers
  `(0, false)`; the exit code and the three streams of `secretservice.RunHelper` come back
  unchanged. Arguments after the first are not parsed: the helper's input is a JSON request
  on stdin, and a request can never turn into an ordinary command.
- `internal/cli/secrethelper_other.go` (`!linux`) is the `(0, false)` stub and does not
  import `internal/secretservice`, which is Linux-only in its entirety; on other platforms
  the name stays an ordinary unknown command.
- `Main` was split into a thin wrapper and `mainWith(args, build, in, out, errW)`, which
  takes the streams as parameters so tests see where each one goes without replacing the
  process-wide `os` streams. `mainWith` calls the dispatch first, before `run`; the
  exported signature and the behaviour of `run`, `report` and the usage text are unchanged.
  The helper has no line in the command list or in any command's help, and an empty
  argument list still prints the command list.
- The helper protocol is untouched: `internal/secretservice` (helper, service and their
  tests), `go.mod`, `go.sum` and `core/` were not changed, and no dependency, profile field
  or user-visible interface was added.
- Tests: `internal/cli/secrethelper_linux_test.go` proves the dispatch through `mainWith`
  with a JSON request on the injected stdin (a valid JSON response with `unavailable` and
  the helper's exit code 0), the malformed request (the helper's exit code 2, its `request`
  message and no usage), the not-handled argument lists (nothing read or written) and the
  invisibility in the command list. The malformed-request test pins the order: with the
  dispatch moved after `run` it fails on `unknown command`. `secrethelper_other_test.go`
  (`!linux`) pins that the name remains an unknown command through `mainWith`.
- Checks: `gofmt` clean on `internal/cli` and `internal/secretservice`; `git diff --check`
  clean; `go build ./...` and `CGO_ENABLED=0 go build ./...`; the focused race on
  `internal/cli` and `internal/secretservice` with `DNSTT_CLI_TEST_DBUS=1` (the
  `secretservice` tests bring up their own `dbus-daemon`); the project race with
  `DNSTT_CLI_TEST_DBUS=1`, `DNSTT_CLI_TEST_NETNS=0` and exactly the four external-network
  netguard live tests excluded, so no live call was made; the CI vet split on Linux and for
  Windows; the Windows cross-build and the compilation of the tests of every package that
  builds there, `internal/cli` included; and an end-to-end run of a built binary against a
  non-existent session bus (a valid request gives the JSON response, a broken one the
  helper's code 2 and no usage). The tests never touch the real keyring: the session bus
  address is pointed at a socket inside the test's temporary directory.

What the handoff needs: the live matrix re-checks the keyring branch of P0 under `sudo`
with a `secretservice:` reference; a file-backed reference keeps working either way, and
the matrix node already depends on this task.

No real problem was left incompletely fixed because of cost.
