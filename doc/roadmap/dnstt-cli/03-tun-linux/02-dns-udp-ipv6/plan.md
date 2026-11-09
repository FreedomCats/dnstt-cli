---
kind: task
status: todo
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

## Done when

- A UDP DNS query to the adapter's address from the test client reaches a DNS server
  behind a fake exit as one length-prefixed DNS-over-TCP exchange, and the answer comes
  back to the client as UDP with the query's ID and payload.
- The exchange is bounded: a dead tunnel, a bad length prefix or a timeout answers
  SERVFAIL with the query's ID; a datagram shorter than a DNS header is dropped. No query
  blocks the netstack's packet path: the exchange runs in its own goroutine with an
  in-flight limit.
- Any other UDP packet to any address produces an ICMP type 3 code 3 reply carrying the
  original IP header and the first 8 bytes of the datagram; no stream is opened and no
  DNS handler is involved.
- An IPv6 packet is dropped before it reaches the stack (the link reader checks the
  version); no stream and no reply. IPv6 is closed outward by the nftables rules in the
  later child; the netstack is the second line.
- The UDP and DNS tests run with no root on a channel link, under `-race`; `go vet ./...`,
  the Linux build and the Windows cross-build pass.

## Depends on

[Netstack TCP](../01-netstack-tcp/plan.md).
