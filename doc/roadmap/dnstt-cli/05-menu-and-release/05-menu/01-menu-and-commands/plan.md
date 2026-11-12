---
kind: task
status: todo
---

# The menu and the command reuse

## Task

The interactive entry of `architecture.md` §10a: `dnstt-cli` without arguments in a terminal
opens the menu; without a terminal it prints the command list, as it does now; `menu` opens it
explicitly and `menu -connect <profile>` connects at once. The menu has no logic of its own:
each item calls the same function as the command behind it.

What the start has today: `run` prints the command list for no arguments
(`internal/cli/root.go:114-120`), the command list is `commands()` (`:70-78`), and the hidden
secret-service helper is dispatched before parsing (`:99-111`). `connect` already owns the
status line (`internal/cli/connect.go:250-256`, `:383-459`), the Ctrl+C ladder
(`:223-248`) and the root refusal (`:149-150`); its body has to become callable for one
connection without re-parsing arguments, because the menu supplies a profile name and no
flags. The wizard was built for this caller: it takes a `prompter`, the offered modes, the
`tun` refusal and the save function (`internal/cli/wizard.go:22-33`), and `profile add`
already computes those (`internal/cli/profile.go:231-278`). `internal/lines` keeps the one
reader of the process.

Out of scope: the edit items (child 2), the mid-session resolver offer (child 3), elevation
(child 5), and the link parser itself (the import stage), which this task only calls.

## Done when

- No arguments: stdin and stdout a terminal → the menu; otherwise the command list byte for
  byte as now (`root_test.go` pins it). `menu` in the command list and in `help`.
- `menu -connect <profile>` connects without the menu loop; the profile name is the only
  argument, an unknown profile is an ordinary error.
- One connection runs through the same function as `connect`, so the flags' behaviour, the
  status line, the drain, the kill-switch lines and the exit codes are the command's, not a
  copy; the `connect` tests pass unchanged.
- The loop shows the numbered items, reads with the shared line reader, takes an empty answer
  as the default item, `0` exits, and end of input closes the menu without writing anything.
  Every item it has now calls the existing body: connect, `resolvers check` (the offer to
  drop the dead ones comes with child 2), another profile
  (list and switch the current one), delete profile (confirmation first, then the
  `profile rm` rules: the secret goes with the profile, the last profile removes
  `profiles.toml`), exit.
- With no profile the wizard runs; its first question offers a `slipnet://` link by hidden
  input or the questions; the defaults are `home`, `network` and `tun` where supported; after
  the wizard the menu continues with the new profile.
- Signals: while connected, the first Ctrl+C stops that connection and returns to the menu,
  the second does not wait for open connections; in the menu Ctrl+C closes it; SIGTERM and
  SIGHUP stop the connection, close the menu and return. No third-signal `os.Exit` inside the
  menu.
- A redirected stdout has no status line; a menu started with redirected streams reads until
  the end of input and closes cleanly, which is what the tests and `distcheck` use.
- Tests use buffers and a scripted prompter, with the connect body replaced by a seam; they
  cover the start decision, each item's dispatch, EOF, the wizard path and the signals
  through an injected channel.
- The local matrix (build, vet split, race with the stated skips, Windows cross-build and test
  compilation, `git diff --check`) passes. The real terminal — keys, hidden input, the
  double-click pause on Windows — is a manual check recorded in the Outcome; Windows is not
  run.
