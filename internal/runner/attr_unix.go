//go:build !linux && !windows

package runner

import "syscall"

// sysProcAttr puts a captured command in its own process group, so the shell
// and everything it starts can be signalled as one. Parent-death signalling
// (Pdeathsig) is Linux-only; here the drain a leaving session runs
// (StopCaptured) is the whole of what stops an orphan.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
