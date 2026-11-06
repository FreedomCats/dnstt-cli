---
kind: task
status: done
detailed: true
---

# Network resolver support

## Task

Make the R5 `network` resolver usable by both `connect` and `resolvers check` on Linux and
Windows, and make resolver host names without pins resolve through the configured bootstrap
server order. This fixes the default profile path as well as explicit `network` entries before
live acceptance. See [`architecture.md`](../../../../../architecture.md), R5 and §04.

## Steps

1. Wire the system DNS readers into the application. In `internal/app/tunnel.go`, replace
   the `NetworkDNS` stub with platform-specific calls to `linuxsys.NetworkDNS()` and
   `winsys.NetworkDNS(0)`, using platform-specific helper files if needed so the common app
   package builds on Linux, Windows, and other supported build targets. Keep DNS lookup
   dynamic so a changed system resolver list is observed on subsequent dials.
2. Connect bootstrap resolution in `internal/app/tunnel.go` to the current network DNS and
   `resolvers.BootstrapServers`, preserving the documented R5 precedence: profile bootstrap
   servers alone when configured; otherwise network DNS followed by address-based UDP
   resolvers. Pass the bootstrap lookup to the supervisor so unpinned resolver host names
   use it, while pinned and literal-address resolvers do not need it.
3. Update `internal/cli/resolvers.go` so `resolvers check` resolves the `network` entry to
   the network's DNS servers and constructs the same bootstrap lookup for every resolver.
   Report network-DNS lookup failures as failures for the affected resolver rather than
   silently skipping `network`; retain the command's existing handshake and exit-code
   behavior.
4. Add focused tests in `internal/app` and `internal/cli` (and platform-specific tests where
   needed): `network` expands to usable network resolvers; unpinned names resolve through
   explicit bootstrap servers or the network/UDP fallback in R5 order; pinned/address
   resolvers do not trigger bootstrap; DNS-reader errors are surfaced. Use injected or local
   test DNS rather than live resolvers.
5. Run formatting and the full Go checks, including Linux tests and Windows cross-build/tests
   supported by the repository. Verify `connect` and `resolvers check` compile for both
   platforms and that no test requires external DNS.

## Done when

- `connect` accepts `network` on Linux and Windows and uses the system DNS list available at
  each resolver dial; the default empty resolver list therefore works as `network`.
- `resolvers check` attempts `network` instead of skipping it, and unpinned DoH/DoT host
  names use the same R5 bootstrap precedence as `connect`.
- Tests cover network DNS, both bootstrap precedence paths, pinned/address bypass, and
  network-DNS errors without querying live DNS.
- `go test ./...` passes, and the Windows build/test check used by CI passes.

## Risks and what is needed from the user

- Linux and Windows system-DNS readers have different APIs; keep their selection behind
  build-tagged files and retain a clear unsupported-platform error.
- The network DNS reader must be called dynamically, not captured once when the tunnel is
  built, so network changes do not leave bootstrap with stale servers.
- No live access is needed for this task. The subsequent live-run tool and acceptance runs
  verify behavior against the user's environment.

## Outcome

Implemented R5 network resolver support in `connect` and `resolvers check`. Linux and
Windows read their system DNS through platform-specific helpers; unsupported platforms
return an explicit error. The network list is read dynamically for resolver dials and
bootstrap cache misses. Profile bootstrap servers take precedence by themselves; otherwise
bootstrap uses network DNS followed by address-based UDP resolvers. Pinned and literal
addresses bypass bootstrap. `resolvers check` attempts each network DNS server and reports
system DNS failures as failed checks.

Focused tests cover resolver expansion, bootstrap precedence and fallback, dynamic DNS
reads, pinned and literal-address bypass, and DNS-reader errors using injected or local DNS.
`go test ./...`, `go test -race -count=1 ./...`, Linux build and vet checks, Windows amd64
build and vet checks, and Windows test-package compilation passed. Windows tests were not
executed on Windows. No live access was needed.
