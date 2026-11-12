package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/FreedomCats/dnstt-cli/internal/config"
)

// runProfileImport imports one or more `slipnet://` links (architecture.md §10). In a
// terminal every link is asked by hidden input: it carries the exit password. Otherwise
// the links are read from stdin, one per line.
func runProfileImport(ctx context.Context, e *env, args []string) error {
	if len(args) > 0 {
		return usagef("extra argument: profile import takes no arguments")
	}

	profiles, err := config.LoadProfiles()
	if err != nil {
		return err
	}
	store, err := newSecretStore()
	if err != nil {
		return err
	}

	if isTerminalReader(e.stdin) {
		return importInteractive(ctx, e, profiles, store, newLinePrompter(e.stdin, e.stdout))
	}
	return importFromInput(e, profiles, store)
}

// importInteractive asks one link at a time. A bad paste is reported and the question
// repeats; after each imported profile it asks for another link, and an empty answer
// finishes. The end of input cancels when nothing was imported yet.
func importInteractive(ctx context.Context, e *env, profiles *config.Profiles, store config.SecretStore, p prompter) error {
	imported := 0
	for {
		question := "slipnet:// link: "
		if imported > 0 {
			question = "Another link (Enter to finish): "
		}
		ok, err := importOne(ctx, e, p, profiles, store, question)
		if err != nil {
			// The end of input cancels only when nothing was written: earlier links of
			// this run stay imported.
			if imported > 0 && errors.Is(err, errCancelled) {
				return nil
			}
			return err
		}
		if !ok {
			if imported == 0 {
				return errCancelled
			}
			return nil
		}
		imported++
	}
}

// importOne asks one link and imports it. A bad paste is reported and the question repeats;
// the name is asked when the link has none or it is taken. An empty answer returns false
// and writes nothing; the end of input is errCancelled, or ctx.Err() when the context was
// cancelled.
func importOne(ctx context.Context, e *env, p prompter, profiles *config.Profiles, store config.SecretStore, question string) (imported bool, err error) {
	for {
		answer, err := p.AskSecret(ctx, question)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return false, ctxErr
			}
			if errors.Is(err, io.EOF) {
				return false, errCancelled
			}
			return false, err
		}
		if strings.TrimSpace(answer) == "" {
			return false, nil
		}

		link, err := config.ParseLink(answer)
		if err != nil {
			fmt.Fprintln(e.stdout, err)
			continue
		}

		name := link.Name
		if name == "" || profiles.Profile[name] != nil {
			if name, err = (&wizard{ask: p, out: e.stdout}).askName(ctx, profiles); err != nil {
				return false, err
			}
		}
		prof, err := importLink(link, name, profiles, store)
		if err != nil {
			return false, err
		}
		reportImportedProfile(e, name, prof)
		return true, nil
	}
}

// importFromInput reads all links from stdin. A bad line is a warning on stderr and does
// not stop the rest; a taken name is a warning too, and that link is skipped.
func importFromInput(e *env, profiles *config.Profiles, store config.SecretStore) error {
	raw, err := io.ReadAll(e.stdin)
	if err != nil {
		return fmt.Errorf("cannot read the input: %w", err)
	}

	links, warnings := config.ParseLinks(string(raw))
	for _, warning := range warnings {
		fmt.Fprintln(e.stderr, warning)
	}
	if len(links) == 0 && len(warnings) == 0 {
		return errors.New("no slipnet:// link in the input")
	}

	imported := 0
	for _, link := range links {
		name := link.Name
		if name == "" {
			name = "home"
		}
		if profiles.Profile[name] != nil {
			fmt.Fprintf(e.stderr, "profile %q already exists; the link was skipped\n", name)
			continue
		}
		prof, err := importLink(link, name, profiles, store)
		if err != nil {
			return err
		}
		reportImportedProfile(e, name, prof)
		imported++
	}
	if imported == 0 {
		return errors.New("no profile was imported")
	}
	return nil
}

// importLink stores the link's password, adds its profile to a copy of the set and saves
// the copy. On a failed store or save nothing is left behind; a failed save after a
// successful Put removes the secret again. The caller's set is replaced only after the
// save, so the next link of the same run sees the imported names.
func importLink(link *config.Link, name string, profiles *config.Profiles, store config.SecretStore) (*config.Profile, error) {
	prof := link.Profile

	var ref string
	if link.Password != "" {
		var err error
		if ref, err = store.Put(name+".password", link.Password); err != nil {
			return nil, fmt.Errorf("cannot store the exit password: %w", err)
		}
		prof.Exit.PasswordRef = ref
	}

	next := &config.Profiles{
		DefaultProfile: profiles.DefaultProfile,
		Profile:        make(map[string]*config.Profile, len(profiles.Profile)+1),
	}
	for existing, p := range profiles.Profile {
		next.Profile[existing] = p
	}
	next.Profile[name] = prof
	if next.DefaultProfile == "" {
		next.DefaultProfile = name
	}

	if err := saveProfiles(next); err != nil {
		if ref == "" {
			return nil, err
		}
		if deleteErr := store.Delete(ref); deleteErr != nil {
			return nil, errors.Join(err,
				fmt.Errorf("the exit password could not be removed from the secret store: %w", deleteErr))
		}
		return nil, errors.Join(err, errors.New("the exit password was removed from the secret store"))
	}

	*profiles = *next
	return prof, nil
}

// reportImportedProfile says what was imported, never its password or link. A dnstt_ssh
// profile is stored but cannot connect until the SSH exit stage exists.
func reportImportedProfile(e *env, name string, prof *config.Profile) {
	fmt.Fprintf(e.stdout, "Profile %q imported (exit %s).\n", name, prof.Exit.Type)
	if prof.Exit.Type == "ssh" {
		fmt.Fprintln(e.stdout, "The SSH exit connects only after stage 06-ssh-exit; the profile is stored and cannot connect yet.")
	}
}
