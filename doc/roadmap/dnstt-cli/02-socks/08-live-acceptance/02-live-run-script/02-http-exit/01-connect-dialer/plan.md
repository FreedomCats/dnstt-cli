---
kind: task
status: in-progress
detailed: true
---

# HTTP CONNECT dialer

## Task

Implement the protocol-only HTTP exit selected by the user: an `exit.Dialer` that turns a
tunnel stream into a TCP connection through an HTTP proxy using CONNECT and Basic
authentication. `exit.OpenStream` already represents the fixed service behind the dnstt
server; the dialer must not open a direct proxy socket or introduce a proxy URL.
See `architecture.md` R3 and §04 and the parent HTTP exit stage.

Basic is the only authenticated HTTP scheme here. No negotiation of Digest, NTLM or other
schemes, no HTTPS-to-proxy or HTTP/2 proxy mode, no server changes, no SOCKS5 fallback and
no changes to the local datapath are included. Profile/app integration and the selected
lazy readiness contract belong to the next task. This task needs only the standard library
and compatible project code; do not inspect or copy incompatible third-party code.

## Steps

1. **Add the implementation.** Add `internal/exit/http.go` with an HTTP implementation of
   `exit.Dialer`, using `OpenStream`, user and password fields like the SOCKS5 dialer.
   Update the protocol-neutral package comment in `internal/exit/exit.go` if necessary;
   leave the `Dialer`/`OpenStream` signatures and SOCKS5 login/check semantics unchanged.
2. **Validate the target and credentials before opening a stream.** Accept `host:port`
   with a numeric port in 1–65535. Preserve a hostname for remote resolution; format IPv4
   and bracketed IPv6 authority correctly. Reject missing hosts/ports, IPv6 zones, whitespace,
   control characters and URL/userinfo/path syntax that could alter a CONNECT request. Do
   not call a local DNS resolver. Apply RFC 7617 credential restrictions: no colon in the
   username, no control characters in either credential, and no password without a user.
   Validation errors name only the invalid field, not its value. Preserve existing SOCKS5
   validation rather than imposing HTTP rules on it.
3. **Send CONNECT and Basic authentication.** Send `CONNECT <authority> HTTP/1.1` and a
   matching `Host` header on the tunnel stream, without a request body. With a configured
   user, send `Proxy-Authorization: Basic <base64(user + ":" + password)>`; without a user
   omit the header. Handle a short/failed write. Do not log requests, credentials or the
   encoded token, add another auth scheme, or downgrade to SOCKS5 after refusal.
4. **Parse the response and preserve target bytes.** Bound the complete response header
   and status-line size, not just each read buffer. Parse an HTTP/1.x response with standard
   library facilities where appropriate; handle bounded informational responses before the
   final status and accept final 2xx CONNECT success. On success, do not consume a response
   body or apply HTTP body framing: subsequent bytes are the target stream, even if a proxy
   sends body-length headers. Return a `net.Conn` wrapper that drains any buffered target
   bytes before reading the underlying stream and forwards deadlines and `Close`.
5. **Classify failures without private response text.** A complete 407 is an
   `exit.LoginError` with a fixed HTTP-authentication message; other final non-2xx statuses
   return a typed HTTP CONNECT refusal carrying the numeric status only, not a SOCKS5 reply
   code. A demonstrably non-HTTP/malformed/oversized response is a stable protocol-error
   category. EOF, truncation and transport interruption must remain identifiable as stream
   failures, not proof of a wrong password or protocol. Do not print parser input, reason
   phrases, response headers, proxy challenges or response bodies. Never forward
   `Proxy-Authorization` into target traffic.
6. **Honor context, deadlines and close behavior.** Bound stream acquisition and the whole
   exchange by the caller context and the existing exit handshake limit of 60 seconds
   (`internal/exit/socks5.go:45`). Set the deadline before a potentially stalled write,
   arrange cancellation to unblock reads/writes, handle the completion/cancellation race,
   and close the stream on every failure. On success remove the cancellation hook and
   clear handshake deadlines. Canceling the Dial context after success must not break the
   established connection; the returned connection's `Close` closes the stream.
7. **Add focused protocol tests.** Add `internal/exit/http_test.go` and an independent test
   proxy fixture in `internal/exit/http_server_test.go` if useful. Cover names, IPv4,
   bracketed IPv6, invalid authorities/injection, invalid credentials before `Open`,
   authenticated and unauthenticated success, the exact Basic header, 407, other statuses,
   malformed/oversized/truncated responses, informational responses, short writes, target
   bytes in the same write as the header, silent proxy, cancellation/deadline expiry,
   cancellation after success and stream closure. Use made-up credentials and assert raw
   user/password/token and malicious response markers are absent from errors/diagnostics.
8. **Verify the actual tunnel path.** Add an HTTP variant in
   `internal/exit/tunnel_test.go` or `internal/exit/http_tunnel_test.go`: a generated-key
   `core/server.StartTest` hands streams to a loopback Basic-authenticated HTTP proxy fixture
   that maps a reserved hostname to an echo server. Dial through `client.Session.OpenStream`
   and exchange data; test refusal and a subsequent successful stream on the same session.
   Keep existing SOCKS5 tests passing; no profile, real secret store or external DNS is used.
9. **Run checks.** Run `go test -race -count=1 ./internal/exit`, then
   `go test -race -count=1 ./...` and `CGO_ENABLED=0 go build ./...`. Use the CI vet split
   from `.github/workflows/linux.yml`: full vet for non-core packages and
   `go vet -testinggoroutine=false ./core/...` only for upstream's known warnings.
   Check `GOOS=windows GOARCH=amd64 go build ./...` and the supported Windows
   vet/test-package compilation. On Windows, run the normal tests from
   `.github/workflows/windows.yml`; cross-compilation alone is not Windows runtime evidence.

## Done when

- The HTTP dialer sends an authority-form CONNECT and optional Basic header through the
  supplied tunnel stream; target hostnames are resolved only by the server-side proxy.
- A final 2xx returns a working TCP connection without losing bytes after the response
  header. Other statuses, protocol errors and incomplete exchanges are distinguishable
  without leaking credentials or peer-supplied text.
- Authentication refusal, failed writes, malformed/oversized replies, timeout and
  cancellation close the stream; successful connections outlive the Dial context and have
  no handshake deadline left.
- Unit and in-process dnstt integration tests pass with generated identities, synthetic
  credentials and loopback services. Existing SOCKS5 tests and project Linux/Windows
  build/test checks remain green under the established vet invocation.

## Risks and what is needed from the user

- HTTP parsers buffer beyond the final header; dropping that buffer loses TLS/SSH/banner
  bytes from targets that speak first. A bounded reader must not truncate the later tunnel
  payload. Successful CONNECT ignores HTTP message-body framing (RFC 9110 §9.3.6).
- smux writes need a deadline set before the write, not only after cancellation. Reuse the
  existing SOCKS5 lifetime convention, not its wire protocol or error text.
- No live access, profile creation or secret-store write is required. The next task wires
  the selected lazy `Connected` meaning and fatal 407 behavior into the application.
