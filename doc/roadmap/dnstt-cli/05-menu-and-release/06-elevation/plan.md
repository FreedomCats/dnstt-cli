---
kind: task
status: todo
depends: [05-menu]
---

# Elevation from the menu

## Task

`architecture.md` §10a, privileges: `tun` without root — the menu re-runs one connection
through `sudo` in the same terminal, and if that is impossible it uses `socks` for this time.
`connect` already refuses `tun` without root with a hint (`internal/cli/connect.go:143-150`,
the `Requires.Admin` check); the menu is where the hint becomes an action.

One connection, not the menu: the child is the same binary running `connect <profile>
-datapath tun`, with stdin, stdout, stderr and the working directory inherited, so `sudo` asks
for its password in the terminal and the `connect` status line and Ctrl+C work as on the
command line. The settings, the secrets and the keyring stay the invoking user's — that is
already the `internal/sudo`, `internal/config` and `internal/secretservice` contract, and the
live TUN matrix exercised it. The parent must not take the terminal's signals for itself while
the child runs: it ignores SIGINT and SIGTERM around `Wait` and restores them after, so the
first Ctrl+C reaches the child.

The decision is taken before the connect body, from the same requirements `connect` and the
wizard use: `(&datapath.Tun{}).Requires()` — `Admin` and `Unsupported` — is what
`internal/cli/profile.go:254-261` already asks for the wizard's modes, and the `Admin` refusal
is `internal/cli/connect.go:143-150`. A `tun` profile on a system where that datapath needs
root and the process has none is the case for `sudo`; a process already under `sudo`
(`sudo.Invoker()` non-nil) and root itself run directly, without a second `sudo`. On Windows
the elevated run in a new window is part of `04-tun-windows` (blocked); here that branch
reports that the elevated run is unavailable, and the menu offers `socks`, cross-built only.

Out of scope: the Windows UAC window and `04-tun-windows`'s elevation, new flags for the menu,
and any password handling — `sudo` prompts by itself.

## Done when

- A `tun` profile in a non-root menu session runs one connection as `<the same binary> connect
  <profile> -datapath tun` under `sudo`, with inherited stdio and working directory; the
  password prompt, the status line and Ctrl+C are the terminal's; the child's exit returns to
  the menu, and its non-zero exit is reported without closing the menu.
- Root and an already-elevated session run `connect` directly, with no second `sudo` and no
  prompt.
- `sudo` missing, or a child that cannot start, prints the reason and offers `socks` for this
  time; accepting runs the connection with `-datapath socks`, declining returns to the menu.
- The child's argv is built, never a shell string; no password or secret is passed on a
  command line or in the environment.
- Tests with an injected runner see the argv, the inherited streams and the `socks` fallback;
  the direct paths are covered too; the Windows branch compiles and is covered by a unit test
  of the refusal.
- The local matrix (build, vet split, race with the stated skips, Windows cross-build and test
  compilation, `git diff --check`) passes.
- The real check — a terminal, the `sudo` prompt and a session through the tunnel — is a
  user-run step recorded in the Outcome: no agent-run host `tun` session is made without a
  fresh explicit request, and Windows is not run.
