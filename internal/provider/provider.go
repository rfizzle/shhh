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
