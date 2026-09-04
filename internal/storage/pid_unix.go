//go:build !windows

package storage

import (
	"errors"
	"os"
	"syscall"
)

// pidAlive reports whether a process with this id is still running.
//
// Signal 0 is the portable Unix existence check: nothing is delivered, the
// kernel only resolves the id. ESRCH is the one answer that means gone;
// EPERM means the process is there and owned by somebody else, which on a
// shared machine is still a running process and must not be read as a dead
// one — reading it as dead is what would close a live session's row and let
// two sessions each believe they are alone.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// FindProcess never fails on Unix, so the signal is the whole check.
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, os.ErrPermission)
}
