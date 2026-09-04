//go:build !linux && !windows

package process

import "syscall"

// sysProcAttr puts each supervised process in its own process group so the
// whole tree can be signalled. Parent-death signalling (Pdeathsig) is
// Linux-only; elsewhere the supervisor's Close path is what prevents orphans.
//
// A process getting a terminal asks for no group of its own: opening one
// makes it a session leader, which already puts it in a group of its own, and
// setpgid on a session leader is refused by the kernel.
func sysProcAttr(tty bool) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: !tty}
}
