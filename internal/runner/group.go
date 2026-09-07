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
// The kill is a timer inside this process, which is why quitting needs
// StopCaptured below. A session that cancels and then exits never runs that
// timer, so a command which ignores the interrupt outlives the session that
// started it — the orphan the whole mechanism exists to stop, reappearing at
// the one moment nobody is left to notice it.
//
// Nothing signals a group whose wait has returned. The pid is the group's
// only while the command is still there; once it has been reaped the machine
// is free to hand that number to something else, and on a fork-heavy build it
// will do so well inside the grace.
//
// WaitDelay is the backstop underneath all of it. A grandchild that inherited
// the output pipe keeps it open after its parent is gone, and a read on that
// pipe is what Wait is blocked on — so without a delay a command that is
// already dead can still hold the turn open indefinitely.
// See docs/capabilities/containment.md#a-cancelled-command-takes-its-children-with-it.

import (
	"os/exec"
	"sync"
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
	// reapGrace is the slice of a quit's drain held back for the kill: the
	// signal still has to be delivered and the wait still has to return, and
	// neither is instant. The interrupt gets everything before it, so the
	// whole drain fits inside killGrace and quitting stays fast.
	reapGrace = 250 * time.Millisecond
)

// group is one captured command: the process group it leads, and whether it
// has been waited on. Stopping one needs both — see the pid note above.
type group struct {
	cmd *exec.Cmd
	// exited is closed once Wait has returned, which is the moment the pid
	// stops meaning this command.
	exited chan struct{}
	// once so that a second wait — which os/exec refuses anyway, with an
	// error — stays a refusal rather than becoming a panic on a closed
	// channel.
	once sync.Once
}

// live is every captured command that has started and not yet been waited on.
// It is package state for the reason the session environment and the adopter
// are: a session runs commands through half a dozen paths — plain, tailed,
// contained, in a sub-agent's worktree — and a session on its way out has to
// find all of them, not the ones that happened to go through the surface it
// was looking at.
var (
	liveMu sync.Mutex
	live   = map[*group]struct{}{}
)

// prepare is the one place a captured command is configured. Every capture
// form goes through it, because a form that missed one of these settings is a
// path that leaks processes only under the conditions nobody tests.
func prepare(cmd *exec.Cmd, dir string) *group {
	g := &group{cmd: cmd, exited: make(chan struct{})}
	cmd.Dir = dir
	cmd.Env = Environ()
	cmd.SysProcAttr = sysProcAttr()
	cmd.WaitDelay = waitDelay
	cmd.Cancel = func() error { return stopGroup(g) }
	return g
}

// start spawns the command and puts it on the live list. A spawn that failed
// never joins it: there is no process, so there is nothing for a drain to
// signal and nothing to take off again.
func (g *group) start() error {
	if err := g.cmd.Start(); err != nil {
		return err
	}
	liveMu.Lock()
	live[g] = struct{}{}
	liveMu.Unlock()
	return nil
}

// wait waits on the command and records that its pid is spent. Every capture
// waits through this rather than through cmd.Wait, because a group nothing
// marked as finished is one a pending kill can signal by number after the
// machine has handed that number to something else.
func (g *group) wait() error {
	err := g.cmd.Wait()
	g.once.Do(func() { close(g.exited) })
	g.release()
	return err
}

// release takes the group off the live list without saying it has exited. It
// is what a hand-over does: ownership moved to the taker, which decides when
// the command stops, so a session leaving must not signal it on the way out.
func (g *group) release() {
	liveMu.Lock()
	delete(live, g)
	liveMu.Unlock()
}

// StopCaptured ends every captured command still running and waits for it:
// interrupt, wait, kill whatever ignored the interrupt, wait again — the same
// sequence the process supervisor runs for a background process, run
// synchronously because the caller is about to exit and there is no later.
//
// A session that only cancels leaves the kill to a timer inside a process
// that is going away, so a command which ignores SIGINT survives the session
// that started it and goes on holding the port or the lock the next attempt
// needs. The whole drain is bounded by killGrace, so a quit with something to
// stop is still a quit and not a wait.
// See docs/capabilities/containment.md#a-cancelled-command-takes-its-children-with-it.
func StopCaptured() {
	liveMu.Lock()
	groups := make([]*group, 0, len(live))
	for g := range live {
		groups = append(groups, g)
	}
	liveMu.Unlock()
	if len(groups) == 0 {
		return
	}
	for _, g := range groups {
		interruptGroup(g)
	}
	awaitGroups(groups, killGrace-reapGrace)
	for _, g := range groups {
		killGroup(g)
	}
	awaitGroups(groups, reapGrace)
}

// awaitGroups blocks until every group has been waited on, or until the
// window is spent. One timer across all of them rather than one each: the
// window is how long the drain may take, not how long each command gets.
func awaitGroups(groups []*group, window time.Duration) {
	timer := time.NewTimer(window)
	defer timer.Stop()
	for _, g := range groups {
		select {
		case <-g.exited:
		case <-timer.C:
			return
		}
	}
}
