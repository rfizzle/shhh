package mcp

import (
	"fmt"
	"strings"
)

// PromptBlock is the section of the system prompt that says which servers
// the session connected and what each one is for. It names only servers
// that answered: a prompt that describes a server the session does not have
// promises tools the model will try to call
// (docs/capabilities/coding-agent.md#the-agent-knows-what-this-machine-has).
// Every tool's own description already reaches the model through its
// schema, so the block is one line per server plus whatever the server
// asked to have said — its instructions are the one thing a schema cannot
// carry.
func PromptBlock(ts *Toolset) string {
	return promptBlock(ts.Servers())
}

// ReadOnlyPromptBlock is the block over the read-only servers alone — what
// a child agent, which was handed only those, is told.
func ReadOnlyPromptBlock(ts *Toolset) string {
	var servers []*Server
	for _, s := range ts.Servers() {
		if s.Definition.ReadOnly {
			servers = append(servers, s)
		}
	}
	return promptBlock(servers)
}

func promptBlock(servers []*Server) string {
	if len(servers) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# MCP servers\n")
	b.WriteString("Tools named `<server>" + Separator + "<tool>` come from MCP servers the user connected. ")
	b.WriteString("A server marked read-only runs without asking; every other server's tools need the user's answer before they run, like a command.\n")
	for _, s := range servers {
		def := s.Definition
		fmt.Fprintf(&b, "- %s — %s", def.Name, countTools(len(s.Tools)))
		if def.ReadOnly {
			b.WriteString(", read-only")
		}
		if title := serverTitle(s); title != "" {
			b.WriteString(" (" + title + ")")
		}
		b.WriteString("\n")
		if s.Instructions != "" {
			b.WriteString("  The server says:\n")
			for _, line := range strings.Split(s.Instructions, "\n") {
				b.WriteString("  > " + line + "\n")
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// serverTitle is what the server calls itself when that adds something
// beyond the name the user gave it.
func serverTitle(s *Server) string {
	title := s.Info.Title
	if title == "" {
		title = s.Info.Name
	}
	if title == "" || strings.EqualFold(title, s.Definition.Name) {
		return ""
	}
	if s.Info.Version != "" {
		title += " " + s.Info.Version
	}
	return title
}

func countTools(n int) string {
	if n == 1 {
		return "1 tool"
	}
	return fmt.Sprintf("%d tools", n)
}
