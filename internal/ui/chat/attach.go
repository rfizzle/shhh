package chat

// Sub-agent management and steering (S-077, DESIGN-TUI.md §9): the agent
// list (`/agents` / ctrl+a) is a live view of every agent with cancel and
// kill actions, and attaching renders a child's session on the full chat
// surface — same components, breadcrumb header, steering input, approval
// cards in place, and mode changes clamped to the orchestrator's ceiling.
// Attach is a focus switch, not a second UI: the transcript the surface
// renders is whichever agent is focused, and esc pops one lineage level.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// viewState is one surface's saved scroll position.
type viewState struct {
	yoffset  int
	atBottom bool
}

// childView is the model-side state for one child's surface: its mirrored
// transcript (carrying local expansion flags the supervisor doesn't know
// about) and its scroll position, so attach/detach loses nothing.
type childView struct {
	entries []entry
	scroll  viewState
}

// entries returns the transcript the surface currently renders: the attached
// child's mirrored entries, or the orchestrator's own.
func (m *Model) entries() *[]entry {
	if m.attachedTo != "" && m.subagents != nil {
		return &m.syncChildView(m.attachedTo).entries
	}
	return &m.transcript
}

// syncChildView mirrors the supervisor's transcript for name into the child
// view, preserving per-row expansion state (entries are append-only and
// index-stable; pending tool rows settle in place).
func (m *Model) syncChildView(name string) *childView {
	cv := m.childViews[name]
	if cv == nil {
		cv = &childView{scroll: viewState{atBottom: true}}
		m.childViews[name] = cv
	}
	for i, te := range m.subagents.Transcript(name) {
		e := convertChildEntry(te)
		if i < len(cv.entries) {
			e.expanded = cv.entries[i].expanded
			cv.entries[i] = e
		} else {
			cv.entries = append(cv.entries, e)
		}
	}
	return cv
}

// convertChildEntry maps a supervisor transcript entry onto the chat entry
// the shared renderers understand.
func convertChildEntry(te subagent.TranscriptEntry) entry {
	switch te.Kind {
	case subagent.EntryUser:
		return entry{kind: entryUser, text: te.Text}
	case subagent.EntryAssistant:
		return entry{kind: entryAssistant, text: te.Text}
	case subagent.EntryTool:
		result := te.Result
		if te.Pending {
			result = pendingToolResult
		}
		return entry{kind: entryTool, toolName: te.Tool, toolArgs: te.Args, toolResult: result}
	default:
		return entry{kind: entrySystem, text: te.Text}
	}
}

// renderAttachedHistory renders the focused child's transcript plus its
// in-flight assistant text.
func (m *Model) renderAttachedHistory() string {
	cv := m.syncChildView(m.attachedTo)
	w := m.transcriptWidth()
	var b strings.Builder
	// A child's transcript groups into steps like the parent's (S-090).
	body, prev, havePrev := joinUnits(m.transcriptUnits(cv.entries, w, false, -1), entry{}, false)
	b.WriteString(body)
	if s := m.subagents.StreamingText(m.attachedTo); s != "" {
		if havePrev {
			b.WriteString(separatorBefore(prev, entry{kind: entryAssistant}))
		}
		b.WriteString(assistantStyle.Render("Assistant") + "\n" + renderMarkdown(s, w))
	}
	if b.Len() == 0 {
		return welcomeStyle.Render("No activity from this agent yet.")
	}
	return b.String()
}

// breadcrumb is the attached header path, e.g. "orchestrator ▸ writer-1"
// (nesting one segment per lineage level).
func (m Model) breadcrumb() string {
	var parts []string
	for n := m.attachedTo; n != ""; {
		parts = append([]string{n}, parts...)
		p, ok := m.subagents.Parent(n)
		if !ok {
			break
		}
		n = p
	}
	return strings.Join(append([]string{"orchestrator"}, parts...), " ▸ ")
}

// saveScroll stores the current surface's scroll position before a focus
// switch.
func (m *Model) saveScroll() {
	vs := viewState{yoffset: m.viewport.YOffset, atBottom: m.atBottom}
	if m.attachedTo == "" {
		m.parentView = vs
	} else if cv := m.childViews[m.attachedTo]; cv != nil {
		cv.scroll = vs
	}
}

// restoreScroll re-applies the newly focused surface's scroll position.
func (m *Model) restoreScroll() {
	vs := viewState{atBottom: true}
	if m.attachedTo == "" {
		vs = m.parentView
	} else if cv := m.childViews[m.attachedTo]; cv != nil {
		vs = cv.scroll
	}
	if vs.atBottom {
		m.viewport.GotoBottom()
	} else {
		m.viewport.SetYOffset(vs.yoffset)
	}
	m.atBottom = m.viewport.AtBottom()
}

// attach focuses the surface on name ("" refocuses the orchestrator),
// closing the agent list if it was open.
func (m *Model) attach(name string) {
	m.saveScroll()
	m.attachedTo = name
	m.agentList = nil
	m.killConfirm = nil
	m.killTarget = ""
	m.answerAgent = ""
	// The prompt gutter shows the child's name while attached (S-082), so the
	// textarea re-fits around it.
	m.syncInputWidth()
	m.syncViewport()
	m.viewport.SetContent(m.renderHistory())
	m.restoreScroll()
}

// detachOne pops one breadcrumb level: back to the child's spawner, or the
// orchestrator at the top.
func (m *Model) detachOne() {
	if m.attachedTo == "" {
		return
	}
	parent, _ := m.subagents.Parent(m.attachedTo)
	m.attach(parent)
}

// noteChild appends a front-end note to the focused child's transcript (so
// it survives attach/detach); it falls back to the parent transcript when
// the agent is unknown.
func (m *Model) noteChild(name, text string) {
	if err := m.subagents.Note(name, subagent.TranscriptEntry{Kind: subagent.EntrySystem, Text: text}); err != nil {
		m.appendEntry(entry{kind: entrySystem, text: text})
	}
}

// purgeChildAsks declines and removes every queued ask from one agent (its
// turn was cancelled or it was killed — the requests are moot, never parked).
func (m *Model) purgeChildAsks(name string) {
	if m.answerAgent == name {
		m.answerAgent = ""
	}
	kept := m.childAsks[:0]
	for _, a := range m.childAsks {
		if a.Agent == name {
			a.Respond(false)
			continue
		}
		kept = append(kept, a)
	}
	m.childAsks = kept
}

// --- agent list (§9a) ---

// openAgentList shows the agent manager in the bottom panel.
func (m Model) openAgentList() (tea.Model, tea.Cmd) {
	if m.subagents == nil {
		m.appendEntry(entry{kind: entrySystem, text: "Sub-agents are unavailable in this session."})
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
		return m, nil
	}
	switch m.state {
	case stateConfirmRun, statePlanApprove, stateFocus:
		return m, nil
	}
	if m.agentList != nil {
		return m, nil
	}
	m.agentList = &components.AgentList{MaxLines: m.maxConfirmPanelHeight()}
	rows, _ := m.buildAgentRows()
	m.agentList.Rows = rows
	m.syncViewport()
	return m, nil
}

// buildAgentRows assembles the live rows — orchestrator first, then
// blocked-on-approval children, then the rest in spawn order — and the
// parallel agent-name index ("" is the orchestrator).
func (m Model) buildAgentRows() ([]components.AgentRow, []string) {
	rows := []components.AgentRow{m.orchestratorRow()}
	names := []string{""}
	var blocked, rest []subagent.Status
	for _, st := range m.subagents.Snapshot() {
		if st.State == subagent.StateBlocked {
			blocked = append(blocked, st)
		} else {
			rest = append(rest, st)
		}
	}
	for _, st := range append(blocked, rest...) {
		// The row draws the child's progress through the fan-out lane's
		// renderer, so the manager and the transcript say the same thing
		// about the same child (S-111).
		progress := m.childProgress(st)
		row := components.AgentRow{
			Name:     st.Name,
			Task:     firstLine(st.Task),
			Status:   st.Detail,
			Progress: &progress,
			Note:     childNote(st),
			// A blocked child can be answered here only while its request is
			// still queued; a failed one can be run again on its task.
			Answerable: st.State == subagent.StateBlocked && m.pendingAskFor(st.Name) != nil,
			Retryable:  st.State == subagent.StateFailed,
		}
		switch {
		case st.Name == m.attachedTo:
			row.State = components.AgentCurrent
		case st.State == subagent.StateBlocked:
			row.State = components.AgentBlocked
		case st.State == subagent.StateDone:
			row.State = components.AgentDone
		case st.State == subagent.StateFailed:
			row.State = components.AgentFailed
		default:
			row.State = components.AgentRunning
		}
		rows = append(rows, row)
		names = append(names, st.Name)
	}
	return rows, names
}

// pendingAskFor is the approval this agent is waiting on, if the session
// still holds it.
func (m Model) pendingAskFor(name string) *subagent.Ask {
	for _, ask := range m.childAsks {
		if ask.Agent == name {
			return ask
		}
	}
	return nil
}

func (m Model) orchestratorRow() components.AgentRow {
	state := components.AgentRunning
	if m.attachedTo == "" {
		state = components.AgentCurrent
	}
	status := "ready"
	switch m.state {
	case stateStreaming:
		status = "streaming…"
	case stateRunningCmd:
		status = "running…"
	case stateClassifying:
		status = "checking permission…"
	case stateConfirmRun, statePlanApprove:
		status = "waiting on you"
	}
	if r := m.agent.Rounds(); r > 0 && m.state != stateInput {
		status = fmt.Sprintf("round %d · %s", r, status)
	}
	return components.AgentRow{
		State:  state,
		Name:   "orchestrator",
		Status: status,
		Spend:  m.spendLabel(m.TotalTokensIn, m.TotalTokensOut),
	}
}

// spendLabel formats an agent's spend: dollars when the pricing table knows
// the model, a token count otherwise, empty before any usage.
func (m Model) spendLabel(in, out int64) string {
	if in == 0 && out == 0 {
		return ""
	}
	if m.prices != nil && m.modelName != "" {
		if inCost, outCost, found := m.prices.Cost(m.modelName, in, out); found {
			return formatCost(inCost + outCost)
		}
	}
	return "~" + formatTokenCount(in+out) + " tok"
}

// agentListLines renders the live agent list (plus the inline kill confirm
// when armed), one row per line. While a row's approval is being answered the
// card takes the panel instead — the list is what it returns to, so the two
// never render at once (S-111).
func (m Model) agentListLines() []string {
	if ask := m.listAnswerAsk(); ask != nil {
		return strings.Split(m.listAnswerCard(ask).View(m.contentWidth()), "\n")
	}
	rows, _ := m.buildAgentRows()
	m.agentList.Rows = rows
	m.agentList.MaxLines = m.maxConfirmPanelHeight()
	if m.agentList.Focus >= len(rows) {
		m.agentList.Focus = max(len(rows)-1, 0)
	}
	lines := strings.Split(m.agentList.View(m.contentWidth()), "\n")
	if m.killConfirm != nil {
		lines = append(lines, m.killConfirm.View(m.contentWidth()))
	}
	return lines
}

// renderAgentList pads the list to the bottom panel height.
func (m Model) renderAgentList() string {
	lines := m.agentListLines()
	h := m.bottomPanelHeight()
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines[:h], "\n")
}

// updateAgentList routes keys while the agent list is open: enter attaches,
// x cancels the focused agent's turn, X arms the inline kill confirm, esc
// dismisses the list.
func (m Model) updateAgentList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+d" {
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
	// An answer in progress owns the keys: the card is over the list, and
	// answering it (either way) hands the list back.
	if ask := m.listAnswerAsk(); ask != nil {
		return m.updateListAnswer(msg, ask)
	}
	if m.killConfirm != nil {
		done, result := m.killConfirm.Update(msg)
		if !done {
			return m, nil
		}
		target := m.killTarget
		m.killConfirm = nil
		m.killTarget = ""
		if confirmed, _ := result.(bool); confirmed {
			if err := m.subagents.Kill(target); err != nil {
				m.noteChild(target, err.Error())
			} else {
				m.purgeChildAsks(target)
			}
		}
		m.syncViewport()
		return m, nil
	}

	rows, names := m.buildAgentRows()
	m.agentList.Rows = rows
	if m.agentList.Focus >= len(rows) {
		m.agentList.Focus = max(len(rows)-1, 0)
	}
	done, result := m.agentList.Update(msg)
	res, ok := result.(components.AgentListResult)
	if !ok {
		return m, nil
	}
	if done && res.Action == components.AgentBack {
		m.agentList = nil
		m.answerAgent = ""
		m.syncViewport()
		return m, nil
	}
	if res.Index < 0 || res.Index >= len(names) {
		return m, nil
	}
	name := names[res.Index]
	switch res.Action {
	case components.AgentAttach:
		if name == m.attachedTo {
			m.agentList = nil
			m.syncViewport()
			return m, nil
		}
		m.attach(name)
		return m, nil
	case components.AgentCancel:
		if name == "" {
			// The orchestrator's turn: same semantics as Ctrl+C.
			if m.state == stateStreaming {
				m.cancelStreaming()
				m.viewport.SetContent(m.renderHistory())
				m.viewport.GotoBottom()
				return m, m.autosaveCmd()
			}
			return m, nil
		}
		if err := m.subagents.CancelTurn(name); err != nil {
			m.noteChild(name, err.Error())
		} else {
			m.purgeChildAsks(name)
		}
		return m, nil
	case components.AgentAnswer:
		// The card renders over the list and comes back to it (§9a): opening
		// the manager because something needs you should not then send you
		// into that child's session to say yes.
		if name == "" || m.pendingAskFor(name) == nil {
			return m, nil
		}
		m.answerAgent = name
		m.syncViewport()
		return m, nil
	case components.AgentRetry:
		if name == "" {
			return m, nil // the orchestrator's turn is re-run by asking again
		}
		if err := m.subagents.Retry(name); err != nil {
			m.noteChild(name, err.Error())
		} else {
			m.appendEntry(entry{kind: entrySystem, text: "Retrying " + name + " on its original task."})
			m.viewport.SetContent(m.renderHistory())
			m.viewport.GotoBottom()
		}
		return m, nil
	case components.AgentKill:
		if name == "" {
			return m, nil // the orchestrator is quit with Ctrl+D, never killed from here
		}
		// The confirm states what survives as well as what does not: a kill
		// that only names its casualties reads as bigger than it is.
		m.killConfirm = &components.Confirm{Prompt: "Kill " + name +
			"? Its turn stops and its isolated workspace is discarded; its transcript stays and the other agents keep running."}
		m.killTarget = name
		m.syncViewport()
		return m, nil
	}
	return m, nil
}

// listAnswerAsk is the approval being answered from the list, if one is: the
// row's request, still queued. A request that resolved elsewhere (the agent
// was killed, its turn cancelled) takes the surface with it rather than
// leaving a card over nothing.
func (m Model) listAnswerAsk() *subagent.Ask {
	if m.agentList == nil || m.answerAgent == "" {
		return nil
	}
	return m.pendingAskFor(m.answerAgent)
}

// listAnswerCard is the routed approval card as it renders over the list.
// The hints drop [g] and [ctrl+a]: the manager is already what is underneath,
// and answering here is the whole point of being here.
func (m Model) listAnswerCard(ask *subagent.Ask) *components.ApprovalCard {
	card := m.childAskCard(ask)
	card.ExtraHints = []string{"esc: deny, back to the agents"}
	return card
}

// updateListAnswer routes keys to the card over the list. Either answer
// resolves the request and returns to the list; esc/n declines, because a
// routed request is never silently dropped.
func (m Model) updateListAnswer(msg tea.KeyMsg, ask *subagent.Ask) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+d" {
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
	done, result := m.listAnswerCard(ask).Update(msg)
	if !done {
		return m, nil
	}
	m.answerAgent = ""
	for i, queued := range m.childAsks {
		if queued == ask {
			m.childAsks = append(m.childAsks[:i], m.childAsks[i+1:]...)
			break
		}
	}
	approved := result == components.ApprovalApprove
	ask.Respond(approved)
	verdict := "Declined"
	if approved {
		verdict = "Approved"
	}
	m.appendEntry(entry{kind: entrySystem, text: verdict + " " + ask.Agent + " ▸ " + ask.Title})
	m.syncViewport()
	m.viewport.SetContent(m.renderHistory())
	m.viewport.GotoBottom()
	return m, nil
}

// --- attached-view interaction (§9b) ---

// attachedSubmit handles Enter while attached: scoped slash commands run
// against the child, anything else is queued mid-turn steering (S-058
// mechanics, applied to the child).
func (m Model) attachedSubmit() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return m, nil
	}
	m.recordInput(text)
	m.input.Reset()
	if parts := strings.Fields(text); strings.HasPrefix(parts[0], "/") && !strings.Contains(parts[0][1:], "/") {
		return m.attachedCommand(parts)
	}
	if err := m.subagents.Steer(m.attachedTo, text); err != nil {
		m.noteChild(m.attachedTo, "Cannot steer: "+err.Error())
	}
	m.viewport.SetContent(m.renderHistory())
	m.viewport.GotoBottom()
	m.atBottom = true
	return m, nil
}

// attachedCommand runs one child-scoped slash command.
func (m Model) attachedCommand(parts []string) (tea.Model, tea.Cmd) {
	name := m.attachedTo
	switch parts[0] {
	case "/exit":
		if err := m.subagents.Kill(name); err != nil {
			m.noteChild(name, err.Error())
			break
		}
		m.purgeChildAsks(name)
		m.detachOne()
		m.appendEntry(entry{kind: entrySystem, text: "Killed " + name + "."})
	case "/stats":
		m.noteChild(name, m.childStatsReport(name))
	case "/diff":
		m.attachedDiff(name)
	case "/mode":
		m.attachedModeCommand(parts)
	case "/agents":
		return m.openAgentList()
	case "/attach":
		// Hop straight to another agent without going through the list
		// (S-087); bare /attach opens it.
		return m.attachCommand(parts)
	case "/detach":
		m.detachOne()
	default:
		m.noteChild(name, "Commands while attached: /stats, /diff, /mode [name], /agents, /attach <name>, /detach, /exit (kill this agent). Plain text steers the agent; esc detaches.")
	}
	m.viewport.SetContent(m.renderHistory())
	m.viewport.GotoBottom()
	m.atBottom = true
	return m, nil
}

// attachedDiff notes the child's cumulative workspace diff (writers only).
func (m *Model) attachedDiff(name string) {
	patch, err := m.subagents.WorktreeDiff(name)
	if err != nil {
		m.noteChild(name, err.Error())
		return
	}
	if strings.TrimSpace(patch) == "" {
		m.noteChild(name, "No changes in the agent's workspace yet.")
		return
	}
	hunks, files := subagent.PatchHunks(patch)
	adds, dels := diff.Stats(hunks)
	_ = m.subagents.Note(name, subagent.TranscriptEntry{
		Kind:   subagent.EntryTool,
		Tool:   "diff",
		Args:   fmt.Sprintf(`{"agent":%q}`, name),
		Result: fmt.Sprintf("+%d −%d across %d file(s)\n%s", adds, dels, files, strings.TrimRight(patch, "\n")),
	})
}

// attachedModeCommand shows or sets the attached child's mode; modes above
// the orchestrator's ceiling are disabled, never silently clamped.
func (m *Model) attachedModeCommand(parts []string) {
	name := m.attachedTo
	if len(parts) < 2 {
		m.noteChild(name, m.childModeStatus(name))
		return
	}
	mode, err := agent.ParseMode(parts[1])
	if err != nil {
		m.noteChild(name, "Error: "+err.Error())
		return
	}
	ceiling := m.subagents.ParentMode()
	if agent.ClampMode(mode, ceiling) != mode {
		m.noteChild(name, fmt.Sprintf("Mode %s is disabled: it exceeds the orchestrator's ceiling (%s).", mode, ceiling))
		return
	}
	eff, setErr := m.subagents.SetAgentMode(name, mode)
	if setErr != nil {
		m.noteChild(name, "Error: "+setErr.Error())
		return
	}
	m.noteChild(name, fmt.Sprintf("Mode set to %s — %s.", eff, eff.Describe()))
}

// cycleAttachedMode is Shift+Tab while attached: the next mode in the cycle
// at or under the orchestrator's ceiling; skipped over-limit modes are named
// as disabled.
func (m Model) cycleAttachedMode() (tea.Model, tea.Cmd) {
	name := m.attachedTo
	cur, ok := m.subagents.AgentMode(name)
	if !ok {
		return m, nil
	}
	ceiling := m.subagents.ParentMode()
	cycle := m.modeCycle
	if len(cycle) == 0 {
		cycle = agent.DefaultCycle()
	}
	idx := 0
	for i, mode := range cycle {
		if mode == cur {
			idx = i
			break
		}
	}
	next := cur
	var disabled []string
	for step := 1; step <= len(cycle); step++ {
		cand := cycle[(idx+step)%len(cycle)]
		if agent.ClampMode(cand, ceiling) == cand {
			next = cand
			break
		}
		disabled = append(disabled, cand.String())
	}
	if len(disabled) > 0 {
		m.noteChild(name, fmt.Sprintf("Disabled (exceeds the orchestrator's ceiling %s): %s.", ceiling, strings.Join(disabled, ", ")))
	}
	if next != cur {
		if _, err := m.subagents.SetAgentMode(name, next); err == nil {
			m.noteChild(name, fmt.Sprintf("Mode set to %s — %s.", next, next.Describe()))
		}
	}
	m.viewport.SetContent(m.renderHistory())
	if m.atBottom {
		m.viewport.GotoBottom()
	}
	return m, nil
}

// attachedCancel is Ctrl+C while attached: cancel the child's turn when it
// has one, otherwise clear the draft.
func (m Model) attachedCancel() (tea.Model, tea.Cmd) {
	name := m.attachedTo
	if st, ok := m.subagents.Get(name); ok {
		switch st.State {
		case subagent.StateRunning, subagent.StateBlocked:
			if err := m.subagents.CancelTurn(name); err != nil {
				m.noteChild(name, err.Error())
			} else {
				m.purgeChildAsks(name)
			}
			m.viewport.SetContent(m.renderHistory())
			m.viewport.GotoBottom()
			return m, nil
		}
	}
	if strings.TrimSpace(m.input.Value()) != "" {
		m.input.Reset()
		m.historyIdx = len(m.inputHistory)
	}
	return m, nil
}

// childStatsReport is /stats scoped to the attached child.
func (m Model) childStatsReport(name string) string {
	st, ok := m.subagents.Get(name)
	if !ok {
		return "No agent named " + name + "."
	}
	mode, _ := m.subagents.AgentMode(name)
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s (%s) — %s\n", st.Name, st.Role, st.Detail)
	fmt.Fprintf(&sb, "  task:       %s\n", firstLine(st.Task))
	if st.Model != "" {
		fmt.Fprintf(&sb, "  model:      %s\n", st.Model)
	}
	if len(st.Paths) > 0 {
		fmt.Fprintf(&sb, "  paths:      %s\n", strings.Join(st.Paths, ", "))
	}
	fmt.Fprintf(&sb, "  mode:       %s (ceiling: %s)\n", mode, m.subagents.ParentMode())
	fmt.Fprintf(&sb, "  tool calls: %d\n", st.ToolCalls)
	spend := fmt.Sprintf("  spend:      ↑%s ↓%s tokens", formatTokenCount(st.TokensIn), formatTokenCount(st.TokensOut))
	if label := m.spendLabel(st.TokensIn, st.TokensOut); strings.HasPrefix(label, "$") {
		spend += "  " + label
	}
	sb.WriteString(spend)
	if q := m.subagents.QueuedSteering(name); q > 0 {
		fmt.Fprintf(&sb, "\n  queued steering: %d", q)
	}
	return sb.String()
}

// childModeStatus is /mode with no argument, scoped to the attached child.
func (m Model) childModeStatus(name string) string {
	mode, ok := m.subagents.AgentMode(name)
	if !ok {
		return "No agent named " + name + "."
	}
	ceiling := m.subagents.ParentMode()
	cycle := m.modeCycle
	if len(cycle) == 0 {
		cycle = agent.DefaultCycle()
	}
	labels := make([]string, len(cycle))
	for i, c := range cycle {
		labels[i] = c.String()
		if agent.ClampMode(c, ceiling) != c {
			labels[i] += " (disabled)"
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Mode: %s — %s.\n", mode, mode.Describe())
	fmt.Fprintf(&sb, "Ceiling: %s (a child is never more permissive than the orchestrator).\n", ceiling)
	sb.WriteString("Cycle (Shift+Tab): " + strings.Join(labels, " → "))
	return sb.String()
}

// renderChildStatusBar is the status bar scoped to the attached child.
func (m Model) renderChildStatusBar(width int) string {
	name := m.attachedTo
	st, ok := m.subagents.Get(name)
	if !ok {
		return statusBarStyle.Render(name)
	}
	mode, _ := m.subagents.AgentMode(name)
	parts := []string{childModeSegment(mode), statusBarStyle.Render(st.Detail)}
	if st.State == subagent.StateBlocked {
		parts[1] = ctxAlertStyle.Render(st.Detail)
	}
	if spend := m.spendLabel(st.TokensIn, st.TokensOut); spend != "" {
		parts = append(parts, statusBarStyle.Render(spend))
	}
	if q := m.subagents.QueuedSteering(name); q > 0 {
		parts = append(parts, statusBarStyle.Render(fmt.Sprintf("queued %d", q)))
	}
	left := strings.Join(parts, "  ")
	right := statusBarStyle.Render(name)
	pad := width - lipgloss.Width(left) - lipgloss.Width(right)
	if pad < 1 {
		right = ""
		pad = width - lipgloss.Width(left)
	}
	if pad < 0 {
		pad = 0
	}
	return left + strings.Repeat(" ", pad) + right
}

// childModeSegment mirrors the orchestrator's mode segment styling.
func childModeSegment(mode agent.Mode) string {
	name := strings.ReplaceAll(mode.String(), "-", " ")
	switch mode {
	case agent.ModeAcceptEdits, agent.ModeAuto:
		return modePermissiveStyle.Render("⏵⏵ " + name)
	default:
		return modeGatedStyle.Render("⏸ " + name)
	}
}
