//go:build !windows

package runner

import (
	"syscall"
	"time"
)

// signalGroup signals the command's whole process group, or nothing at all
// when the group is already gone.
//
// The negative pid is the group: signalling cmd.Process alone is what the
// context's default does, and it is the behaviour this exists to replace.
// A group that has already gone reports ESRCH, which is the success case
// arriving by another name, so nothing is reported from here — the caller's
// error is the command's own outcome.
//
// The guard is not about that error. Once the wait has returned the pid has
// been reaped and the machine is free to hand it to something else, so a
// signal sent after that is not a late kill for this command; it is a live
// one for a stranger.
func signalGroup(g *group, sig syscall.Signal) {
	if g.cmd.Process == nil {
		return
	}
	select {
	case <-g.exited:
		return
	default:
	}
	_ = syscall.Kill(-g.cmd.Process.Pid, sig)
}

// interruptGroup asks the group to stop the way the reader's key asks.
func interruptGroup(g *group) { signalGroup(g, syscall.SIGINT) }

// killGroup ends the group without asking.
func killGroup(g *group) { signalGroup(g, syscall.SIGKILL) }

// stopGroup interrupts the command's whole process group and kills whatever
// is still there after killGrace.
func stopGroup(g *group) error {
	interruptGroup(g)
	if g.cmd.Process == nil {
		return nil
	}
	killAfterGrace(g.cmd.Process.Pid, g.exited, killGrace)
	return nil
}

// killAfterGrace kills the group once the grace is up, unless the wait
// returned first — in which case there is nothing of this command left to
// kill and the pid may already belong to something else.
//
// It waits on the wait rather than testing it when the timer fires, so the
// window between the two is as narrow as one select can make it.
func killAfterGrace(pgid int, exited <-chan struct{}, grace time.Duration) {
	timer := time.NewTimer(grace)
	go func() {
		defer timer.Stop()
		select {
		case <-exited:
		case <-timer.C:
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
	}()
}
