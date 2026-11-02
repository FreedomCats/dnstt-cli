---
kind: task
status: done
detailed: true
---

# Vendoring the core

## Task

`scripts/vendorcore` — the rule engine from csqtt-cli, rewritten for Go, and `core/` filled
from the mirror through it. Every departure from the upstream becomes a rule with a
mandatory match count, so an upstream update is not a manual merge. `E0`, `F2`;
`architecture.md` R1.

## Steps

1. Carry over `rules.go`, `apply.go`, `main.go` from csqtt-cli `scripts/vendorcore`: a rule
   is a file, a pattern, a replacement and a mandatory match count; files of our own come
   from `scripts/vendorcore/_files/`; excluded upstream files must exist upstream; `-check`
   compares `core/` with the upstream plus the rules and names every extra or changed
   file. Not carried: `crates.go` and everything about Cargo; `sockets.go` (the socket
   guard) comes next, in the session task.
2. Convert in Go, by the command rather than by rules: `package main` → the package name
   of the target directory; the import prefix `www.bamsoftware.com/git/dnstt.git/` →
   `github.com/FreedomCats/dnstt-cli/core/`; CRLF → LF before the rules.
3. Copy the upstream packages: `dns/`, `noise/`, `turbotunnel/` as they are; `dnstt-client/`
   → `core/client/`; `dnstt-server/` → `core/server/` (used only by tests); `COPYING` goes
   along. `README`, `CHANGELOG`, `TODO`, `man/`, `go.mod`, `go.sum` stay upstream; their
   absence in `core/` is not an error.
4. Upstream dependencies into our `go.mod` at the upstream versions; `x/crypto` and `x/net`
   are shared with the carried packages and may be raised.
5. `core/CORE_VERSION`, written by the command: upstream URL (the mirror), commit, commit
   date, version from the first heading of `CHANGELOG`, numbers of files, rules and added
   files.
6. No rules yet except the package and import conversion.

## Done when

`go run ./scripts/vendorcore -upstream ../dnstt` fills `core/`, `go build ./core/...` and
`go test ./core/...` pass (the upstream tests included), `-check` passes, and a manual edit
of any file in `core/` makes `-check` fail with that file's name.

## Outcome

Own files live in `scripts/vendorcore/_files/` instead of a plain `files/`: a sibling
`files/` was picked up by `go build ./...` and broke the build. `core/` is never run
through `gofmt` — the rewritten import paths change the order of imports, and formatting
it would make `-check` and the upstream diverge. `go vet` runs on `core/` with
`-testinggoroutine=false`: two upstream `noise` tests call `t.Fatal` from a goroutine.
The socket guard and the rule set stayed in the next task, so this one closes with an
empty rule set besides the conversion.
