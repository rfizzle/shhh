package chat

// Sub-agent orchestration surface: the parent session renders child
// activity as compact progress rows, routes detached children's approval
// requests through the same approval-card surface (labeled with the agent
// name), and cancels the whole child tree with the turn.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// subagentEventMsg carries one supervisor notification into the Update loop.
type subagentEventMsg struct{ ev subagent.Event }

// WithSubagents wires the sub-agent supervisor; the model listens for
// its events and keeps its parent-mode ceiling current.
func (m Model) WithSubagents(sup *subagent.Supervisor) Model {
	m.subagents = sup
	m.childViews = map[string]*childView{}
	sup.SetParentMode(m.mode)
	sup.SetParentGrants(m.grants())
	return m
}

// syncChildGrants pushes the session's [a] grants down to the supervisor: a
// category the user waved through for the session is waved through for
// children too, instead of being re-asked once per agent.
func (m *Model) syncChildGrants() {
	if m.subagents != nil {
		m.subagents.SetParentGrants(m.grants())
	}
}

// applyMode changes the session's permission mode, keeping the sub-agent
// ceiling in sync (children are never more permissive than the parent).
func (m *Model) applyMode(mode agent.Mode) {
	if mode != m.mode {
		m.signal(signalMode, mode.String())
	}
	m.mode = mode
	if m.subagents != nil {
		m.subagents.SetParentMode(mode)
	}
}

// listenSubagents waits for the next supervisor event.
func listenSubagents(ch <-chan subagent.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return subagentEventMsg{ev: ev}
	}
}

// handleSubagentEvent processes one supervisor event and re-arms the
// listener.
func (m Model) handleSubagentEvent(ev subagent.Event) (tea.Model, tea.Cmd) {
	switch ev.Kind {
	case subagent.EventAsk:
		// A patch from one of the backlog run's writers is the run's to
		// take; it never reaches a card.
		if next, cmd, ok := m.todoLaneAsk(ev.Ask); ok {
			nm := next.(Model)
			return nm, tea.Batch(cmd, listenSubagents(nm.subagents.Events()))
		}
		m.childAsks = append(m.childAsks, ev.Ask)
		// A routed approval arrives the way every other decision does: on
		// screen, and holding the keyboard only if there is no sentence for
		// its letters to belong to. It arms itself because it is
		// a queue rather than a turn state, so setTurnState never sees it.
		m.armArrival()
	case subagent.EventDone:
		// A finished child can no longer act on its asks.
		m.purgeChildAsks(ev.Status.Name)
		m.signal(signalSubagent, ev.Status.State.String())
		m.appendEntry(entry{kind: entrySystem, text: fmt.Sprintf("Agent %s: %s", ev.Status.Name, ev.Status.Detail)})
		// A reviewer the backlog runner spawned answers its review stage.
		if next, cmd, ok := m.todoReviewDone(ev.Status); ok {
			nm := next.(Model)
			return nm, tea.Batch(cmd, listenSubagents(nm.subagents.Events()))
		}
		// A writer building one of its lanes answers the fan-out stage.
		if next, cmd, ok := m.todoWriterDone(ev.Status); ok {
			nm := next.(Model)
			return nm, tea.Batch(cmd, listenSubagents(nm.subagents.Events()))
		}
	case subagent.EventPatch:
		m.recordChildPatch(ev.Patch)
		m.todoLanePatched(ev.Patch)
	}
	m.syncViewport()
	m.viewport.SetLines(m.renderHistoryLines())
	if m.atBottom {
		m.viewport.GotoBottom()
	}
	return m, listenSubagents(m.subagents.Events())
}

// recordChildPatch files a child's applied patch in the session changeset
// . A child edits inside its own worktree, so the patch landing on the
// real checkout is the moment this session changed — and the record says
// which agent's work it was.
func (m *Model) recordChildPatch(p *subagent.PatchApplied) {
	if p == nil {
		return
	}
	var evicted []int64
	for _, f := range p.Files {
		evicted = append(evicted, m.changes.Add(m.turnCount, changeset.Record{
			Path:         f.Path,
			Before:       f.Before,
			After:        f.After,
			BeforeExists: f.BeforeExists,
			AfterExists:  f.AfterExists,
			Agent:        p.Agent,
			Origin:       changeset.ChildPatch,
			Track:        m.tracker.Track(f.Path),
		})...)
	}
	m.noteEvictedTurns(evicted)
}

// activeChildAsk is the routed approval currently presentable: deferred
// while the parent's own prompts, a surface (focus mode, a full-screen diff,
// a picker), or the agent list hold the bottom panel; attached, only the
// focused child's asks render in place — the rest stay visible via
// the badge and agent list.
func (m Model) activeChildAsk() *subagent.Ask {
	if len(m.childAsks) == 0 {
		return nil
	}
	if m.state.isSurface() {
		return nil
	}
	switch m.state {
	case stateConfirmRun, statePlanApprove:
		return nil
	}
	if m.agentList != nil {
		return nil
	}
	if m.attachedTo != "" {
		for _, ask := range m.childAsks {
			if ask.Agent == m.attachedTo {
				return ask
			}
		}
		return nil
	}
	return m.childAsks[0]
}

// updateChildAsk routes keys to the presented child approval card. Its esc/n
// path declines — a routed request is never silently dropped or auto-denied.
// Detached, [g] jumps into the agent's attached view instead of answering
// (docs/interface/surfaces.md#the-agent-manager).
func (m Model) updateChildAsk(msg tea.KeyPressMsg, ask *subagent.Ask) (tea.Model, tea.Cmd) {
	if keys.Match(msg, keys.Draft.Quit) {
		m.quitting = true
		m.cancelSubagents()
		if m.cancel != nil {
			m.cancel()
		}
		if m.runCancel != nil {
			m.runCancel()
		}
		return m, m.quitCmd()
	}
	// [g] is a bare letter, so it belongs to the card only while the card was
	// handed the keyboard. One holding it by arrival claims nothing but its
	// answers, and "go ahead, but…" is a sentence.
	if msg.String() == "g" && !m.heldOnArrival && m.attachedTo != ask.Agent {
		m.attach(ask.Agent)
		return m, nil
	}
	// The manager is reachable from a routed approval too: the card
	// steps aside while the list is open and comes back when it closes.
	if keys.Match(msg, keys.Draft.Agents) {
		return m.openAgentList()
	}
	done, result := m.childAskCard(ask).Update(msg)
	if !done {
		return m, nil
	}
	if result == components.ApprovalRelease {
		// The card had the keyboard by arrival and this key is not one of its
		// answers: it is the start of a sentence, and the ask stays queued.
		return m.releaseToDraft(msg)
	}
	for i, queued := range m.childAsks {
		if queued == ask {
			m.childAsks = append(m.childAsks[:i], m.childAsks[i+1:]...)
			break
		}
	}
	// The keyboard goes straight back to the draft, at the same character —
	// unless another ask was already queued behind this one: that is the
	// queue advancing, so the next card arms the way any arrival does, with
	// releaseDecision's stamp keeping the grace window shut
	// (docs/interface/surfaces.md#the-approval-card).
	m.releaseDecision()
	m.armArrival()
	approved := result == components.ApprovalApprove
	ask.Respond(approved)
	verdict := "Declined"
	if approved {
		verdict = "Approved"
	}
	m.appendEntry(entry{kind: entrySystem, text: verdict + " " + ask.Agent + " ▸ " + ask.Title})
	m.syncViewport()
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m, nil
}

// childAskCard builds the approval card for a routed child request, title
// prefixed with the agent name
// (docs/interface/surfaces.md#the-agent-manager). Attached to that agent, the
// prefix drops (the breadcrumb already names it) — detached, [g] offers the
// jump into its view.
func (m Model) childAskCard(ask *subagent.Ask) *components.ApprovalCard {
	card := &components.ApprovalCard{}
	defer m.applyNotYetLive(card)
	prefix := ask.Agent + " ▸ "
	if m.attachedTo == ask.Agent {
		prefix = ""
	} else {
		card.ExtraHints = []string{"g: attach to " + ask.Agent, keys.Shown(keys.Draft.Agents) + ": agents"}
	}
	switch ask.Kind {
	case subagent.AskCommand:
		card.Variant = components.ApprovalCommand
		card.Title = prefix + "Approve command"
		card.Headline = ask.Agent + " wants to " + ask.Title
		card.Question = "Run this command?"
		if len(ask.Warnings) > 0 {
			card.Warnings = []string{strings.Join(ask.Warnings, "; ")}
		}
	case subagent.AskEdit:
		card.Variant = components.ApprovalEdit
		card.Title = prefix + "Approve edit"
		card.Headline = ask.Agent + " wants to " + ask.Title
		card.Hunks = ask.Hunks
		card.Question = "Apply this change in the agent's workspace?"
	case subagent.AskPatch:
		card.Variant = components.ApprovalEdit
		card.Title = prefix + "Apply patch"
		card.Headline = ask.Agent + " finished and wants to " + ask.Title
		card.Hunks = ask.Hunks
		card.Question = "Apply the agent's patch to your workspace?"
		// A patch over files another agent already changed is the one case
		// where two isolated writers can still collide.
		if len(ask.Warnings) > 0 {
			card.Warnings = []string{strings.Join(ask.Warnings, "; ")}
		}
	default:
		card.Variant = components.ApprovalGeneric
		card.Title = prefix + "Approve tool"
		card.Headline = ask.Agent + " wants to " + ask.Title
		card.Summary = firstLine(ask.Summary)
		card.Question = "Allow this?"
	}
	return card
}

// childAskLines renders the presented child approval card, one row per line.
func (m Model) childAskLines(ask *subagent.Ask) []string {
	return strings.Split(m.childAskCard(ask).View(m.contentWidth()), "\n")
}

// childAskPanelLines is the routed card plus the rail that names the
// keyboard's owner and the draft it is holding while it does.
func (m Model) childAskPanelLines(ask *subagent.Ask) []string {
	return m.dressDecision(m.childAskLines(ask), m.contentWidth())
}

// renderChildAsk pads the card to the bottom panel height, like the parent's
// own confirm prompt.
func (m Model) renderChildAsk(ask *subagent.Ask) string {
	lines := m.childAskPanelLines(ask)
	h := m.bottomPanelHeight()
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines[:h], "\n")
}

// cancelSubagents cancels the whole child tree (Ctrl+C / quit semantics,
// blocked approval waits unblock, children finish as cancelled with
// well-formed conversations, and queued asks are dropped as declined.
func (m *Model) cancelSubagents() {
	if m.subagents == nil {
		return
	}
	m.subagents.CancelAll()
	for _, ask := range m.childAsks {
		ask.Respond(false)
	}
	m.childAsks = nil
}

// maxAgentRows bounds how many progress rows the panel occupies.
const maxAgentRows = 6

// activeAgentStatuses are the children still working (queued, running, or
// blocked); finished ones live on as transcript entries instead.
func (m Model) activeAgentStatuses() []subagent.Status {
	if m.subagents == nil {
		return nil
	}
	var out []subagent.Status
	for _, st := range m.subagents.Snapshot() {
		switch st.State {
		case subagent.StateQueued, subagent.StateRunning, subagent.StateBlocked:
			out = append(out, st)
		}
	}
	return out
}

// agentRowsHeight is how many lines the progress rows currently occupy; the
// rows hide while the agent list or an attached view covers them.
func (m Model) agentRowsHeight() int {
	if m.attachedTo != "" || m.agentList != nil {
		return 0
	}
	n := len(m.activeAgentStatuses())
	if n == 0 {
		return 0
	}
	if n > maxAgentRows {
		return maxAgentRows + 1
	}
	return n
}

// renderAgentRows renders one compact row per working child: state glyph,
// name, task, live status, and spend.
func (m Model) renderAgentRows(width int) string {
	statuses := m.activeAgentStatuses()
	if len(statuses) == 0 {
		return ""
	}
	overflow := 0
	if len(statuses) > maxAgentRows {
		overflow = len(statuses) - maxAgentRows
		statuses = statuses[:maxAgentRows]
	}
	var rows []string
	for _, st := range statuses {
		glyph := sty.Tool.Render("◇")
		detail := sty.StatusBar.Render(st.Detail)
		if st.State == subagent.StateBlocked {
			glyph = sty.Error.Render("⚠")
			detail = sty.Error.Render(st.Detail)
		}
		left := glyph + " " + st.Name + "  " + sty.ToolArgs.Render(clipText(firstLine(st.Task), max(width/3, 8)))
		right := detail
		if spend := st.TokensIn + st.TokensOut; spend > 0 {
			right += "  " + sty.StatusBar.Render("~"+formatTokenCount(spend)+" tok")
		}
		rows = append(rows, joinRow(left, right, width))
	}
	if overflow > 0 {
		rows = append(rows, sty.ToolArgs.Render(fmt.Sprintf("… +%d more agents", overflow)))
	}
	return strings.Join(rows, "\n")
}

// joinRow left-aligns left and right within width, clipping left when needed.
func joinRow(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap >= 2 {
		return left + strings.Repeat(" ", gap) + right
	}
	return left + "  " + right
}

func clipText(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return "…"
	}
	return s[:maxLen-1] + "…"
}
