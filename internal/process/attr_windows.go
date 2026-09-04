//go:build windows

package process

import "syscall"

// sysProcAttr gives a supervised process a console of its own, so that the
// Ctrl+C for the session's own terminal is not delivered to a background
// process the reader did not mean to interrupt. Process groups, and the
// parent-death signalling Linux has, do not exist here; signal_windows.go is
// what ends a tree instead.
// A terminal is never given to a process here (pty_windows.go says why), so
// the request cannot reach this.
func sysProcAttr(bool) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

// createNewProcessGroup is CREATE_NEW_PROCESS_GROUP, spelled out rather than
// imported so this file needs nothing outside the standard library.
const createNewProcessGroup = 0x00000200
