---
kind: task
status: todo
detailed: true
depends: [01-measurement-method, 02-timeout-probes]
---

# HTTP CONNECT control for the idle checks

## Task

`timeout-idle` can currently qualify an idle window only against an authenticated SOCKS5
exit (`timeout_idle.go:346-361`). The user's server has no such exit: it is an HTTP proxy,
and the accepted [measurement method](../01-measurement-method/plan.md#control-boundary-amendment)
now names the selected real exit, checked by this tool's own probe, as the pre/post
usability control. Add that HTTP control to the accepted command without touching the
measured window or the accepted SOCKS5 path: each check opens one stream of the same raw
session and speaks HTTP/1.1 `CONNECT` through the tunnel with Basic credentials, then fetches
the same explicit target body, using the tool's existing independent HTTP helper
(`http_probe.go`) and the existing synchronous target verifier (`idle_verify.go`). The
window remains a transport-level Noise-channel observation: the exit type changes only the
checks before and after it, never the trace, the estimator or the evidence fields.

This is tooling only, all under `doc/runs/_live/`: no product exit code,
`app.BuildTunnel`, supervisor or profile format participates, and the `compare` HTTP path
and the selftest keep their behavior. Acceptance here is synthetic: it must not read a real
profile, store or secret, contact a real resolver, exit or target, or perform any live
measurement — those belong to [Live timeout measurements](../05-timeout-measurements/plan.md),
which depends on this result. Use the [parent stage](../plan.md), architecture R3 and §04
for the exit and smux boundaries, and the accepted
[Healthy-idle observation command](../02-timeout-probes/02-idle-observation/plan.md) whose
lifecycle, record bounds and redaction this extends.

Expected change, all ignored: `http_probe.go` (a transport-independent CONNECT exchange),
`idle_verify.go` (the HTTP worker and a mode-aware result), `timeout_idle.go` (exit
selection, records, dispatch plumbing), `main.go` (help), `README.md`,
`idle_verify_test.go` and `timeout_idle_test.go`. `selftest_exit.go` already provides the
loopback HTTP CONNECT fixture (`serveHTTP`, `selftest_exit.go:203`) with Basic
authentication, 407 and 502, and is reused rather than recreated. One review and one
synthetic validation set accept the result.

## Steps

1. In `http_probe.go`, split the exchange out of `dialHTTPConnect` (`http_probe.go:57-125`)
   into a helper that takes an already-connected `net.Conn`, the target authority and the
   optional credentials: it validates the authority with `validTargetAuthority`, writes the
   single `CONNECT` request with `Host` and `Proxy-Authorization: Basic` only when a user is
   set (with the existing short-write handling), reads the replies with the existing
   `readConnectResponse`/`readConnectLine`, and returns a `*bufferedConn` with the same
   error categories: `*httpConnectStatusError` for 407 and other non-2xx, malformed,
   truncated or oversized replies, canceled and timed out. `dialHTTPConnect` keeps its
   loopback-only guard, its own dial, its deadline install/clear and the
   `context.AfterFunc` cancellation, so `compare` and the selftest produce the same bytes
   and errors; the extracted helper never dials, never resolves and never changes a
   deadline. Do not add a second CONNECT parser or use the product's HTTP exit code as an
   oracle.

2. Add a mode-aware HTTP worker next to `idleVerifyWorker` (`idle_verify.go:397-473`). It
   owns one synchronous operation: acquire one stream through the existing
   `idleSession.OpenStream` seam wrapped in `idleConn` with the verification deadline,
   write the CONNECT request to the target authority derived from the parsed target URL
   (IDNA-mapped host, scheme's default port), send Basic credentials when the selected exit
   has a user, validate the reply, then hand the buffered connection to the existing
   `idleHTTPTransport` and `probeWithClient` with
   `probeOptions{expectSHA256, refuseRedirects}` — the full-body digest, 2xx requirement,
   redirect refusal, TLS verification and header budget are shared, not reimplemented.
   Classify the outcome as `connect=complete` on an authenticated 2xx CONNECT,
   `proxy_auth_required` (407), `connect_refused` (other non-2xx), `connect_invalid`
   (malformed/truncated), `connect_timeout` or `canceled`; a 2xx without verified target
   data, a 407, a failed closure or non-zero local streams cannot pass. Never fall back to
   SOCKS5, to a direct TCP dial, to a local target lookup or to the `compare` verdict.

3. Make `idleVerification` and `idleRecord` carry the control mode and the HTTP-specific
   fields (`idle_verify.go:81-105`, `timeout_idle.go:88-131`); `pass()` requires the fields
   of the selected mode, so an HTTP result cannot qualify on SOCKS5 method/login fields and
   the SOCKS5 result keeps its authenticated RFC 1928 requirement. Keep the separate
   status/end reason, numeric timings, closure, local-stream and session-end fields as they
   are. The HTTP mode records only the authentication form (Basic sent or absent), the
   CONNECT status class, the target-data outcome, the HTTP status and the body byte count —
   no header or payload dumps. Ensure the existing Basic-token registration
   (`idleRedactors`, `timeout_idle.go:291`, and `registerExitCredentials`, `log.go:39-44`)
   covers the new path, and the target URL/path/query stay filtered.

4. Change the exit selection in `timeout_idle.go:346-361`: `""`/`socks5` keeps the accepted
   authenticated SOCKS5 path and its 255-byte/user rules; `http` selects the new control and
   validates Basic credentials the way the `secret` command already does for HTTP
   (`secret.go:28-45`): non-empty `exit.user`, no colon or control characters in it. The
   password comes from the selected `exit.password_ref` and may be empty, matching the
   existing exit-credential semantics. `ssh` and unknown types are refused before the
   password lookup, network-DNS selection and traffic, with one fixed diagnostic that does
   not echo the value. Read only the selected `exit.password_ref`; add no flag — the
   profile's exit type selects the control and the accepted command line stays unchanged.
   Record the control mode in the `setup` record and keep `timeout-dial` untouched.

5. Update `main.go`'s `timeout-idle` help and trailing lines (`main.go:66-77`,
   `main.go:115-118`): the command accepts a SOCKS5 or HTTP CONNECT exit, the pre/post
   checks use the tool's own probe for that exit, `ssh` and unknown exits are refused, no
   application traffic runs inside the window, and all private evidence is kept. Change no
   other command's flags or text.

6. Add synthetic tests to `idle_verify_test.go` and `timeout_idle_test.go`, reusing
   `selftestExit` in HTTP mode (`selftest_exit.go:203`) and the existing pipe/fake-session
   fixtures (`idlePipeFixture`, `testIdleSession`):
   - Basic CONNECT success with the complete expected body over one generated session, with
     the same target and digest used before and after the window;
   - missing or wrong credentials → 407 → `proxy_auth_required`, with no target request
     reaching the target and no SOCKS5 fallback;
   - refused target/502 → `connect_refused`; malformed, truncated, informational-only and
     oversized CONNECT replies classified without a panic or unbounded read;
   - an unreachable stream, a session end, cancellation and `-verify-limit` expiry during
     acquisition, request write, reply read and body: the accepted ownership applies —
     worker joined, late stream closed, session torn down as in the SOCKS5 path; remaining
     local streams or failed closure cannot pass;
   - SOCKS5 regressions stay green, and an HTTP result never passes with SOCKS5 fields or
     the reverse;
   - dispatch/help: `http` accepted, `ssh`/unknown refused before secret lookup, invalid
     target or digest refused early; synthetic profile/store bytes remain unchanged and no
     real configuration path, store or metadata is touched;
   - private/public output and logs contain no user, password, Basic token, target
     URL/path/query or echoed error, and numeric fields keep exact values.
   Use injected clocks/deadlines and short synthetic windows; never wait several real
   minutes.

7. Extend the ignored `README.md` only where this result changes it: the `timeout-idle`
   availability text (`README.md:446-472`), a reserved HTTP-exit example next to the
   SOCKS5/DoH/DoT/UDP ones, and one line that the measured window is transport-level and
   independent of the exit type while only the pre/post control follows it. Map the HTTP
   control's records into the existing Healthy Idle Windows form fields
   (`README.md:538-548`) and state that a failed or 407 CONNECT is a failed usability
   check, not a timeout measurement. Do not rewrite the accepted method, the fresh-dial
   section, the paired `danted` sections or the selftest.

8. Run the parent's [local validation for code children](../plan.md#local-validation-for-code-children)
   with cached dependencies (`GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`): focused and full
   `go test -race -count=1 ./doc/runs/_live`, explicit Linux/Windows tool vet and builds,
   the both-exit loopback selftest/redaction, the project race suite with
   `DNSTT_CLI_TEST_DBUS=1`/`DNSTT_CLI_TEST_NETNS=0` and exactly the four named exclusions,
   the static product build and exact CI vet split, Windows product/tool builds and test
   compilation, formatting/whitespace, relative links, English, ignore/package-list
   exclusion and privacy. Never force-add the tool or private records; no live request,
   real profile/store access, product change or timeout value belongs here.

## Done when

- `timeout-idle` accepts a profile whose exit is `http` and performs both checks, before and
  after the window, each through one tunnel stream of the same session using the tool's own
  HTTP/1.1 CONNECT exchange with Basic credentials, and verifies the complete expected 2xx
  body by the same explicit URL and SHA-256 criterion as the SOCKS5 path. The SOCKS5 path
  keeps its accepted behavior, and `ssh`/unknown exits are refused before secret lookup or
  traffic.
- A successful CONNECT without verified target data, a 407, another non-2xx, a
  malformed/truncated reply, a timeout, cancellation, an unclosed stream, a non-zero
  local-stream count or an unconfirmed session close cannot produce a passing check. An
  HTTP result cannot pass on SOCKS5 fields or the reverse. The window opens no application
  stream, and the trace, estimator and evidence fields do not depend on the exit type.
- The command records the control mode and the HTTP outcome categories in bounded private
  records and filtered output. No user, password, Basic token, target URL/path/query or
  echoed error appears in public output, the private schema keeps exact numeric values, and
  a setup or evidence failure keeps its explicit status.
- Synthetic tests cover Basic success, 407 with correct encoding, wrong credentials, a
  refused target, malformed/truncated/informational/oversized replies, unreachable or ended
  streams, cancellation and expiry at each phase, closure/stream-remain cases and SOCKS5
  regressions. Tests use generated values, the loopback `selftestExit` and fake sessions
  only, never a real profile, store or network service.
- Help, README and the existing private evidence form agree; the accepted method,
  `timeout-dial`, `compare`, both exit fixtures and the selftest are unchanged in behavior.
  The [parent validation matrix](../plan.md#local-validation-for-code-children) passes for
  the ignored tool; links, English, privacy and ignore checks pass; nothing under
  `doc/runs/` is force-added.
- No product code, profile field, timeout value, exit behavior or dependency changes. The
  live run and the timeout decision remain with
  [Live timeout measurements](../05-timeout-measurements/plan.md); this task's acceptance
  makes no live request.

## Risks and what is needed from the user

- The real exit's authentication may not be plain Basic (another scheme, a portal login, or
  credentials in a nonstandard header): the control then ends in 407 and the live
  measurements cannot qualify. Report it and stop; do not add an auth scheme, a probe flag
  or a product change without the user's decision.
- The target must be reachable from the server-side exit, not from the agent's machine; the
  CONNECT authority uses the URL host and the proxy resolves it. A target only the agent can
  reach is a wrong target, not a failed exit.
- The tunnel stream has no context-aware `OpenStream`; the accepted ownership (deadline,
  single-session teardown, late-stream collection, joined worker) is reused. A newly
  necessary unjoinable operation is a reason to stop and report, not to detach it.
- The CONNECT reply may include informational responses and leftover target bytes; the
  accepted `readConnectResponse` and `bufferedConn` handle them, and the target request must
  not lose buffered first bytes. Reusing the helper is required; a second parser would be a
  defect.
- Needed from the user: nothing for this local task. The values, the live runs and the
  timeout decision belong to the later child.
