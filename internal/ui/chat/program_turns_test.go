package chat

// The turns screen, from the command line to a turn's review and back
// (program_routes_test.go says what these are for).

import "testing"

// `/turns` reaches the screen through the whole program, newest first, and
// enter on the newest turn opens that turn's review, whose way out comes back
// to the list.
func TestProgram_TurnsOpensEveryTurnAndReviewsOne(t *testing.T) {
	root := programRepo(t, nil)
	tm := runProgramAt(t, twoEditTurns(root), 130, 40)
	runTwoTurns(t, tm)

	send(tm, "/turns")
	waitForAll(t, tm, "/turns · 2 turns", "[enter] review turn 2")
	programPress(t, tm, "enter")
	waitForAll(t, tm, "leave, change nothing", "turn 2")
	programPress(t, tm, "esc")
	waitForText(t, tm, "/turns · 2 turns")

	frameHas(t, finalFrame(t, tm), "✓ turn 2", "✓ turn 1", "1 file changed")
}
