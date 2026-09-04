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
// carry — and, under it, the resources the server publishes, which is the
// one part of the catalog no tool schema can hold, since a uri is data and a
// schema is a shape.
//
// A server's prompts are deliberately absent. A prompt is a command the
// person types, and telling the model about one would offer it something
// it has no way to invoke (docs/capabilities/mcp.md#a-prompt-is-a-command).
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
	resources := false
	for _, s := range servers {
		if len(s.Resources) > 0 {
			resources = true
		}
	}
	if resources {
		// The catalog of URIs is here rather than in the tool's schema
		// because the schema describes the call's shape and the uris are
		// the data it is made with; the block is where the model is told
		// facts about this session.
		b.WriteString("`" + ResourceToolName + "` reads any resource listed below, by uri. Reading one changes nothing and never asks.\n")
	}
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
		for _, r := range s.Resources {
			b.WriteString("  resource " + r.URI)
			if detail := resourceDetail(r); detail != "" {
				b.WriteString(" — " + detail)
			}
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// resourceDetail is the one line a resource earns beside its uri: what it
// is, in the server's words, or failing that what it is called.
func resourceDetail(r Resource) string {
	if r.Description != "" {
		return strings.ReplaceAll(r.Description, "\n", " ")
	}
	if r.Title != "" {
		return r.Title
	}
	return ""
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
