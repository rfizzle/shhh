package chat

// The tools screen, from the command line to a server's reason and the
// checkout's answer (program_routes_test.go says what these are for).

import "testing"

// Bare /mcp reaches the screen through the whole program on the servers,
// the pointer walks to the one that did not start and shows why, [a] on the
// project server asks before it answers, and esc goes back to the prompt.
func TestProgram_McpOpensWhereTheToolsCameFrom(t *testing.T) {
	h := toolsFixture()
	m, _ := scriptedSession(programTurn{text: "nobody asked the model"})
	m = withTools(m, h)
	tm := runProgramAt(t, m, 130, 40)

	send(tm, "/mcp")
	waitForAll(t, tm, "/mcp · 5 servers", "mcp servers", "✓ docs", "language servers")
	programPress(t, tm, "down")
	programPress(t, tm, "down")
	waitForAll(t, tm, "[a] trust this checkout")
	programPress(t, tm, "a")
	waitForAll(t, tm, "Answer for the checkout?")
	programPress(t, tm, "y")
	waitForAll(t, tm, "trusted /repo", "[a] withdraw trust")
	programPress(t, tm, "down")
	waitForAll(t, tm, "server linear: connect: EOF", "what would move it")
	programPress(t, tm, "esc")
	waitForGone(t, tm, "/mcp · 5 servers")

	frameHas(t, finalFrame(t, tm), "trusted /repo")
	if len(h.asked) != 1 || len(h.asked[0]) != 0 {
		t.Fatalf("the program's [a] asked the trust seam %v", h.asked)
	}
}
