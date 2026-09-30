package chat

// The alerts screen, from the command line to an episode's runs and back
// (program_routes_test.go says what these are for).

import (
	"context"
	"testing"
)

// A command that fails twice and then passes is one alert: `/alerts` reaches
// it through the whole program standing, enter shows both runs, and once the
// third run comes back clean the same screen says what answered it.
func TestProgram_AlertsOpensEveryAlertAndItsRuns(t *testing.T) {
	codes := []int{2, 2, 0}
	m, _ := scriptedSession(programTurn{text: "nobody asked the model"})
	m = m.WithRunner(legacyRunner(func(context.Context, string) (string, int) {
		code := codes[0]
		codes = codes[1:]
		return "checking", code
	}))
	tm := runProgramAt(t, m, 130, 40)
	runBang := func() {
		send(tm, "!make check")
		waitForText(t, tm, "run it once")
		tm.Send(programAllow)
		waitForGone(t, tm, "run it once")
	}

	runBang()
	runBang()
	send(tm, "/alerts")
	waitForAll(t, tm, "/alerts · 1 standing", "✗ make check", "exit 2 · 2 runs", "not yet")
	programPress(t, tm, "enter")
	waitForAll(t, tm, "each run", "[enter] hide the runs")
	programPress(t, tm, "esc")
	waitForGone(t, tm, "/alerts · 1 standing")

	runBang()
	send(tm, "/alerts")
	waitForAll(t, tm, "/alerts · 1 superseded", "✓ make check")

	frameHas(t, finalFrame(t, tm), "make check came back clean")
}
