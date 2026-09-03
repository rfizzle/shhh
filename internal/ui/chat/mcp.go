package chat

// MCP servers in a session. The tools a connected server offers are on the
// executor chain like every other optional tool; what the chat model needs
// to know is which names are a server's, which of those the person marked
// read-only, what became of every server the session was told to reach, and
// what /mcp prints
// (docs/capabilities/mcp.md#a-call-is-a-command-unless-you-said-otherwise).

import "github.com/rfizzle/shhh/internal/ui/components"

// MCP wires the session's MCP servers into the chat TUI. The zero value
// means the session connected none.
type MCP struct {
	// Has reports whether a tool name belongs to a connected server.
	Has func(name string) bool
	// ReadOnly reports whether the tool's server was marked read-only by
	// the person, which is what lets its rows draw as reads.
	ReadOnly func(name string) bool
	// Manage backs the /mcp slash command.
	Manage func(args []string) string
	// Sources is one entry per server the session was told to reach, as the
	// connect left it, in the rail's own vocabulary — a second enum in
	// between would only be this one restated, and a mapping to get it wrong
	// in. It is a value rather than a call because nothing in a session
	// changes it: the servers are dialled once before the first turn, and
	// trusting one takes effect in the next session
	// (docs/capabilities/mcp.md#a-checkout-cannot-start-a-process).
	Sources []components.InspectorToolSource
}

// WithMCP enables /mcp and tells the transcript which rows are server
// calls.
func (m Model) WithMCP(servers MCP) Model {
	m.mcp = servers
	if m.mcp.ReadOnly == nil {
		m.mcp.ReadOnly = func(string) bool { return false }
	}
	return m
}

// toolRailRows is how many source rows the TOOLS block draws before it folds
// the rest into a count. Four, the same bound the backlog's block uses: the
// block answers "is what I configured up", and past four rows that is a
// listing, which is what /mcp is for.
const toolRailRows = 4

// inspectorTools is the TOOLS block: the built-in toolset first, then every
// server the session was told to reach, and what the recall budget left out
// of the prompt. It is present only when something could have gone missing —
// an external source, or a memory that did not fit — because a session with
// nothing but its own tools has no way to have lost any, and the block would
// be a row saying the obvious.
func (m Model) inspectorTools() *components.InspectorTools {
	if len(m.mcp.Sources) == 0 && m.memory.Omitted == 0 {
		return nil
	}
	t := &components.InspectorTools{MemoryOmitted: m.memory.Omitted}
	if n := m.builtinToolCount(); n > 0 {
		t.Up++
		t.Sources = append(t.Sources, components.InspectorToolSource{
			Name: "built-in", State: components.ToolSourceUp, Note: plural(n, "tool"),
		})
	}
	// The block exists so a source that did not answer leaves a trace, which
	// decides what the fold is allowed to take: a source that is up is the one
	// the reader can afford not to see, so the healthy rows go first and every
	// other kind keeps its row for as long as there is one.
	keep := make([]bool, len(m.mcp.Sources))
	room := max(toolRailRows-len(t.Sources), 0)
	for _, healthy := range []bool{false, true} {
		for i, s := range m.mcp.Sources {
			if room == 0 {
				break
			}
			if keep[i] || (s.State == components.ToolSourceUp) != healthy {
				continue
			}
			keep[i], room = true, room-1
		}
	}
	for i, s := range m.mcp.Sources {
		// The heading counts what answered over every source, so a server the
		// fold took still counts towards it.
		if s.State == components.ToolSourceUp {
			t.Up++
		}
		if !keep[i] {
			t.More++
			continue
		}
		t.Sources = append(t.Sources, s)
	}
	return t
}

// builtinToolCount is every registered tool that did not come from a server.
// The session already knows both halves — the definitions it was built with
// and which names a server owns — so the count is a walk rather than a number
// anything has to keep. Without the half that says which names are a server's
// it is zero rather than a total that silently counts every server's tools as
// shhh's own: a count nobody can vouch for is worse than no row.
func (m Model) builtinToolCount() int {
	if m.mcp.Has == nil {
		return 0
	}
	n := 0
	for _, d := range m.toolDefs {
		if m.mcp.Has(d.Name) {
			continue
		}
		n++
	}
	return n
}
