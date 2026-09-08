package process

import "testing"

// The supervisor addresses a process tree by negating its pid, and two small
// values stop naming a tree once they are negated: -1 is POSIX's broadcast to
// every process the caller may signal, and -0 is the caller's own group. Both
// reach the session the supervisor is running in, so a stop that let one
// through would end the shell, the test run, or the CI job doing the stopping.
//
// This is checked on the predicate rather than by sending a signal, because a
// test that proved the broadcast by performing it would take the rest of the
// suite with it — which is exactly how the bug this guards was found.
func TestSignalable_RefusesAPidThatWouldNotNameAGroup(t *testing.T) {
	for _, pid := range []int{-1, 0, 1} {
		if signalable(pid) {
			t.Errorf("pid %d must never be signalled: negated it is a broadcast or this process's own group", pid)
		}
	}
	for _, pid := range []int{2, 1234, 999999} {
		if !signalable(pid) {
			t.Errorf("pid %d can lead a group of its own and must be signallable", pid)
		}
	}
}
