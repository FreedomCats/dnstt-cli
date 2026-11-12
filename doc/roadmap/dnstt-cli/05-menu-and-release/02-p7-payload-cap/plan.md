---
kind: task
status: done
detailed: true
---

# A cap on the payload per query (P7)

## Task

P7 of `architecture.md` §07: a cap on the payload that one DNS query carries. The session
derives its KCP MTU from the encoded capacity of the tunnel domain
(`core/client/session.go:110`), which is the largest payload a name can carry; a smaller cap
is what the `slipnet://` payload-size field asks for (field 39 of `architecture.md` §10) and
what a resolver that truncates large answers needs. The session file is added by
`scripts/vendorcore/_files/client/session.go`, so the patch is a `_files` edit and a
regeneration, as in P6.

The profile key is decided by the user: **`payload_size`**, in bytes of the DNS payload per
query; `0` or absent means the domain's maximum, and the wizard does not ask it (the
`slipnet://` import writes it, child 3). The value reaches the session through
`app.TunnelOptions` into `client.Config`, and the session takes the smaller of the domain's
capacity and the cap. The wire format and the server are not touched: a smaller MTU stays
within what the tunnel already carries, and the existing 80-byte floor stays.

Out of scope: the import that writes the key (child 3), any change to the server, and any
automatic choice of a cap from a failed answer — the value belongs to the profile, it is not
guessed.

## Steps

1. **The profile key** — `internal/config/profiles.go`, `internal/config/profiles_test.go`:
   - `Profile` (`:23-35`) gains `PayloadSize int \`toml:"payload_size"\`` after `TunDNS`, with
     the comment: bytes of the DNS payload one query carries; 0 or absent — the domain's
     maximum. No `omitempty`: a zero is written like the other defaults, and an absent key
     reads as zero either way;
   - add `"payload_size": true` to `knownProfileKeys` (`:315`), so a misspelt key stays an
     unknown-key error;
   - in the per-profile validation loop (`validateProfiles`), refuse a negative value with a
     message naming `payload_size`; no upper bound is checked here, because the floor depends
     on the domain and is enforced by the session.
2. **The core cap** — `scripts/vendorcore/_files/client/session.go`:
   - `Config` (`:36-58`) gains `PayloadSize int`, documented: 0 — the domain's maximum;
     otherwise the cap on the DNS payload of one query, in bytes, the same unit as field 39;
   - in `Dial` (`:110`) take `capacity := dnsNameCapacity(cfg.Domain)`, then
     `if cfg.PayloadSize > 0 && cfg.PayloadSize < capacity { capacity = cfg.PayloadSize }`,
     and keep the rest of the formula (`capacity - 8 - 1 - numPadding - 1`);
   - the `mtu < 80` error (`:111-113`) names `payload_size` and the value when the cap caused
     it, together with the bytes left, and keeps the domain wording otherwise. No silent
     truncation;
   - nothing else changes: with `PayloadSize == 0` the MTU and the bytes on the wire are
     exactly today's.
3. **The app** — `internal/app/tunnel.go`, `internal/app/timeouts_test.go`:
   - `sessionConfig` (`:183-192`) sets `PayloadSize: prof.PayloadSize`, so the first session
     and every redial carry it (the supervisor copies the config on each dial);
   - the comment above `sessionConfig` (`:180-182`) says the keepalive is not set by a profile
     key; keep it about the keepalive, not about the whole config.
4. **`profile show`** — `internal/cli/profile.go:126-155`: print `Payload Size: N` when the
   value is non-zero, like `uTLS` and `Tun DNS` are printed only when set; at zero the line is
   omitted, because zero is the maximum.
5. **Tests**:
   - `scripts/vendorcore/_files/client/session_test.go` (in-package, so `kcpConn.GetMtu()` is
     reachable): a session with `PayloadSize` below the domain's capacity has the expected
     smaller MTU; `0` gives today's MTU; a cap above the capacity is ignored; a cap that
     leaves fewer than 80 bytes fails `Dial` with an error naming `payload_size` and the value;
     a capped session still carries a stream through the in-process server. No new `core/`
     file, so the `files`/`added` counts stay as they are;
   - `internal/config/profiles_test.go`: round-trip through save and reload, an absent key
     reads 0, an explicit 0 stays 0, a negative value is refused, a misspelt key is an
     unknown-key error;
   - `internal/app/timeouts_test.go` (next to `TestSessionConfigCarriesPolicyKeepAlive`):
     `sessionConfig` carries the profile's value;
   - `internal/cli/profile_show_test.go`: the line appears with a value and is absent at 0.
6. **Docs** — `doc/architecture.md` §10 and `dnstt-cli.example.toml`: add `payload_size = 0`
   under `tun_dns` with the comment "cap on the payload per query, bytes; 0 — the domain's
   maximum". The §07 P7 row and its stage column stay as they are (the roadmap holds state).
7. **Regeneration and checks** — `go run ./scripts/vendorcore -upstream ../dnstt`, then
   `go run ./scripts/vendorcore -check -upstream ../dnstt`, and a second generation compared
   byte for byte; `gofmt` outside `core/`, `go build ./...` and `CGO_ENABLED=0 go build ./...`,
   the CI vet split on Linux and Windows, `go test -race ./core/client ./internal/config
   ./internal/app ./internal/cli`, the full `go test -race -count=1 ./...` with
   `DNSTT_CLI_TEST_DBUS=1`, `DNSTT_CLI_TEST_NETNS=0` and the four external `netguard` live
   tests excluded by `-skip`, the Windows cross-build with `go test -c` of every package with
   tests, and `git diff --check`.

Easy to get wrong:

- the cap is on `dnsNameCapacity`, before the per-message overhead is subtracted: capping the
  MTU instead would make the numbers in `slipnet://` mean something else;
- the 80-byte floor already exists; a cap must produce a clear error at that floor, not a
  panic in `SetMtu` and not a silently raised value;
- `sessionConfig` runs for every dial, so the value must come from the profile, not from a
  package-level default;
- the `core/CORE_VERSION` counts do not change here (no new file); if a separate test file is
  added after all, let the tool rewrite them.

## Done when

- `payload_size` round-trips through save and reload; absent and explicit `0` both mean the
  domain's maximum; a negative value and a misspelt key are refused with a message naming the
  key; `profile show` shows the value when set and omits it at 0.
- The session caps the KCP MTU: a cap below the domain's capacity gives the smaller MTU, a cap
  at or above the capacity changes nothing, and a cap that leaves fewer than 80 bytes fails
  `Dial` naming `payload_size` and the value; the tests of step 5 prove all four and that the
  uncapped session is unchanged.
- `sessionConfig` carries the value for the first session and for redials.
- `doc/architecture.md` §10 and `dnstt-cli.example.toml` list the key.
- `core/` is regenerated from the recorded upstream commit, `vendorcore -check` passes and a
  second generation is byte-identical.
- The local matrix of step 7 passes; no live run and no new dependency.

## Outcome

Done: the profile key `payload_size` caps the payload of one DNS query, and the session
turns the cap into a smaller KCP MTU.

- Profile (`internal/config/profiles.go`): `Profile.PayloadSize int` with
  `toml:"payload_size"` after `tun_dns`, no `omitempty` — a save writes the zero like the
  other defaults, and an absent key reads as zero. `payload_size` is in
  `knownProfileKeys`, so a misspelt key stays an unknown-key error; `validateProfile`
  refuses a negative value with a message naming the key. The upper bound is not checked
  there: the floor depends on the domain and is enforced by the session.
- Core (`scripts/vendorcore/_files/client/session.go`, generated into `core/client/`):
  `Config.PayloadSize`; `Dial` takes `capacity := dnsNameCapacity(cfg.Domain)`, lowers it
  to `PayloadSize` when `0 < PayloadSize < capacity`, and only then subtracts the
  per-message overhead (`8 + 1 + numPadding + 1`). A cap at or above the capacity changes
  nothing; a cap that leaves fewer than 80 bytes fails `Dial` with
  `payload_size %d leaves only %d bytes for payload, below the 80-byte minimum`, and the
  domain's own error keeps its wording. With `PayloadSize == 0` the MTU, the bytes on the
  wire and the log line are exactly as before. The server, the wire format and the
  transports are not touched.
- App (`internal/app/tunnel.go`): `sessionConfig` carries `prof.PayloadSize`, so the first
  session and every redial get it; the comment above stays about the keepalive.
- `profile show` prints `Payload Size: N` only when the value is non-zero, after `Tun DNS`.
- Docs: `doc/architecture.md` §10 and `dnstt-cli.example.toml` list `payload_size = 0` under
  `tun_dns`. The wizard does not ask it, and nothing writes it yet: the `slipnet://` import
  is child 3.

The plan expected `kcpConn.GetMtu()` in the session tests, but kcp-go v5.6.8 has no MTU
getter. The tests read the MTU from the line `Dial` logs after `SetMtu` succeeds, so they
still assert the value the KCP session was given. Another deviation is in the generation
only: the first `go run ./scripts/vendorcore` does not compile while `internal/app` already
sets the new field and `core/client` does not have it yet, because `scripts/internal/script`
imports `internal/app`; that first run used a `go run -overlay` with a temporary
`tunnel.go`, and the tool itself was the only writer of `core/`. On the final tree
`go run ./scripts/vendorcore -check -upstream ../dnstt` runs with no overlay.

Tests: `TestSessionPayloadSizeMTU` (0 → 134, 120 → 107, at the capacity → 134, above →
134), `TestSessionPayloadSizeTooSmall` (90 → 77, the error names `payload_size` and 90) and
`TestSessionPayloadSizeCarriesStream` (a 20 KB stream through a capped session) in the core;
`TestProfilePayloadSizeRoundTrip`, `TestProfilePayloadSize` (absent, explicit zero, set),
`TestProfilePayloadSizeNegative` and `TestProfilePayloadSizeMisspelt` in config;
`TestSessionConfigCarriesPayloadSize` in app; `TestProfileShowPayloadSize` in the CLI.

Checks on the final tree: `gofmt` outside `core/` clean; `go build ./...` and
`CGO_ENABLED=0 go build ./...`; the CI vet split on Linux and with `GOOS=windows`;
`go test -race ./core/client ./internal/config ./internal/app ./internal/cli`; the full
`go test -race -count=1 ./...` with `DNSTT_CLI_TEST_DBUS=1`, `DNSTT_CLI_TEST_NETNS=0` and
the four external `netguard` live tests skipped; the Windows cross-build and `go test -c`
of all 19 packages with Windows tests; `git diff --check`. `core/` comes from `0c5c52a`
with `CORE_VERSION` unchanged (`files = 34`, `rules = 41`, `added = 13`); `vendorcore
-check` passes and a second generation is byte-identical.

Out of the plan and not done: no `slipnet://` import, no new dependency, no live run.

Not touched, worth a later look: `resolvers check` builds `client.Config` without the cap
and the upstream `run` in `core/client/main.go` computes the MTU without it — both are
diagnostic or unused paths, not sessions of a profile. Found once during the matrix, not
reproduced in three repeats: `sockets.track` closes a socket while holding `s.mu`, and
`trackedConn.Close` calls `s.forget`, which takes the same lock — closing a session between
a successful dial and its tracking would deadlock. It comes from P1, before this task, and
P7 does not touch the transports; a separate task should fix it.
