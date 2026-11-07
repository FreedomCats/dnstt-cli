---
kind: stage
status: in-progress
base: 348ed5b
depends: [01-network-resolver]
---

# Live-run tooling

The local-only tooling that makes later acceptance attributable. The existing SOCKS5
comparison remains the first child; the selected HTTP exit is implemented as a separate
product stage; the final child extends the comparison without changing the upstream
`dnstt-client` process. The user-dependent runs stay in [Live runs](../04-live-runs/plan.md).

Everything under `doc/runs/_live/` is outside git (`.gitignore`, `AGENTS.md`). It may read
the user's profile and secret store and its logs may contain private resolvers and
addresses. The tool is a Go program that runs on Linux and Windows as
`go run ./doc/runs/_live <command>`; the leading `_` keeps it out of `./...` and CI.

`E1`, `S8`; former plan (`notes/former-plan.md`, S0): compare upstream `dnstt-client` with
the working-tree application against the same server and resolver.

## Done when

- The SOCKS5 baseline child preserves the reviewed local tool and its independent probe.
- The HTTP exit child adds `exit.type = "http"` to the product, with Basic authentication,
  tests and unchanged SOCKS5 behavior.
- The HTTP comparison child makes the upstream side use an independent HTTP CONNECT probe
  when the profile selects the HTTP exit, while the in-process `dnstt-cli` side continues
  to expose and test its local SOCKS5 listener.
- Local selftests, tool checks and privacy checks pass for both exit protocols. Live user
  runs are accepted only by the later live-runs stage.

## Children

1. [SOCKS5 baseline live-run tool](01-socks5-baseline/plan.md) — preserve the implemented
   local comparison, profile diagnostics, secret command and SOCKS5 selftest. Task.
2. [HTTP CONNECT exit](02-http-exit/plan.md) — add the product exit type and its protocol
   tests while retaining `socks5`. Stage: protocol dialer, then profile/supervisor
   integration with lazy first-application-`CONNECT` authentication and fatal 407 stop.
3. [HTTP-aware live comparison](03-http-comparison/plan.md) — extend the ignored live tool
   and its local selftest to exercise HTTP CONNECT independently. Task.

## Planning constraints

The first child's local implementation and review can be accepted on their own; the
former task's user-run DoH/UDP criterion is transferred to `04-live-runs`, not waived.
No implementation or live acceptance is marked done by this replan. The existing Outcome
below is retained from the former task as implementation evidence; it is not HTTP evidence.

The HTTP profile-integration child uses the selected lazy readiness decision: HTTP
authentication starts on the first real application `CONNECT`, `Connected` before that
request means tunnel readiness only, and a final 407 stops the supervisor. HTTP CONNECT and
Basic are already approved; no startup target or new profile field is introduced.

## Outcome

The initial local implementation and code review are complete. The profile-loading
failure reported before password input was traced to a missing configuration file by a
metadata-only check. The stage remains in progress until its children and its integration
acceptance are complete. User-dependent live acceptance belongs to `04-live-runs`.

- The ignored Go tool implements `compare`, `secret`, `selftest` and command help. Its
  local README documents the commands, verdicts, privacy rules and remaining live checks.
- `compare` runs upstream first, then the working-tree application stack, with one
  resolver and an independent SOCKS5 HTTP probe. `network` uses the first system DNS
  server on both sides. All requested attempts must pass for a side to pass.
- Password storage uses the platform secret store with read-back verification and prints
  only the reference; it leaves the profile file unchanged. Key material is passed to
  upstream through a transient private file. Logs and output stay outside git.
- The setup instructions now explain that `-profile` selects an existing profile rather
  than creating one. The local README gives platform configuration locations, a valid
  placeholder skeleton and the fields needed before password storage and before a live
  comparison. The diagnostic check did not read or change real configuration contents.
- The common profile loader now distinguishes missing files, access failures, wrong file
  types and invalid configuration through fixed messages without raw paths, profile
  names or TOML values. Isolated tests cover those cases and verify that a profile with
  no password reference can reach password input before the secret store is opened.
  Follow-up review passed without findings, and focused and project-wide checks remain
  green under the established vet invocation.
- Review fixed URL-bearing HTTP error diagnostics, with tests for timeout, refusal and
  redirect errors. Comparison output explicitly warns about different default uTLS
  distributions for DoH/DoT; an explicit upstream-compatible distribution can be selected
  for both clients. Each client's default behavior and the specified verdicts are kept.
- Local selftest passes: both clients fetch the expected body with the server running;
  after it stops, both fail with `not dnstt-cli`. Generated server identity values are
  absent from captured output and logs. This is local evidence only.
- The tool's Linux and Windows vet checks, race tests, Windows cross-build, project build,
  project race tests and the established vet check with `-testinggoroutine=false` pass.
  Plain project vet retains the two pre-existing upstream goroutine warnings in
  `core/noise/noise_test.go`. The tool is excluded from `go list ./...` and ignored by git.
- The available upstream source can be built, but its Git revision cannot be verified;
  the tool reports the specified warning. Windows runtime behavior and DPAPI were not
  exercised.

Still required in `04-live-runs`: the user stores the real exit password, sets its profile
reference and runs `compare` against an authenticated exit through a working DoH resolver
and a working UDP resolver. The upstream client must reach the target through both; the
corresponding `dnstt-cli` verdicts and private run records belong in `doc/local/`. No live
acceptance is claimed by completion of this tooling stage.
