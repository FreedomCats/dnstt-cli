//go:build windows

package cli

import (
	"context"
	"syscall"
	"unsafe"
)

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	getConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
)

// pauseBeforeExit waits for Enter after an error in a window opened by a double click
// (architecture.md §10a): its console belongs to one process, and the window would close
// with it before the reason was read. A console shared with a shell stays after the program
// exits, so there is nothing to wait for.
//
// The question goes through the menu's prompter: internal/lines stays the one reader of
// stdin.
func (m *menu) pauseBeforeExit(ctx context.Context) {
	if !ownsConsole() {
		return
	}
	_, _ = m.ask.Ask(ctx, "Press Enter to close this window: ")
}

// ownsConsole reports whether this process is the only one on its console:
// GetConsoleProcessList returns the number of processes attached to it. A buffer of two is
// enough: anything above one means the console is shared and the pause is not needed.
func ownsConsole() bool {
	var pids [2]uint32
	n, _, _ := getConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	return n == 1
}
