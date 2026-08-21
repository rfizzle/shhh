package chat

// Session observability (S-065): the Model reports content-free events —
// usage totals, tool-call durations/outcomes, and mode decisions with
// enum-like reason codes — to an Observer the CLI wires to storage. Nothing
// here ever carries prompts, outputs, paths, or command text.

import (
	"fmt"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
)

// Observer receives content-free session events. Any callback may be nil.
// Tool names, decisions ("allow"/"deny"/"ask"), outcomes ("ok"/"error"), and
// reason codes are all drawn from closed sets.
type Observer struct {
	// Usage reports the session's cumulative totals after each request.
	Usage func(turns, tokensIn, tokensOut int64)
	// ToolCall reports one executed tool call.
	ToolCall func(tool string, duration time.Duration, outcome string)
	// Decision reports one mode-policy verdict for a gated tool call.
	Decision func(decision, reason string)
}

// WithObserver wires session observability (S-065); the zero Observer
// disables it.
func (m Model) WithObserver(o Observer) Model {
	m.observer = o
	return m
}

// WithToolTokenEstimate sets the estimated token cost of the registered tool
// definitions, shown in /stats' context occupancy breakdown.
func (m Model) WithToolTokenEstimate(n int64) Model {
	m.toolDefTokens = n
	return m
}

// Decision codes for Observer.Decision.
const (
	decisionAllow = "allow"
	decisionDeny  = "deny"
	decisionAsk   = "ask"
)

// Tool outcomes for Observer.ToolCall.
const (
	outcomeOK    = "ok"
	outcomeError = "error"
)

// reasonCode maps a mode-policy reason string (a closed set produced by
// agent.ModePolicy.Decide) to its enum-like storage code, so free text can
// never leak into the metrics.
func reasonCode(raw string) string {
	switch raw {
	case agent.ModeAcceptEdits.String() + " mode":
		return "mode-accept-edits"
	case agent.ModeAuto.String() + " mode":
		return "mode-auto"
	case "session policy":
		return "session-grant"
	case "allowlist":
		return "allowlist"
	case "plan mode":
		return "plan-mode"
	case "plan mode inspection":
		return "plan-inspection"
	}
	return "other"
}

// askReason is the reason code recorded when policy falls through to
// prompting the user.
func askReason(a agent.Action) string {
	if a.SafetyFlagged {
		return "safety"
	}
	return "policy"
}

// outcomeFromResult classifies a tool result by the error convention every
// executor follows ("error: ..." prefixes).
func outcomeFromResult(result string) string {
	if strings.HasPrefix(result, "error:") {
		return outcomeError
	}
	return outcomeOK
}

func (m *Model) notifyUsage() {
	if m.observer.Usage != nil {
		m.observer.Usage(m.turnCount, m.TotalTokensIn, m.TotalTokensOut)
	}
}

func (m *Model) recordToolEvent(tool string, duration time.Duration, outcome string) {
	if m.observer.ToolCall != nil {
		m.observer.ToolCall(tool, duration, outcome)
	}
}

func (m *Model) recordDecision(decision, reason string) {
	if m.observer.Decision != nil {
		m.observer.Decision(decision, reason)
	}
}

// contextBreakdown is the /stats occupancy estimate: how the conversation's
// token budget splits across the system prompt, tool definitions, user and
// assistant messages, and tool results.
type contextBreakdown struct {
	System      int64
	Tools       int64
	Messages    int64
	ToolResults int64
}

func (b contextBreakdown) total() int64 {
	return b.System + b.Tools + b.Messages + b.ToolResults
}

func (m Model) contextBreakdown() contextBreakdown {
	b := contextBreakdown{Tools: m.toolDefTokens}
	for i, msg := range m.agent.Messages() {
		switch {
		case i == 0 && msg.Role == provider.RoleSystem:
			b.System += agent.EstimateTokens(msg.Content)
		case msg.Role == provider.RoleTool:
			b.ToolResults += agent.EstimateTokens(msg.Content)
		default:
			b.Messages += agent.EstimateTokens(msg.Content)
			for _, tc := range msg.ToolCalls {
				b.Messages += agent.EstimateTokens(tc.Arguments)
			}
		}
	}
	return b
}

// statsReport renders /stats: the current session's context occupancy
// breakdown and cumulative spend.
func (m Model) statsReport() string {
	b := m.contextBreakdown()
	var sb strings.Builder
	fmt.Fprintf(&sb, "Context occupancy (~%s estimated of %s window):\n",
		formatTokenCount(b.total()), formatTokenCount(m.contextWindow()))
	fmt.Fprintf(&sb, "  system prompt     ~%s\n", formatTokenCount(b.System))
	fmt.Fprintf(&sb, "  tool definitions  ~%s\n", formatTokenCount(b.Tools))
	fmt.Fprintf(&sb, "  messages          ~%s\n", formatTokenCount(b.Messages))
	fmt.Fprintf(&sb, "  tool results      ~%s\n", formatTokenCount(b.ToolResults))
	if m.contextTokens > 0 {
		fmt.Fprintf(&sb, "  last request carried ~%s (provider-reported)\n", formatTokenCount(m.contextTokens))
	}

	spend := fmt.Sprintf("Session spend: ↑%s ↓%s tokens", formatTokenCount(m.TotalTokensIn), formatTokenCount(m.TotalTokensOut))
	if m.prices != nil && m.modelName != "" {
		if inCost, outCost, found := m.prices.Cost(m.modelName, m.TotalTokensIn, m.TotalTokensOut); found {
			total := inCost + outCost
			if total < 0.01 {
				spend += fmt.Sprintf("  $%.4f", total)
			} else {
				spend += fmt.Sprintf("  $%.2f", total)
			}
		}
	}
	sb.WriteString(spend + "\n")
	fmt.Fprintf(&sb, "Turns: %d", m.turnCount)
	return sb.String()
}
