---
kind: stage
status: in-progress
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
