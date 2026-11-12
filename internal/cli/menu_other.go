//go:build !windows

package cli

import "context"

// pauseBeforeExit does nothing: a shell keeps the output of the program after it exits, so
// there is no window to hold open. The Windows version waits for Enter after an error in a
// window opened by a double click.
func (m *menu) pauseBeforeExit(context.Context) {}
