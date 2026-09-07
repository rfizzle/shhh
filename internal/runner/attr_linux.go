//go:build linux

package runner

import "syscall"

// sysProcAttr puts a captured command in its own process group, so the shell
// and everything it starts can be signalled as one, and — on Linux — has the
// kernel kill that shell if this process goes without running its shutdown
// path.
//
// The parent-death signal is the backstop under the drain a leaving session
// runs (StopCaptured): the drain covers quitting, and this covers the endings
// no code of ours reaches — a crash, a SIGKILL, the terminal going away. It
// reaches the shell alone rather than the group, so it is the second answer
// and not the first.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}
