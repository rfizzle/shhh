//go:build !windows

package cli

import "syscall"

// tightenUmask makes new files private to the user and returns the call that
// puts the previous umask back.
func tightenUmask() func() {
	old := syscall.Umask(0o077)
	return func() { syscall.Umask(old) }
}
