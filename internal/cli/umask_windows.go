//go:build windows

package cli

// tightenUmask does nothing here: Windows has no umask, and the chmod after
// the listen is as much as the platform offers.
func tightenUmask() func() { return func() {} }
