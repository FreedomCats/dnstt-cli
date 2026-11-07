---
kind: task
status: todo
detailed: true
depends: [01-connect-dialer]
---

# HTTP profile and supervisor integration

## Task

Expose the tested HTTP dialer as `exit.type = "http"` through the existing profile,
secret-store and application path. Preserve `socks5` as the default, its existing login-only
session check, and the recognized-but-deferred `ssh` path. No new proxy URL, authentication
scheme or profile field is selected by this plan.

The selected readiness contract is lazy. For an HTTP exit, the supervisor publishes
`Connected` after the tunnel handshake without opening a hidden exit stream. The first real
application `CONNECT` supplies its target to the HTTP dialer, which performs HTTP/1.1
CONNECT with Basic authentication on that stream. Before that request, `Connected` means
tunnel readiness only and does not prove the exit login. A final HTTP 407 becomes the
existing `exit.LoginError`, asks the supervisor to stop, and is returned without a resolver
retry. There is no startup target, probe field, synthetic destination or new profile field.

The concrete integration points are `internal/config/profiles.go` and
`internal/config/profiles_test.go` (type validation and round-trip),
`internal/app/tunnel.go` (secret lookup, exit construction, stream opener and `CheckExit`
callback), and the app/command integration tests in `internal/app/tunnel_test.go` and
`internal/cli/connect_test.go`. `internal/supervisor/tunnel.go` and its tests need explicit
HTTP 407 failure handling. The local `internal/datapath` remains SOCKS5; HTTP connection
refusals may retain its existing generic SOCKS5 failure mapping without fabricating an
RFC 1928 reply code.

The lazy contract deliberately differs from the SOCKS5 startup check because HTTP
authentication is bound to a target-bearing CONNECT request. The implementation must keep
the two contracts explicit rather than making a no-op HTTP `CheckExit` that would claim the
credentials were verified.

## Done when

- The selected lazy readiness and 407 stop behavior are implemented by concrete code and
  tests; no user-owned readiness choice is left to the implementer.
- HTTP profiles load, save and build with the existing `exit.user` and `password_ref`.
  A minimal pre-secret profile remains loadable, so local hidden-input secret setup is
  possible before the password reference is added. Invalid HTTP user/reference combinations
  fail safely before a tunnel stream is opened.
- A local unauthenticated SOCKS5 client carries data through the full
  app/supervisor/dnstt path to a Basic-authenticated HTTP proxy. The first real application
  request performs the HTTP authentication; the supervisor reports `Connected` before that
  request as tunnel readiness only. Wrong credentials produce one fatal 407 stop, while
  target refusal, protocol mismatch, cancellation and reconnection follow the approved
  per-stream/retry contract and have explicit tests.
- The shared exit dialer has an immutable opener bound to the supervisor's
  `OpenStream`. The SOCKS5 startup checker receives a session-local opener without mutating
  the datapath dialer, concurrent application requests do not race, and a new session is
  used after reconnection.
- Architecture R3, R8, §03/§04, §08 and §10 plus the placeholder example describe the new
  exit and lazy readiness semantics; documentation never contains actual server or
  credential values. The exact documentation-only edits are listed in step 9 for the lead.
  Existing `socks5` and `ssh` meanings, secret-store APIs and the local listener stay
  unchanged.
- Focused config/app/supervisor/command tests, full race tests, established vet, project
  build and Windows cross-build/test compilation pass without live services. Update
  `dnstt-cli.example.toml` comments to list the new type with placeholders only.

## Steps

1. **Validate and round-trip the profile.** In `internal/config/profiles.go`, add `http` to
   the allowed exit types and keep an empty type mapped to the existing `socks5` default.
   Retain the current `user` and `password_ref` fields and unknown-key checks; do not add a
   proxy URL, target, readiness or authentication-scheme field. Preserve a minimal profile
   with an empty `password_ref` so the local hidden-input setup can run before the reference
   is pasted. Add HTTP cases to `internal/config/profiles_test.go` for validation, TOML
   save/load round-trip, the existing reference-only secret rule, and invalid HTTP
   user/reference combinations (a `password_ref` without a `user` fails before a stream;
   an empty reference remains a valid pre-secret/empty-password state). Do not impose HTTP
   credential restrictions on `socks5`.

2. **Read the secret and construct the selected dialer.** In `internal/app/tunnel.go`, keep
   secret lookup through the existing `config.GetSecret`/platform `SecretStore` path. Read
   a referenced password before any tunnel stream is opened; if the reference is absent,
   do not open the store and leave the password empty for the minimal pre-secret profile.
   Build the tested HTTP dialer with the profile's `user`, resolved password and its bound
   stream opener. Reject a non-empty `password_ref` paired with an empty `user` before
   `client.Dial`; an empty reference supplies an empty password without opening the store.
   Return a safe construction error on a missing, foreign or unreadable reference, and never
   include the password, Basic token or raw secret-store payload in errors or logs. Keep the current SOCKS5 lookup and the explicit
   `ssh` not-implemented error unchanged.

3. **Bind an immutable, session-safe stream opener.** Bind each application dialer once to
   an opener that calls `supervisor.Tunnel.OpenStream(ctx)`, so every HTTP CONNECT obtains a
   stream from the current session and a waiting request can move to the next session after
   reconnection. Remove the current `BuildDatapath` pattern that assigns a session opener
   into a shared `*exit.SOCKS5` value. For the SOCKS5 pre-Connected check, create a
   session-local checker value with the `open` callback supplied by
   `supervisor.Options.CheckExit`; never replace the opener of the concurrently used
   datapath dialer. Exercise this binding under `-race` with concurrent streams and a
   session transition.

4. **Implement lazy HTTP readiness.** In the supervisor options assembled by
   `internal/app/tunnel.go`, retain `CheckExit` for `socks5` so every new session performs
   the existing login-only `SOCKS5.Check` before `Connected`. For `http`, do not call
   `CheckExit` and do not open a startup stream. Let the first real local SOCKS5 request
   pass its target to the HTTP dialer; each application stream then performs its own bounded
   CONNECT and Basic exchange. State and status tests must show that `Connected` can occur
   before the HTTP proxy has seen a request, while a successful real request is the first
   evidence of an authenticated HTTP exit.

5. **Make HTTP 407 a fatal supervisor stop.** In `internal/supervisor/tunnel.go`, add a
   concurrency-safe, one-shot fatal-exit signal safe to call from an application stream
   goroutine. The supervisor run goroutine must own the transition to `StateStopping`, close
   or drain the current session, wake waiting `OpenStream` calls, and return the first fatal
   error from `Run`. Wrap the HTTP application dialer in the app path so a `*exit.LoginError`
   caused by a final 407 calls this signal exactly once and is returned to the local
   datapath. It must not call `Restart`, mark a resolver unhealthy, enter backoff or redial
   after the 407. Other HTTP target refusals, malformed responses, stream interruptions and
   context cancellation remain per-request/retry failures; they close their streams and
   allow the supervisor's normal reconnection behavior. Add supervisor tests for the state
   transition, returned error, no-redial guarantee, pending-stream wakeup and concurrent 407
   callers.

6. **Keep the local SOCKS5 datapath protocol-neutral.** In `internal/app/tunnel.go` and
   `internal/datapath/socks.go`, keep the listener loopback-only and unauthenticated, retain
   the `Up` check, target preservation and the existing 60-second dial context, and pass
   either exit dialer through the same `datapath.Socks` path. An HTTP 407 must reach the
   application as the existing general SOCKS5 failure while the supervisor reports the
   fatal login error; do not fabricate an RFC 1928 reply code for HTTP statuses. Extend
   `internal/datapath/socks_test.go` or the app integration fixture to cover successful
   HTTP target data, local SOCKS5 without exit credentials, target refusal and the listener
   behavior after a session reconnect.

7. **Preserve the other exit contracts.** Keep empty `exit.type` and explicit `socks5`
   behavior, including the login-only `Check`, `LoginError` finality before `Connected`,
   remote target-name handling and secret lookup. Keep `ssh` recognized by validation and
   rejected by `BuildTunnel` with its existing deferred-stage error; do not add SSH fields
   or alter its later stage. Add regression assertions in the config, app and command test
   suites that these paths remain unchanged.

8. **Cover app and command behavior end to end.** Add focused tests in
   `internal/app/tunnel_test.go`, `internal/supervisor/tunnel_test.go`, and
   `internal/cli/connect_test.go` using generated dnstt identities, loopback services and
   an isolated secret store. Verify HTTP profile construction, secret lookup before stream
   opening, tunnel-only `Connected`, first-request Basic authentication, 407 shutdown,
   cancellation during stream acquisition/exchange, a new session after reconnection,
   SOCKS5 `Check`, the unchanged SSH error, and safe diagnostics without credentials or
   peer response text. Keep all test targets, users, passwords and keys synthetic.

9. **Hand off the documentation-only update.** After code review, the lead updates
   `doc/architecture.md` without live values: R3 names HTTP/1.1 CONNECT with Basic beside
   SOCKS5 and keeps SSH second; §03 lists the HTTP exit module; §04 lists HTTP CONNECT as
   an `exit.Dialer` protocol; §08 says HTTP `Connected` means tunnel readiness until the
   first real application CONNECT, says that CONNECT performs Basic authentication, and
   says 407 enters `Stopping` while other stream failures retry; §10's example and text
   list `socks5 | http | ssh` while retaining the existing `user` and `password_ref`
   fields. Update `dnstt-cli.example.toml`'s type comment with the same placeholder-only
   list. Do not add a startup target or profile field.

10. **Run the product checks.** Run the focused config, exit, app, datapath, supervisor and
    CLI tests with `-race`, then the full race suite, the established CI vet split,
    `CGO_ENABLED=0 go build ./...`, and the Linux checks. Verify
    `GOOS=windows GOARCH=amd64 go build ./...`, Windows vet and test-package compilation,
    and the normal Windows tests when a Windows runner is available. No live server,
    profile or secret-store entry is needed for this task.

## Risks and what is needed from the user

- The fatal supervisor signal must preserve the original HTTP 407 error while keeping state
  transitions owned by the run goroutine; otherwise a concurrent application request can
  race a reconnect or leave `Run` waiting indefinitely.
- The first HTTP request may fail after the status line has already shown `Connected`; the
  status wording and command tests must keep tunnel readiness separate from exit-login
  verification.
- Raw secret-store errors may contain private references or paths. The HTTP construction
  path must not echo secret values, the Basic token or raw credentials. Tests use isolated
  stores or injected lookup only.
- No real profile, credential or live network access is needed from the user. The later
  `04-live-runs` stage owns authenticated DoH/UDP and Windows acceptance.
