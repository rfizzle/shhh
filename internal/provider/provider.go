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
	// pasted images, files off the clipboard. Each provider's
	// converter decides how to put them on the wire; see attachment.go.
	Attachments []Attachment
	// Reasoning is the thinking the model did before this message, kept in
	// the provider's own form (reasoning.go). Only the assistant turn
	// that requested tools needs it, and only the providers that require it
	// back put it on the wire.
	Reasoning []ReasoningBlock
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
	// Signature is the opaque per-part reasoning token some providers attach
	// to the tool call itself and require back on the next request.
	// Gemini 3 is the one that does: the thought signature rides the
	// functionCall part, and a history that drops it hands the model a plan
	// it cannot recognise as its own. It is base64 where the provider's is
	// binary, so it survives the JSON a resumed session is stored as.
	Signature string
}

type Usage struct {
	// PromptTokens is every input token the request is billed for, cached
	// ones included. The dialects do not agree on that: most report a prompt
	// count that already contains what they served from cache, while the
	// Messages API reports the three parts side by side and leaves the sum to
	// the reader. The converter is where they are made to agree, because a
	// figure whose meaning depends on which provider answered cannot be
	// added up, and the session ledger adds it up across all of them.
	PromptTokens     int
	CompletionTokens int
	// CachedTokens is the part of PromptTokens the provider served from its
	// prompt cache, and CacheCreationTokens the part it wrote into the cache
	// for the next request to read. Both are subsets of PromptTokens, they
	// never overlap, and zero means "not reported", not "nothing cached".
	//
	// They are separate because they are priced apart: a read is a fraction
	// of the input rate and a write is a premium over it.
	// See docs/capabilities/providers.md#the-prompt-prefix-is-paid-for-once.
	CachedTokens        int
	CacheCreationTokens int
}

type StreamEvent struct {
	Token     string
	ToolCalls []ToolCall
	Usage     *Usage
	Done      bool
	Err       error
	// Reasoning is the thinking blocks this response produced. It
	// rides the terminal event beside ToolCalls, and for the same reason:
	// what the model finished has to survive into the next request.
	Reasoning []ReasoningBlock
	// Thinking is reasoning text as it arrives, the way Token is answer text
	// as it arrives. It is a second channel rather than more Token because
	// the two are different acts and the transcript draws them as different
	// things — thinking is a row of its own, and a provider that streamed it
	// as a token would print the model's private murmur as its reply.
	//
	// Reasoning above is still what travels back on the next request: the
	// blocks are the provider's own signed form, and this text is only what
	// the screen can show of them. A provider that has one and not the other
	// is normal in both directions.
	Thinking string
}

type CompletionOpts struct {
	Model       string
	Temperature *float64
	MaxTokens   int
	Tools       []Tool
	// ToolChoice is what the request says about calling a tool:
	// ToolChoiceAuto leaves it to the model, ToolChoiceNone describes the
	// tools and forbids calling one. The empty string sends no field, which
	// every dialect reads as auto.
	//
	// It is a bare string because each dialect spells it differently and the
	// converter is where they are made to agree. Those two are the whole set
	// a caller may send: every provider honours both, and a value outside
	// them is one some dialects forward and others quietly drop.
	// See docs/capabilities/providers.md#a-request-says-whether-a-tool-may-be-called.
	ToolChoice string
	// Effort is the reasoning level asked of the model (
	// reasoning.go). EffortOff — the zero value — sends nothing.
	Effort Effort
}

// The two values ToolChoice may carry. Naming a specific tool is
// deliberately not among them: the newest models refuse a forced choice
// outright, so a caller built on one is built on something being withdrawn.
// A caller that wants a particular tool asks for it in the prompt.
// See docs/capabilities/providers.md#a-request-says-whether-a-tool-may-be-called.
const (
	// ToolChoiceAuto lets the model call a tool or answer in text.
	ToolChoiceAuto = "auto"
	// ToolChoiceNone leaves the tools on the request and forbids calling
	// one, so the model answers in text without the tool schemas moving.
	ToolChoiceNone = "none"
)

type Provider interface {
	StreamCompletion(ctx context.Context, messages []Message, opts CompletionOpts) (<-chan StreamEvent, error)
	Name() string
}
