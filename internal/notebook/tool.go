package notebook

import (
	"encoding/json"
	"fmt"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
)

// The notebook's two tools. Both auto-run in every mode: a note is text in
// a store the user can list and clear, and a read is a read. Writing needs
// no confirmation the way a memory does because a note claims nothing
// beyond this conversation.
// See docs/capabilities/chat.md#what-they-share.
const (
	WriteToolName = "write_note"
	ReadToolName  = "read_note"
)

// Definitions are the tool definitions every agent in a chat session gets.
func Definitions() []provider.Tool {
	return []provider.Tool{
		{
			Name:        WriteToolName,
			Description: "Write a note to the session's shared notebook, which every agent in this session (the orchestrator and all delegates) can read. Use it for what the rest of the session will need: a fact established, a source and what it said, a decision the user made. One paragraph, titled. Notes persist with the conversation and are not the user's durable memory.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"title": {"type": "string", "description": "A short title another agent could pick the note out by"},
					"body": {"type": "string", "description": "The note: one paragraph, with sources (paths, URLs) where it has them"}
				},
				"required": ["title", "body"]
			}`),
		},
		{
			Name:        ReadToolName,
			Description: "Read the session's shared notebook. With no arguments returns every note; with query returns the notes containing every word of it; with id returns that one note in full.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Words to match against titles and bodies"},
					"id": {"type": "integer", "description": "A note's number, as shown in [nN]"}
				}
			}`),
		},
	}
}

// WrapExecutor routes the notebook tools to the store, signed by author,
// and passes everything else on. Each agent wraps with its own name, so a
// delegate's notes carry the delegate's name.
func (s *Store) WrapExecutor(author string, next agent.ToolExecutor) agent.ToolExecutor {
	return func(name string, args json.RawMessage) (string, error) {
		switch name {
		case WriteToolName:
			var a struct {
				Title string `json:"title"`
				Body  string `json:"body"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			n, dropped, err := s.Write(author, a.Title, a.Body)
			if err != nil {
				return "", err
			}
			out := fmt.Sprintf("Wrote note [n%d] %q.", n.ID, n.Title)
			if dropped != "" {
				out += fmt.Sprintf(" The notebook is full, so the oldest note (%q) was dropped.", dropped)
			}
			return out, nil
		case ReadToolName:
			var a struct {
				Query string `json:"query"`
				ID    int64  `json:"id"`
			}
			if len(args) > 0 {
				if err := json.Unmarshal(args, &a); err != nil {
					return "", fmt.Errorf("invalid arguments: %w", err)
				}
			}
			if a.ID > 0 {
				for _, n := range s.List() {
					if n.ID == a.ID {
						return Format([]Note{n}), nil
					}
				}
				return "", fmt.Errorf("no note %d", a.ID)
			}
			notes := s.Find(a.Query)
			if len(notes) == 0 && a.Query != "" {
				return fmt.Sprintf("No note matches %q; the notebook holds %d.", a.Query, s.Len()), nil
			}
			return Format(notes), nil
		}
		return next(name, args)
	}
}
