package chat

// Two-pane cockpit (S-092, DESIGN-TUI.md §15). At or above 130 content
// columns the surface splits: the transcript keeps the left pane, a
// 46-column inspector rail takes the right, and one dim │ column divides
// them. Below 130 the rail is dropped entirely and the single-pane layout is
// exactly what it was.
//
// The split is horizontal only — viewport height accounting (chromeHeight,
// viewportHeight, syncViewport) is untouched — and the prompt frame spans
// both panes, because steering is a session-level act. Takeover surfaces
// (approval cards, pickers, the full-screen diff, the agent list) span the
// full width and hide the rail, restoring it when they are dismissed.
//
// Everything the rail shows is already known to the session; the rail is a
// passive renderer fed from here, like components.Cockpit.

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/components"
)

const (
	// paneDividerWidth is the single │ column between the panes.
	paneDividerWidth = 1
	// contextBurnSamples bounds the per-round context series to what the
	// rail's sparkline can draw.
	contextBurnSamples = 8
)

var paneDividerStyle = lipgloss.NewStyle().Foreground(components.Palette.Dim)

// twoPane reports whether the surface is split. Width is the first condition;
// a takeover surface (or an attached child's session, which is a different
// session's transcript) is the second.
func (m Model) twoPane() bool {
	if m.contentWidth() < components.InspectorMinContentWidth {
		return false
	}
	return !m.inspectorHidden()
}

// inspectorHidden reports whether something is covering the rail. Takeover
// surfaces span both panes (§15b); the attached view is a child's session, so
// the orchestrator's turn-scoped rail has nothing true to say beside it.
func (m Model) inspectorHidden() bool {
	if m.attachedTo != "" || m.agentList != nil || m.activeChildAsk() != nil {
		return true
	}
	switch m.state {
	case stateConfirmRun, statePlanApprove, stateRewindPick, statePick, stateDiffFull, stateModelList:
		return true
	}
	return false
}

// transcriptWidth is the width the transcript wraps to: the reduced pane
// width when the surface is split, the full content width otherwise.
func (m Model) transcriptWidth() int {
	if !m.twoPane() {
		return m.contentWidth()
	}
	w := m.contentWidth() - components.InspectorWidth - paneDividerWidth
	if w < 1 {
		return 1
	}
	return w
}

// turnStartIndex is the first entry of the current turn — the last user entry
// in the transcript. It returns len(transcript) when no turn has started.
func (m Model) turnStartIndex() int {
	for i := len(m.transcript) - 1; i >= 0; i-- {
		if m.transcript[i].kind == entryUser {
			return i
		}
	}
	return len(m.transcript)
}

// turnEntries is the current turn's slice of the transcript.
func (m Model) turnEntries() []entry {
	return m.transcript[m.turnStartIndex():]
}

// turnElapsed is how long the current turn has been running — live while it
// works, final once it is done.
func (m Model) turnElapsed() time.Duration {
	if m.turnStarted.IsZero() {
		return 0
	}
	if m.working() || m.turnEnded.IsZero() {
		return time.Since(m.turnStarted)
	}
	return m.turnEnded.Sub(m.turnStarted)
}

// inspectorData assembles the rail from what the session already tracks. A
// block with nothing to say is left nil, and the component omits it.
func (m Model) inspectorData() components.InspectorRail {
	return components.InspectorRail{
		Turn:    m.inspectorTurn(),
		Changes: m.inspectorChanges(),
		Agents:  m.inspectorAgents(),
		Context: m.inspectorContext(),
		Spend:   m.inspectorSpend(),
	}
}

// inspectorTurn counts this turn's steps and tools. The step count is
// observed, not declared, so it feeds "step 3" and no meter; a declared plan
// (S-104) is the one place a total is authoritative, and only then does the
// progress meter have a true denominator.
func (m Model) inspectorTurn() *components.InspectorTurn {
	es := m.turnEntries()
	t := components.InspectorTurn{Running: m.working(), Elapsed: m.turnElapsed()}
	for _, blk := range stepBlocks(es) {
		if blk.step != nil {
			t.Step = blk.step.ordinal
		}
	}
	for _, e := range es {
		if isActivityEntry(e) {
			t.Tools++
		}
	}
	if t.Tools == 0 && !t.Running {
		return nil
	}
	return &t
}

// inspectorChanges aggregates this turn's applied edits by path (a file
// edited twice is one row with the net counts) and notes a command that came
// back broken — the failing-test state as far as the session can see it until
// the changeset store lands (S-097).
func (m Model) inspectorChanges() *components.InspectorChanges {
	var c components.InspectorChanges
	at := map[string]int{}
	for _, e := range m.turnEntries() {
		switch e.kind {
		case entryDiff:
			if e.diff == nil {
				continue
			}
			adds, dels := diff.Stats(e.diff.Hunks)
			c.Added += adds
			c.Removed += dels
			if i, ok := at[e.diff.Path]; ok {
				c.Files[i].Added += adds
				c.Files[i].Removed += dels
				continue
			}
			at[e.diff.Path] = len(c.Files)
			c.Files = append(c.Files, components.InspectorFile{Path: e.diff.Path, Added: adds, Removed: dels})
		case entryCommand:
			if e.exitCode != 0 {
				c.Failure = firstLine(e.text)
				c.FailureNote = components.OutcomeExit(e.exitCode)
			}
		}
	}
	if len(c.Files) == 0 && c.Failure == "" {
		return nil
	}
	return &c
}

// inspectorAgents lists the children still in flight; finished ones belong to
// the agent manager, not to a block about what is running now.
func (m Model) inspectorAgents() []components.InspectorAgent {
	if m.subagents == nil {
		return nil
	}
	var agents []components.InspectorAgent
	for _, st := range m.subagents.Snapshot() {
		switch st.State {
		case subagent.StateRunning, subagent.StateBlocked:
		default:
			continue
		}
		agents = append(agents, components.InspectorAgent{
			Name:    st.Name,
			Detail:  st.Detail,
			Spend:   m.spendLabel(st.TokensIn, st.TokensOut),
			Tools:   st.ToolCalls,
			Blocked: st.State == subagent.StateBlocked,
		})
	}
	return agents
}

// inspectorContext reports occupancy against the model's window, with the
// same thresholds the status bar and S-055's trim warnings use, plus the
// per-round burn behind the sparkline (S-093). A session with fewer than two
// rounds reported has no trend, and the row says the number is an estimate
// rather than drawing a flat line.
func (m Model) inspectorContext() *components.InspectorContext {
	b := m.contextAccounting()
	tokens := b.total()
	if tokens <= 0 {
		return nil
	}
	window := m.contextWindow()
	c := components.InspectorContext{
		Pct:       int(tokens * 100 / window),
		Tokens:    tokens,
		Window:    window,
		WarnPct:   warnThresholdPercent,
		AlertPct:  trimThresholdPercent,
		Estimated: !b.Reported,
	}
	if m.TotalTokensIn != 0 || m.TotalTokensOut != 0 {
		c.Tokens1 = "↑" + formatTokenCount(m.TotalTokensIn)
		c.Tokens2 = "↓" + formatTokenCount(m.TotalTokensOut)
	}
	c.Burn = m.vitals.series()
	return &c
}

// inspectorSpend splits the cost between this session's own requests and its
// children, which are priced with the same table (the approximation the agent
// rows already make).
func (m Model) inspectorSpend() *components.InspectorSpend {
	var childIn, childOut int64
	if m.subagents != nil {
		for _, st := range m.subagents.Snapshot() {
			childIn += st.TokensIn
			childOut += st.TokensOut
		}
	}
	if m.TotalTokensIn == 0 && m.TotalTokensOut == 0 && childIn == 0 && childOut == 0 {
		return nil
	}
	s := components.InspectorSpend{
		Turn:    m.spendLabel(m.turnTokensIn, m.turnTokensOut),
		Main:    m.spendLabel(m.TotalTokensIn, m.TotalTokensOut),
		Session: m.spendLabel(m.TotalTokensIn+childIn, m.TotalTokensOut+childOut),
		Model:   m.modelName,
	}
	if childIn != 0 || childOut != 0 {
		s.Children = m.spendLabel(childIn, childOut)
	}
	return &s
}

// splitPanes joins the body with the inspector rail, line by line: the
// transcript pane padded to its width, the divider column, then the rail.
// The rail is rendered to the body's own height, so the split adds no rows.
func (m Model) splitPanes(body string) string {
	data := m.inspectorData()
	if data.Empty() {
		return body
	}
	lines := strings.Split(body, "\n")
	rail := data.Lines(components.InspectorWidth, len(lines))
	if len(rail) == 0 {
		return body
	}
	pane := m.transcriptWidth()
	divider := paneDividerStyle.Render("│")
	out := make([]string, len(lines))
	for i, line := range lines {
		line = clipRow(line, pane)
		row := line + strings.Repeat(" ", max(0, pane-lipgloss.Width(line))) + divider
		if i < len(rail) {
			row += rail[i]
		}
		out[i] = strings.TrimRight(row, " ")
	}
	return strings.Join(out, "\n")
}

// inspectorStatus is the /stats-adjacent line describing the split, used by
// /ui to say what the current layout is.
func (m Model) inspectorStatus() string {
	if m.twoPane() {
		return fmt.Sprintf("two panes — %d-column transcript + %d-column inspector rail",
			m.transcriptWidth(), components.InspectorWidth)
	}
	return fmt.Sprintf("one pane — %d columns", m.contentWidth())
}
