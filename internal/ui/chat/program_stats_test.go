package chat

// The spend screen, from the command line to a turn on the turns screen
// (program_routes_test.go says what these are for).

import (
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

// A session that has run two billed turns: `/stats` reaches the bill through
// the whole program with the model and both turns on it, the pointer walks to
// the older turn over the group rails, and enter opens it on the turns
// screen.
func TestProgram_StatsOpensTheBillAndATurnFromIt(t *testing.T) {
	usage := &provider.Usage{PromptTokens: 1200, CompletionTokens: 80}
	m, _ := scriptedSession(
		programTurn{text: "The first answer.", usage: usage},
		programTurn{text: "The second answer.", usage: usage},
	)
	tm := runProgramAt(t, m, 130, 40)

	send(tm, "first")
	waitForText(t, tm, "The first answer.")
	send(tm, "second")
	waitForText(t, tm, "The second answer.")

	send(tm, "/stats")
	waitForAll(t, tm, "/stats · 1 model · 2 turns", "session total", "by model", "by turn", "✓ turn 2", "✓ turn 1")
	// The total, the model, turn 2, turn 1: three steps down.
	programPress(t, tm, "down", "down", "down")
	waitForText(t, tm, "[enter] open the turn")
	programPress(t, tm, "enter")
	waitForAll(t, tm, "/turns", "turn 1")
	programPress(t, tm, "esc")
	waitForGone(t, tm, "/turns")

	frameHas(t, finalFrame(t, tm), "The second answer.")
}
