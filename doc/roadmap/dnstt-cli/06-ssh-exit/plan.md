---
kind: stage
status: in-progress
base: 7db7e37
---

# SSH exit

Goal (`architecture.md` §12, E5, R3): an exit that is one SSH connection inside one stream,
with `direct-tcpip` channels for connections, password or key authentication, and the
server host key pinned in the profile.

The stage is cut by result: the protocol, the profile and its wiring, the paths that author a
profile, and live acceptance. The decisions taken when the stage was detailed:

- The pinned value is the OpenSSH SHA-256 fingerprint (`SHA256:<base64>`, as `ssh-keygen -lf`
  prints it, prefix optional) and is required for an SSH profile; MD5 and `known_hosts` lines
  are refused, because the first is deprecated and the second drags the key type and its
  encoding into the profile.
- Exactly one credential: `password_ref` or `key_ref`. A key with a passphrase is decrypted
  when it is put into the store and stored as the private key, because §10 has no passphrase
  field and a passphrase kept next to its key protects nothing.
- A `slipnet://` link carries no host key (§10's fields 17 and 19 are the SSH port and host,
  which are not used), so the fingerprint is asked at import: an interactive import asks, a
  stdin import refuses that line with a warning naming `exit.host_key`.
- The user's server has no `sshd`: live acceptance is a synthetic `sshd` in a disposable
  container, as the SOCKS5 stage did for `danted`, and the real server's item is `n`.

## Done when

- `exit` has an SSH implementation next to SOCKS5, chosen by the profile's exit type.
- One SSH connection lives in one tunnel stream; every proxied connection is a
  `direct-tcpip` channel on it.
- Authentication with a password or a key; the key is kept in the secret store.
- The server host key is pinned by fingerprint in the profile; a mismatch stops the tunnel
  with a clear message and is not retried.
- The supervisor's check (`Check`) covers SSH the same way it covers SOCKS5.
- Live acceptance against a server with `sshd`.

## Children

1. [SSH client over the tunnel stream](01-stream-client/plan.md) — one SSH connection inside
   one stream, `direct-tcpip` per connection, password and key with a passphrase, the pinned
   fingerprint with a final mismatch error, `Check` for the supervisor. Task; protocol and
   tests only, no profile.
2. [Profile and connect wiring](02-profile-and-wiring/plan.md) — `exit.host_key` and
   `exit.key_ref` with their rules, the exit built from a profile, the fingerprint and the key
   in `profile show`, the key removed with the profile. Task; a hand-written SSH profile
   connects.
3. [Wizard, import and menu](03-wizard-import-menu/plan.md) — the `ssh` exit type in the
   wizard, `dnstt_ssh` links with a password or a key, the menu's exit secret and fingerprint
   items. Task; no SSH profile needs a hand-edited file.
4. [Live acceptance with a synthetic `sshd`](04-live-acceptance/plan.md) — password and key
   runs through a real tunnel to a container `sshd`, the wrong fingerprint stopping the
   tunnel, `n` for the user's real server. Task; synthetic, environment-scoped evidence.
