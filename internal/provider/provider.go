package provider

import (
	"context"
	"encoding/json"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
	// Attachments are the non-conversational parts the message carries —
	// pasted images, files off the clipboard (S-134). Each provider's
	// converter decides how to put them on the wire; see attachment.go.
	Attachments []Attachment
}

type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type Usage struct {
	PromptTokens     int
	CompletionTokens int
	// CachedTokens is the part of PromptTokens the provider served from its
	// prompt cache, when it reports one; zero means "not reported", not
	// "nothing cached" (S-093).
	CachedTokens int
}

type StreamEvent struct {
	Token     string
	ToolCalls []ToolCall
	Usage     *Usage
	Done      bool
	Err       error
}

type CompletionOpts struct {
	Model       string
	Temperature *float64
	MaxTokens   int
	Tools       []Tool
	ToolChoice  string
}

type Provider interface {
	StreamCompletion(ctx context.Context, messages []Message, opts CompletionOpts) (<-chan StreamEvent, error)
	Name() string
}
