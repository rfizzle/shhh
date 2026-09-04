//go:build windows

package storage

import "os"

// pidAlive reports whether a process with this id is still running.
//
// Windows has no signal 0 to send: Process.Signal refuses anything but a
// kill, so the existence check is the open itself. FindProcess is
// OpenProcess here and fails outright when nothing answers to the id, which
// is the difference from Unix that makes this file necessary. The handle it
// hands back is a real one and is released again straight away — a leaked
// handle keeps the exited process's slot alive, which would make a dead
// session look live for as long as shhh runs.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}
