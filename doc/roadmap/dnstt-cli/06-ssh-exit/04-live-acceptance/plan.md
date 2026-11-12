---
kind: task
status: todo
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
