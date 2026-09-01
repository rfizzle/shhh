//go:build !linux && !windows

package process

import "syscall"

// sysProcAttr puts each supervised process in its own process group so the
// whole tree can be signalled. Parent-death signalling (Pdeathsig) is
// Linux-only; elsewhere the supervisor's Close path is what prevents orphans.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
