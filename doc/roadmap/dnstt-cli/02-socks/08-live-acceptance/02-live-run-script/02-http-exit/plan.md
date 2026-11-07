---
kind: stage
status: done
base: c31d81c
---

# HTTP CONNECT exit

Add the selected `exit.type = "http"` product path using HTTP CONNECT and Basic
authentication, while keeping the current SOCKS5 behavior and the later SSH stage.
The server hands tunnel streams to one fixed TCP service (`architecture.md` R3); the
existing `exit.user` and `exit.password_ref` carry the credentials, without adding a
proxy URL field or changing the local loopback SOCKS5 datapath.

The protocol dialer and profile/supervisor integration have separate reviewable results.
The dialer is protocol-only; integration follows the selected lazy readiness contract:
`Connected` means tunnel readiness until the first real application `CONNECT`, which carries
the target and performs Basic authentication. A final HTTP 407 is an `exit.LoginError` that
stops the supervisor without a resolver retry. There is no startup target, hidden probe or
new profile field.

## Done when

- An `exit.Dialer` carries TCP through a bounded HTTP CONNECT exchange with optional Basic
  authentication, remote target names and safe errors; independent protocol and in-process
  dnstt tests pass.
- `exit.type = "http"` loads, round-trips and builds through the application with the existing
  password reference. The local SOCKS5 listener works through it with no app-side credentials.
- The selected lazy HTTP readiness and authentication-failure behavior is implemented and
  covered by tests without introducing an external probe target or new profile fields.
- Existing `socks5` behavior and the default exit are unchanged; `ssh` remains recognized and
  explicitly deferred. Project tests, vet, build and supported Windows checks pass.

## Children

1. [HTTP CONNECT dialer](01-connect-dialer/plan.md) — implement and test the stream-level
   HTTP protocol independently of profile selection and session readiness. Task.
2. [Profile and supervisor integration](02-profile-integration/plan.md) — expose `http` in
   profiles and the application using lazy first-application-`CONNECT` authentication and
   fatal 407 stop behavior. Task.

## Outcome

The HTTP CONNECT exit is implemented at the stream protocol and integrated through profiles,
secret lookup, the supervisor and the existing local SOCKS5 datapath. HTTP authentication is
lazy by design: `Connected` means tunnel readiness until the first real application CONNECT;
a final HTTP 407 stops the supervisor without resolver retry. Existing SOCKS5 behavior and
the deferred SSH path remain intact.

- The protocol dialer uses bounded HTTP/1.1 CONNECT with optional Basic authentication over
  the supplied tunnel stream. It preserves target bytes buffered with the final response,
  uses safe typed failures, and has focused tests plus an in-process generated-identity
  tunnel test.
- The profile and application path reuses `exit.user` and `exit.password_ref`, binds streams
  to the current supervisor session, and keeps SOCKS5 startup login checks session-local.
  The first HTTP application request performs authentication; fatal 407 wakes pending
  streams and prevents resolver penalties, backoff and redial. Other per-stream failures do
  not stop the supervisor.
- Independent reviews found and verified fixes for concurrent reads on the returned stream
  and for a shutdown race that could turn the local SOCKS5 failure reply into EOF. No
  integration findings remain.
- The architecture documentation and placeholder example describe HTTP CONNECT/Basic,
  shared secret-reference fields and HTTP readiness semantics. Documentation was committed
  separately as `c140146`.
- Focused race tests, the full race suite with isolated D-Bus, the exact CI vet split, static
  Linux build, Windows amd64 cross-build/vet and applicable test-package compilation passed.
  The stage-level integration review of `c31d81c..c140146` found no inconsistencies or
  privacy issues. Windows runtime, live services, network-mutating checks and netns tests
  were not run.
- No dependency or profile field was added, and no real profile or secret store was accessed.
  An existing signal-reader goroutine in `internal/cli/connect.go` may remain blocked after
  `runConnect` returns; review confirmed it is unchanged and not worsened by this stage, so
  it remains a separate follow-up. No real problem was left incompletely fixed because of cost.
