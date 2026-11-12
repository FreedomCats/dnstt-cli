---
kind: stage
status: done
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

## Outcome

The stage is accepted. An SSH exit sits next to SOCKS5 and HTTP and is chosen by the
profile's `exit.type`: one cached SSH connection per tunnel session carries every
application connection as a `direct-tcpip` channel, authentication is a password or a key
kept in the secret store, the server host key is pinned by its OpenSSH SHA-256 fingerprint,
and a mismatch stops the tunnel with the presented and the pinned fingerprints and no
redial. The supervisor's check covers SSH the way it covers SOCKS5, and the live acceptance
ran against a synthetic `sshd` while recording the user's real server as `n`.

- [`01-stream-client`](01-stream-client/plan.md) (`37ff57f`) — `internal/exit/ssh.go`:
  `SSH{Open, User, Password, Signer, HostKey}`, `ParseKey`, `ParseFingerprint`, `Dial`,
  `Check`. One connection per dialer cached under a mutex with a generation counter and a
  single-flight open; `client.Wait` clears it so the next `Dial` reconnects. `HostKeyError`
  and `IsHostKeyMismatch` in `internal/exit/exit.go`; the mismatch is a final cause in
  `internal/supervisor/tunnel.go`, so the supervisor stops instead of redialing.
- [`02-profile-and-wiring`](02-profile-and-wiring/plan.md) (`b28d137`) — `exit.host_key` and
  `exit.key_ref` with the rules of §10 as load errors, the fingerprint normalized only
  through `exit.ParseFingerprint`, exactly one credential, both fields refused with any
  other exit type; `internal/app` builds `exit.SSH` from the profile with the key through
  `exit.ParseKey(pem, "")` and wraps HTTP and SSH in one `fatalExit`; `profile show` prints
  the fingerprint and the key's reference, `profile rm` deletes the stored key with the
  password.
- [`03-wizard-import-menu`](03-wizard-import-menu/plan.md) (`59ab514`) — the wizard offers
  `ssh` and asks the method, the password twice or the key path with its passphrase, then
  the fingerprint plainly; a `dnstt_ssh` link imports with its password or its key, the
  fingerprint is asked in a terminal and a stdin line is a warning naming `exit.host_key`;
  the menu replaces the exit secret of either shape and changes the pinned fingerprint.
  `exit.DecryptKey` is the one place that turns an encrypted key into the stored PEM, and
  one `exitSecret` keeps the wizard, the import and the menu on the same store names,
  messages and profile field, so a key profile never gains `password_ref`.
- [`04-live-acceptance`](04-live-acceptance/plan.md) (`8e901f5`) — password and key runs
  each reached `connected` and carried two sequential `direct-tcpip` connections with the
  served body's digest; a foreign fingerprint exited 1 naming `exit.host_key` and both
  fingerprints, one session, one SSH connection closed before authentication, no channel,
  no data at the target and no redial. The user's server is `n`: its exit is an HTTP proxy
  and it has no `sshd`. The evidence is synthetic and environment-scoped.
- Documentation, with the lead: `a5c2c9e` (§10 fields and rules), `4b919f0` (a link gets its
  fingerprint asked at import), `9bac88f` (§10a wizard and menu items, §10 import
  paragraph), `765959a` (`README.md`).

Integration acceptance of `7db7e37..8e901f5` (8 commits, 33 files):

- Each item of `## Done when` was checked against the code, not against the children's
  prose: the exit switch in `internal/app/tunnel.go`, the cached connection and
  `direct-tcpip` in `internal/exit/ssh.go`, the store read and `exit.ParseKey` in the build,
  the fingerprint rule and its final classification in the exit, the supervisor and
  `finishConnect`, and the `CheckExit` hook for SSH.
- The parts agree: `exit.ParseFingerprint` is the only copy of the fingerprint rule,
  `exit.DecryptKey` the only place that removes a passphrase, and `exitSecret` the only
  description of the exit secret shared by the three authoring paths. `profile show` never
  prints key material, `profile rm` removes the key with the profile, and a key profile
  cannot end up with `password_ref`. `rg -n "06-ssh-exit" internal/` and the "stage E5"
  wording are gone from code, command descriptions, the example config and the README.
- `socks5` and `http` are unchanged: SOCKS5 is not wrapped, HTTP keeps its login refusal
  and its drain, `dnstt` links keep fields 12 and 13, and `connect` behaves as before. The
  exit tests, the app tests and the supervisor table cover the new final cause and the
  wrapper.
- The full check matrix on the final tree: `gofmt` on the tracked files outside `core/`;
  `go build ./...` and `CGO_ENABLED=0 go build ./...`; the CI vet split on Linux and with
  `GOOS=windows`; `go test -race -count=1 ./...` with `DNSTT_CLI_TEST_DBUS=1`,
  `DNSTT_CLI_TEST_NETNS=0` and the four external `netguard` live tests skipped; the Windows
  cross-build and `go test -c`; `git diff --check` on the tree and on the range — all clean.
  An integration review of the range found no blocking issue.

Not seen, and why:

- The user's real server — `n`: its exit is an HTTP proxy and it has no `sshd`. It is never
  counted as a pass.
- The `tun` datapath with an SSH exit was not exercised live: the acceptance ran through
  the local SOCKS5 listener, so the datapath's deadline handling of a `direct-tcpip`
  channel rests on the exit's tests only.
- Channel types other than `direct-tcpip`, long-lived sessions, a reconnect after the SSH
  connection died (only the ordinary dead-connection path is tested), the wrong-password
  and wrong-key paths, and any `sshd` build other than the container's were not exercised.
- `exit.SSH.Dial`'s single retry, when the transport dies between the cached-client lookup
  and the channel open, has no deterministic test: it needs a hook in production code.

Recorded limitations and debts:

- A `dnstt_ssh` link with a login but neither a password nor a key is accepted by the parser
  and refused later by the profile rule, which names both `exit.password_ref` and
  `exit.key_ref`; the interactive import asks the fingerprint before that. Nothing is
  stored. Refusing such a line in the parser, with the field in the message, is a candidate
  for a later node.
- The menu's delete-profile confirmation still says "its exit password" for a key profile,
  and `profile set` still answers "Edit the file directly" for every profile. Both are older
  code, recorded in [Wizard, import and menu](03-wizard-import-menu/plan.md) as
  cost-deferred; switching the authentication method of an existing profile from the menu
  stays out of scope.
- Drift in the lead's documents, noticed at acceptance and left to the lead: §08's
  `Connected` and the `Stopping` diagram do not name the SSH readiness check or the pinned
  fingerprint mismatch; R3 still calls the SSH exit "a second stage"; §10a's link sentence
  says the hidden input holds "the exit password", while a `dnstt_ssh` link may carry the
  private key.
