package chat

// A server the session did not wait for, from the first frame to the turn its
// tools join (program_routes_test.go says what these are for).

import (
	"sync/atomic"
	"testing"

	"github.com/rfizzle/shhh/internal/ui/components"
)

// The session opens with a server starting: its TOOLS row says so with its
// seconds, turns up on the next paint after its connect ends with no line
// sent, and the line after that is where it joins — the transcript says so
// ahead of the message that starts the turn.
func TestProgram_AServerStartingTurnsUpAndJoinsAtTheNextLine(t *testing.T) {
	var answered, joined atomic.Bool
	since := clock()
	rows := func() []components.InspectorToolSource {
		if answered.Load() {
			return []components.InspectorToolSource{{Name: "docs", State: components.ToolSourceUp, Note: "1 tool"}}
		}
		return []components.InspectorToolSource{{Name: "docs", State: components.ToolSourceStarting, Since: since}}
	}
	m, _ := scriptedSession(
		programTurn{text: "the first answer"},
		programTurn{text: "the second answer"},
	)
	m.setToolDefinitions([]ToolTokens{{Name: "read_file"}})
	m.mcp = MCP{
		Has:     func(name string) bool { return joined.Load() && name == "docs__search" },
		Sources: rows(),
		Live:    rows,
		Join: func(system string) (MCPJoin, bool) {
			if !answered.Load() || joined.Swap(true) {
				return MCPJoin{}, false
			}
			return MCPJoin{
				Notes:       []string{"mcp: docs: up — 1 tool, from this turn"},
				Sources:     rows(),
				System:      system,
				ServerTools: []ToolTokens{{Name: "docs__search"}},
			}, true
		},
	}
	tm := runProgramAt(t, m, 130, 40)

	send(tm, "what is in the docs")
	waitForAll(t, tm, "the first answer", "▸ docs", "starting · ", "1 of 2 up")

	answered.Store(true)
	waitForAll(t, tm, "✓ docs", "2 of 2 up")
	waitForGone(t, tm, "starting · ")
	if joined.Load() {
		t.Fatal("the server joined without a line at a boundary")
	}

	send(tm, "and now")
	waitForAll(t, tm, "mcp: docs: up — 1 tool, from this turn", "the second answer")
	if !joined.Load() {
		t.Fatal("the line did not take the join")
	}
}
