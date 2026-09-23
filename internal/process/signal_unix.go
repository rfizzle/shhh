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
	// A negated small pid is a broadcast rather than a tree; signalable
	// (signal.go) is where that is spelled out.
	if !signalable(pid) {
		return
	}
	switch sig {
	case signalKill:
		for _, real := range killSequence {
			_ = syscall.Kill(-pid, real)
		}
	default:
		// signalTerm, and anything a later value might add: a supervisor
		// that has to guess guesses in the direction the process can still
		// clean up after.
		_ = syscall.Kill(-pid, syscall.SIGTERM)
	}
}

// killSequence is how a group is killed: frozen first, then killed. It
// mirrors the runner's, which is where the orphan was reproduced; the two
// packages share no code and a kill is not worth an import for.
//
// A signal to a group reaches its members one at a time, and on macOS the
// delivery can be preempted between two of them. A kill that reaches the
// child a shell is waiting on before it reaches the shell wakes the shell,
// which runs the next command in its list before its own kill arrives — and
// that command was not in the group when the kill went out, so it survives
// it. A stopped member cannot die, so nothing wakes while the stop is going
// round, and by the time the kill goes round nothing in the group is running
// to start anything new.
//
// A process started with a terminal takes no Setpgid (attr_unix.go), but
// opening the terminal makes it a session leader, and a session leader leads
// a group of its own whose id is its pid — so the negative pid names the
// same tree and the sequence reaches it the same way. SIGSTOP cannot be
// caught or ignored, so a program that has taken over the terminal's own
// stop key is frozen all the same, and the SIGKILL behind it ends a stopped
// member without resuming it first.
var killSequence = []syscall.Signal{syscall.SIGSTOP, syscall.SIGKILL}
