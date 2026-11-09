package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"

	"github.com/FreedomCats/dnstt-cli/internal/lines"
)

// prompter asks the person the questions of the wizard. It is an interface for the sake of
// tests and of the menu (E4), which will pass its own implementation.
type prompter interface {
	// Ask prints the question and returns the answer line.
	Ask(ctx context.Context, question string) (string, error)
	// AskSecret is Ask for a value that must not be echoed.
	AskSecret(ctx context.Context, question string) (string, error)
}

// linePrompter asks on a line reader and writes the questions to out. It owns the one
// reader of the process: the wizard is its only caller until the menu passes its own.
type linePrompter struct {
	in  io.Reader
	out io.Writer
	r   *lines.Reader
}

// newLinePrompter returns a prompter reading lines from in and writing questions to out.
func newLinePrompter(in io.Reader, out io.Writer) *linePrompter {
	return &linePrompter{in: in, out: out, r: lines.New(in)}
}

// Ask writes the question and returns the next line. The caller includes the default in
// the question: the wizard decides what an empty answer means.
func (p *linePrompter) Ask(ctx context.Context, question string) (string, error) {
	if _, err := fmt.Fprint(p.out, question); err != nil {
		return "", err
	}
	return p.r.Read(ctx)
}

// AskSecret is Ask with the terminal's echo turned off when in is a terminal. A
// redirected stdin or a test buffer has no echo to turn off: the same line reader is used,
// and the shared reader stays the only one reading the stream (internal/lines).
func (p *linePrompter) AskSecret(ctx context.Context, question string) (string, error) {
	if _, err := fmt.Fprint(p.out, question); err != nil {
		return "", err
	}
	if f, ok := p.in.(*os.File); ok {
		fd := int(f.Fd())
		if term.IsTerminal(fd) {
			hidden, err := term.ReadPassword(fd)
			// The terminal did not echo the Enter that ended the hidden input.
			fmt.Fprintln(p.out)
			if err != nil {
				return "", err
			}
			return string(hidden), nil
		}
	}
	return p.r.Read(ctx)
}
