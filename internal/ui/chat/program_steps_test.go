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
