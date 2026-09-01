package runner

// Killing what a command started, not just the command.
//
// Every captured command is `$SHELL -c <text>`, so the process the runner
// holds is a shell and the work is its children. Cancelling used to signal
// only that shell: the context's default is to kill the one process it
// spawned, which leaves a build, a dev server or a test runner alive with
// nothing left watching it. A session could be interrupted half a dozen times
// and leave half a dozen orphans behind, each still holding the port or the
// lock the next attempt needs.
//
// So a captured command gets its own process group and cancellation signals
// the group. Interrupt first, because that is what the reader pressed and it
// is the signal a test runner or a compiler knows how to stop cleanly on;
// then kill, after a grace period, for anything that ignored it.
//
// WaitDelay is the backstop underneath both. A grandchild that inherited the
// output pipe keeps it open after its parent is gone, and a read on that pipe
// is what Wait is blocked on — so without a delay a command that is already
// dead can still hold the turn open indefinitely.
// See docs/capabilities/containment.md#a-cancelled-command-takes-its-children-with-it.

import (
	"os/exec"
	"time"
)

const (
	// killGrace is how long a cancelled group has to stop on its own before
	// it is killed. Long enough for a test runner to finish tearing down its
	// fixtures, short enough that nobody watches it.
	killGrace = 3 * time.Second
	// waitDelay bounds how long Wait may block after cancellation on output
	// that some surviving relative still holds open. It is longer than
	// killGrace so the ordinary path — interrupt, then kill the group — is
	// what actually stops the command, and this only fires when that did not.
	waitDelay = killGrace + 2*time.Second
)

// prepare is the one place a captured command is configured. Every capture
// form goes through it, because a form that missed one of these settings is a
// path that leaks processes only under the conditions nobody tests.
func prepare(cmd *exec.Cmd, dir string) *exec.Cmd {
	cmd.Dir = dir
	cmd.Env = Environ()
	cmd.SysProcAttr = sysProcAttr()
	cmd.WaitDelay = waitDelay
	cmd.Cancel = func() error { return stopGroup(cmd) }
	return cmd
}
