//go:build windows

package process

import (
	"os/exec"
	"strconv"
)

// signalGroup ends a process and everything below it.
//
// Windows has no process groups to signal and no graceful signal to send one:
// a console application can be sent Ctrl+C only through the console it shares,
// which a supervised background process deliberately does not have. taskkill
// is the supported way to reach a tree, and /T is what makes it the tree
// rather than the one process.
//
// The consequence is that the grace period the supervisor gives a process
// before killing it does not buy it a clean shutdown here — /F is a hard
// terminate either way, and the softer form without it is a request a
// non-windowed process never receives. The sequence is kept because the
// supervisor's contract is the same on both platforms; what differs is that
// on Windows the first attempt is no gentler than the second.
func signalGroup(pid int, sig termSignal) {
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
}
