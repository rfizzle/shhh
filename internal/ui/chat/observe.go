package chat

// Session observability: the Model reports content-free events —
// usage totals, tool-call durations/outcomes, and mode decisions with
// enum-like reason codes — to an Observer the CLI wires to storage. Nothing
// here ever carries prompts, outputs, paths, or command text.

import (
	"fmt"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/digest"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// Pos is where in the session an event happened: the turn, and the tool
// round within it. It is what turns a pile of tool events into a shape —
// forty searches in one turn's round 30–70 is a circling investigation,
// forty searches across forty turns is a session.
type Pos struct {
	Turn  int64
	Round int64
}

// Observer receives content-free session events. Any callback may be nil.
// Tool names, decisions ("allow"/"deny"/"ask"), outcomes ("ok"/"error"),
// error classes, turn outcomes, signal codes and reason codes are all drawn
// from closed sets; the only free strings are identifiers — a skill's name,
// a saved session's name.
// See docs/capabilities/sessions-and-memory.md#observations-are-what-the-session-did.
type Observer struct {
	// Usage reports the session's cumulative totals after each request —
	// every request, not just the agent's own, and already priced. The cost
	// comes with the tokens because the recorder cannot work it out for
	// itself: a session mixes models, and pricing one total against one of
	// them is how a mixed session gets misreported.
	Usage func(turns, tokensIn, tokensOut int64, cost float64, priced bool)
	// ToolCall reports one executed tool call. class is the error's class
	// when the outcome is an error, and "empty" for a search that found
	// nothing.
	ToolCall func(at Pos, tool string, duration time.Duration, outcome, class string)
	// Decision reports one mode-policy verdict for a gated tool call.
	Decision func(at Pos, decision, reason string)
	// Turn reports a turn closing: how it ended, how many tool rounds it
	// took and how long it ran. A turn that pauses at its round cap
	// reports once as paused and, if granted more rounds, once more when
	// it finally ends.
	Turn func(turn, rounds int64, duration time.Duration, outcome string)
	// Signal reports one of the loop's own safeguards or a workflow
	// transition firing, with a qualifier from a closed set.
	Signal func(at Pos, code, reason string)
	// Session names the saved conversation this session is writing, so
	// metadata and transcript can be joined by someone who asks to.
	Session func(name string)
}

// WithObserver wires session observability; the zero Observer
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
	outcomeOK    = digest.OutcomeOK
	outcomeError = digest.OutcomeError
)

// Turn outcomes for Observer.Turn.
const (
	turnDone      = "done"
	turnCancelled = "cancelled"
	turnFailed    = "failed"
	turnCapPaused = "cap-paused"
)

// Signal codes for Observer.Signal. Each names the thing that fired; the
// reason beside it is its qualifier, from the closed set the comment gives.
const (
	// signalRepeat: the repeat detector told the model it was circling.
	// Reason: the tool name.
	signalRepeat = "repeat-notice"
	// signalTrim: old tool results were elided to make room. Reason: how
	// many, as a number.
	signalTrim = "context-trimmed"
	// signalSummary: the summarizer read the session. Reason: "on-target",
	// "off-target" or "unclear". Every reading is recorded, not just the
	// drifting ones — a drift rate needs its denominator.
	signalSummary = "summary"
	// signalSteer: the user sent instructions into a running turn. Reason:
	// how many messages, as a number.
	signalSteer = "steered"
	// signalIntervene: the session interrupted its own turn to ask it to take
	// stock. Reason: "steer" (a drift verdict was acted on) or "check-in"
	// (the round interval came round). Separate from signalSteer because the
	// question a drift rate asks is what the session did on its own, and
	// folding the two together would put the user's own messages in the
	// numerator.
	signalIntervene = "intervened"
	// signalUndo: the user took a turn's edits back. Reason: "".
	signalUndo = "undo"
	// signalMode: the permission mode changed. Reason: the new mode.
	signalMode = "mode"
	// signalPlan: a plan card was answered. Reason: "approved", "kept" or
	// "rejected".
	signalPlan = "plan"
	// signalRounds: a round-cap pause was answered. Reason: "granted" or
	// "uncapped".
	signalRounds = "rounds"
	// signalSubagent: a child finished. Reason: its final state.
	signalSubagent = "subagent"
	// signalRun: the backlog runner moved. Reason: the action taken, or
	// "replan", "stopped", "kept", "lane-refused".
	signalRun = "run"
	// signalSkill: the user activated a skill by command. Reason: its name.
	signalSkill = "skill"
)

// Error classes for Observer.ToolCall, from the shape of the result text.
// Every executor reports failure as an "error: ..." result, and the classes
// below are the ones a reader can act on differently: a bad-args failure is
// a prompt's fault, an out-of-scope one is policy's, a not-found one is the
// model's picture of the tree being stale.
const (
	classDeclined   = "declined"
	classPlanMode   = "plan-mode"
	classOutOfScope = "out-of-scope"
	classNotFound   = "not-found"
	classPermission = "permission"
	classTimeout    = "timeout"
	classCancelled  = "cancelled"
	classBadArgs    = "bad-args"
	classUnknown    = "unknown-tool"
	classOther      = "other"
	// classExitStatus is a command that ran and exited non-zero.
	classExitStatus = "exit-status"
	// classEmpty qualifies a successful search that matched nothing.
	classEmpty = "empty"
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
	// A refusal for what the call reaches carries the directory in
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

// classFromResult names the class of a failed result, or "empty" for a
// search that found nothing, by matching the shape of the text. The text
// itself never leaves this function.
func classFromResult(result string) string {
	if !strings.HasPrefix(result, "error:") {
		if strings.HasPrefix(result, "No matches") {
			return classEmpty
		}
		return ""
	}
	r := strings.ToLower(result)
	switch {
	case strings.Contains(r, "declined") || strings.Contains(r, "not approved") || strings.Contains(r, "denied"):
		return classDeclined
	case strings.Contains(r, "plan mode"):
		return classPlanMode
	case strings.Contains(r, "outside the") || strings.Contains(r, "scope"):
		return classOutOfScope
	case strings.Contains(r, "cancelled") || strings.Contains(r, "canceled"):
		return classCancelled
	case strings.Contains(r, "no such file") || strings.Contains(r, "not found") || strings.Contains(r, "does not exist"):
		return classNotFound
	case strings.Contains(r, "permission"):
		return classPermission
	case strings.Contains(r, "timed out") || strings.Contains(r, "timeout") || strings.Contains(r, "deadline exceeded"):
		return classTimeout
	case strings.Contains(r, "unknown tool") || strings.Contains(r, "no tool executor"):
		return classUnknown
	case strings.Contains(r, "invalid") || strings.Contains(r, "missing") || strings.Contains(r, "required") ||
		strings.Contains(r, "unmarshal") || strings.Contains(r, "parse") || strings.Contains(r, "argument"):
		return classBadArgs
	}
	return classOther
}

// pos is where the session is now.
func (m Model) pos() Pos {
	return Pos{Turn: m.turnCount, Round: int64(m.agent.Rounds())}
}

// notifyUsage reports what the whole session has spent — the agent's turns,
// the classifier, the summary and every sub-agent — from the ledger the
// provider gate fills. Without a ledger there is only the agent's own
// accounting to report, which is what a session assembled without one has.
func (m *Model) notifyUsage() {
	if m.observer.Usage == nil {
		return
	}
	if m.ledger == nil {
		cost, priced := m.usageTotalCost(m.TotalTokensIn, m.TotalTokensOut)
		m.observer.Usage(m.turnCount, m.TotalTokensIn, m.TotalTokensOut, cost, priced)
		return
	}
	t := m.ledger.Total()
	m.observer.Usage(m.turnCount, t.In, t.Out, t.Cost, t.Priced)
}

// usageTotalCost prices a token pair against the session's current model, for
// the ledgerless case.
func (m Model) usageTotalCost(in, out int64) (float64, bool) {
	if m.prices == nil || m.modelName == "" {
		return 0, false
	}
	inCost, outCost, found := m.prices.Cost(m.modelName, in, out)
	if !found {
		return 0, false
	}
	return inCost + outCost, true
}

// recordToolResult records a tool call from its result text: the outcome
// and, for a failure, its class.
func (m *Model) recordToolResult(tool string, duration time.Duration, result string) {
	m.recordToolEvent(tool, duration, digest.Outcome(result), classFromResult(result))
}

func (m *Model) recordToolEvent(tool string, duration time.Duration, outcome, class string) {
	if m.observer.ToolCall != nil {
		m.observer.ToolCall(m.pos(), tool, duration, outcome, class)
	}
}

func (m *Model) recordDecision(decision, reason string) {
	if m.observer.Decision != nil {
		m.observer.Decision(m.pos(), decision, reason)
	}
}

// recordTurn reports the turn that is closing. It runs from the one place
// every turn ends (appendTurnClose), so no turn can end unrecorded.
func (m *Model) recordTurn(outcome string) {
	if m.observer.Turn == nil {
		return
	}
	var elapsed time.Duration
	if !m.turnStarted.IsZero() {
		end := m.turnEnded
		if end.IsZero() {
			end = time.Now()
		}
		elapsed = end.Sub(m.turnStarted)
	}
	m.observer.Turn(m.turnCount, int64(m.agent.Rounds()), elapsed, outcome)
}

// turnOutcomeCode is the turn's close state as the recorder's closed set.
func (m Model) turnOutcomeCode() string {
	if m.pausedAtRoundLimit() {
		return turnCapPaused
	}
	switch m.turnOutcome {
	case components.TurnCancelled:
		return turnCancelled
	case components.TurnFailed:
		return turnFailed
	}
	return turnDone
}

func (m *Model) signal(code, reason string) {
	if m.observer.Signal != nil {
		m.observer.Signal(m.pos(), code, reason)
	}
}

// The context occupancy breakdown itself lives with the rest of the session
// vitals (vitals.go), so /stats and the inspector rail quote one
// source rather than two estimates that drift.

// statsReport renders /stats: the current session's context occupancy
// breakdown and cumulative spend, from the same accounting the inspector
// rail reads.
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

	// Session spend is the ledger's, not the turn accounting's: the agent's
	// own rounds are only one of the things this session pays for.
	total := m.sessionSpend()
	spend := fmt.Sprintf("Session spend: ↑%s ↓%s tokens",
		formatTokenCount(total.In), formatTokenCount(total.Out))
	if total.Cached > 0 {
		spend += fmt.Sprintf(" (%s cached)", formatTokenCount(total.Cached))
	}
	if total.Priced {
		spend += "  " + formatCost(total.Cost)
	}
	sb.WriteString(spend + "\n")

	sb.WriteString(m.spendBySourceReport())

	sb.WriteString(m.spendByModelReport())

	fmt.Fprintf(&sb, "Turns: %d", m.turnCount)
	if t, ok := m.vitals.lastTurn(); ok {
		fmt.Fprintf(&sb, " · last turn ↑%s ↓%s in %s",
			formatTokenCount(t.In), formatTokenCount(t.Out), components.FormatElapsed(t.Elapsed))
	}
	// The occupancy half of this report has a surface of its own that draws
	// it and itemises it below the category. Naming it here is how a reader
	// who came for the breakdown finds out there is a better answer to it.
	sb.WriteString("\n/context draws the occupancy and itemises it down to the tool.")
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

// sessionSpend is what the whole session has spent — the agent's turns, the
// permission classifier, the session summary and every sub-agent. It comes
// from the provider gate's ledger, so a feature added later is included
// without this function changing. A session with no ledger has only the
// agent's own accounting to report.
func (m Model) sessionSpend() meter.Totals {
	if m.ledger != nil {
		return m.ledger.Total()
	}
	return meter.Totals{
		In:     m.vitals.totalIn,
		Out:    m.vitals.totalOut,
		Cached: m.vitals.totalCached,
		Cost:   m.vitals.totalCost,
		Priced: m.vitals.priced,
	}
}

// spendBySourceReport breaks the session total down by what spent it. It
// answers the question the total cannot: how much of this went on the work
// the user asked for, and how much on the machinery around it.
//
// A named requester — a sub-agent — gets its own row under its class, because
// a fan-out that ran away with the budget is only actionable if you can see
// which child did it.
func (m Model) spendBySourceReport() string {
	if m.ledger == nil {
		return ""
	}
	sources := m.ledger.BySource()
	origins := m.ledger.ByOrigin()
	// One requester says nothing the total above does not.
	if len(sources) < 2 && len(origins) < 2 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("By source:\n")
	for _, src := range sources {
		sb.WriteString(spendRow(string(src.Origin.Source), 24, src))
		named := 0
		for _, o := range origins {
			if o.Origin.Source == src.Origin.Source && o.Origin.Label != "" {
				named++
			}
		}
		// Naming one child under a class of one repeats the row above it.
		if named < 2 {
			continue
		}
		for _, o := range origins {
			if o.Origin.Source == src.Origin.Source && o.Origin.Label != "" {
				sb.WriteString(spendRow("  "+o.Origin.Label, 24, o))
			}
		}
	}
	return sb.String()
}

// spendByModelReport prices the session per model, so the total can be
// reconciled against what each one actually answered. A fan-out and a
// mid-session /model switch both put more than one model on the bill; one
// model says nothing the total does not, so it says nothing.
func (m Model) spendByModelReport() string {
	if m.ledger == nil {
		return legacyModelSplitReport(m.vitals.modelSplit())
	}
	rows := m.ledger.ByModel()
	if len(rows) < 2 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("By model:\n")
	for _, e := range rows {
		name := e.Model
		if name == "" {
			name = "(unnamed)"
		}
		sb.WriteString(spendRow(name, 24, e))
	}
	return sb.String()
}

// spendRow renders one breakdown line. An unpriced row reports its tokens
// rather than a cost of zero, which would read as "this was free".
func spendRow(label string, width int, e meter.Entry) string {
	line := fmt.Sprintf("  %-*s ↑%s ↓%s", width, label,
		formatTokenCount(e.In), formatTokenCount(e.Out))
	switch {
	case e.Priced:
		line += "  " + formatCost(e.Cost)
	case e.Requests == 0:
		// Something the session switched to and never used says so, rather
		// than reporting a cost of nothing as though it ran.
		line += "  (no requests)"
	}
	return line + "\n"
}

// legacyModelSplitReport is the per-model breakdown for a session assembled
// without a ledger, which is every session in a test that builds the model
// directly.
func legacyModelSplitReport(split []modelSpend) string {
	if split == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("By model:\n")
	for _, ms := range split {
		name := ms.Model
		if name == "" {
			name = "(unnamed)"
		}
		sb.WriteString(spendRow(name, 24, meter.Entry{
			In: ms.In, Out: ms.Out, Cost: ms.Cost, Priced: ms.Priced, Requests: ms.Requests,
		}))
	}
	return sb.String()
}
