//go:build !windows

package runner

import (
	"os/exec"
	"syscall"
	"time"
)

// sysProcAttr puts a captured command in its own process group, so the shell
// and everything it starts can be signalled as one.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// stopGroup interrupts the command's whole process group and kills whatever
// is still there after killGrace.
//
// The negative pid is the group: signalling cmd.Process alone is what the
// context's default does, and it is the behaviour this exists to replace.
// A group that has already gone reports ESRCH, which is the success case
// arriving by another name, so nothing is reported from here — the caller's
// error is the command's own outcome.
func stopGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	pgid := cmd.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGINT)
	time.AfterFunc(killGrace, func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })
	return nil
}
