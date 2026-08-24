// Package agent owns the front-end-agnostic agentic loop state: the
// conversation message list, stream requests, tool dispatch, the queue of
// approval-gated tool calls, and the per-turn iteration guard. Front-ends
// (the chat TUI today, a headless runner later) drive an Agent step by step
// and surface approvals and progress to the user themselves.
package agent

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

// StreamFunc opens a completion stream over a message list.
type StreamFunc func([]provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error)

// ToolExecutor runs one tool call and returns its result text.
type ToolExecutor func(name string, args json.RawMessage) (string, error)

// ApprovalGate reports whether a tool call must be approved by the user
// before it may run. The front-end implements it (the chat TUI gates
// execute_command, mutating tools, and registered gated tools). The gate sees
// the whole call, so a tool can gate on its arguments (the process tool gates
// only its start action, S-073).
type ApprovalGate func(tc provider.ToolCall) bool

// ToolResult pairs an executed tool call with its result text and how long
// the call took (zero when the call never ran, e.g. a cancellation).
type ToolResult struct {
	Call     provider.ToolCall
	Result   string
	Duration time.Duration
}

// DefaultMaxToolRounds bounds how many consecutive tool-call rounds one user
// turn may trigger before the loop pauses for fresh input.
const DefaultMaxToolRounds = 25

// CancelledResult is the synthetic tool result recorded for calls still
// outstanding when the user cancels a turn, keeping the conversation
// well-formed for the next request.
const CancelledResult = "error: cancelled by user"

// Agent is the loop state for one conversation. It is a passive state
// machine: the front-end feeds it stream events and approval decisions and
// asks it what to do next, so the same Agent can back an interactive TUI or
// a headless run.
type Agent struct {
	stream   StreamFunc
	executor ToolExecutor

	messages  []provider.Message
	runID     int
	rounds    int
	maxRounds int

	// executing is true while auto-run tool calls run in the background;
	// pending holds every call of the current round still owed a result, and
	// queue the subset awaiting user approval.
	executing bool
	pending   []provider.ToolCall
	queue     []provider.ToolCall
}

func New(initial []provider.Message, stream StreamFunc) *Agent {
	return &Agent{messages: initial, stream: stream}
}

// SetExecutor sets the executor used for auto-run (non-gated) tool calls.
func (a *Agent) SetExecutor(executor ToolExecutor) { a.executor = executor }

// SetMaxRounds overrides the per-turn tool-round cap; n <= 0 keeps
// DefaultMaxToolRounds.
func (a *Agent) SetMaxRounds(n int) { a.maxRounds = n }

// MaxRounds is the effective per-turn tool-round cap.
func (a *Agent) MaxRounds() int {
	if a.maxRounds > 0 {
		return a.maxRounds
	}
	return DefaultMaxToolRounds
}

// Messages is the conversation as it stands. The returned slice is the
// Agent's own; callers must not mutate it.
func (a *Agent) Messages() []provider.Message { return a.messages }

// SetMessages replaces the conversation wholesale (resume, /clear,
// compaction).
func (a *Agent) SetMessages(msgs []provider.Message) { a.messages = msgs }

// Append adds one message to the conversation.
func (a *Agent) Append(msg provider.Message) { a.messages = append(a.messages, msg) }

// StartTurn begins a fresh user turn: the text joins the conversation and
// the round counter resets.
func (a *Agent) StartTurn(text string) {
	a.rounds = 0
	a.Append(provider.Message{Role: provider.RoleUser, Content: text})
}

// RequestMessages snapshots the conversation for a stream request, so
// in-flight requests are immune to later mutation.
func (a *Agent) RequestMessages() []provider.Message {
	msgs := make([]provider.Message, len(a.messages))
	copy(msgs, a.messages)
	return msgs
}

// Stream opens a completion stream over msgs.
func (a *Agent) Stream(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
	return a.stream(msgs)
}

// RunID identifies the current turn's asynchronous work; results stamped
// with an older ID are stale and must be dropped by the front-end.
func (a *Agent) RunID() int { return a.runID }

// Rounds is how many tool-call rounds the current user turn has used.
func (a *Agent) Rounds() int { return a.rounds }

// ResetRounds clears the round counter (a fresh user input continues a
// capped turn).
func (a *Agent) ResetRounds() { a.rounds = 0 }

// CapReached reports whether this turn has used up its tool rounds.
func (a *Agent) CapReached() bool { return a.rounds >= a.MaxRounds() }

// Executing reports whether auto-run tool calls are executing in the
// background.
func (a *Agent) Executing() bool { return a.executing }

// BeginToolRound records the assistant message that requested calls and
// splits them into auto-run and approval-gated sets using gate; gated calls
// wait in the approval queue until the front-end resolves them.
func (a *Agent) BeginToolRound(text string, calls []provider.ToolCall, gate ApprovalGate) (auto, gated []provider.ToolCall) {
	a.rounds++
	a.Append(provider.Message{
		Role:      provider.RoleAssistant,
		Content:   text,
		ToolCalls: calls,
	})
	for _, tc := range calls {
		if gate != nil && gate(tc) {
			gated = append(gated, tc)
		} else {
			auto = append(auto, tc)
		}
	}
	a.queue = gated
	if len(auto) > 0 {
		a.executing = true
		a.pending = calls
	} else {
		a.pending = gated
	}
	return auto, gated
}

// ExecuteCalls dispatches calls through the executor. Safe to run off the
// UI goroutine: it touches no Agent state besides the executor.
func (a *Agent) ExecuteCalls(calls []provider.ToolCall) []ToolResult {
	results := make([]ToolResult, 0, len(calls))
	for _, tc := range calls {
		start := time.Now()
		result := a.ExecuteCall(tc)
		results = append(results, ToolResult{Call: tc, Result: result, Duration: time.Since(start)})
	}
	return results
}

// ExecuteCall runs one tool call through the Agent's executor.
func (a *Agent) ExecuteCall(tc provider.ToolCall) string {
	return ExecuteWith(a.executor, tc)
}

// ExecuteWith runs one tool call through an explicit executor, formatting
// failures as error tool results.
func ExecuteWith(executor ToolExecutor, tc provider.ToolCall) string {
	if executor == nil {
		return "error: no tool executor configured"
	}
	out, err := executor(tc.Name, json.RawMessage(tc.Arguments))
	if err != nil {
		return "error: " + err.Error()
	}
	return out
}

// RecordAutoResults appends the auto-run results to the conversation; what
// remains owed for this round is the approval queue.
func (a *Agent) RecordAutoResults(results []ToolResult) {
	for _, r := range results {
		a.Append(provider.Message{
			Role:       provider.RoleTool,
			Content:    r.Result,
			ToolCallID: r.Call.ID,
		})
	}
	a.executing = false
	a.pending = a.queue
}

// QueuedApprovals is how many tool calls are waiting for approval.
func (a *Agent) QueuedApprovals() int { return len(a.queue) }

// NextApproval is the head of the approval queue, if any.
func (a *Agent) NextApproval() (provider.ToolCall, bool) {
	if len(a.queue) == 0 {
		return provider.ToolCall{}, false
	}
	return a.queue[0], true
}

// ResolveApproval records content as the head approval's tool result — its
// output, a decline, or an argument error — and pops it from the queue.
func (a *Agent) ResolveApproval(content string) {
	if len(a.queue) == 0 {
		return
	}
	a.Append(provider.Message{
		Role:       provider.RoleTool,
		Content:    content,
		ToolCallID: a.queue[0].ID,
	})
	a.queue = a.queue[1:]
	a.pending = a.queue
}

// CancelTurn aborts the current round: outstanding calls get synthetic error
// results so the conversation stays well-formed, the approval queue is
// dropped, and the run ID advances so stale asynchronous results are fenced
// off. Returns the calls that received synthetic results.
func (a *Agent) CancelTurn() []provider.ToolCall {
	a.runID++
	var cancelled []provider.ToolCall
	if a.executing {
		for _, tc := range a.pending {
			a.Append(provider.Message{
				Role:       provider.RoleTool,
				Content:    CancelledResult,
				ToolCallID: tc.ID,
			})
		}
		cancelled = a.pending
		a.executing = false
	}
	a.pending = nil
	a.queue = nil
	return cancelled
}
