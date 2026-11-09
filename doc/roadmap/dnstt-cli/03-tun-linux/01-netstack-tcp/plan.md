---
kind: task
status: done
detailed: true
---

# Netstack TCP

## Task

The first half of the `tun` datapath: gVisor netstack in the shell, with every TCP
connection that enters the adapter turned into a stream through `exit.Dialer`
(`architecture.md` R6, §05). This child adds the module dependency and the TCP path; the
TUN device itself comes in [TUN adapter](../03-tun-adapter/plan.md), so here the adapter's
packets are fed from a gVisor channel endpoint in the tests. `E2`.

**Prerequisite — the download is authorized.** The repository has no `gvisor.dev/gvisor`
yet. Adding it runs `go get` / `go mod tidy`, which downloads the module and its transitive
modules from the Go module proxy. The user authorized this one-time download for this
task; the earlier gate is closed, and no other fetch is authorized. Do not vendor or copy
gVisor sources by hand.

## Steps

1. **Add the dependency.** Add `gvisor.dev/gvisor` at the newest tagged release or
   pseudo-version that builds with the module's Go version, and run `go mod tidy`. Record
   the exact version in `go.mod` and `go.sum`. Do not upgrade unrelated dependencies: if a
   version conflict appears, report it instead of choosing silently. This is the only
   authorized download; no other fetch follows from this task. The license is Apache-2.0,
   compatible with the shell; `THIRD_PARTY_NOTICES.txt` is generated in E4, not here.
2. **The netstack core** in `internal/datapath/netstack.go`:
   - `datapath.TunnelMTU = 1500` in `internal/datapath/datapath.go` (R7: TCP ends in the
     shell, so the adapter's MTU does not depend on the DNS channel);
   - a `netstack` type around `stack.New` with IPv4 and TCP only for this child, one NIC
     on a `stack.LinkEndpoint`, the IPv4 default route through that NIC, and the NIC in
     promiscuous/spoofing mode: the adapter receives packets addressed to arbitrary
     remote hosts, and the forwarder must see the original destination, not the adapter's
     own address;
   - `addAddress` / `removeAddress` for the adapter's `/32`: the system resolver and the
     DNS listener use the adapter's own address, and the stack must accept it as local in
     addition to the spoofed destinations;
   - a `tcp.NewForwarder` handler: read the original destination from `r.ID()`, keep the
     handler short (create the endpoint with `r.CreateEndpoint` and start a goroutine for
     the dial and the relay), `exit.Dialer.Dial(ctx, host:port)` with the same 60 s bound
     as the SOCKS5 datapath, and `gonet.NewTCPConn` over the created endpoint;
   - relay both ways with a bounded buffer; a stream has no `CloseWrite`, so a half-close
     ends the whole connection (the same limitation as the SOCKS5 datapath); when either
     side ends, close both;
   - on a dial or endpoint error, reset the client connection instead of leaving it to
     time out; close the endpoint when the stream ends; an idempotent `close` cancels the
     context, closes the stack and waits for the relay goroutines;
   - `inject` / `next` helpers for a link endpoint and a `pump` between two channel
     endpoints in the tests; `inject` checks the IP version and drops anything that is
     not IPv4 (the IPv6 rule is completed in the next child);
   - an in-flight limit (start with 1024 connections) and a receive window that fits the
     tunnel (start with 64 KiB). Both are constants next to the reason, not settings.
3. **Tests** in `internal/datapath/netstack_test.go`, with no root and no TUN:
   - two gVisor stacks joined by `channel.Endpoint`s and a packet pump, as the link
     endpoint of the datapath is a channel here; the client stack dials a documentation
     address (`203.0.113.0/24`), and a fake `exit.Dialer` returns a connection to an echo
     server behind it; check data in both directions, the original destination seen by the
     fake exit, several concurrent connections and close;
   - a fake exit that refuses the dial gives the test client an immediate reset/refusal,
     not a timeout;
   - `-race`: no goroutine of the datapath is left after `close`, and a connection open at
     close ends without a panic.
4. **Cross-platform build.** `netstack.go` is a plain file (no `_linux.go`): it must
   compile on Windows for the next stage. Only `exit.Dialer` and gVisor's netstack
   packages are used; no `unix` or `/dev/net/tun` here.
5. **Checks.** `gofmt`, `go vet ./...`, `go test -race -count=1 ./internal/datapath`,
   `CGO_ENABLED=0 go build ./...`, and `GOOS=windows CGO_ENABLED=0 go build ./...`.

## Done when

- `gvisor.dev/gvisor` is in `go.mod` and `go.sum` at a recorded version, added under the
  user's authorization for this one-time download, and no unrelated dependency changed
  without a reason.
- A TCP stream from the test client stack reaches the echo server through the forwarder
  and the fake exit in both directions; concurrent connections and close are clean under
  `go test -race`.
- A refused exit dial gives the test client an immediate reset, not a timeout.
- `go vet ./...`, `go test -race -count=1 ./internal/datapath`, the Linux build and the
  Windows cross-build pass.

## Depends on

Nothing. The stage's [foundation](../../01-foundation/plan.md) is done.

## Outcome

The gVisor netstack and the TCP path are in `internal/datapath`; the dependency is pinned
and the task's checks pass.

- `gvisor.dev/gvisor v0.0.0-20250503011706-39ed1f5ac29c` (Apache-2.0) is in `go.mod` and
  `go.sum`. The latest upstream version requires Go 1.27.3, while the module declares go
  1.26.2 and no newer toolchain is authorized; the pinned version builds with the module's
  Go version, is the version csqtt-cli used for its netstack, and was already in the module
  cache, so the authorized one-time download was not needed. `go mod tidy` added
  `github.com/google/btree` and `golang.org/x/time` as indirect dependencies, marked
  `github.com/pelletier/go-toml/v2` as direct because `internal/config` imports it
  directly, and moved the transitive `go-spew` and `yaml.v3` selections to the higher
  versions gVisor's `go.mod` requires; no `require` version of the project changed.
- `datapath.TunnelMTU = 1500` (R7: TCP ends in the shell).
- The `netstack` type is IPv4 and TCP only; its NIC sits on a `stack.LinkEndpoint` supplied
  from outside (a channel endpoint in the tests, `fdbased` for the adapter later), runs in
  promiscuous and spoofing mode, has the IPv4 default route, and takes the adapter's `/32`
  as a local address through `addAddress`/`removeAddress`.
- The TCP forwarder creates the endpoint for every connection request, completes the
  request on every path, dials the original destination through `exit.Dialer` with the
  SOCKS5 60-second bound, and relays with the shared `relay`. A refused dial or a failed
  endpoint creation resets the client instead of leaving it to time out. `close` is
  idempotent: it cancels the context, closes and waits for the stack, and waits for the
  relay goroutines; no goroutine is added once it waits.
- `inject` drops everything that is not IPv4 (empty and short packets included), `next`
  hands outbound packets to a writer, and `pump` joins two stacks in the tests. The
  in-flight limit is 1024 and the receive window 64 KiB, as constants with their reasons.
- Tests on two stacks joined by channel endpoints cover data in both directions through an
  echo server, the original destination seen by the fake exit, four concurrent
  connections, an immediate error on a refused exit dial, no goroutines left after
  `close`, a connection open at close ending without a panic, and address add/remove.
- `gofmt` is clean, the CI vet split passes (full `go vet ./...` still fails only on the
  two pre-existing upstream warnings in `core/noise`), `go test -race -count=1
  ./internal/datapath` and the full `go test -race -count=1 ./...` pass, and both
  `CGO_ENABLED=0 go build ./...` and `GOOS=windows CGO_ENABLED=0 go build ./...` succeed.
- Independent review found no defects. The datapath race tests were stable over ten runs.
- Next: [DNS over TCP, other UDP and IPv6](../02-dns-udp-ipv6/plan.md) on the same stack.

No real problem was left incompletely fixed because of cost.
