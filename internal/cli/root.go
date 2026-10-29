// Package cli parses the dnstt-cli command line and runs what stands behind it.
//
// Parsing uses the standard flag package with subcommands, without external libraries:
// there are few commands, their flags are simple, and a library would bring its own help
// format, its own exit code conventions and one more dependency to keep updated.
//
// Only the frame lives here: the command list, help and exit codes. Command bodies live
// next to it.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/FreedomCats/dnstt-cli/internal/app"
)

// Exit codes. There are three, and they are kept apart on purpose: a script needs to tell
// "the command was typed wrong" from "the command ran and failed".
const (
	exitOK    = 0 // everything worked
	exitError = 1 // the command ran and returned an error
	exitUsage = 2 // a command line error: no such command, flag or argument
)

// Build is build information, set in cmd/dnstt-cli through -ldflags.
type Build struct {
	Version string
	Commit  string
}

// env is a command's environment: input and output streams, build information and what
// tests replace.
//
// A separate struct for the sake of tests: they put buffers in place of os.Stdout and
// os.Stderr, and commands write nowhere but to env.
type env struct {
	stdout io.Writer
	stderr io.Writer
	stdin  io.Reader
	build  Build
}

// command is one dnstt-cli command.
//
// A command has no flag set of its own here: its run creates one, because parsing and
// flag values are needed in one place. Help for a command is printed the same way — help
// calls run with the -h argument.
type command struct {
	// name is what the command is called on the command line.
	name string
	// args is the syntax after the command name, for the "Usage" line.
	args string
	// summary is one line for the command list. Lower case, no full stop.
	summary string
	// description is a paragraph between the syntax line and the flag list. Optional.
	description string
	// run parses its arguments and does the work.
	run func(ctx context.Context, e *env, args []string) error
}

// commands lists all dnstt-cli commands in the order help shows them: first what the
// program is run for, then the service ones.
func commands() []*command {
	return []*command{
		versionCommand(),
		helpCommand(),
	}
}

func lookup(name string) *command {
	for _, cmd := range commands() {
		if cmd.name == name {
			return cmd
		}
	}
	return nil
}

// Main is the CLI entry point. It returns the exit code; cmd/dnstt-cli calls os.Exit.
func Main(args []string, build Build) int {
	e := &env{
		stdout: os.Stdout,
		stderr: os.Stderr,
		stdin:  os.Stdin,
		build:  build,
	}
	return run(context.Background(), e, args)
}

// run is split from Main for the sake of tests: they need their own streams and context.
func run(ctx context.Context, e *env, args []string) int {
	if len(args) == 0 {
		// A question "what can you do", not an error: the command list goes to stdout and
		// the exit code is zero.
		printUsage(e.stdout)
		return exitOK
	}

	name := args[0]
	if isHelpFlag(name) {
		printUsage(e.stdout)
		return exitOK
	}

	cmd := lookup(name)
	if cmd == nil {
		fmt.Fprintf(e.stderr, "%s: unknown command %q\n", app.Name, name)
		fmt.Fprintf(e.stderr, "Command list: %s help\n", app.Name)
		return exitUsage
	}

	return report(e, cmd, cmd.run(ctx, e, args[1:]))
}

// report turns a command's error into an exit code and prints what has not been said yet.
func report(e *env, cmd *command, err error) int {
	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, flag.ErrHelp):
		// Help was asked for, so printing it is the successful outcome.
		return exitOK
	case errors.Is(err, errReported):
		// The flag package has already printed both the error and the help.
		return exitUsage
	}

	var ue usageError
	isUsage := errors.As(err, &ue)
	if errors.Is(err, context.Canceled) {
		fmt.Fprintf(e.stderr, "%s %s: interrupted\n", app.Name, cmd.name)
		return exitError
	}
	fmt.Fprintf(e.stderr, "%s %s: %v\n", app.Name, cmd.name, err)
	if isUsage {
		fmt.Fprintf(e.stderr, "Help: %s %s -h\n", app.Name, cmd.name)
		return exitUsage
	}
	return exitError
}

// usageError is a command line error not reported yet: an extra argument, a missing
// subcommand, incompatible flags. It differs from an ordinary error by the exit code and a
// hint where to look for help.
type usageError struct {
	msg string
}

func (e usageError) Error() string { return e.msg }

func usagef(format string, a ...any) error {
	return usageError{msg: fmt.Sprintf(format, a...)}
}

// errReported is an error already shown to the user. It must not be printed a second
// time: the flag package writes both the reason and the command's help itself.
var errReported = errors.New("error in command arguments")

// newFlagSet creates a command's flag set with our help instead of the standard one.
func newFlagSet(cmd *command) *flag.FlagSet {
	fs := flag.NewFlagSet(app.Name+" "+cmd.name, flag.ContinueOnError)
	fs.Usage = func() { writeUsage(cmd, fs) }
	return fs
}

// parse parses a command's flags.
//
// Help for -h goes to stdout and an error message to stderr: help was asked for, the
// error was not. The flag package writes both into one stream, so the stream is chosen in
// advance, from the arguments themselves.
func parse(e *env, fs *flag.FlagSet, args []string) error {
	if wantsHelp(args) {
		fs.SetOutput(e.stdout)
	} else {
		fs.SetOutput(e.stderr)
	}

	err := fs.Parse(args)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, flag.ErrHelp):
		return err
	default:
		return fmt.Errorf("%w: %w", errReported, err)
	}
}

func isHelpFlag(arg string) bool {
	return arg == "-h" || arg == "-help" || arg == "--help"
}

// wantsHelp reports whether help was asked for. Arguments after "--" do not count: they
// are values, not flags.
func wantsHelp(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if isHelpFlag(arg) {
			return true
		}
	}
	return false
}

// printUsage prints the command list — what is shown when run without arguments.
func printUsage(w io.Writer) {
	fmt.Fprintf(w, "%s — a console client for dnstt: a SOCKS5 proxy or a VPN through a DNS tunnel.\n", app.Name)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Usage: %s <command> [flags] [arguments]\n", app.Name)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	for _, cmd := range commands() {
		fmt.Fprintf(w, "  %-9s %s\n", cmd.name, cmd.summary)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Help for a command: %s help <command> or %s <command> -h\n", app.Name, app.Name)
}

// writeUsage prints help for one command: the syntax line, the description and the flags.
// The flag package calls it through fs.Usage, so it writes where the flag set writes.
func writeUsage(cmd *command, fs *flag.FlagSet) {
	w := fs.Output()

	fmt.Fprintf(w, "Usage: %s %s", app.Name, cmd.name)
	if cmd.args != "" {
		fmt.Fprintf(w, " %s", cmd.args)
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "\n%s\n", capitalize(cmd.summary))
	if cmd.description != "" {
		fmt.Fprintf(w, "\n%s\n", strings.TrimRight(cmd.description, "\n"))
	}

	if hasFlags(fs) {
		fmt.Fprintln(w, "\nFlags:")
		fs.PrintDefaults()
	}
}

func hasFlags(fs *flag.FlagSet) bool {
	found := false
	fs.VisitAll(func(*flag.Flag) { found = true })
	return found
}

// capitalize raises the first letter: summary is written in lower case for an even command
// list, while in a command's help it is a sentence of its own.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// --- version ---

func versionCommand() *command {
	return &command{
		name:    "version",
		summary: "build version and commit",
		run:     runVersion,
	}
}

func runVersion(_ context.Context, e *env, args []string) error {
	fs := newFlagSet(versionCommand())
	if err := parse(e, fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usagef("extra argument: version takes no arguments")
	}

	fmt.Fprintf(e.stdout, "%s %s (%s)\n", app.Name, e.build.Version, e.build.Commit)
	fmt.Fprintf(e.stdout, "build: %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	return nil
}

// --- help ---

func helpCommand() *command {
	return &command{
		name:    "help",
		args:    "[command]",
		summary: "help for a command",
		run:     runHelp,
	}
}

func runHelp(ctx context.Context, e *env, args []string) error {
	// help's own flag set is empty, but it is created all the same: otherwise
	// `dnstt-cli help -h` would look for a command named "-h".
	fs := newFlagSet(helpCommand())
	if err := parse(e, fs, args); err != nil {
		return err
	}

	if fs.NArg() == 0 {
		printUsage(e.stdout)
		return nil
	}
	if fs.NArg() > 1 {
		return usagef("extra argument: help takes at most one command name")
	}

	cmd := lookup(fs.Arg(0))
	if cmd == nil {
		return usagef("unknown command %q; command list: %s help", fs.Arg(0), app.Name)
	}

	// The command prints its help itself: its flag set is created in its run, and there is
	// no other way to reach it. The returned flag.ErrHelp counts as success — see report.
	return cmd.run(ctx, e, []string{"-h"})
}
