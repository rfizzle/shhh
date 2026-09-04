package chat

// Two-pane cockpit (docs/interface/surfaces.md#the-inspector-rail). At
// or above 130 content columns the surface splits: the transcript keeps the
// left pane, the inspector rail takes the right — 46 columns at the rung and
// wider with the terminal — and one dim │ column divides them. Below 130 the
// rail is dropped entirely and the single-pane layout is exactly what it was.
//
// The split is horizontal only — it is one of the two constraints the
// column half of the layout model resolves (layout.go), and the row
// budget it hands out is unchanged — and the prompt frame spans both panes,
// because steering is a session-level act. Takeover surfaces
// (approval cards, pickers, the full-screen diff, the agent list) span the
// full width and hide the rail, restoring it when they are dismissed. An
// attached child's session is not a takeover: the rail stays, and its AGENTS
// block marks the row the keyboard is in.
//
// Everything the rail shows is already known to the session; the rail is a
// passive renderer fed from here, like components.Cockpit. It is also
// somewhere to go from: its rows carry what they name, so a click on a
// changed file opens that file's diff and a click on a session attaches to it
// (railclick.go).
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
	// widest rail's sparkline can draw. It is the ceiling rather than the
	// current width because the series outlives any one terminal size: a
	// window dragged wider must not find that the rounds it could now show
	// were thrown away while it was narrow.
	contextBurnSamples = components.SparkCellsRailMax
)

// paneStyles is the two-pane cockpit's own group.
type paneStyles struct {
	Divider lipgloss.Style
}

func newPaneStyles(p components.ColorTokens) paneStyles {
	return paneStyles{Divider: lipgloss.NewStyle().Foreground(p.Dim.Color())}
}

// twoPane reports whether the surface is split. Width is the first condition;
// a takeover surface is the second.
func (m Model) twoPane() bool { return m.columns().inspector.Dx() > 0 }

// inspectorHidden reports whether something is covering the rail. Takeover
// surfaces span both panes.
//
// An attached child is not one of them, and the obvious argument that it
// should be — the rail's numbers are the parent's — is the reason it is not.
// The changeset, the context and the bill are the session's whichever session
// the keyboard is in, so hiding all three to read one child's transcript
// costs every standing question the rail exists to answer and settles none.
// What keeps that honest is the map: it marks the row holding the keyboard,
// so the transcript on the left is visibly one child's and the numbers on the
// right are visibly the session's
// (docs/interface/surfaces.md#the-inspector-rail).
func (m Model) inspectorHidden() bool {
	if m.agentList != nil {
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
	case stateConfirmRun, statePlanApprove, stateRewindPick, statePick, stateTodoPropose, statePasteDrop, stateScaffold, statePersona, stateTodoPause, stateDiffFull, stateOutputFull, stateReview, stateContext, stateModelList:
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
		Tools:   m.inspectorTools(),
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
	if m.planRun == nil || len(steps) == 0 || !m.codingSurfaces() {
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
	if !m.codingSurfaces() {
		return nil
	}
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
			Mode:     f.ModeChange,
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

// inspectorAgents is the session map: this session first, then every child in
// spawn order — running, waiting or finished. Listing only what is in flight
// answers "what is running now" and leaves "what has this run done" to a
// surface somebody has to open, which is the interrogation the rail exists to
// end. Carrying every session is also what makes the rail usable while the
// keyboard is in a child: the row it is in is marked, so the blocks under it
// are visibly the session's rather than that child's.
//
// The order is the supervisor's own, which is spawn order, and it is the same
// order the cycle walks (attach.go), so moving one row on the keyboard moves
// one row on screen.
func (m Model) inspectorAgents() []components.InspectorAgent {
	if m.subagents == nil {
		return nil
	}
	snapshot := m.subagents.Snapshot()
	if len(snapshot) == 0 {
		return nil
	}
	agents := []components.InspectorAgent{m.orchestratorAgent()}
	for _, st := range snapshot {
		// Every surface that draws a child reads it through the same
		// progress struct — the fan-out lane, the manager's row and this —
		// so what the rail says about a child cannot drift from what the
		// transcript beside it says about the same child.
		p := m.childProgress(st)
		a := components.InspectorAgent{
			Name:    st.Name,
			Detail:  st.Detail,
			Spend:   p.Spend,
			Tools:   p.Tools,
			Step:    p.Step,
			Steps:   p.Steps,
			State:   p.State,
			Focused: st.Name == m.attachedTo,
		}
		switch st.State {
		case subagent.StateDone, subagent.StateFailed:
			// The supervisor's own word for how it ended, because the
			// supervisor is the only thing that knows; the line under it
			// says what it found or why it broke, rather than repeating the
			// word with a tool count on it.
			a.Outcome, a.Detail = st.State.String(), childNote(st)
		}
		agents = append(agents, a)
	}
	return agents
}

// orchestratorAgent is the map's first row: this session itself. Its state is
// what the session is doing rather than a lifecycle — nothing spawned the
// orchestrator and nothing collects it — so it is running while a turn is,
// waiting while a decision stands in front of you, and idle otherwise. That
// is the same three answers the manager's row gives in words, and it is built
// from that row so the two cannot come to disagree.
func (m Model) orchestratorAgent() components.InspectorAgent {
	row := m.orchestratorRow()
	a := components.InspectorAgent{
		Name:    row.Name,
		Detail:  row.Status,
		Spend:   row.Spend,
		Self:    true,
		Focused: m.attachedTo == "",
		State:   components.FanoutIdle,
	}
	switch {
	case m.working():
		a.State = components.FanoutRunning
	case m.state == stateConfirmRun || m.state == statePlanApprove:
		a.State = components.FanoutBlocked
	}
	return a
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
		Corrected: b.Corrected,
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

// WithRailWidth fixes the inspector rail's column count for this session.
// Zero — an unset key, or `/ui rail auto` — leaves it to the width ladder.
func (m Model) WithRailWidth(cols int) Model {
	m.railCols = cols
	return m
}

// inspectorStatus is the /stats-adjacent line describing the split, used by
// /ui to say what the current layout is. It names the rail's width as it
// resolved, not as it was asked for: a number the ladder would not allow at
// this terminal is the whole reason a person reads this line.
func (m Model) inspectorStatus() string {
	if m.twoPane() {
		return fmt.Sprintf("two panes — %d-column transcript + %d-column inspector rail (%s)",
			m.paneWidth(), m.columns().inspector.Dx(), m.railSource())
	}
	return fmt.Sprintf("one pane — %d columns", m.contentWidth())
}

// railSource says where the rail's width came from, in the parenthesis the
// readout ends with: the ladder, the session's own setting, or a setting one
// of the two limits moved. The limits are named apart because they are
// different answers to "why is this not the number I typed" — one of them
// goes away on a wider terminal and the other never does — and a reader who
// typed a number and got a different one is owed which.
func (m Model) railSource() string {
	if m.railCols <= 0 {
		return "auto"
	}
	switch got := m.columns().inspector.Dx(); {
	case got > m.railCols:
		return fmt.Sprintf("set to %d, widened to the narrowest rail there is", m.railCols)
	case got < m.railCols:
		return fmt.Sprintf("set to %d, as wide as this terminal allows", m.railCols)
	}
	return "set"
}

// railCommand handles /ui rail: how many columns the inspector rail takes.
// `auto` hands it back to the width ladder.
func (m *Model) railCommand(parts []string) string {
	if len(parts) == 2 {
		return fmt.Sprintf("Layout: %s.\n%s", m.inspectorStatus(), railUsage)
	}
	if len(parts) != 3 {
		return railUsage
	}
	cols, err := components.ParseRailWidth(parts[2])
	if err != nil {
		return "Error: " + err.Error()
	}
	m.railCols = cols
	m.invalidateRenderCache()
	// A rail that changed width is a transcript that changed width, and
	// nothing else on this path resizes the viewport: without this the feed
	// stays wrapped to the old pane until the next terminal resize, which
	// reads as a command that half worked.
	m.syncViewport()
	if m.contentWidth() < components.InspectorMinContentWidth {
		// Nothing on screen changes at this width, so the reply has to carry
		// the whole answer: the setting took, and the rung is why it is not
		// visible.
		return fmt.Sprintf("Inspector rail %s — this terminal is too narrow to split, so nothing changes until it is %d columns wide.",
			railSetting(cols), components.InspectorMinContentWidth+horizontalPadding*2)
	}
	return fmt.Sprintf("Inspector rail %s — %s.", railSetting(cols), m.inspectorStatus())
}

// railSetting is the setting in the words the reply leads with.
func railSetting(cols int) string {
	if cols <= 0 {
		return "back on the width ladder"
	}
	return fmt.Sprintf("set to %d columns", cols)
}

// railUsage is the one line /ui rail answers with, on its own and when the
// value is not one it takes.
const railUsage = "Usage: /ui rail <auto|columns> — auto widens the rail with the terminal; a number fixes it, held to what the terminal has room for."
