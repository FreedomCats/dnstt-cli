---
kind: task
status: todo
depends: [01-socks5-baseline, 02-http-exit]
---

# HTTP-aware live comparison

## Task

Extend the ignored tool in `doc/runs/_live/` to compare the selected HTTP exit and retain
its SOCKS5 baseline. The upstream control path is independent of the product's HTTP dialer;
the in-process application path deliberately exercises the completed product integration.
The tool remains independent of user-dependent live acceptance. Detail the ordered
implementation steps after the HTTP profile/supervisor behavior is accepted; preserve the
existing source and review fixes.

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

## Done when

- `socks5` comparison retains its independent probe, original verdicts and local selftest.
- `http` comparison makes the upstream path authenticate with an independent HTTP CONNECT
  helper and the in-process path use the product's HTTP exit behind its local SOCKS5 proxy;
  CONNECT is exercised even when `-url` is a plain HTTP target. Both clients fetch the
  complete known body in local HTTP and HTTPS cases.
- The in-process result distinguishes tunnel `Connected` from verified HTTP credentials:
  the first real application CONNECT is observed before a product pass is recorded, and a
  product-side 407 stops the supervisor without being reported as a successful listener
  readiness check.
- A synthetic remote hostname reaches the proxy without a local target lookup. Success,
  407, target refusal, malformed/oversized/truncated replies, buffered target bytes,
  cancellation and close are covered without importing the product CONNECT code.
- Output, private logs, subprocess arguments/environment and target requests contain no
  raw or Basic-encoded exit credentials. Output/logs also omit echoed challenges, malicious
  response text, the server key and tunnel domain; test-marker checks cover success,
  failure and redirects. Keep the existing upstream exception explicit: the domain and
  resolver are necessarily arguments to that process, while the key is supplied only in
  its transient private file. Private resolver logs and captures still stay ignored.
- Local server-up/server-down checks pass for both exit protocols, with isolated generated
  identities and secret stores only. These results do not claim live DoH/UDP or Windows
  runtime acceptance.
- `go test -race ./doc/runs/_live`, tool Linux/Windows vet and cross-build checks pass;
  `go list ./...` still excludes the tool and `git check-ignore` confirms it is ignored.
  Project race/build and the CI vet split (full vet outside `core/`, the known upstream
  check disabled only for `core/`) remain green. The local README distinguishes exit code
  0 from an actual upstream/CLI pass.

## Risks and what is needed from the user

- The independent helper must not share `internal/exit` code, or a product parsing/auth bug
  may be hidden by the control path. No additional dependency is needed.
- HTTP response text, URLs and Basic tokens are private. Use fixed safe errors before
  writing terminal output or logs; extend `log.go` tests to reject encoded credentials as
  well as the raw values. Never publish a live log even after filtering.
- Later acceptance needs a locally configured HTTP profile and working DoH and UDP resolver
  entries. A SOCKS5-only or Noise-only upstream pass cannot satisfy the HTTP acceptance item.
