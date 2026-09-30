package chat

// The session's own working steps, from the model's steps calls to the rail
// (program_routes_test.go says what these are for).

import (
	"testing"

	"github.com/rfizzle/shhh/internal/plan"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
)

// A list the model sets with the steps tool reaches the rail's STEPS block
// through the whole program, and the next call replaces it whole: a step
// marked, a step reworded, and one taken back all land as the one list.
func TestProgram_TheSessionsOwnStepsReachTheRail(t *testing.T) {
	dir := fixtureDir(t, map[string]string{"loop.go": "package fixture\n", "round.go": "package fixture\n"})
	steps := func(id, args string) provider.ToolCall {
		return provider.ToolCall{ID: id, Name: plan.StepsToolName, Arguments: args}
	}
	m, _ := scriptedSession(
		programTurn{text: "Working through it in steps.\n", calls: append([]provider.ToolCall{
			steps("s1", `{"steps":[{"title":"Read the loop"},{"title":"Patch the limit"},{"title":"Test it"}]}`)}, reads("loop.go")...)},
		programTurn{text: "The ceiling is what is wrong.\n", calls: append([]provider.ToolCall{
			steps("s2", `{"steps":[{"title":"Read the loop","done":true},{"title":"Raise the ceiling","done":true},{"title":"Test again"}]}`)}, reads("round.go")...)},
		programTurn{text: "Changed round.go and round_test.go."},
	)
	m = m.WithWorkspace(dir).WithToolExecutor(plan.WrapStepsExecutor(subagent.RootedExecutor(dir, tools.Execute)))
	tm := runProgramAt(t, m, 130, 40)

	send(tm, "raise the round ceiling")
	waitForText(t, tm, "round_test.go")

	frameHas(t, finalFrame(t, tm), "STEPS", "2 of 3 · Test again")
}

// An approved plan being executed is the checklist, and bare /plan opens it
// on the steps screen through the whole program: the plan's own title and
// count in the header, the step the run carried out marked done and the one
// it has not reached queued.
func TestProgram_BarePlanOpensTheApprovedPlansChecklist(t *testing.T) {
	dir := fixtureDir(t, map[string]string{"loop.go": "package fixture\n"})
	m, _ := scriptedSession(
		programTurn{text: "Here is what I would do.\n\n## Plan: make the round limit recoverable\n\n1. Locate the round accounting\n   files: loop.go\n   action: read\n2. Return a sentinel when the rounds run out\n   files: loop.go\n   action: edit\n"},
		programTurn{text: "Locate the round accounting\n", calls: reads("loop.go")},
		programTurn{text: "The counter is read at the top of the loop."},
	)
	m = m.WithWorkspace(dir).WithToolExecutor(subagent.RootedExecutor(dir, tools.Execute))
	tm := runProgramAt(t, m, 130, 40)

	for range 4 {
		programPress(t, tm, "shift+tab")
	}
	waitForText(t, tm, "⏸ plan")
	send(tm, "plan the round counter change")
	waitForText(t, tm, "make the round limit recoverable")
	tm.Send(programHandover)
	programPress(t, tm, "enter")
	waitForText(t, tm, "read at the top of the loop")
	send(tm, "/plan")
	waitForAll(t, tm, "/plan · make the round limit recoverable · 1 of 2 done", "queued")

	frameHas(t, finalFrame(t, tm), "✓ Locate the round accounting", "the approved plan")
}
