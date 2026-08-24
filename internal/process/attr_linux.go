//go:build linux

package process

import "syscall"

// sysProcAttr puts each supervised process in its own process group so the
// whole tree can be signalled, and — on Linux — has the kernel SIGKILL it if
// this process dies without running its shutdown path, so a cancelled session
// still leaves no orphans.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}
