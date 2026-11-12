---
kind: stage
status: in-progress
base: 9ce7e2f
depends: [04-slipnet-import]
---

# The menu and the wizard

Goal (`architecture.md` §10a): `dnstt-cli` without arguments in a terminal opens a menu that
calls the same code as the commands; without a terminal it prints the command list. With no
profile yet the wizard runs, with a `slipnet://` link or the questions; the connect flow keeps
the resolvers visible and offers to change them; when they run out mid-session the menu offers
to check and edit them and connects again.

Most pieces exist and are meant for this menu: `connect` with its status line and Ctrl+C
handling (`internal/cli/connect.go`), the wizard with its `prompter` seam
(`internal/cli/wizard.go:22-33`, `internal/cli/prompt.go:14-21`), `resolvers check`,
`profile rm`, and the one shared line reader (`internal/lines`). The menu adds the loop, the
items, the reuse of the command bodies and the signals of §10a.

## Done when

- No arguments in a terminal opens the menu; without a terminal, no arguments print the
  command list; `menu` opens it and `menu -connect <profile>` connects at once.
- The items call the same code as the commands: connect, check resolvers, edit resolvers, new
  exit password, change domain or key, another profile, delete profile, exit (`0`).
- With no profile the wizard runs: a link by hidden input or the questions, the defaults
  `home`, `network` and `tun` where supported.
- Connect shows the saved resolvers and offers to connect with them or change them first; the
  changed list is saved to the profile.
- Ctrl+C while connected returns to the menu and the second one does not wait; in the menu it
  closes the menu; SIGTERM and SIGHUP remove the tunnel and close the menu; the status line
  sits at the bottom and is absent with redirected output.
- When no resolver completes a handshake for a full round mid-session, the menu offers to
  check and edit them and connects again.
- `connect` on the command line behaves exactly as before.

## Children

1. [The menu and the command reuse](01-menu-and-commands/plan.md) — the start, the loop, the
   items over the existing command bodies (connect, check resolvers, another profile, delete
   profile, exit), the wizard, the status line and the signals. Task.
2. [Editing the profile in the menu](02-profile-editing/plan.md) — the connect-time resolver
   offer, edit resolvers, check and drop dead ones, a new exit password, a new domain or key.
   Task.
3. [Resolvers run out mid-session](03-resolvers-exhausted/plan.md) — the supervisor's event
   for a full failed round and the menu's offer to check, edit and reconnect. Task.

The loop and the reuse come first: every later item is a branch of the same loop. Editing
comes next, because the mid-session offer reuses the edit path. The exhausted-resolvers event
is last: it needs a small supervisor seam and is the only part that touches the state machine.

Limits: the Windows double-click behaviour (the window does not close on an error) and a
console's signals are cross-built and compiled only, because `04-tun-windows` is blocked and
no Windows machine is available; a real terminal check of the keys and the hidden input is a
manual check on Linux.
