---
kind: stage
status: done
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
The completed children and integration review establish local tooling acceptance only;
the live-runs stage retains the authenticated server and platform gates.

The HTTP profile-integration child uses the selected lazy readiness decision: HTTP
authentication starts on the first real application `CONNECT`, `Connected` before that
request means tunnel readiness only, and a final 407 stops the supervisor. HTTP CONNECT and
Basic are already approved; no startup target or new profile field is introduced.

## Outcome

All children and the integrated local acceptance are complete. The SOCKS5 baseline is
recorded by `c31d81c`; the HTTP product work by `4fd6506`, `9d64312`, `c140146` and its
stage acceptance `056ba4a`; the independent HTTP comparison by `cd76955`. Review covered
`348ed5b..cd76955`, all 31 changed tracked files and the complete saved ignored tool.
It found no new integration defect or inconsistency and required no further code change.

- The ignored Go tool provides `compare`, `secret`, `selftest` and help. Comparisons run
  upstream and then the application serially with the selected resolver, retaining
  all-attempts-required verdicts and exit codes. Safe profile diagnostics, secret-store
  read-back, unchanged profile files and private log filtering remain covered.
- The product supports HTTP/1.1 CONNECT with Basic using the existing exit user and
  password reference. Authentication starts with the first real application CONNECT;
  earlier `Connected` means tunnel readiness only. A final 407 stops the supervisor
  without resolver retry. SOCKS5 behavior and the deferred SSH choice are preserved.
- The upstream HTTP control path uses an independent standard-library CONNECT helper;
  the upstream binary remains a raw TCP forwarder. The application probe uses its local
  unauthenticated SOCKS5 listener and exercises the real product HTTP exit. Both paths
  fetch complete known HTTP and HTTPS bodies through generated local fixtures.
- Reviews corrected concurrent stream reads and authentication-failure shutdown in the
  product, and timeout, response-version, CONNECT framing and response-text validation
  in the independent helper. Child Outcomes retain the details and regression evidence.
  No finding remains unresolved, and no new review finding was rejected at integration.
- Tool and applicable project race suites passed, including mandatory isolated D-Bus
  tests. Static product build, tool Linux/Windows vet, exact Linux/Windows CI vet split,
  Windows product/tool builds and compilation of the tool and all 19 applicable project
  test packages passed. Formatting, range whitespace, package-list exclusion and Git
  ignore checks passed; all saved tool files remained unchanged during acceptance.
- Loopback selftest and supplemental isolated execution verified both exits with HTTP
  and HTTPS during server-up and server-down phases, with generated identity and
  credential filtering. The normal selftest's down loop remains HTTPS-only; a temporary
  overlay supplied the additional HTTP-down evidence without changing saved source.
  Tests used synthetic profiles, temporary isolated stores and generated keys and
  certificates. No real profile, secret store or its metadata was accessed.
- Four external TCP/DNS tests were excluded from the applicable project race suite:
  `TestDualStackListener`, `TestLoopbackIndexBlocks`, `TestPhysicalIndexPasses` and
  `TestFamilyWithoutRouteRefuses`. Netns and network-mutating checks were not run.
  The Windows-only external-DNS test was compiled without execution. Plain full vet
  retains two known upstream noise-test warnings; the prescribed split passes.
- The tool documents the local 255-byte `secret` input limit, different default uTLS
  distributions and the unverifiable upstream revision. No new profile field, dependency
  or hidden startup target was introduced. The upstream-only domain/resolver argument
  exception and transient private public-key file remain explicit; logs stay outside Git.

Authenticated DoH/UDP comparisons, the SOCKS5 live matrix, measured timeouts, the `danted`
question and actual Windows runtime/DPAPI remain later gates. Local acceptance does not
establish them. The pre-existing signal-reader goroutine in `internal/cli/connect.go`
may remain blocked after normal return; review confirmed it unchanged and not worsened,
so it remains a separate follow-up. No real problem was left partly fixed because of cost.
