//go:build linux

package process

import "syscall"

// sysProcAttr puts each supervised process in its own process group so the
// whole tree can be signalled, and — on Linux — has the kernel SIGKILL it if
// this process dies without running its shutdown path, so a cancelled session
// still leaves no orphans.
//
// A process getting a terminal asks for no group of its own: opening one
// makes it a session leader, which already puts it in a group of its own, and
// setpgid on a session leader is refused by the kernel.
func sysProcAttr(tty bool) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: !tty, Pdeathsig: syscall.SIGKILL}
}
