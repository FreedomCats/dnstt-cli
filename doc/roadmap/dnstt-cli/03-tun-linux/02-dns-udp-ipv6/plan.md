---
kind: task
status: done
detailed: true
depends: [01-netstack-tcp]
---

# DNS over TCP, other UDP and IPv6

## Task

The rest of the netstack behaviour (`architecture.md` R7, §05, §05a): UDP :53 from the
adapter is not answered locally but carried as a DNS-over-TCP exchange through
`exit.Dialer` to the tunnel's DNS (`tun_dns`); any other UDP is refused at once with ICMP
"port unreachable", so QUIC falls back to TCP without a timeout; IPv6 is not carried. The
TCP forwarder and the netstack come from [Netstack TCP](../01-netstack-tcp/plan.md); this
child adds the UDP side and the DNS client.

`tun_dns` is the address behind the server; the system gets the adapter's own address
instead (the next child). The DNS handler therefore listens on the adapter address, and
every query is one stream to `tun_dns:53`.

Two existing pieces keep the change small:

- UDP is registered as a transport protocol with **no default handler**. A UDP endpoint
  bound to the adapter's address on port 53 takes the DNS queries; every other datagram
  finds no endpoint, and gVisor's IPv4 layer itself sends ICMP destination unreachable,
  code port unreachable, with the original IP header and at least the first 8 bytes
  (RFC 1122 §3.2.2.1, quoted at `ipv4.go:1354` in the pinned gVisor). No ICMP is built by
  hand, and no stream is opened.
- `golang.org/x/net/dns/dnsmessage` is already a direct dependency (used by
  `internal/resolvers/bootstrap.go`): framing and the SERVFAIL reply need no new module.

The exchange produces no log line: DNS names are browsing history. Nothing in this child
touches the adapter, `connect`, the supervisor, nftables or the profile; those are the
later children of the stage.

## Steps

1. **The DNS exchange** — new file `internal/datapath/dns.go`, the file `architecture.md`
   §03 already names for "DNS over TCP through the exit":
   - constants, each with its reason next to it: `dnsPort = 53`, `dnsHeaderLen = 12`
     (RFC 1035 header; a shorter datagram is not a query), `dnsMaxSize = 1<<16 - 1`,
     `dnsQueryTimeout = 10 * time.Second` (a stub resolver gives up in seconds, much
     earlier than the 60 s of a TCP dial; a later answer is useless), `dnsInFlight = 64`
     (a resolver has a handful of queries at once; each exchange is a tunnel stream).
   - `dnsExchange(ctx, dialer, upstream, query)`: one
     `dialer.Dial(ctx, net.JoinHostPort(upstream.String(), "53"))`; write
     `binary.BigEndian.AppendUint16(nil, uint16(len(query)))` followed by the query in one
     `Write`; read the two-byte length; reject 0 or anything below `dnsHeaderLen` as a bad
     prefix; `io.ReadFull` the answer; reject an answer whose first two bytes differ from
     the query's ID or whose QR bit is clear. Close the stream on every path (`defer`).
     Bound the whole exchange by `ctx`: use
     `context.AfterFunc(ctx, func() { conn.SetDeadline(time.Unix(1, 0)) })` so both the
     timeout and `close` unblock a read (the same pattern as
     `internal/resolvers/bootstrap.go:327`).
   - `dnsServfail(query []byte) []byte`: parse the header and the questions with
     `dnsmessage`; build a response with the same ID, `Response: true`, the same opcode and
     RD bit, `RCode: dnsmessage.RCodeServerFailure`, and the questions echoed (a stub
     resolver matches the question, not only the ID). If the question section does not
     parse, a header-only SERVFAIL with the same ID. Nil only when even the 12-byte header
     does not parse; the listener has already required the length.
   - No logging of queries or answers anywhere in this file.

2. **The UDP side** — `internal/datapath/netstack.go`:
   - `newNetstack(link stack.LinkEndpoint, dialer exit.Dialer, tunDNS netip.Addr)`: add
     `udp.NewProtocol` next to `tcp.NewProtocol` in `TransportProtocols`; leave the UDP
     default handler unset (the Task section explains why that is the ICMP path). Keep
     `tunDNS` in the struct; refuse a set address that is not IPv4 or is unspecified. The
     zero `netip.Addr` means "no DNS listener" — the stacks of the TCP tests.
   - `addAddress`: after `AddProtocolAddress` succeeds, if `tunDNS` is valid, bind the DNS
     listener with `gonet.DialUDP` on the `tcpip.FullAddress{NIC: nicID, Addr:
     tcpip.AddrFrom4(a.As4()), Port: dnsPort}`, no remote address and `ip4.ProtocolNumber`
     as the network protocol (the address must be assigned first; this is why the listener
     starts here and not in the constructor), record the address it is bound to, and start
     `serveDNS` through `start()` so `close` cannot race with the goroutine. If the bind or
     `start` fails, remove the address again and return the error: the caller's `Start` has
     a clear reason to fail. A second `addAddress` for a different address while the
     listener is bound is an error, not a silent rebind.
   - `serveDNS`: one `ReadFrom` loop into a 64 KiB buffer. A datagram shorter than
     `dnsHeaderLen` is dropped (no answer: it is DNS traffic, not "other UDP", so no ICMP
     either). Copy the datagram before spawning, take a slot from a `dnsInFlight` channel;
     when no slot is free, write a SERVFAIL at once instead of queueing (a retry is
     cheaper than a queue that outlives the client). Otherwise a goroutine:
     `ctx, cancel := context.WithTimeout(n.ctx, n.dnsTimeout)`, `dnsExchange`, on any error
     `dnsServfail`, `WriteTo` the client from the address `ReadFrom` returned, release the
     slot, `wg.Done`. The read loop itself never blocks on the tunnel.
   - `n.dnsTimeout` is a field defaulted from `dnsQueryTimeout`; tests lower it to tens of
     milliseconds. It is not a profile field and not a flag. Update the `wg` and `start`
     comments: the wait group now covers the DNS read loop and the exchanges as well as the
     TCP streams.
   - `removeAddress`: close the listener when it is bound to `a`, clear it, then call
     `RemoveAddress`. `close`: close the listener before `stack.Close()` so `ReadFrom`
     returns, then cancel the context, set `closed` and wait for `wg` as now. A read error
     after the close is expected and not logged.
   - `inject`: keep the version check; update its comment — the IPv6 rule is complete here,
     and the next child's `fdbased` endpoint needs no second filter because no IPv6 network
     protocol is registered.

3. **DNS unit tests** — new file `internal/datapath/dns_test.go`, package `datapath`:
   - `dnsServfail` gives a parsable response with the query's ID, QR set, RCode SERVFAIL
     and the question echoed; a query that is only a 12-byte header (QDCOUNT 0) gives a
     parsable header-only SERVFAIL.
   - `dnsExchange` against a fake DNS-over-TCP server on `net.Pipe`: the query is written as
     one length-prefixed frame, the answer is returned unchanged, and a zero or short
     length prefix, an answer with another ID or without the QR bit, a stream that ends
     before the answer and a `ctx` that expires while the server is silent each return an
     error instead of blocking.
   - Generated data only: a query built with `dnsmessage` for `t.example.com.`, documentation
     addresses, no resolver and no service.

4. **Netstack UDP tests** — `internal/datapath/netstack_test.go`:
   - `newTestNetstack` gains a `tunDNS netip.Addr` parameter; the existing calls pass the
     zero address. `newPair` keeps its signature and passes zero to both stacks; a new
     `newDNSPair` gives the server stack the documentation `tun_dns` (`203.0.113.53`) and
     the client none. `addAddresses` stays as it is.
   - DNS through the exit, end to end: a UDP query from the client stack to the adapter's
     address `192.0.2.2:53`; the fake `exit.Dialer` records its target and returns one end
     of a `net.Pipe` whose other end speaks DNS over TCP and answers with a crafted A
     record. Assert: the exit saw `203.0.113.53:53`, not the adapter's address; exactly one
     dial and one length-prefixed exchange for one query; the client got the answer as one
     UDP datagram from `192.0.2.2:53` with the query's ID, the same payload the fake DNS
     server wrote, and the A record parseable from it.
   - Bounded failures, end to end: a dialer that refuses; a fake DNS server that writes a
     zero or short length prefix; one that answers with another ID; a silent server with
     `server.dnsTimeout = 50 * time.Millisecond`. Each gives the client a UDP reply with
     the query's ID and `RCodeServerFailure` and returns without a hang.
   - A 5-byte datagram to `192.0.2.2:53` gets no reply within a short bound and opens no
     dial.
   - A slow exchange does not block the read path: the fake DNS server delays the answer
     for `slow.t.example.com.` by a couple of hundred milliseconds and answers
     `fast.t.example.com.` at once; the client sends both, and the fast answer arrives
     first. Several concurrent queries each get their own answer.
   - Other UDP: a one-way pump records the client's UDP datagram to `203.0.113.7:443` and
     delivers it to the server stack; the test reads the server's outbound packet (the
     `next` helper of the link in the tests) and parses it with `header.IPv4` and
     `header.ICMPv4`: type 3, code 3, source `203.0.113.7` (the original destination),
     destination `192.0.2.1`, and a payload equal to the original IPv4 header plus at least
     the first 8 bytes of the datagram — the whole datagram in this test. The fake exit saw
     no dial. A second datagram to the adapter's address `192.0.2.2` on port 5353 gives the
     same ICMP.
   - IPv6: an IPv6 datagram (`2001:db8::/32` header) injected into the server stack
     produces no outbound packet within a short bound and no dial.
   - Close with a query in flight: the query ends without a panic and no datapath goroutine
     survives; reuse the goroutine wait loop of `TestNetstackCloseLeavesNothing`.

5. **Checks.** `gofmt` on the changed files; `go vet ./...` (full vet still reports only
   the two pre-existing warnings in `core/noise`, unchanged); `go test -race -count=1
   ./internal/datapath` twice and `go test -race -count=1 ./...`; `CGO_ENABLED=0 go build
   ./...` and `GOOS=windows CGO_ENABLED=0 go build ./...`. Both new files are plain Go:
   no build tags, no `unix`, no syscalls, so the Windows cross-build must stay green. No
   new module and no download: `golang.org/x/net/dns/dnsmessage` is already direct.

## Done when

- A UDP DNS query to the adapter's address from the test client reaches a fake DNS server
  behind a fake exit as one length-prefixed DNS-over-TCP exchange to `tun_dns:53`, and the
  answer comes back to the client as UDP with the query's ID and payload; the exit is
  dialed at `tun_dns:53`, never at the adapter's address.
- The exchange is bounded: a dead tunnel, a zero or short length prefix, an answer with
  another ID and a timeout each answer SERVFAIL with the query's ID; a datagram shorter
  than a DNS header is dropped with no reply. The exchange runs in its own goroutine, so a
  slow query delays no other one, and no more than `dnsInFlight` (64) are in flight; the
  rest are refused at once with SERVFAIL.
- Any other unicast UDP packet produces an ICMP type 3 code 3 reply from the original
  destination, carrying the original IPv4 header and at least the first 8 bytes of the
  datagram; no stream is opened and no DNS handler is involved. Multicast and broadcast
  datagrams get no ICMP error (RFC 1812 §4.3.2.7) and are otherwise out of scope.
- An IPv6 packet never reaches transport: `inject` drops it by IP version and the stack
  registers no IPv6 network protocol; no reply, no dial. The outward IPv6 block by
  nftables remains in the later child.
- The UDP and DNS tests run with no root and no TUN device, on gVisor channel endpoints,
  under `-race`; `gofmt`, the CI vet split, `go test -race ./...`, the Linux build and the
  Windows cross-build pass. No new dependency and no fetch.

## Depends on

[Netstack TCP](../01-netstack-tcp/plan.md). The stage's [foundation](../../01-foundation/plan.md)
is done.

## Outcome

The UDP side of the netstack and the DNS client are in `internal/datapath`; the task's
checks pass and no module file changed.

- `internal/datapath/dns.go`: `dnsExchange` — one dial to `tun_dns:53`, one length-prefixed
  write, the length read, zero or short prefixes rejected, the ID and QR bit checked, a
  `context.AfterFunc` deadline so a timeout or close unblocks the read, and the stream
  closed on every path. `dnsServfail` keeps the query's ID, sets Response, the opcode and
  RD bit and SERVFAIL, and echoes the questions; a corrupt question section gives a
  header-only answer, and only a datagram below the header gives nil. No query or answer
  is logged.
- The stack registers UDP next to TCP with no default handler: a datagram that finds no
  endpoint is answered by gVisor's IPv4 layer itself with ICMP type 3 code 3. IPv6 is not
  registered, and `inject` drops it by IP version. `newNetstack` takes `tunDNS`; the zero
  address means no DNS listener, a set address must be IPv4 and not unspecified.
- `addAddress` binds the DNS listener on the adapter's address and port 53 after the
  address is assigned, and starts its read loop under the same lock as `close`; a bind
  failure removes the address, and a second address while the listener is bound is an
  error and is removed too. `removeAddress` closes the listener bound to the address
  before removing it. `close` takes the listener and sets `closed` in one critical
  section, closes the listener, cancels the context, closes the stack and waits for the
  read loop, the exchanges and the TCP relays. This differs from the plan's wording in
  one order only: `closed` is set before the listener is closed. The plan's order leaves a
  window in which `addAddress` passes its check and binds a new listener that `close`
  would then miss.
- `serveDNS` reads into a 64 KiB buffer, drops a datagram below the DNS header without an
  answer, copies the datagram, takes one of `dnsInFlight` slots or answers SERVFAIL at
  once when none is free, and exchanges in its own goroutine. `dnsTimeout` is a field
  defaulted to `dnsQueryTimeout` (10 s); tests lower it.
- Tests: unit tests of `dnsExchange` against a `net.Pipe` DNS-over-TCP server and of
  `dnsServfail`; two stacks joined by channel endpoints cover end-to-end DNS through a
  fake exit (one dial to `203.0.113.53:53`, the answer back as one UDP datagram from the
  adapter's address), every failure path answering SERVFAIL with the query's ID, a short
  datagram getting no answer and no dial, slow and fast queries, concurrent queries, the
  64-slot limit and the release of a slot by a finished exchange, ICMP type 3 code 3 with
  the original packet, IPv6 producing nothing, the DNS listener across `removeAddress`
  and a refused second `addAddress`, and close with an exchange in flight leaving no
  goroutine.
- `gofmt` is clean; the CI vet split passes (full `go vet ./...` still fails only on the
  two pre-existing `core/noise` warnings); `go test -race -count=1 ./internal/datapath`
  and the full `go test -race -count=1 ./...` pass; `CGO_ENABLED=0 go build ./...` and
  `GOOS=windows CGO_ENABLED=0 go build ./...` succeed. `go.mod` and `go.sum` are
  unchanged: no new dependency and no fetch.
- Independent review found no defects in the code; two test-completeness remarks were
  accepted and fixed: the in-flight test now proves that a finished exchange releases its
  slot, and a new test covers the listener across `removeAddress` and a refused second
  `addAddress`.
- Next: [TUN adapter, routes and system DNS](../03-tun-adapter/plan.md).

No real problem was left incompletely fixed because of cost.
