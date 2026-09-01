//go:build !windows

package process

import "syscall"

// signalGroup signals a process's whole group (never just the leader). The
// negative pid is the group; the supervisor puts every child in one of its
// own (attr_*.go) so that a dev server's own children go with it.
func signalGroup(pid int, sig termSignal) {
	_ = syscall.Kill(-pid, syscall.Signal(sig))
}
