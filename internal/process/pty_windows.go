//go:build windows

package process

import (
	"errors"
	"os"
	"os/exec"
)

// startPTY refuses on Windows. A console there is not a file the supervisor
// can read and write the way a pty is, and the pseudoconsole that comes
// closest is a different mechanism with a different contract — so this says
// no in a sentence rather than half-supporting it, and the process starts
// without one when the caller drops the request.
func startPTY(*exec.Cmd) (*os.File, error) {
	return nil, errors.New("a terminal cannot be given to a process on Windows; start it without pty and read its output with the read action")
}
