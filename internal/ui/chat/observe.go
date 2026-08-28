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
	"github.com/rfizzle/shhh/internal/ui/components"
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
		// The blanket grants: every edit, every command (/permissions allow).
		return "session-grant"
	case "session grant":
		// The scoped ones [a] records — a command's leading words, a file's
		// directory. They are a different decision and count separately.
		return "session-scope"
	case "allowlist":
		return "allowlist"
	case "plan mode":
		return "plan-mode"
	case "plan mode inspection":
		return "plan-inspection"
	}
	// A refusal for what the call reaches (S-141) carries the directory in
	// its reason, so it is matched by shape rather than by equality — the
	// free text still never reaches the metrics.
	if strings.HasPrefix(raw, "outside the working scope") {
		return "out-of-scope"
	}
	return "other"
}

// askReason is the reason code recorded when policy falls through to
// prompting the user.
func askReason(a agent.Action) string {
	switch {
	case a.SafetyFlagged:
		return "safety"
	case a.ScopeSensitive:
		return "scope-sensitive"
	case len(a.OutOfScope) > 0:
		return "out-of-scope"
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

// The context occupancy breakdown itself lives with the rest of the session
// vitals (S-093, vitals.go), so /stats and the inspector rail quote one
// source rather than two estimates that drift.

// statsReport renders /stats: the current session's context occupancy
// breakdown and cumulative spend, from the same accounting the inspector
// rail reads (S-093).
func (m Model) statsReport() string {
	b := m.contextAccounting()
	source := "estimated"
	if b.Reported {
		source = "provider-reported"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Context occupancy (~%s of %s window, %s):\n",
		formatTokenCount(b.total()), formatTokenCount(m.contextWindow()), source)
	for _, row := range []struct {
		label  string
		tokens int64
		always bool
	}{
		{"system prompt", b.System, true},
		{"project context", b.Project, false},
		{"tool definitions", b.Tools, true},
		{"messages", b.Messages, true},
		{"tool results", b.ToolResults, true},
	} {
		if row.tokens == 0 && !row.always {
			continue
		}
		fmt.Fprintf(&sb, "  %-17s ~%s\n", row.label, formatTokenCount(row.tokens))
	}

	spend := fmt.Sprintf("Session spend: ↑%s ↓%s tokens",
		formatTokenCount(m.vitals.totalIn), formatTokenCount(m.vitals.totalOut))
	if m.vitals.totalCached > 0 {
		spend += fmt.Sprintf(" (%s cached)", formatTokenCount(m.vitals.totalCached))
	}
	if m.vitals.priced {
		spend += "  " + formatCost(m.vitals.totalCost)
	}
	sb.WriteString(spend + "\n")

	// A session that changed model mid-flight is priced per model (S-107), so
	// the total above can be reconciled against what each one actually
	// answered. One model says nothing the total does not, so it says nothing.
	if split := m.vitals.modelSplit(); split != nil {
		sb.WriteString("By model:\n")
		for _, ms := range split {
			name := ms.Model
			if name == "" {
				name = "(unnamed)"
			}
			line := fmt.Sprintf("  %-24s ↑%s ↓%s", name,
				formatTokenCount(ms.In), formatTokenCount(ms.Out))
			switch {
			case ms.Priced:
				line += "  " + formatCost(ms.Cost)
			case ms.Requests == 0:
				// A model the session switched to and never used says so,
				// rather than reporting a cost of nothing as though it ran.
				line += "  (no requests)"
			}
			sb.WriteString(line + "\n")
		}
	}

	fmt.Fprintf(&sb, "Turns: %d", m.turnCount)
	if t, ok := m.vitals.lastTurn(); ok {
		fmt.Fprintf(&sb, " · last turn ↑%s ↓%s in %s",
			formatTokenCount(t.In), formatTokenCount(t.Out), components.FormatElapsed(t.Elapsed))
	}
	return sb.String()
}

// formatCost is the shared dollar format: four decimals below a cent, two
// above, so a cheap session is not reported as $0.00.
func formatCost(v float64) string {
	if v < 0.01 {
		return fmt.Sprintf("$%.4f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}
