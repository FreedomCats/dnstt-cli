//go:build windows

// Command ctrlbreak sends Ctrl+Break to a console process by its PID, to stop a dnstt-cli
// running in the background the same way Ctrl+C would stop it.
//
//	tasklist /FI "IMAGENAME eq dnstt-cli.exe"
//	go run ./scripts/ctrlbreak -pid 31180
//
// It is needed for live checks from Git Bash and other shells where dnstt-cli runs in the
// background. `kill -INT` from Git Bash does not reach a Windows process, and a background
// process lives in its own group where Ctrl+C is disabled. Ctrl+Break to the group by PID
// does get through, and Go turns it into the same SIGINT as Ctrl+C: dnstt-cli stops the
// tunnel and cleans up after itself.
//
// The command detaches from its own console, attaches to the console of the process,
// removes signal handling from itself and generates CTRL_BREAK_EVENT.
//
// A process started with Start-Process in PowerShell sits in its own console but is not a
// group leader, so Ctrl+Break by PID cannot be sent there. Then the command sends Ctrl+C
// to its whole console if nobody but that process is in it; otherwise it is an error, and
// nothing is sent.
//
// The PID is the Windows PID from tasklist, not $! from Git Bash: that is an msys PID.
//
// Call it from a different shell invocation than the one that started dnstt-cli. If
// dnstt-cli and the command are started by one command line, they are in one console
// process group, and Ctrl+Break reaches the shell too: dnstt-cli stops cleanly, but the
// shell dies with it.
//
// Checked this way: Git Bash background (`dnstt-cli … &`), Start-Process with its own
// window (including -WindowStyle Hidden) and Start-Process -NoNewWindow. It does not work
// for a process started inside `cmd /c` in a shared console: it is not a group leader,
// and the system sends Ctrl+Break by its PID to the whole console, shell included.
//
// -pid is the Windows PID of the process; without the flag the command asks for it.
//
// Ported from PowerShell (scripts/send-ctrl-break.ps1).
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/FreedomCats/dnstt-cli/scripts/internal/script"
)

const self = "ctrlbreak"

var (
	kernel32              = windows.NewLazySystemDLL("kernel32.dll")
	procFreeConsole       = kernel32.NewProc("FreeConsole")
	procAttachConsole     = kernel32.NewProc("AttachConsole")
	procSetConsoleHandler = kernel32.NewProc("SetConsoleCtrlHandler")
	procGetConsoleProcs   = kernel32.NewProc("GetConsoleProcessList")
)

func main() {
	pid := flag.Uint("pid", 0, "Windows PID of the process")
	flag.Parse()
	if flag.NArg() > 0 {
		flag.Usage()
		os.Exit(2)
	}
	if *pid == 0 {
		// PowerShell asked for a mandatory parameter given without a value.
		fmt.Fprint(os.Stdout, "ProcessId: ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		v, err := strconv.ParseUint(strings.TrimSpace(line), 10, 32)
		if err != nil || v == 0 {
			script.Fail(self, fmt.Errorf("PID: %q", strings.TrimSpace(line)))
		}
		*pid = uint(v)
	}
	if err := run(uint32(*pid)); err != nil {
		script.Fail(self, err)
	}
}

func run(pid uint32) error {
	procFreeConsole.Call()
	if r, _, err := procAttachConsole.Call(uintptr(pid)); r == 0 {
		return fmt.Errorf("AttachConsole(%d): error %d", pid, errno(err))
	}
	// Without this the signal sent to the group would reach the command itself too.
	// SetConsoleCtrlHandler(NULL) removes only Ctrl+C from the process; Ctrl+Break is
	// absorbed by the Go runtime handler, but only when someone wants the signal: with
	// signal.Ignore it answers "not handled", and the default handler terminates the
	// command with STATUS_CONTROL_C_EXIT (exit code 130 in Git Bash).
	procSetConsoleHandler.Call(0, 1)
	signal.Notify(make(chan os.Signal, 1), os.Interrupt)
	err := windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, pid)
	if err == windows.ERROR_INVALID_PARAMETER {
		// The process is not a group leader: this happens if it was started in its own
		// console without CREATE_NEW_PROCESS_GROUP (Start-Process in PowerShell). Then
		// the signal goes to the whole console (group 0), but only if nobody but that
		// process and the command itself is in it. And it is Ctrl+C, not Ctrl+Break: the
		// command lets Ctrl+C pass by itself at the system level, while Ctrl+Break to the
		// whole console terminated it even with signal.Notify. Ctrl+C is not disabled for
		// such a process: no group of its own was created.
		if err = consoleHasOnly(pid); err == nil {
			err = windows.GenerateConsoleCtrlEvent(windows.CTRL_C_EVENT, 0)
		}
	}
	if err != nil {
		if e, ok := err.(windows.Errno); ok {
			return fmt.Errorf("GenerateConsoleCtrlEvent: error %d", uint32(e))
		}
		return err
	}
	time.Sleep(500 * time.Millisecond)
	procFreeConsole.Call()
	return nil
}

// consoleHasOnly checks that only the process pid and the command itself are attached to
// the console the command is attached to.
func consoleHasOnly(pid uint32) error {
	list := make([]uint32, 64)
	r, _, err := procGetConsoleProcs.Call(uintptr(unsafe.Pointer(&list[0])), uintptr(len(list)))
	n := uint32(r)
	if n == 0 {
		return fmt.Errorf("GetConsoleProcessList: error %d", errno(err))
	}
	if int(n) > len(list) {
		return fmt.Errorf("PID %d is not a group leader, and its console has %d processes", pid, n)
	}
	self := windows.GetCurrentProcessId()
	for _, p := range list[:n] {
		if p != pid && p != self {
			return fmt.Errorf("PID %d is not a group leader, and its console also has PID %d", pid, p)
		}
	}
	return nil
}

// errno returns the Windows error number, like Marshal.GetLastWin32Error.
func errno(err error) uint32 {
	if e, ok := err.(windows.Errno); ok {
		return uint32(e)
	}
	return 0
}
