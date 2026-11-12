---
kind: task
status: todo
depends: [01-menu-and-commands]
---

# Editing the profile in the menu

## Task

The §10a items that change the profile, and the connect-time offer that keeps the resolvers
visible: before a connection the menu prints the profile's resolvers and offers to connect
with them (Enter) or change them first; the changed list is saved to the profile. The items
are: check resolvers with the offer to drop the dead ones, edit resolvers, a new exit
password, a new domain or key.

The pieces exist and are reused, not copied: the `resolvers check` body
(`internal/cli/resolvers.go`), the wizard's validators (`validDomainName`,
`config.IsValidPubkey`, `resolvers.Expand`) and its secret-store pattern: the secret is
written first, the profile second, a failed save removes the secret without printing it, and a
failed delete names the error without the value (`internal/cli/wizard.go:91-122`). `profile
set` stays the placeholder it is; the menu edits the profile directly through the same
`SaveProfilesTo`/`SaveProfiles` calls.

Out of scope: the menu loop and the start (child 1), the mid-session offer (child 3), and any
editing from the command line beyond what exists.

## Done when

- Connect prints the profile's resolvers and asks: Enter connects with them, `e` goes to the
  resolver editor, and the edited list is saved and used for this connect; declining the
  connection returns to the menu.
- The resolver editor shows the current list and asks one entry per line, empty finishes;
  every entry is validated by `resolvers.Expand`; an empty result is saved as `["network"]`,
  the §10 default; the profile is written only after a valid list.
- The check item runs the existing `resolvers check` body for the profile's resolvers and,
  when some failed, offers to remove them and save; a refusal leaves the profile as it was;
  removing every resolver is not offered (the list would become empty).
- A new exit password is asked twice by hidden input, non-empty, without control characters;
  it goes to the store under the profile's reference, `exit.password_ref` is updated, a
  failed save leaves the old reference and the old secret, and the old secret is deleted only
  after a successful save (a failed delete is reported without the value).
- A new domain or key is asked with the wizard's validators, saved, and the next connect uses
  it; an invalid answer is re-asked.
- End of input at any question cancels the edit and writes nothing.
- Tests use a temporary configuration directory and a fake store: each edit's save, each
  failure path, the empty result and EOF; no secret appears in the TOML or in the output.
- The local matrix of child 1 passes.
