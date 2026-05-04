package provider

import "context"

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type Message struct {
	Role    Role
	Content string
}

type StreamEvent struct {
	Token string
	Done  bool
	Err   error
}

type CompletionOpts struct {
	Model       string
	Temperature *float64
	MaxTokens   int
}

type Provider interface {
	StreamCompletion(ctx context.Context, messages []Message, opts CompletionOpts) (<-chan StreamEvent, error)
	Name() string
}
