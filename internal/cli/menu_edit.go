package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/FreedomCats/dnstt-cli/internal/config"
	"github.com/FreedomCats/dnstt-cli/internal/resolvers"
)

// offerResolversQuestion is the question of the connect-time offer; the hint is printed
// when the answer is none of the three keys.
const (
	offerResolversQuestion = "Connect with these resolvers? [Enter to connect, e to edit, n for the menu]: "
	offerResolversHint     = `the answer must be Enter to connect, "e" to edit or "n" for the menu`
)

// profileResolvers is the resolver list of a profile as a connect uses it: an empty list is
// the §10 default, "network".
func profileResolvers(prof *config.Profile) []string {
	if len(prof.Resolvers) == 0 {
		return []string{resolvers.NetworkEntry}
	}
	return prof.Resolvers
}

// printResolvers prints the resolver list of a profile, one entry per line.
func printResolvers(w io.Writer, name string, list []string) {
	fmt.Fprintf(w, "Resolvers of profile %q:\n", name)
	for _, entry := range list {
		fmt.Fprintf(w, "  %s\n", entry)
	}
}

// offerResolvers prints the current resolvers and asks whether to connect with them. It
// returns whether to connect: Enter connects, "e" opens the editor and connects with what
// was saved, "n" returns false and the loop shows the menu again. The end of input is
// errCancelled, which closes the menu.
func (m *menu) offerResolvers(ctx context.Context, e *env, profiles *config.Profiles, name string) (bool, error) {
	prof := profiles.Profile[name]
	if prof == nil {
		// The body names the missing profile: there is nothing to show.
		return true, nil
	}
	printResolvers(e.stdout, name, profileResolvers(prof))
	for {
		answer, err := m.ask.Ask(ctx, offerResolversQuestion)
		if err != nil {
			return false, readError(ctx, err)
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "":
			return true, nil
		case "e":
			if err := m.editResolvers(ctx, e, name); err != nil {
				return false, err
			}
			return true, nil
		case "n":
			return false, nil
		default:
			fmt.Fprintln(e.stdout, offerResolversHint)
		}
	}
}

// editResolvers shows the current list and saves a new one. Every entry goes through
// resolvers.Expand, and nothing is written before the whole list is valid. Child 3 reuses
// this editor for the mid-session offer.
func (m *menu) editResolvers(ctx context.Context, e *env, name string) error {
	profiles, err := config.LoadProfiles()
	if err != nil {
		return err
	}
	prof, ok := profiles.Profile[name]
	if !ok {
		return fmt.Errorf("profile %q not found", name)
	}
	printResolvers(e.stdout, name, profileResolvers(prof))
	list, err := askResolverList(ctx, m.ask, e.stdout)
	if err != nil {
		return err
	}
	prof.Resolvers = list
	if err := m.save(profiles); err != nil {
		return err
	}
	printResolvers(e.stdout, name, list)
	fmt.Fprintf(e.stdout, "Resolvers of profile %q saved.\n", name)
	return nil
}

// editResolversItem edits the resolvers of the current profile.
func (m *menu) editResolversItem(ctx context.Context, e *env, _ <-chan struct{}) error {
	profiles, err := config.LoadProfiles()
	if err != nil {
		return err
	}
	return m.editResolvers(ctx, e, currentProfile(profiles))
}

// checkResolversItem checks the resolvers of the current profile and offers to drop the
// entries that failed. An exitCodeError from the body means nothing passed the check, and
// the body has printed that itself: removing every resolver is not offered.
func (m *menu) checkResolversItem(ctx context.Context, e *env, _ <-chan struct{}) error {
	profiles, err := config.LoadProfiles()
	if err != nil {
		return err
	}
	name := currentProfile(profiles)
	outcomes, err := m.check(ctx, e, name)
	if err != nil {
		return err
	}

	failed := failedEntries(outcomes)
	if len(failed) == 0 {
		return nil
	}
	kept := keptEntries(outcomes)
	if len(kept) == 0 {
		// It cannot happen: a run where nothing passed is an exitCodeError above, and an
		// empty list must never be written.
		return nil
	}

	fmt.Fprintf(e.stdout, "%d of %d resolvers failed: %s\n", len(failed), len(failed)+len(kept), strings.Join(failed, ", "))
	answer, err := m.ask.Ask(ctx, "Remove them and save the profile? (y/N): ")
	if err != nil {
		return readError(ctx, err)
	}
	if !isYes(answer) {
		return nil
	}

	// The check took a while: reload before writing the file back.
	profiles, err = config.LoadProfiles()
	if err != nil {
		return err
	}
	prof, ok := profiles.Profile[name]
	if !ok {
		return fmt.Errorf("profile %q not found", name)
	}
	prof.Resolvers = kept
	if err := m.save(profiles); err != nil {
		return err
	}
	printResolvers(e.stdout, name, kept)
	return nil
}

// failedEntries is the entries with no passing check, in the order of the checks, without
// duplicates.
func failedEntries(outcomes []resolverOutcome) []string {
	return entryList(outcomes, false)
}

// keptEntries is the entries with at least one passing check, in the order of the checks,
// without duplicates. The network entry expands to one check per system server and stays
// once.
func keptEntries(outcomes []resolverOutcome) []string {
	return entryList(outcomes, true)
}

// entryList collects the entries by their aggregated result: an entry stays if any of its
// checks passed.
func entryList(outcomes []resolverOutcome, pass bool) []string {
	passed := make(map[string]bool)
	for _, outcome := range outcomes {
		if outcome.ok {
			passed[outcome.entry] = true
		}
	}

	var entries []string
	seen := make(map[string]bool)
	for _, outcome := range outcomes {
		// The bootstrap parse error has no entry: it is not a resolver of the profile.
		if outcome.entry == "" || seen[outcome.entry] {
			continue
		}
		seen[outcome.entry] = true
		if passed[outcome.entry] == pass {
			entries = append(entries, outcome.entry)
		}
	}
	return entries
}

// newExitSecretItem replaces the exit credential of the current profile: the password of a
// password exit, the key and its passphrase of a key exit. The shape follows the exit type
// and the reference the profile carries, so an ssh profile with a key never gains a
// password_ref and the other way round; switching the method of a profile is a later
// change. The value goes to the secret store first under a fresh name; the profile is saved
// second, so a failed save can remove the new entry and leave the old reference and value
// in place.
func (m *menu) newExitSecretItem(ctx context.Context, e *env, _ <-chan struct{}) error {
	profiles, err := config.LoadProfiles()
	if err != nil {
		return err
	}
	name := currentProfile(profiles)
	prof, ok := profiles.Profile[name]
	if !ok {
		return fmt.Errorf("profile %q not found", name)
	}
	if prof.Exit.User == "" {
		// A credential without a login is refused at connect time: the menu must not
		// build that profile.
		fmt.Fprintf(e.stdout, "profile %q has no exit login, so the exit secret is not used\n", name)
		return nil
	}

	// An absent exit type is socks5, the product default the connect applies.
	exitType := prof.Exit.Type
	if exitType == "" {
		exitType = "socks5"
	}
	var secret exitSecret
	if exitType == "ssh" && prof.Exit.KeyRef != "" {
		if secret.key, err = askExitKey(ctx, m.ask, e.stdout); err != nil {
			return err
		}
	} else if secret.password, err = askExitPassword(ctx, m.ask, e.stdout, exitType); err != nil {
		return err
	}

	store, err := m.store()
	if err != nil {
		return err
	}
	suffix, old := secret.secretSuffix(), prof.Exit.PasswordRef
	if secret.key != "" {
		old = prof.Exit.KeyRef
	}
	newName, err := newSecretName(name, suffix, old)
	if err != nil {
		return err
	}
	ref, err := store.Put(newName, secret.value())
	if err != nil {
		return fmt.Errorf("cannot store the %s: %w", secret.kind(), err)
	}

	secret.apply(&prof.Exit, ref)
	if err := m.save(profiles); err != nil {
		if deleteErr := store.Delete(ref); deleteErr != nil {
			return errors.Join(err,
				fmt.Errorf("the %s could not be removed from the secret store: %w", secret.kind(), deleteErr))
		}
		return errors.Join(err, fmt.Errorf("the %s was removed from the secret store", secret.kind()))
	}

	// The old entry goes only after the file points at the new one: a failed save above has
	// kept it.
	if old != "" {
		if err := store.Delete(old); err != nil {
			fmt.Fprintf(e.stderr, "the previous %s could not be removed from the secret store: %v\n", secret.kind(), err)
		}
	}
	fmt.Fprintf(e.stdout, "The %s of profile %q was updated.\n", secret.kind(), name)
	return nil
}

// newSecretName is the name of the new exit secret entry: the wizard's name when the
// profile has none yet, a fresh one otherwise, with the suffix of the shape
// (exitSecret.secretSuffix). A fresh name is what makes the rollback possible: Put under
// the existing name would overwrite the old value under the same id before the profile was
// saved.
func newSecretName(profile, suffix, oldRef string) (string, error) {
	if oldRef == "" {
		return profile + suffix, nil
	}
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("cannot create a secret name: %w", err)
	}
	return profile + suffix + "-" + hex.EncodeToString(buf[:]), nil
}

// changePinnedFingerprintItem asks a new pinned host key fingerprint of the current profile
// with the current value as the default answer, and saves it once. The value is public, so
// it is shown in full; a profile that is not an SSH exit has no fingerprint to change.
func (m *menu) changePinnedFingerprintItem(ctx context.Context, e *env, _ <-chan struct{}) error {
	profiles, err := config.LoadProfiles()
	if err != nil {
		return err
	}
	name := currentProfile(profiles)
	prof, ok := profiles.Profile[name]
	if !ok {
		return fmt.Errorf("profile %q not found", name)
	}
	if prof.Exit.Type != "ssh" {
		fmt.Fprintf(e.stdout, "profile %q has no pinned host key: exit.type is not ssh\n", name)
		return nil
	}

	fingerprint, err := askFingerprint(ctx, m.ask, e.stdout, prof.Exit.HostKey)
	if err != nil {
		return err
	}
	if fingerprint == prof.Exit.HostKey {
		fmt.Fprintln(e.stdout, "nothing changed")
		return nil
	}
	prof.Exit.HostKey = fingerprint
	if err := m.save(profiles); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "Pinned host key fingerprint of profile %q updated.\n", name)
	return nil
}

// changeServerItem asks for a new domain and key with the current values as the defaults.
// Both answers are collected before anything is written, and the file is written once.
func (m *menu) changeServerItem(ctx context.Context, e *env, _ <-chan struct{}) error {
	profiles, err := config.LoadProfiles()
	if err != nil {
		return err
	}
	name := currentProfile(profiles)
	prof, ok := profiles.Profile[name]
	if !ok {
		return fmt.Errorf("profile %q not found", name)
	}

	domain, err := askDomainValue(ctx, m.ask, e.stdout, prof.Domain)
	if err != nil {
		return err
	}
	pubkey, err := askPubkeyValue(ctx, m.ask, e.stdout, prof.Pubkey)
	if err != nil {
		return err
	}

	if domain == prof.Domain && pubkey == prof.Pubkey {
		fmt.Fprintln(e.stdout, "nothing changed")
		return nil
	}
	prof.Domain = domain
	prof.Pubkey = pubkey
	if err := m.save(profiles); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "Profile %q updated: domain %s, key %s.\n", name, maskSensitive(domain), maskSensitive(pubkey))
	return nil
}
