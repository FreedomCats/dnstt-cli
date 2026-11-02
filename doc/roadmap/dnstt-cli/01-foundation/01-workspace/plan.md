---
kind: task
status: done
detailed: true
---

# Workspace

## Task

Prepare a machine for the work. Nothing is committed here: it is a checklist that a new
session goes through to confirm the state, and the result of it is the first thing the CI
relies on. `E0`, `F0`.

## Steps

1. Clone next to the repository: the core mirror `https://github.com/FreedomCats/dnstt`
   (as `../dnstt`) and `https://github.com/FreedomCats/csqtt-cli` (as `../csqtt-cli`).
2. Go: the version from csqtt-cli's `go.mod` (1.26.x). An older local Go fetches it by
   itself through `GOTOOLCHAIN=auto` when `go.mod` asks for it.
3. `git config user.name FreedomCats` and `user.email noreply@freedomcats.invalid` in the
   repository (`AGENTS.md`, "Commits").
4. Start the stage log `doc/local/plan-e0.md`.

## Done when

`go version` in the repository reports 1.26.x and both clones are in place.

## Outcome

Go 1.26.2 in use. The clones stay as siblings of the repository; the core is checked out
at the commit named in `core/CORE_VERSION`. The git identity is set in the repository's
own `.git/config`, so it does not follow the developer to other repositories.
