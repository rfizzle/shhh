package agent

// Headless drives an Agent's loop synchronously to completion (S-057): one
// user turn, stream events consumed inline, tool rounds dispatched until the
// model produces a final message or the per-turn round cap is hit. The chat
// TUI drives the same Agent asynchronously through Bubble Tea messages; this
// runner is the scriptable front-end behind `shhh code -p` and each sub-agent
// (S-068). Steering and interruption (S-077) let a supervisor redirect or
// cancel a running turn the way the TUI's S-058 mechanics do.

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/rfizzle/shhh/internal/provider"
)

// ErrRoundCap is returned by Headless.Run when a turn uses up its tool
// rounds without reaching a final assistant message.
var ErrRoundCap = errors.New("tool round cap reached")

// ErrInterrupted is returned by Headless.Run when Interrupt cancels the turn.
// The conversation is left well-formed: outstanding tool calls received
// synthetic results via Agent.CancelTurn, mirroring the TUI's cancelStreaming
// semantics.
var ErrInterrupted = errors.New("turn interrupted")

// Headless runs an Agent to completion without a UI. Gate decides which tool
// calls need approval; Resolve decides and (if approved) executes each gated
// call, returning the tool-result content to record — in a non-interactive
// run "approval" is policy, not a prompt. The On* hooks surface activity to
// the front-end and may be nil.
type Headless struct {
	Agent   *Agent
	Gate    ApprovalGate
	Resolve func(tc provider.ToolCall) string

	OnText       func(text string)                         // streamed assistant tokens
	OnToolCall   func(tc provider.ToolCall)                // before a call runs or is resolved
	OnToolResult func(tc provider.ToolCall, result string) // after its result is recorded
	OnUsage      func(u *provider.Usage)                   // per-request usage, when reported

	// Steer, when set, is drained after each tool round: returned messages
	// join the conversation as user messages before the next stream request
	// and reset the round counter (S-058 semantics for headless runs).
	Steer func() []string

	mu           sync.Mutex
	streamCancel func()
	interrupted  bool
}

// Interrupt cancels the current turn from another goroutine: the in-flight
// stream is aborted and Run returns ErrInterrupted at the next checkpoint,
// after Agent.CancelTurn has kept the conversation well-formed. The Headless
// is reusable afterwards — a later Run starts a fresh turn.
func (h *Headless) Interrupt() {
	h.mu.Lock()
	h.interrupted = true
	if h.streamCancel != nil {
		h.streamCancel()
	}
	h.mu.Unlock()
}

func (h *Headless) wasInterrupted() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.interrupted
}

// Run executes one user turn to completion and returns the final assistant
// text. The conversation (including tool results) accumulates on the Agent,
// so callers can inspect Messages() afterwards or run another turn.
func (h *Headless) Run(prompt string) (string, error) {
	h.mu.Lock()
	h.interrupted = false
	h.mu.Unlock()

	h.Agent.StartTurn(prompt)
	for {
		text, calls, err := h.streamOnce()
		if err != nil {
			return "", err
		}
		if h.wasInterrupted() {
			// A partial response without tool calls is safe to keep; tool calls
			// from an aborted stream are dropped whole so no assistant message
			// is ever owed results. CancelTurn fences off the run either way.
			if len(calls) == 0 && text != "" {
				h.Agent.Append(provider.Message{Role: provider.RoleAssistant, Content: text})
			}
			h.Agent.CancelTurn()
			return "", ErrInterrupted
		}
		if len(calls) == 0 {
			if text != "" {
				h.Agent.Append(provider.Message{Role: provider.RoleAssistant, Content: text})
			}
			return text, nil
		}

		auto, _ := h.Agent.BeginToolRound(text, calls, h.Gate)
		results := make([]ToolResult, 0, len(auto))
		for _, tc := range auto {
			h.notifyCall(tc)
			r := ToolResult{Call: tc, Result: h.Agent.ExecuteCall(tc)}
			results = append(results, r)
			h.notifyResult(tc, r.Result)
		}
		h.Agent.RecordAutoResults(results)

		for {
			tc, ok := h.Agent.NextApproval()
			if !ok {
				break
			}
			h.notifyCall(tc)
			result := h.resolveGated(tc)
			h.Agent.ResolveApproval(result)
			h.notifyResult(tc, result)
		}

		if h.wasInterrupted() {
			h.Agent.CancelTurn()
			return "", ErrInterrupted
		}

		// Steering messages queued mid-turn join the conversation between tool
		// rounds; they count as fresh user input, so they also reset the round
		// counter (matching the TUI's injectSteering).
		if h.Steer != nil {
			if msgs := h.Steer(); len(msgs) > 0 {
				for _, msg := range msgs {
					h.Agent.Append(provider.Message{Role: provider.RoleUser, Content: msg})
				}
				h.Agent.ResetRounds()
			}
		}

		// Mirrors the TUI's resumeToolLoop: the cap is checked after a round's
		// results are recorded, before the next stream request. Headless has no
		// user to hand control back to, so hitting the cap is a failure — and
		// an uncapped agent (--max-rounds 0) never reaches it, which is why
		// that spelling is only offered to a foreground run someone can
		// interrupt.
		if h.Agent.CapReached() {
			return "", fmt.Errorf("%w after %d rounds", ErrRoundCap, h.Agent.Rounds())
		}
	}
}

// streamOnce opens one completion stream over the current conversation and
// consumes it, returning the assistant text and any tool calls that ended the
// stream. Like the TUI's terminalMsg, a tool-call event ends the stream.
func (h *Headless) streamOnce() (string, []provider.ToolCall, error) {
	events, cancel, err := h.Agent.Stream(h.Agent.RequestMessages())
	if err != nil {
		return "", nil, err
	}
	h.mu.Lock()
	if h.interrupted {
		h.mu.Unlock()
		cancel()
		return "", nil, nil
	}
	h.streamCancel = cancel
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.streamCancel = nil
		h.mu.Unlock()
		cancel()
	}()

	var text strings.Builder
	for ev := range events {
		if ev.Err != nil {
			if h.wasInterrupted() {
				// The abort we caused is not a real stream failure.
				return text.String(), nil, nil
			}
			return "", nil, ev.Err
		}
		if ev.Token != "" {
			text.WriteString(ev.Token)
			if h.OnText != nil {
				h.OnText(ev.Token)
			}
		}
		if ev.Usage != nil && h.OnUsage != nil {
			h.OnUsage(ev.Usage)
		}
		if len(ev.ToolCalls) > 0 {
			return text.String(), ev.ToolCalls, nil
		}
		if ev.Done {
			break
		}
	}
	return text.String(), nil, nil
}

func (h *Headless) resolveGated(tc provider.ToolCall) string {
	if h.Resolve == nil {
		return "error: tool " + tc.Name + " cannot be approved in this session"
	}
	return h.Resolve(tc)
}

func (h *Headless) notifyCall(tc provider.ToolCall) {
	if h.OnToolCall != nil {
		h.OnToolCall(tc)
	}
}

func (h *Headless) notifyResult(tc provider.ToolCall, result string) {
	if h.OnToolResult != nil {
		h.OnToolResult(tc, result)
	}
}
