---
kind: stage
status: todo
---

# SSH exit

Goal (`architecture.md` §12, E5, R3): an exit that is one SSH connection inside one stream,
with `direct-tcpip` channels for connections, password or key authentication, and the
server host key pinned in the profile.

Detailed when its turn comes; below is the outline from `architecture.md`.

## Done when

- `exit` has an SSH implementation next to SOCKS5, chosen by the profile's exit type.
- One SSH connection lives in one tunnel stream; every proxied connection is a
  `direct-tcpip` channel on it.
- Authentication with a password or a key; the key is kept in the secret store.
- The server host key is pinned by fingerprint in the profile; a mismatch stops the tunnel
  with a clear message and is not retried.
- The supervisor's check (`Check`) covers SSH the same way it covers SOCKS5.
- Live acceptance against a server with `sshd`.

## Outline

1. The SSH client over the stream (`x/crypto/ssh`), one connection, channels.
2. Authentication: password, key, passphrase; the key in the secret store.
3. The pinned host key and its mismatch error.
4. `Check` for the new session, and the "final" reasons of the supervisor.
5. Live acceptance.
