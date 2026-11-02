---
kind: task
status: done
detailed: true
---

# CI

## Task

Two workflows, as in csqtt-cli, so that a change is tested on both platforms and the core
is checked against the mirror on every push. `E0`, `F5`.

## Steps

1. `.github/workflows/linux.yml` and `.github/workflows/windows.yml`; actions pinned by
   commit hash.
2. `test` — `go vet ./...`, `go test -race ./...`; on Linux with `DNSTT_CLI_TEST_DBUS=1`.
3. `netns` (Linux) — the `linuxsys` tests that change the network, in a container with
   `DNSTT_CLI_TEST_NETNS=1`; a skip counts as a failure.
4. `core` — clone the mirror at the commit from `core/CORE_VERSION`, then
   `go run ./scripts/vendorcore -check`.
5. No Cargo and no core build step: the core is Go and built by `go build`.

## Done when

Both workflows are green on `main`.

## Outcome

Green on `main`: `linux` (test, netns, core) and `windows` (test, core). The `netns` job
turns a skip into a failure for its own tests only; the IPv6-dependent `netguard` test is
skipped by the plain `test` job on kernels without IPv6, and CI runs without `-v`, so it is
not yet confirmed that it runs there rather than skips. Left open in the root.
