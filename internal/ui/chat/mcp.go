package chat

// MCP servers in a session. The tools a connected server offers are on the
// executor chain like every other optional tool; what the chat model needs
// to know is which names are a server's, which of those the person marked
// read-only, and what /mcp prints
// (docs/capabilities/mcp.md#a-call-is-a-command-unless-you-said-otherwise).

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
