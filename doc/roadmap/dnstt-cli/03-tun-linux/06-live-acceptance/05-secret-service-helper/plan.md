---
kind: task
status: in-progress
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
