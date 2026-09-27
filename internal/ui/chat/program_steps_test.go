package chat

// The session's own working steps, from the stream's prose to the rail
// (program_routes_test.go says what these are for).

import (
	"testing"

	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
)

// A list written before a call reaches the rail's STEPS block through the
// whole program; a mark moves it and a list under `steps:` revises it, and
// the report the turn ends on is not taken as a list.
func TestProgram_TheSessionsOwnStepsReachTheRail(t *testing.T) {
	dir := fixtureDir(t, map[string]string{"loop.go": "package fixture\n", "round.go": "package fixture\n"})
	m, _ := scriptedSession(
		programTurn{text: "1. Read the loop\n2. Patch the limit\n3. Test it\n", calls: reads("loop.go")},
		programTurn{text: "progress: 1\nsteps:\n2. Raise the ceiling\n3. Test again\n", calls: reads("round.go")},
		programTurn{text: "progress: 2\nChanged:\n1. round.go\n2. round_test.go"},
	)
	m = m.WithWorkspace(dir).WithToolExecutor(subagent.RootedExecutor(dir, tools.Execute))
	tm := runProgramAt(t, m, 130, 40)

	send(tm, "raise the round ceiling")
	waitForText(t, tm, "round_test.go")

	frameHas(t, finalFrame(t, tm), "STEPS", "2 of 3 · Test again")
}
