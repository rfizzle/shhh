package agent

// Headless drives an Agent's loop synchronously to completion (S-057): one
// user turn, stream events consumed inline, tool rounds dispatched until the
// model produces a final message or the per-turn round cap is hit. The chat
// TUI drives the same Agent asynchronously through Bubble Tea messages; this
// runner is the scriptable front-end behind `shhh code -p`.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
)

// ErrRoundCap is returned by Headless.Run when a turn uses up its tool
// rounds without reaching a final assistant message.
var ErrRoundCap = errors.New("tool round cap reached")

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
}

// Run executes one user turn to completion and returns the final assistant
// text. The conversation (including tool results) accumulates on the Agent,
// so callers can inspect Messages() afterwards or run another turn.
func (h *Headless) Run(prompt string) (string, error) {
	h.Agent.StartTurn(prompt)
	for {
		text, calls, err := h.streamOnce()
		if err != nil {
			return "", err
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

		// Mirrors the TUI's resumeToolLoop: the cap is checked after a round's
		// results are recorded, before the next stream request. Headless has no
		// user to hand control back to, so hitting the cap is a failure.
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
	defer cancel()

	var text strings.Builder
	for ev := range events {
		if ev.Err != nil {
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
