---
kind: task
status: done
depends: [01-stream-client, 02-profile-and-wiring]
---

# Live acceptance with a synthetic `sshd`

## Task

Prove the SSH exit against a real `sshd` through a real tunnel. The user's server has no
`sshd` — its exit is an HTTP proxy — so the service is a synthetic one in a disposable
container, the same bounded substitute the SOCKS5 stage used for `danted`
([synthetic `danted`](../../02-socks/08-live-acceptance/03-timeouts-and-danted/08-synthetic-danted/plan.md)).
The user's standing permission covers downloads and installs inside the container; nothing on
the host is installed, started or reconfigured, and no network of the host is changed.

The environment is entirely local and disposable: `dnstt-server` built from the vendored
`core/server` with `-gen-key` and the reserved domain `t.example.com`; a container with
`openssh-server`, one password login and one `authorized_keys` entry for a generated key,
`AllowTcpForwarding` at its default so `direct-tcpip` is served; a fixed-body HTTP target
inside the container; the client on the host side through a loopback UDP resolver
(`udp:127.0.0.1:<port>`), an isolated `XDG_CONFIG_HOME` and a file-backed secret store, as the
existing live tooling does it. No profile, store, resolver or server of the user participates.

Runs: one connect with password authentication and one with key authentication, each carrying
an application connection through the local SOCKS5 listener to the verified target body; then
the same profile with a fingerprint that is not the container's, where the tunnel must stop on
the first session with the message that names both fingerprints and must not redial. The
user's server itself is recorded as `n — the server's exit is an HTTP proxy, there is no
sshd`, and that item is never counted as a pass.

## Done when

- The environment is reproducible from the record and fully synthetic: the image, the
  `openssh-server` version and the generated key material are identified privately; the tunnel
  server, the domain, the resolver and the target are local; nothing of the user's was read or
  written and nothing on the host was changed.
- A password run and a key run each carried a `direct-tcpip` connection through the tunnel to
  the fixed-body target and returned the verified body; the exit's `Check` passed on the
  session, and the record shows one SSH connection per session and one channel per
  application connection.
- The wrong-fingerprint run stopped the tunnel on the first session with an error naming
  `exit.host_key` and both fingerprints, with exactly one dial and no retry, and no data
  reached the target.
- The real-server item is recorded as `n` with the reason, not as a pass, and the published
  text states that the evidence is synthetic and environment-scoped — this `sshd` build and
  configuration only, not the user's server.
- Raw records, keys and identifiers stay in ignored `doc/local/` or `doc/runs/`; the published
  text and the roadmap pass the privacy check, and `git diff --check` is clean.

## Outcome

The SSH exit was accepted live against a real `sshd` reached through a real DNS tunnel, in a
disposable synthetic environment. The evidence is synthetic and environment-scoped: it speaks
about this `sshd` build and configuration in this container only, not about the user's server,
other versions or deployments.

- Environment: a local tunnel server built from the vendored `core/server` with a generated
  key and the reserved tunnel domain; a disposable container with `openssh-server` — one local
  login with a generated password and one generated public key, `AllowTcpForwarding` left at
  the package default, so `direct-tcpip` is served — and a fixed-body HTTP target inside it;
  the client on the host through its local SOCKS5 listener, a loopback UDP resolver, and an
  isolated config with the file-backed secret store. Nothing of the user's was read or
  written, and the only downloads were Debian packages inside the container — the SSH server
  and `ca-certificates`. The image identifier, the package version, the key material, the
  target digest and the raw logs stay in the private record.
- The vendored `core/server` is a library package, so the tunnel server command line
  (`-gen-key`, `-privkey-file`, `-udp`) was assembled privately from the same unchanged
  upstream source with only its package clause changed; `core/` itself was not modified.
- Password run and key run: each connected, passed the exit's readiness check on the first
  session (the client reported `connected`), and carried two sequential application
  connections through `direct-tcpip` to the target; both returned the body whose SHA-256
  matched the served file.
- The SSH connection shape, as observed rather than assumed: one tunnel session per run. The
  readiness check opens its own short SSH connection, authenticates and closes it with no
  channels. The session's cached SSH connection then carries the application channels. Each
  application connection is exactly one `direct-tcpip` channel: two sequential application
  connections produced two channels on the same cached SSH connection, with no third SSH
  connection and no third tunnel stream, and the target saw one TCP connection per channel. So
  the record reads "one cached SSH connection per session carries the session's channels, one
  channel per application connection"; the check connection is extra and carries none.
- Wrong fingerprint: the tunnel stopped on the first session with exit status 1, and the error
  named `exit.host_key` and both fingerprints, the presented one and the pinned one. Exactly
  one tunnel session and one SSH connection appeared; the connection was closed before
  authentication; no channel was opened, the target received nothing, and there was no redial
  or backoff.
- The user's real server is recorded as `n`: its exit is an HTTP proxy and it has no `sshd`.
  It is not counted as a pass.
- Scope: this `sshd` build and configuration in this disposable local environment only. It
  makes no claim about the user's server, other versions or deployments, about channel types
  other than `direct-tcpip`, about long-lived sessions or a reconnect after the SSH connection
  died, or about the wrong-password and wrong-key paths.
