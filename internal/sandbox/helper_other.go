//go:build !linux && !windows

package sandbox

import "golang.org/x/sys/unix"

// pollHangupEvents is nothing beyond the hang-up itself where the platform
// has no half-close event to ask for.
const pollHangupEvents = 0

// pollInputEvents asks the hang-up watch to be woken by input as well: a poll
// for no events does not report the hang-up of a pipe here.
const pollInputEvents = unix.POLLIN

// fionread is the BSDs' and macOS's request for the bytes queued on a
// descriptor (_IOR('f', 127, int)); x/sys does not name it.
const fionread = 0x4004667f

// queuedInput is how many bytes wait unread on fd.
func queuedInput(fd int) (int, error) { return unix.IoctlGetInt(fd, fionread) }
