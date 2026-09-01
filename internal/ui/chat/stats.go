package chat

// /stats: what the session's context is occupied by and what it has spent,
// itemised by source and by model. It reads the same accounting the
// inspector rail does — the context breakdown lives with the rest of the
// session vitals (vitals.go), so the two quote one source rather than two
// estimates that drift.

import (
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// WithToolTokenEstimate sets the estimated token cost of the registered tool
// definitions, shown in /stats' context occupancy breakdown.
func (m Model) WithToolTokenEstimate(n int64) Model {
	m.toolDefTokens = n
	return m
}

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
