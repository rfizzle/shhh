package keys

// The keyboard shhh ships is one keyboard on every platform
// (docs/interface/reserved-keys.md#one-keyboard-on-every-platform).
//
// On macOS the two stock terminals send Option+letter as the character it
// composes until a profile setting is ticked, so an alt chord would be dead
// there by default. That is why keys.go declares no alt chord at all: the
// agent family is on the function row, which every terminal delivers with
// nothing set, and what keys.go declares is what every desk ships. The one
// thing that still differs by desk is a sentence rather than a key — how the
// line editor moves by word — which is what Platform is asked for.

import (
	"runtime"
	"testing"
)

// Platform is the platform this process runs on, which is what a surface
// asks when a sentence it prints depends on the desk rather than on a key:
// how the line editor moves by word, say.
func Platform() string { return running }

// running is Platform's answer. A test binary answers linux whatever the
// host, because the goldens that print the word moves are written against
// the CI runner's desk, and a suite that passed on one desk and failed on the
// next would be testing the desk.
var running = func() string {
	if testing.Testing() {
		return "linux"
	}
	return runtime.GOOS
}()

// UsePlatform makes Platform answer platform, as though the process had
// started there, and returns the call that puts the previous answer back. It
// is for tests of the sentences that depend on the desk; the keyboard itself
// is the same on every one.
func UsePlatform(platform string) (restore func()) {
	was := running
	running = platform
	return func() { running = was }
}
