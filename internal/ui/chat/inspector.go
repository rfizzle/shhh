package chat

// Two-pane cockpit (docs/interface/surfaces.md#the-inspector-rail). At
// or above 130 content columns the surface splits: the transcript keeps the
// left pane, a
// 46-column inspector rail takes the right, and one dim │ column divides
// them. Below 130 the rail is dropped entirely and the single-pane layout is
// exactly what it was.
//
// The split is horizontal only — it is one of the two constraints the
// column half of the layout model resolves (layout.go), and the row
// budget it hands out is unchanged — and the prompt frame spans both panes,
// because steering is a session-level act. Takeover surfaces
// (approval cards, pickers, the full-screen diff, the agent list) span the
// full width and hide the rail, restoring it when they are dismissed.
//
// Everything the rail shows is already known to the session; the rail is a
// passive renderer fed from here, like components.Cockpit.
//
// THIS TURN is the turn; CHANGES, AGENTS, CONTEXT and SPEND are the session
//. The chat transcript is the turn-by-turn feed, so the rail is
// the standing overview beside it rather than a second copy of the same
// scroll.

import (
	"fmt"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/meter"
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

// paneStyles is the two-pane cockpit's own group.
type paneStyles struct {
	Divider lipgloss.Style
}

func newPaneStyles(p components.ColorTokens) paneStyles {
	return paneStyles{Divider: lipgloss.NewStyle().Foreground(p.Dim.Color())}
}

// twoPane reports whether the surface is split. Width is the first condition;
// a takeover surface (or an attached child's session, which is a different
// session's transcript) is the second.
func (m Model) twoPane() bool { return m.columns().inspector.Dx() > 0 }

// inspectorHidden reports whether something is covering the rail. Takeover
// surfaces span both panes; the attached view is a child's session, so
// the orchestrator's rail is answering for the wrong session beside it.
func (m Model) inspectorHidden() bool {
	if m.attachedTo != "" || m.agentList != nil {
		return true
	}
	// A decision still waiting for the keyboard is not a takeover (the
	// mid-sentence rule): the draft is live, the panes above it are what the
	// reader is looking at, and a card landing must not reflow the screen behind
	// it.
	if m.decisionUngated() {
		return false
	}
	if m.activeChildAsk() != nil {
		return true
	}
	switch m.state {
	case stateConfirmRun, statePlanApprove, stateRewindPick, statePick, stateDiffFull, stateReview, stateContext, stateModelList:
		return true
	}
	return false
}

// paneWidth is the transcript pane's own width: the reduced pane when the
// surface is split, the full content width otherwise. It is what the surfaces
// that take the pane over — the full-screen diff, review mode, the agent rows
// — render to, and the columns the body is drawn into (layout.go).
func (m Model) paneWidth() int { return m.columns().pane.Dx() }

// transcriptWidth is the width the transcript wraps to: the pane less the
// scroll gutter's column, which the pane reserves whether or
// not there is anything to draw in it. Everything the viewport shows — the
// feed, reading mode's gutter render, an attached child's session, the start
// screen — wraps to this, and so does the selection's coordinate space, so
// the gutter is never inside anything a drag can reach.
func (m Model) transcriptWidth() int { return max(m.columns().feed.Dx(), 1) }

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
//
// The approved plan's checklist is read once and handed to both blocks that
// need it: THIS TURN takes its denominator from it and PLAN draws it, and
// reading the transcript twice per frame to tell one story would be waste.
func (m Model) inspectorData() components.InspectorRail {
	steps := m.planChecklist()
	return components.InspectorRail{
		Summary: m.inspectorSummary(),
		Turn:    m.inspectorTurn(steps),
		Plan:    m.inspectorPlan(steps),
		Todo:    m.inspectorTodo(),
		Changes: m.inspectorChanges(),
		Agents:  m.inspectorAgents(),
		Context: m.inspectorContext(),
		Spend:   m.inspectorSpend(),
		Frame:   m.spinFrame,
	}
}

// inspectorTurn counts this turn's steps and tools. Without an approved plan
// the step count is observed, not declared, so it feeds "step 3" and no
// meter. An approved plan is the one place a total is authoritative,
// and only then does the progress meter have a true denominator.
func (m Model) inspectorTurn(steps []components.InspectorPlanStep) *components.InspectorTurn {
	es := m.turnEntries()
	t := components.InspectorTurn{Running: m.working(), Elapsed: m.turnElapsed()}
	if len(steps) > 0 {
		t.Step, t.Steps = planProgress(steps), len(steps)
	} else {
		for _, blk := range m.blocksOf(es) {
			if blk.step != nil && !blk.step.queued() {
				t.Step = blk.step.ordinal
			}
		}
	}
	for _, e := range es {
		if isActivityEntry(e) {
			t.Tools++
		}
	}
	// The turn's own files come from the same changeset its close row reads
	//, so THIS TURN and the row it leaves in the transcript cannot
	// report the turn two ways.
	if turn, ok := m.changes.Turn(m.turnCount); ok {
		t.Files, t.Added, t.Removed = turn.Files(), turn.Added, turn.Removed
	}
	if t.Tools == 0 && t.Files == 0 && !t.Running {
		return nil
	}
	return &t
}

// inspectorPlan is the PLAN block: the approved plan as a live checklist, so
// "where are we" never needs asking. It follows the plan rather
// than the turn or the session, because a plan that spans two turns is still
// the answer to the same question and is retired by the next instruction
// rather than by the clock.
func (m Model) inspectorPlan(steps []components.InspectorPlanStep) *components.InspectorPlan {
	if m.planRun == nil || len(steps) == 0 {
		return nil
	}
	return &components.InspectorPlan{
		Steps: steps,
		Done:  planStepsDone(steps),
		Drift: m.planRun.driftLabel(),
		Hint:  planHintRail,
	}
}

// inspectorChanges is the session's net change to the workspace (
// every path this session has touched, collapsed to one row each with
// the turns behind it, and the commands still coming back broken above them.
//
// It is session-scoped deliberately. A file edited in turn 2 is still on
// screen in turn 8, because "what has this session done to my machine" does
// not reset when the agent starts a new turn — the turn-by-turn feed is the
// transcript's job, and THIS TURN is the one block that answers for the turn.
// The rows are read from the changeset store rather than from the transcript,
// so an undo nets out and a child's applied patch counts.
func (m Model) inspectorChanges() *components.InspectorChanges {
	var c components.InspectorChanges
	touched := map[string]bool{}
	if t, ok := m.changes.Turn(m.turnCount); ok {
		for _, r := range t.Records {
			touched[r.Path] = true
		}
	}
	for _, f := range m.changes.SessionFiles() {
		c.Added += f.Added
		c.Removed += f.Removed
		c.Files = append(c.Files, components.InspectorFile{
			Path:     f.Path,
			Added:    f.Added,
			Removed:  f.Removed,
			Turns:    f.Turns,
			ThisTurn: touched[f.Path],
		})
	}
	c.Alerts = m.inspectorAlerts()
	if len(c.Files) == 0 && len(c.Alerts) == 0 {
		return nil
	}
	return &c
}

// inspectorAlerts is the workspace's standing bad news: the commands whose
// most recent run in this session came back broken, oldest first, each with
// the turn that ran it.
//
// An alert follows the workspace rather than the turn — it is cleared by the
// same command coming back clean, not by a new turn starting. That is the
// whole point of the block: a red row that clears itself because the agent
// moved on is the failure this rail exists to prevent.
func (m Model) inspectorAlerts() []components.InspectorAlert {
	type run struct {
		turn   int64
		note   string
		broken bool
	}
	last := map[string]*run{}
	var commands []string
	for _, e := range m.transcript {
		if e.kind != entryCommand {
			continue
		}
		label := firstLine(e.text)
		if label == "" {
			continue
		}
		r, ok := last[label]
		if !ok {
			r = &run{}
			last[label] = r
			commands = append(commands, label)
		}
		r.turn, r.broken, r.note = e.turn, e.exitCode != 0, components.OutcomeExit(e.exitCode)
	}
	var alerts []components.InspectorAlert
	for _, label := range commands {
		if r := last[label]; r.broken {
			alerts = append(alerts, components.InspectorAlert{Label: label, Note: r.note, Turn: r.turn})
		}
	}
	return alerts
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
// same thresholds the status bar and the trim warnings use, plus the
// per-round burn behind the sparkline. A session with fewer than two
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
// children. The session figure is the ledger's — the agent's turns, the
// permission classifier, the session summary and every child, each priced
// against the model that actually answered it — so the rail's bottom line is
// the whole bill rather than the part of it the main agent ran up.
func (m Model) inspectorSpend() *components.InspectorSpend {
	total := m.sessionSpend()
	children := m.childSpend()
	if total.In == 0 && total.Out == 0 && children.In == 0 && children.Out == 0 {
		return nil
	}
	s := components.InspectorSpend{
		Turn:    m.spendLabel(m.turnTokensIn, m.turnTokensOut),
		Main:    m.spendLabel(m.TotalTokensIn, m.TotalTokensOut),
		Session: m.totalsLabel(total),
		Model:   m.modelName,
	}
	if children.In != 0 || children.Out != 0 {
		s.Children = m.totalsLabel(children)
	}
	return &s
}

// childSpend is what every sub-agent has cost. The ledger is the answer where
// there is one: it prices each child against the model that child ran on,
// which a fan-out across several models makes the only defensible figure. A
// session with no ledger falls back to the supervisor's own token counts.
func (m Model) childSpend() meter.Totals {
	if m.ledger != nil {
		return m.ledger.SourceTotal(meter.SourceSubagent)
	}
	var t meter.Totals
	if m.subagents != nil {
		for _, st := range m.subagents.Snapshot() {
			t.In += st.TokensIn
			t.Out += st.TokensOut
		}
	}
	return t
}

// totalsLabel formats a ledger roll-up: the cost it was priced at, or a token
// count where the pricing table knew none of the models involved.
func (m Model) totalsLabel(t meter.Totals) string {
	if t.In == 0 && t.Out == 0 {
		return ""
	}
	if t.Priced {
		return formatCost(t.Cost)
	}
	return m.spendLabel(t.In, t.Out)
}

// inspectorStatus is the /stats-adjacent line describing the split, used by
// /ui to say what the current layout is.
func (m Model) inspectorStatus() string {
	if m.twoPane() {
		return fmt.Sprintf("two panes — %d-column transcript + %d-column inspector rail",
			m.paneWidth(), components.InspectorWidth)
	}
	return fmt.Sprintf("one pane — %d columns", m.contentWidth())
}
