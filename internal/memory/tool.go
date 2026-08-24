package memory

import (
	"encoding/json"
	"fmt"

	"github.com/rfizzle/shhh/internal/provider"
)

// RememberToolName is the model-facing memory-proposal tool. It is
// approval-gated in the chat UI: every call is presented to the user, who
// picks the scope and may amend the text — an agent can never persist a
// memory on its own, in any permission mode.
const RememberToolName = "remember"

// ToolDefinition is the remember tool the agent session registers when
// durable memory is available.
func ToolDefinition() provider.Tool {
	return provider.Tool{
		Name:        RememberToolName,
		Description: "Propose one short memory to keep across sessions: a user preference, a project convention, a correction the user made, or a lesson learned. The user reviews every proposal and chooses to save it (to this project or globally) or to decline; a declined proposal returns an error result — accept it and do not re-propose. Keep entries short, general, and durable; never session-specific facts, file contents, or secrets.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"text": {"type": "string", "description": "The memory itself: one or two short sentences, stated generally"},
				"kind": {"type": "string", "enum": ["preference", "convention", "correction", "lesson"], "description": "What kind of memory this is"},
				"scope": {"type": "string", "enum": ["project", "global"], "description": "Suggested scope: project (this workspace, the default) or global (every workspace)"}
			},
			"required": ["text", "kind"]
		}`),
	}
}

// Draft is a proposed memory parsed from a remember tool call, awaiting the
// user's decision.
type Draft struct {
	Text string
	Kind string
	// Scope is the suggested scope, "project" or "global"; the user chooses
	// the real one on the confirm prompt.
	Scope string
}

// ParseRemember validates a remember call's arguments into a Draft.
func ParseRemember(raw json.RawMessage) (Draft, error) {
	var args struct {
		Text  string `json:"text"`
		Kind  string `json:"kind"`
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return Draft{}, fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Text == "" {
		return Draft{}, fmt.Errorf("text is required")
	}
	if len(args.Text) > MaxTextLen {
		return Draft{}, fmt.Errorf("text is too long (%d chars, max %d) — memories are short, durable statements", len(args.Text), MaxTextLen)
	}
	if !ValidKind(args.Kind) {
		return Draft{}, fmt.Errorf("unknown kind %q (valid: preference, convention, correction, lesson)", args.Kind)
	}
	switch args.Scope {
	case "", "project", GlobalScope:
	default:
		return Draft{}, fmt.Errorf("unknown scope %q (valid: project, global)", args.Scope)
	}
	if args.Scope == "" {
		args.Scope = "project"
	}
	return Draft{Text: args.Text, Kind: args.Kind, Scope: args.Scope}, nil
}
