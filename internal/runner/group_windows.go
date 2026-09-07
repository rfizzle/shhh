//go:build windows

package runner

import "syscall"

// Windows has no process groups in the POSIX sense and no Setpgid, so a
// captured command is configured with neither, and no parent-death signal
// either. Cancellation falls back to the single-process kill the context
// would have done anyway; WaitDelay, which is portable, is what still bounds
// the wait, and the drain a leaving session runs is what stops the command
// before the session goes.
func sysProcAttr() *syscall.SysProcAttr { return nil }

// stopGroup ends the command. os.Process holds a handle here rather than a
// number, so a kill that arrives after the wait cannot land on a stranger and
// needs none of the guarding the unix path does.
func stopGroup(g *group) error {
	if g.cmd.Process == nil {
		return nil
	}
	return g.cmd.Process.Kill()
}

// interruptGroup and killGroup are the same act on this platform: there is no
// signal to ask with, only the kill.
func interruptGroup(g *group) { _ = stopGroup(g) }

func killGroup(g *group) { _ = stopGroup(g) }
