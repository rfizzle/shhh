//go:build windows

package runner

import (
	"os/exec"
	"syscall"
)

// Windows has no process groups in the POSIX sense and no Setpgid, so a
// captured command is configured with neither. Cancellation falls back to the
// single-process kill the context would have done anyway; WaitDelay, which is
// portable, is what still bounds the wait.
func sysProcAttr() *syscall.SysProcAttr { return nil }

func stopGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
