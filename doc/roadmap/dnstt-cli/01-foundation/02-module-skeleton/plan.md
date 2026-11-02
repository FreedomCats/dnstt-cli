---
kind: task
status: done
detailed: true
---

# Module and skeleton

## Task

A Go module under its final name, with the names the program leaves in the system coming
from one place, and a command line that answers `version`. `E0`, `F1`.

## Steps

1. `go.mod`: `module github.com/FreedomCats/dnstt-cli`, the Go version of csqtt-cli.
2. `internal/app`: `Name = "dnstt-cli"` — every name the program leaves in the system comes
   from it (adapter, nftables table, WFP provider, locks, config directory, resolvconf
   entries, secret labels).
3. `cmd/dnstt-cli/main.go`: `version` and `commit` set by `-ldflags`, `cli.Main`.
4. `internal/cli`: a minimal `Main` — `version`, `help` and a command list; the real
   commands come in stage 2.
5. `README.md`: what the program is, that it is in development, that the core is dnstt by
   David Fifield (public domain).

## Done when

`go build ./...` and `go vet ./...` pass and `go run ./cmd/dnstt-cli version` prints `dev`.

## Outcome

`internal/app` is the single source of every system-visible name, as intended: the GUIDs
and the mark value that must differ from csqtt-cli's live elsewhere (`winsys`,
`netguard`) and carry new values. `internal/cli` stays a dispatcher with no logic of its
own, so the menu added later can call the same commands.
