---
kind: task
status: done
detailed: true
depends: [01-socks5-baseline, 02-http-exit]
---

# HTTP-aware live comparison

## Task

Extend the ignored tool in `doc/runs/_live/` to compare the selected HTTP exit and retain
its SOCKS5 baseline. The upstream control path is independent of the product's HTTP dialer;
the in-process application path deliberately exercises the completed product integration.
The tool remains independent of user-dependent live acceptance. Implement this extension
after the HTTP profile/supervisor behavior is accepted, preserving the existing source and
review fixes.

### Protocol paths

- **Upstream side.** `dnstt-client` remains an unchanged raw TCP forwarder to the fixed
  service behind the dnstt server. `upstream.go` keeps the existing build, resolver/uTLS
  flags, transient key file, loopback listener, readiness wait and kill/wait lifecycle.
  For `socks5`, the independent `golang.org/x/net/proxy` client authenticates through that
  listener as before. For `http`, an independent HTTP CONNECT helper authenticates through
  the same listener with Basic and the selected target authority. No upstream patch,
  external wrapper process, proxy URL profile field or server-side change is needed.
- **Independent HTTP helper.** Add a local helper next to `probe.go` using standard-library
  HTTP request/response facilities, not `internal/exit` or a shared CONNECT implementation.
  It dials only the upstream loopback listener, passes the target hostname to the remote
  proxy, bounds the exchange by context/deadline and header limits, preserves buffered
  target bytes and closes on failure. Both HTTP and HTTPS target URLs must exercise CONNECT:
  ordinary `http.Transport.Proxy` uses absolute-form forwarding for plain HTTP and is not
  enough to prove this product path. After CONNECT, the existing HTTP client performs GET
  (and TLS for HTTPS) through the returned connection, without another proxy layer or
  credentials in the target request. Do not send credentials in a subprocess command,
  environment, URL userinfo or temporary credential file.
- **Application side.** `cli.go` keeps `app.BuildTunnel`/`app.BuildDatapath`, copies the
  selected `Exit.Type` unchanged, and probes the local unauthenticated SOCKS5 listener with
  the existing independent SOCKS5 client. Product code, not the app-side probe, performs
  HTTP authentication through the tunnel. For `http`, the supervisor may report `Connected`
  before that probe because authentication is lazy; the first real CONNECT made by the
  product is the authentication check, and a final 407 stops the supervisor.
- **Selection, secret input and verdicts.** `profile.go`, `compare.go` and `secret.go` must
  distinguish SOCKS5 and HTTP requirements while preserving empty-type SOCKS5 defaults and
  a safe setup refusal for unsupported `ssh`. The existing `secret` command currently has
  a SOCKS5-specific 255-byte limit even for other profiles; preserve it as an explicitly
  documented local-tool limitation for now, not an HTTP protocol limit. If it prevents
  acceptance, request an input-limit decision rather than truncating or putting a password
  in flags. Preserve serial runs, all-attempts-required verdicts and exit codes. Note that
  the HTTP upstream result proves the upstream tunnel plus independent helper, not an HTTP
  implementation in the upstream binary, and a listener-ready/Noise-only result is not an
  authenticated HTTP pass. Preserve existing pins/system-DNS/uTLS caveats.
- **Tests and instructions.** `live_test.go` and `selftest.go` retain the SOCKS5 selftest
  and authentication tests; add a loopback CONNECT proxy with made-up Basic credentials,
  known HTTP/HTTPS targets, both-side success, wrong credentials and server-down phases.
  `README.md` documents the selected exit, app-side SOCKS5 listener, helper scope, credential
  input limitation and later DoH/UDP acceptance without any supplied live value. Do not
  create a real profile or write a real secret in this task; isolated test profiles/stores
  remain permitted.

## Steps

1. **Select and validate the exit.** In `profile.go`, extend validation to accept `socks5`,
   `http`, and the existing empty-type SOCKS5 default, while returning a fixed safe setup error
   for `ssh` and unknown types. In `compare.go`, reject unsupported types before reading the
   referenced secret; retain profile loading, resolver selection and serial comparisons. In
   `secret.go`, allow password storage for SOCKS5/HTTP (including the empty-type default) and
   refuse unsupported exit types before prompting or opening the store. For HTTP, follow the
   product's Basic credential validity rules without imposing the SOCKS5 255-byte protocol
   limit. Keep the existing `secret` command's hidden input, store/read-back and 255-byte
   ceiling unchanged; document it as a local-tool constraint, never truncate input or call
   that bound an HTTP protocol limit. If that command ceiling blocks acceptance, stop and
   ask for a decision rather than changing the limit or the stored value.
2. **Add an independent CONNECT stream helper.** Add `http_probe.go` in the existing
   `package main`, using only the Go standard library. Dial only the upstream loopback
   address, send HTTP/1.1 CONNECT with the selected target authority and, when configured,
   Basic credentials in `Proxy-Authorization`, and parse the response without importing
   `internal/exit` or sharing its protocol implementation. Bound the context/deadline, status
   line and total response headers; return fixed/sanitized failures for refusal, malformed, oversized and
   truncated replies. On success, return a connection that first drains any target bytes
   buffered while parsing the response, and close the socket on every unsuccessful or
   canceled exchange. Preserve the target hostname for remote resolution.
3. **Use the helper only for upstream HTTP probes.** In `probe.go`, share response timing,
   status, complete-body reading, expected-body verification and safe error handling between
   the existing SOCKS5 probe and the HTTP CONNECT probe, retaining the current timeout and
   attempt semantics. Configure the HTTP transport so it explicitly CONNECTs for both plain
   HTTP and HTTPS URLs; after CONNECT, plain HTTP uses the origin request and HTTPS performs
   TLS and GET over the returned stream. For HTTPS fixture tests, trust only the generated
   test certificate through test-only roots; never disable certificate verification. Disable
   environment proxy selection and do not put proxy credentials in the target request. In
   `upstream.go`, choose the SOCKS5 or CONNECT probe from the selected exit type while leaving
   the upstream `dnstt-client` process, arguments, environment, key-file exception, listener
   readiness and kill/wait lifecycle unchanged. The upstream HTTP result must be described as
   the tunnel plus independent helper result, not as HTTP support in the upstream binary.
4. **Keep the application probe on local SOCKS5.** In `cli.go`, retain the copied profile's
   `Exit.Type` and the `app.BuildTunnel`/`app.BuildDatapath` path. Route the app-side probe
   explicitly through the existing independent SOCKS5 client, even when the selected exit is
   HTTP; probe its unauthenticated loopback SOCKS5 listener with no app-side exit credentials.
   Let the real application GET trigger product HTTP authentication after `StateConnected`;
   a product-side 407 must make the CLI side fail, never turn tunnel readiness into a probe
   pass. Preserve the product's existing fatal 407 supervisor behavior and the comparison's
   verdicts, exit codes, resolver order and attempt accounting. Supply no exit credentials to
   the app-side SOCKS5 probe; product code performs the HTTP authentication.
5. **Exercise both protocol paths with isolated fixtures.** Extend `live_test.go` with a
   loopback HTTP CONNECT fixture using synthetic Basic credentials and a synthetic target
   hostname mapped by the fixture, not local DNS. Run successful upstream/application
   comparisons against both HTTP and HTTPS targets for both exit types. Cover CONNECT for
   plain HTTP and HTTPS, TLS/GET through the HTTPS tunnel, complete response bodies, correct and wrong credentials
   (including 407), target refusal, malformed/oversized/truncated replies, bytes buffered
   after a successful response, context cancellation and connection closure. Use a generated
   HTTPS target certificate whose SAN matches the synthetic target hostname, trusted only by
   the test client. Include a
   synthetic HTTP credential longer than 255 bytes to show that the helper does not impose
   the SOCKS5/secret-command ceiling; separately retain the secret command's 255-byte input
   rejection test. Verify the app
   path reaches the product HTTP exit through its local SOCKS5 listener and does not count a
   407 as PASS. Isolate test profile and secret storage under the temporary config location.
   On Linux, suppress Secret Service discovery for the selftest so only its temporary
   file-backed store is used; restore the changed environment on exit. On Windows, use only
   the temporary `%APPDATA%` config path and the test's own DPAPI secret in that directory;
   never read the application's existing profile or secret reference. Extend `selftest.go`
   so its isolated generated-identity and temporary profile/secret-store setup runs
   server-up and server-down checks for both SOCKS5 and HTTP.
   For HTTP, use a separate loopback HTTP CONNECT fixture as the test server's fixed backend;
   it checks synthetic Basic credentials and maps a reserved synthetic target hostname to
   the local target without DNS. No real profile, secret store, resolver or live network is
   used.
6. **Prove credential and diagnostic privacy.** Update `log.go`, `compare.go` and
   `live_test.go` as needed so raw credentials and their Basic-encoded form are filtered
   before terminal output, verbose output, logs and tails; register the Basic token before
   any comparison output or log is written. Use synthetic marker values to verify that
   helper errors do not expose peer-supplied status/challenge text, malicious
   response text, target URLs or credentials; verify the actual target requests contain no
   `Proxy-Authorization` header or URL userinfo, and credentials are not in their URLs. With
   a synthetic child-process fixture or captured command construction, verify the tool does
   not pass the selected exit credentials in upstream arguments or environment. Do not dump
   the real inherited environment. Retain the upstream-only exception for the domain and
   resolver arguments and the transient mode-0600 public-key file. Never log a live capture
   or add files under `doc/runs/_live/` to Git.
7. **Update the ignored tool instructions.** In `doc/runs/_live/README.md`, document exit
   selection, empty-type SOCKS5 default, the independently implemented upstream CONNECT
   probe, the app-side SOCKS5 listener and lazy HTTP authentication, the local 255-byte
   secret-command limitation, safe synthetic selftest coverage, and the later authenticated
   DoH/UDP gate in `04-live-runs`. Preserve the baseline commands, verdict/exit-code meaning,
   privacy guidance, upstream caveats and Windows runtime limitation.
8. **Run local checks only.** Run `go test -race -count=1 ./doc/runs/_live`, Linux and
   Windows `go vet` for the tool, both protocol selftests, `go list ./...`, and
   `git check-ignore -v doc/runs/_live/main.go`. Run the project build and race suite plus
   the established CI vet split (full vet outside `core/`, `-testinggoroutine=false` only
   for `core/`). Cross-build the tool and product and compile applicable Windows tests with
   `GOOS=windows CGO_ENABLED=0`, keeping artifacts outside tracked paths. These checks do
   not claim live acceptance or Windows runtime behavior.

## Done when

- `socks5` comparison retains its independent probe, original verdicts and local selftest.
- `http` comparison makes the upstream path authenticate with an independent HTTP CONNECT
  helper and the in-process path use the product's HTTP exit behind its local SOCKS5 proxy;
  CONNECT is exercised even when `-url` is a plain HTTP target. For both exit types, upstream
  and application probes fetch the complete known body from local HTTP and HTTPS targets. The
  HTTPS target uses a generated certificate trusted only in the test, without weakening
  production certificate verification.
- The in-process result distinguishes tunnel `Connected` from verified HTTP credentials:
  the first real application CONNECT is observed before a product pass is recorded, and a
  product-side 407 stops the supervisor without being reported as a successful listener
  readiness check.
- A synthetic remote hostname reaches the proxy without a local target lookup. Success,
  407, target refusal, malformed/oversized/truncated replies, buffered target bytes,
  cancellation and close are covered without importing the product CONNECT code; wrong
  credentials fail safely and close the failed stream.
- Output, private logs and tails contain no raw or Basic-encoded exit credentials. Target
  requests and upstream subprocess arguments/environment contain no selected exit
  credentials. Diagnostics omit echoed challenges, malicious response text, target URLs,
  the server key and tunnel domain; synthetic marker checks cover success, failure and
  redirects. Preserve the explicit upstream exception: the domain and resolver are process
  arguments, while the key is supplied only in its transient private file. Private resolver
  logs and captures still stay ignored.
- Local server-up/server-down selftests pass for both exit protocols, using isolated
  generated identities and temporary profiles/secrets only. These results do not claim live
  DoH/UDP or Windows runtime acceptance.
- Empty `exit.type` still selects SOCKS5, unsupported `ssh` is refused before password input
  or secret lookup, and the `secret` command retains its documented 255-byte local-tool
  ceiling. The independent HTTP helper accepts longer valid synthetic credentials without
  truncating them or treating that local input ceiling as an HTTP protocol limit.
- `go test -race -count=1 ./doc/runs/_live`, tool Linux/Windows vet and Windows cross-build
  and test-compilation checks pass; `go list ./...` still excludes the tool and
  `git check-ignore` confirms that it is ignored. Project race/build and the CI vet split
  (full vet outside `core/`, the known upstream check disabled only for `core/`) remain
  green. The local README distinguishes exit code 0 from an actual upstream/CLI pass.

## Risks and what is needed from the user

- The independent helper must not share `internal/exit` code, or a product parsing/auth bug
  may be hidden by the control path. No additional dependency is needed.
- HTTP response text, URLs and Basic tokens are private. Use fixed safe errors before
  writing terminal output or logs; extend `log.go` tests to reject encoded credentials as
  well as the raw values. Never publish a live log even after filtering.
- Later acceptance needs a locally configured HTTP profile and working DoH and UDP resolver
  entries. A SOCKS5-only or Noise-only upstream pass cannot satisfy the HTTP acceptance item.

## Outcome

The existing ignored comparison tool and its instructions now support both selected exit
protocols. Independent review and two correction rounds completed the local acceptance;
the implementation remains outside Git under `doc/runs/_live/`.

- Selection preserves the empty-type SOCKS5 default. HTTP credentials follow the product's
  Basic validity rules, while unsupported exits are refused before secret lookup or input.
  The `secret` command retains its documented 255-byte local input ceiling; the independent
  HTTP helper accepts longer valid synthetic credentials.
- The upstream path uses a standard-library CONNECT helper independent of `internal/exit`
  for both HTTP and HTTPS targets. It preserves remote hostnames and buffered target bytes,
  bounds status and headers, uses the configured attempt deadline, and closes failed or
  canceled exchanges. The upstream binary remains an unchanged raw TCP forwarder.
- The application path retains `BuildTunnel`/`BuildDatapath` and the selected exit type.
  Its probe uses the local unauthenticated SOCKS5 listener without exit credentials. A real
  request exercises product HTTP authentication; tunnel readiness alone cannot pass a probe,
  and a product-side 407 fails the comparison.
- Isolated fixtures and selftest cover both exits and both target schemes, complete known
  bodies, server-up/server-down behavior, wrong credentials, target refusal, malformed and
  bounded replies, cancellation, closure and buffered bytes. HTTPS uses generated certificates
  and test-only trust roots without disabling verification. Synthetic target names are mapped
  by the fixtures without local target DNS.
- Privacy checks cover raw and Basic-encoded credentials, diagnostic output, private logs
  and tails, target requests, redirects and upstream subprocess construction. The existing
  upstream domain/resolver argument exception and transient private public-key file remain
  explicit. No real profile, secret store or live resolver was accessed for acceptance.
- Review corrected four control-probe defects: an independent timeout ceiling, rejection of
  valid HTTP/1.x responses, interpretation of CONNECT body-framing fields, and acceptance of
  control bytes in the response reason phrase. Regression tests cover each correction;
  fixed diagnostics do not echo peer response text. No review finding remains unresolved.
- Tool race tests, repeated parser/cancellation/privacy regressions, tool vet, Windows tool
  build and test compilation, gofmt, package-list exclusion and Git ignore checks passed.
  The applicable project race suite with isolated D-Bus, static build, CI vet split, Windows
  product build and compilation of applicable project tests also passed. The final selftest
  passed all local combinations and credential/identity filtering checks.
- Project race checks excluded four tests requiring external TCP/DNS. Netns and
  network-mutating checks were not run. Plain full vet retains only the two known upstream
  `testinggoroutine` warnings in `core/noise`; the prescribed CI split passes. The upstream
  revision could not be verified, and the tool reports that limitation.

Authenticated DoH/UDP acceptance remains in `04-live-runs`. Windows runtime and DPAPI,
external vendor verification and live service behavior are not established by these local
checks. No real problem was deferred or only partly fixed because of its implementation cost.
