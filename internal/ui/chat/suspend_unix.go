//go:build !windows

package chat

import (
	"os"
	"os/signal"
	"syscall"
)

// StopProcess stops the whole process group, as Bubble Tea's own suspend
// does, and returns when the shell continues it.
var StopProcess = func() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGCONT)
	defer signal.Stop(c)
	_ = syscall.Kill(0, syscall.SIGTSTP)
	<-c
}
