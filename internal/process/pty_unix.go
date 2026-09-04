//go:build !windows

package process

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

// startPTY starts cmd attached to a pseudo-terminal and returns the master
// side: everything the command writes to its terminal is read from it, and
// everything written to it arrives as the command's terminal input.
//
// It is a separate spawn from the ordinary one because a terminal is what
// some programs check before they will do anything useful: a REPL that only
// prompts on a tty, a tool that asks for a passphrase, a runner whose
// progress output is line-buffered into nothing when its output is a pipe.
// A pipe cannot be talked into any of that.
//
// The pty is also why the process's two output streams become one. A terminal
// has a single stream, so the split between stdout and stderr is gone by the
// time this returns anything, and there is nothing here that can put it back.
func startPTY(cmd *exec.Cmd) (*os.File, error) {
	return pty.Start(cmd)
}
