// Package agent owns the front-end-agnostic agentic loop state: the
// conversation message list, stream requests, tool dispatch, the queue of
// approval-gated tool calls, and the per-turn iteration guard. Front-ends
// (the chat TUI today, a headless runner later) drive an Agent step by step
// and surface approvals and progress to the user themselves.
package agent

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/rfizzle/shhh/internal/attachment"
	"github.com/rfizzle/shhh/internal/provider"
)

// StreamFunc opens a completion stream over a message list. The choice is
// what the request says about calling a tool (provider.ToolChoiceAuto or
// provider.ToolChoiceNone); a front-end's closure puts it on the request
// rather than deciding for itself, because only the caller knows whether it
// is asking for work or for prose.
type StreamFunc func(msgs []provider.Message, choice string) (<-chan provider.StreamEvent, context.CancelFunc, error)

// ToolExecutor runs one tool call and returns its result text.
type ToolExecutor func(name string, args json.RawMessage) (string, error)

// ApprovalGate reports whether a tool call must be approved by the user
// before it may run. The front-end implements it (the chat TUI gates
// execute_command, mutating tools, and registered gated tools). The gate sees
// the whole call, so a tool can gate on its arguments (the process tool gates
// only its start action).
type ApprovalGate func(tc provider.ToolCall) bool

// ToolResult pairs an executed tool call with its result text and how long
// the call took (zero when the call never ran, e.g. a cancellation).
type ToolResult struct {
	Call   provider.ToolCall
	Result string
	// Attachments are the parts of the result that are not text — the image
	// a reader found where it was asked for a file. They ride on the result
	// message, so the providers that can show the model a picture do, and the
	// ones that cannot are left with the notice the tool wrote.
	Attachments []provider.Attachment
	Duration    time.Duration
}

// DefaultMaxToolRounds bounds how many consecutive tool-call rounds one user
// turn may trigger before the loop pauses for fresh input.
//
// The number is a checkpoint interval, not a safety limit: the interactive
// pause it drives is a place to look at what the turn has done, and a
// checkpoint that fires on ordinary work is noise rather than signal. It was
// 25, then 75, and both were spent by an everyday "find this across the repo
// and fix it" turn without anything going wrong. 150 is the first number that
// leaves the ordinary turn alone, which is the only way the pause means
// something when it does arrive.
const DefaultMaxToolRounds = 150

// UnlimitedToolRounds passed to SetMaxRounds removes the per-turn cap: the
// loop runs until the model stops asking for tools. It is for headless runs a
// human is watching in the foreground (`shhh code --print --max-rounds 0`),
// where the cap has nobody to hand control back to and interrupting is the
// way out. The chat TUI never uses it — there the cap is the checkpoint.
const UnlimitedToolRounds = -1

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

	// lastIntervention is the round something last asked the turn to take
	// stock — a check-in or a steer. The check-in interval is measured from
	// it (checkin.go), and intervene is the policy that decides which of the
	// two a round boundary owes (intervene.go).
	lastIntervention int
	intervene        interveneState
	// steering is this surface's interruption tuning — the interval between
	// check-ins, how far it widens, and the wordings (steering.go) — and
	// checkIns how many the turn has had.
	steering Steering
	checkIns int

	// executing is true while auto-run tool calls run in the background;
	// pending holds every call of the current round still owed a result, and
	// queue the subset awaiting user approval.
	executing bool
	pending   []provider.ToolCall
	queue     []provider.ToolCall

	// reasoning is the thinking the response now being folded into the
	// conversation produced. It is latched from the terminal stream
	// event and consumed by the assistant message that round records,
	// because that is the message the next request has to carry it in.
	reasoning []provider.ReasoningBlock

	// keep, when set, marks tool results that context trimming must leave
	// alone. Nil keeps nothing.
	keep func(content string) bool

	// archive, when set, is where a tool result about to be trimmed is put
	// so the model can ask for it back. Nil makes every trim permanent.
	archive func(tool, content string) (id string, ok bool)

	// scrub, when set, rewrites every text that joins the conversation or
	// leaves it for a provider. Nil leaves text alone.
	scrub func(msg provider.Message) provider.Message

	// tree, when set, is the reading that tells a turn the working tree
	// moved under it (tree.go). Nil reads nothing.
	tree *treeState
}

func New(initial []provider.Message, stream StreamFunc) *Agent {
	return &Agent{messages: initial, stream: stream}
}

// SetExecutor sets the executor used for auto-run (non-gated) tool calls.
func (a *Agent) SetExecutor(executor ToolExecutor) { a.executor = executor }

// KeepResults exempts tool results matching keep from context trimming. A
// result is elided on the assumption that it was consumed when it arrived;
// some results are instructions for the rest of the session and are wrong
// to drop — and dropping one fails silently, since the model just carries
// on without them. The caller says which those are.
func (a *Agent) KeepResults(keep func(content string) bool) { a.keep = keep }

// StoreElided installs the archive a tool result is put into just before a
// trim replaces it, returning the id the placeholder then names. Answering
// false is a result that could not be kept, and the trim elides it with the
// bare placeholder rather than stopping: the request that provoked the trim
// still has to fit, and a session whose store is full is exactly the session
// that most needs the window back.
//
// It takes a function rather than a store so this package still knows nothing
// about what evidence is, the way the scrub and the keep predicate do.
// See docs/capabilities/evidence.md#a-trim-makes-the-same-promise.
func (a *Agent) StoreElided(archive func(tool, content string) (string, bool)) {
	a.archive = archive
}

// SetScrub installs the rewrite every message goes through on the way in
// (Append, SetMessages) and on the way out (Stream). It is where the
// session's secrets are removed: the conversation is what gets saved,
// shown and replayed, so a value that reaches it has already leaked, and
// the request is the last door before the provider. Both are scrubbed
// because either alone has a path around it — a resumed session, a stream
// a front-end opens itself.
// See docs/capabilities/secrets.md#the-value-is-scrubbed-at-every-door.
func (a *Agent) SetScrub(scrub func(provider.Message) provider.Message) { a.scrub = scrub }

func (a *Agent) scrubbed(msg provider.Message) provider.Message {
	if a.scrub == nil {
		return msg
	}
	return a.scrub(msg)
}

// SetMaxRounds overrides the per-turn tool-round cap. Zero means "unset" and
// keeps DefaultMaxToolRounds, so a config or flag nobody filled in still gets
// the default; any negative n means UnlimitedToolRounds and removes the cap.
// The two cases were one before, which left no way to ask for no cap at all.
func (a *Agent) SetMaxRounds(n int) {
	if n < 0 {
		n = UnlimitedToolRounds
	}
	a.maxRounds = n
}

// Uncapped reports whether the per-turn cap has been removed. Callers that
// render a ceiling must ask this first: MaxRounds still answers with the
// default, because there is no number that honestly means "no bound".
func (a *Agent) Uncapped() bool { return a.maxRounds < 0 }

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
func (a *Agent) SetMessages(msgs []provider.Message) {
	if a.scrub != nil {
		msgs = append([]provider.Message(nil), msgs...)
		for i := range msgs {
			msgs[i] = a.scrub(msgs[i])
		}
	}
	a.messages = msgs
}

// Append adds one message to the conversation.
func (a *Agent) Append(msg provider.Message) { a.messages = append(a.messages, a.scrubbed(msg)) }

// StartTurn begins a fresh user turn: the text joins the conversation and
// the round counter resets.
func (a *Agent) StartTurn(text string) { a.StartTurnWith(text, nil) }

// StartTurnWith is StartTurn carrying the attachments staged for this turn
// . They ride on the user message itself, so every later snapshot,
// save and resume keeps them beside the sentence that was asked about them.
func (a *Agent) StartTurnWith(text string, atts []provider.Attachment) {
	a.rounds = 0
	a.lastIntervention = 0
	a.checkIns = 0
	a.StartInterveneTurn()
	a.Append(provider.Message{Role: provider.RoleUser, Content: text, Attachments: atts})
}

// RequestMessages snapshots the conversation for a stream request, so
// in-flight requests are immune to later mutation.
func (a *Agent) RequestMessages() []provider.Message {
	msgs := make([]provider.Message, len(a.messages))
	copy(msgs, a.messages)
	return msgs
}

// Stream opens a completion stream over msgs with the tools open to the
// model, which is what a turn wants.
func (a *Agent) Stream(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
	return a.StreamWithChoice(msgs, provider.ToolChoiceAuto)
}

// StreamWithChoice is Stream under an explicit tool choice, for a request
// that wants prose out of a session whose tools are still on the wire.
func (a *Agent) StreamWithChoice(msgs []provider.Message, choice string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
	if a.scrub != nil {
		msgs = append([]provider.Message(nil), msgs...)
		for i := range msgs {
			msgs[i] = a.scrub(msgs[i])
		}
	}
	return a.stream(msgs, choice)
}

// RunID identifies the current turn's asynchronous work; results stamped
// with an older ID are stale and must be dropped by the front-end.
func (a *Agent) RunID() int { return a.runID }

// Rounds is how many tool-call rounds the current user turn has used.
func (a *Agent) Rounds() int { return a.rounds }

// ResetRounds clears the round counter (a fresh user input continues a
// capped turn). The intervention mark goes with it: the user's own message is
// the most direct form of taking stock there is, and the counter it is
// measured against has just gone back to zero.
func (a *Agent) ResetRounds() {
	a.rounds = 0
	a.lastIntervention = 0
	a.checkIns = 0
}

// CapReached reports whether this turn has used up its tool rounds. An
// uncapped agent never reaches it.
func (a *Agent) CapReached() bool { return !a.Uncapped() && a.rounds >= a.MaxRounds() }

// Executing reports whether auto-run tool calls are executing in the
// background.
func (a *Agent) Executing() bool { return a.executing }

// CarryReasoning latches the reasoning blocks a response produced, for the
// assistant message about to record it. Every terminal stream event calls it,
// including the ones with nothing to carry: a stale latch would attach one
// response's thinking to another's tool calls, which is worse than none.
func (a *Agent) CarryReasoning(blocks []provider.ReasoningBlock) { a.reasoning = blocks }

// BeginToolRound records the assistant message that requested calls and
// splits them into auto-run and approval-gated sets using gate; gated calls
// wait in the approval queue until the front-end resolves them.
func (a *Agent) BeginToolRound(text string, calls []provider.ToolCall, gate ApprovalGate) (auto, gated []provider.ToolCall) {
	a.rounds++
	a.Append(provider.Message{
		Role:      provider.RoleAssistant,
		Content:   text,
		ToolCalls: calls,
		Reasoning: a.reasoning,
	})
	a.reasoning = nil
	a.noteTreeCalls(calls)
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

// MaxParallelToolCalls bounds how many of a round's calls run at once.
//
// A batch is worth something only if it is one wait. The prompt tells the
// model to ask for independent reads and searches together — that is the
// advice that stops a session spending a round per question — and running
// what it asks for one at a time makes the batch a queue, so five searches
// over a large tree cost five searches' wall clock. Every call that reaches
// here is auto-run, which is to say read-only: BeginToolRound has already
// taken the mutating and approval-gated ones out.
//
// The bound exists because the calls are not free of the machine. Eight
// concurrent ripgreps or language-server queries saturate an ordinary laptop;
// past that the batch stops getting faster and starts making everything else
// on the machine slower.
const MaxParallelToolCalls = 8

// ExecuteCalls dispatches calls through the executor, concurrently and
// bounded by MaxParallelToolCalls. Results come back in call order, which is
// the order the conversation has to record them in. Safe to run off the UI
// goroutine: it touches no Agent state besides the executor.
//
// A result's non-text parts are collected here, in the goroutine that
// produced it, because this is the only point where the tool that wrote them
// and the result they belong to are both in hand: an executor returns a
// string, and every wrapper on the chain in between passes one.
func (a *Agent) ExecuteCalls(calls []provider.ToolCall) []ToolResult {
	results := make([]ToolResult, len(calls))
	sem := make(chan struct{}, MaxParallelToolCalls)
	var wg sync.WaitGroup
	for i, tc := range calls {
		wg.Add(1)
		go func(i int, tc provider.ToolCall) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			start := time.Now()
			result := a.ExecuteCall(tc)
			results[i] = ToolResult{
				Call:        tc,
				Result:      result,
				Attachments: attachment.TakeResult(result),
				Duration:    time.Since(start),
			}
		}(i, tc)
	}
	wg.Wait()
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
			Role:        provider.RoleTool,
			Content:     r.Result,
			ToolCallID:  r.Call.ID,
			Attachments: r.Attachments,
		})
	}
	a.executing = false
	a.pending = a.queue
}

// QueuedApprovals is how many tool calls are waiting for approval.
func (a *Agent) QueuedApprovals() int { return len(a.queue) }

// PendingApprovals is the approval queue in the order it will be asked,
// head first. It is what the queue strip and batch approval read;
// the slice is a copy, so reading it can never reorder what the agent pops.
func (a *Agent) PendingApprovals() []provider.ToolCall {
	return append([]provider.ToolCall(nil), a.queue...)
}

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
