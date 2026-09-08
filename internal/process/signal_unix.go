//go:build !windows

package process

import "syscall"

// signalGroup signals a process's whole group (never just the leader). The
// negative pid is the group; the supervisor puts every child in one of its
// own (attr_*.go) so that a dev server's own children go with it.
//
// The switch is what makes the two asks real, and a cast cannot replace it:
// termSignal's values are this package's own ordinals (signal.go), so
// syscall.Signal(signalTerm) is signal 0 — a liveness probe that ends nothing
// — and syscall.Signal(signalKill) is SIGHUP, which a daemonised or nohup'd
// process ignores by definition. Cast either one and `process stop` reports
// success having killed nothing, and the tree outlives the session.
func signalGroup(pid int, sig termSignal) {
	var real syscall.Signal
	switch sig {
	case signalKill:
		real = syscall.SIGKILL
	default:
		// signalTerm, and anything a later value might add: a supervisor
		// that has to guess guesses in the direction the process can still
		// clean up after.
		real = syscall.SIGTERM
	}
	_ = syscall.Kill(-pid, real)
}
