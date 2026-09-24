package chat

// Sub-agent management and steering (
// docs/interface/surfaces.md#the-agent-manager): the agent list (`/agents`,
// or its chord) is a live view of every agent with cancel and kill actions, and
// attaching renders a child's session on the full chat surface — same
// components, breadcrumb header, steering input, approval cards in place, and
// mode changes clamped to the orchestrator's ceiling. Attach is a focus
// switch, not a second UI: the transcript the surface renders is whichever
// agent is focused, and esc pops one lineage level.

import (
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
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
	// stream is this child's own stable-prefix cache for the message it is
	// writing (streammd.go). The attached view re-renders that message on
	// every frame the same way the parent's transcript does, and re-parsing
	// a growing answer once per frame is quadratic in its length — the cost
	// the parent stopped paying and the child went on paying. One cache per
	// child rather than one shared: two children write two different
	// messages, and a cache whose prefix is not this content's own drops
	// itself on every frame, which is the uncached render with a copy of the
	// message in front of it.
	stream streamingMarkdown
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
	retireCheckpoints(cv.entries)
	return cv
}

// retireCheckpoints folds every status note but the last of them to its first
// line, which is what the session's own transcript does one note at a time as
// each lands (progress.go). A mirror is rebuilt from the supervisor's
// entries on every sync rather than appended to, so which note is the current
// one is answered here, over the whole list, instead
// (docs/interface/surfaces.md#the-progress-checkpoint).
func retireCheckpoints(entries []entry) {
	last := -1
	for i := range entries {
		if entries[i].checkpoint {
			last = i
		}
	}
	for i := range entries {
		if entries[i].checkpoint {
			entries[i].checkpointReplaced = i != last
		}
	}
}

// convertChildEntry maps a supervisor transcript entry onto the chat entry
// the shared renderers understand.
func convertChildEntry(te subagent.TranscriptEntry) entry {
	switch te.Kind {
	case subagent.EntryUser:
		return entry{kind: entryUser, text: te.Text}
	case subagent.EntryAssistant:
		// A status note the child's run was asked for is drawn at the rung
		// the session draws its own at; which of them is the current one is
		// settled over the whole list (retireCheckpoints).
		return entry{kind: entryAssistant, text: te.Text, checkpoint: te.Checkpoint}
	case subagent.EntryTool:
		result := te.Result
		if te.Pending {
			result = pendingToolResult
		}
		// The account of the decision comes across on the act, in the same
		// fields a session's own call carries it in, so the row the shared
		// renderers draw for a child says what the row for the session's
		// identical call says — a rule's yes with what it cost, or the
		// person who answered the card the call was routed to.
		return entry{kind: entryTool, toolName: te.Tool, toolArgs: te.Args, toolResult: result,
			allowedBy: te.AllowedBy, allowElapsed: te.AllowElapsed, approvedBy: te.ApprovedBy}
	default:
		return entry{kind: entrySystem, text: te.Text, toolResult: te.Result}
	}
}

// renderAttachedHistory renders the focused child's transcript plus its
// in-flight assistant text.
func (m *Model) renderAttachedHistory() string {
	return m.renderChildHistory(m.attachedTo, m.subagents.StreamingText(m.attachedTo))
}

// renderChildHistory is that render, told the message the child is writing
// rather than asking the supervisor for it, so the streaming half can be
// exercised without a provider behind it.
func (m *Model) renderChildHistory(name, streaming string) string {
	cv := m.syncChildView(name)
	w := m.transcriptWidth()
	var b strings.Builder
	// A child's transcript groups into steps like the parent's.
	body, prev, havePrev := joinUnits(m.transcriptUnits(cv.entries, w, false, -1), entry{}, false)
	b.WriteString(body)
	if streaming != "" {
		if havePrev {
			b.WriteString(separatorBefore(prev, entry{kind: entryAssistant}))
		}
		// Through this child's own stable-prefix cache, which is the cache
		// the parent's transcript has had all along (streammd.go): the
		// attached view redraws the arriving message on every frame, and
		// parsing it whole each time is quadratic in its length.
		b.WriteString(cv.stream.Render(streaming, w))
	}
	if b.Len() == 0 {
		return sty.Welcome.Render("No activity from this agent yet.")
	}
	return b.String()
}

// breadcrumb is the attached header path, e.g. "orchestrator ▸ writer-1"
// (nesting one segment per lineage level).
func (m Model) breadcrumb() string {
	return strings.Join(m.breadcrumbParts(), " ▸ ")
}

// breadcrumbParts is that path as its segments: the orchestrator, then one
// per lineage level down to the session the keyboard is in.
func (m Model) breadcrumbParts() []string {
	var parts []string
	for n := m.attachedTo; n != ""; {
		parts = append([]string{n}, parts...)
		p, ok := m.subagents.Parent(n)
		if !ok {
			break
		}
		n = p
	}
	return append([]string{"orchestrator"}, parts...)
}

// breadcrumbNearest is how many segments a path keeps when it cannot keep
// them all: the session the keyboard is in and the one esc goes back to, the
// pair the two live keys act between.
const breadcrumbNearest = 2

// nearestBreadcrumb is the path with everything above those two elided behind
// … — `… ▸ writer-1 ▸ reviewer-1a`. The far segment is the one the rail's own
// map is already drawing, so it is the one the path can afford to lose
// (docs/capabilities/subagents.md#a-child-may-delegate-to-a-configured-depth).
func (m Model) nearestBreadcrumb() string {
	parts := m.breadcrumbParts()
	if len(parts) <= breadcrumbNearest {
		return strings.Join(parts, " ▸ ")
	}
	return strings.Join(append([]string{"…"}, parts[len(parts)-breadcrumbNearest:]...), " ▸ ")
}

// saveScroll stores the current surface's scroll position before a focus
// switch.
func (m *Model) saveScroll() {
	// A selection names lines in the transcript the viewport is about to stop
	// showing, so the switch takes it with it.
	m.cancelSelection()
	vs := viewState{yoffset: m.viewport.YOffset(), atBottom: m.atBottom}
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
	m.killTargets = nil
	m.answerAgent = ""
	// The prompt gutter shows the child's name while attached, so the
	// textarea re-fits around it.
	m.syncInputWidth()
	m.syncViewport()
	m.viewport.SetLines(m.renderHistoryLines())
	m.restoreScroll()
}

// sessionMap is every session the keyboard can be in: the orchestrator ("")
// first, then every child in the tree the rail's AGENTS block draws them in
// (inspector.go, nestAgents) — each agent followed by the agents it started,
// depth-first, then its next sibling, and siblings in the supervisor's own
// spawn order. The tree is built from that spawn order, so a stop keeps its
// place across a kill and a retry the way the roster does, and one step on
// the keyboard is one row on screen everywhere except past a blocked child.
//
// The chord follows the nesting and not the float: a child waiting on an
// answer floats to the top of the map, but the map is read and the chord is
// aimed, and a key whose destination moved every time a child blocked or was
// answered would be a key nobody could aim
// (docs/interface/surfaces.md#the-inspector-rail).
func (m Model) sessionMap() []string {
	names := []string{""}
	if m.subagents == nil {
		return names
	}
	snapshot := m.subagents.Snapshot()
	present := make(map[string]bool, len(snapshot))
	for _, st := range snapshot {
		present[st.Name] = true
	}
	var roots []string
	under := map[string][]string{}
	for _, st := range snapshot {
		parent, _ := m.subagents.Parent(st.Name)
		if parent == "" || !present[parent] {
			roots = append(roots, st.Name)
			continue
		}
		under[parent] = append(under[parent], st.Name)
	}
	placed := make(map[string]bool, len(snapshot))
	var walk func(group []string)
	walk = func(group []string) {
		for _, name := range group {
			// A parent link that came round in a circle would walk forever;
			// a name already placed is where it stops.
			if placed[name] {
				continue
			}
			placed[name] = true
			names = append(names, name)
			walk(under[name])
		}
	}
	walk(roots)
	// Anything the walk could not reach is still a session the keyboard can
	// be in, so it goes at the end rather than out of reach, where the map
	// draws it too.
	for _, st := range snapshot {
		if !placed[st.Name] {
			names = append(names, st.Name)
		}
	}
	return names
}

// cycleAgent moves the keyboard one step along the session map, wrapping at
// both ends, from wherever it is. It goes through attach, so each session
// keeps its own scroll exactly as it does when the manager's enter attaches
// — the map is for seeing and moving, and everything that acts on a child
// stays in the manager. A click on a row of the map is the pointer's way to
// the same act (railclick.go).
//
// It walks every session, the ones the rail has folded away included: a
// folded row is what the block does when it runs out of room, and a session
// the keyboard is in is marked, which is never folded.
func (m Model) cycleAgent(step int) (tea.Model, tea.Cmd) {
	names := m.sessionMap()
	if len(names) < 2 {
		// One session is not a map, and wrapping around it would be a key
		// that redraws the screen and changes nothing.
		return m, nil
	}
	at := 0
	for i, name := range names {
		if name == m.attachedTo {
			at = i
			break
		}
	}
	m.attach(names[(at+step+len(names))%len(names)])
	return m, nil
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
	active := m.activeChildAsk()
	kept := m.childAsks[:0]
	for _, a := range m.childAsks {
		if a.Agent == name {
			a.Respond(false)
			m.forgetChildBlast(a)
			continue
		}
		kept = append(kept, a)
	}
	m.childAsks = kept
	// A hold belonging to a purged ask must not survive it: the next ask
	// would inherit a keyboard nobody granted it, and a letter meant for a
	// sentence could answer a card that was never armed in front of the
	// reader. The next ask, if one is queued, arms on its own terms — this
	// is not the queue advancing, because nothing here was answered, so no
	// stamp is left to shut its grace window.
	if active != nil && active.Agent == name && m.decisionHeld {
		m.decisionHeld, m.heldOnArrival = false, false
		m.graceFrom = time.Time{}
		m.armArrival()
	}
}

// --- agent list ---

// openAgentList shows the agent manager in the bottom panel.
func (m Model) openAgentList() (tea.Model, tea.Cmd) {
	// A session with no supervisor still has a manager to open as long as it
	// can draft: the list is where a person goes to find out what this
	// session has, and "nothing yet, and here is how to make one" is an
	// answer. It is only unavailable when there is neither.
	if m.subagents == nil && !m.personas.Enabled {
		m.appendEntry(entry{kind: entrySystem, text: "Sub-agents are unavailable in this session."})
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
		return m, nil
	}
	// Reading mode holds the panel too, and its keys are answered ahead of any
	// cover (overlay.go), so a manager drawn over it could not be typed into.
	// But it is a way of looking rather than a decision: nothing in it waits
	// for an answer, so the chord leaves it, the way a typed character does,
	// and the manager opens over whatever the turn was showing underneath —
	// which is still refused below if that is a decision
	// (docs/interface/surfaces.md#the-agent-manager).
	if m.state == stateFocus {
		left, _ := m.exitFocusMode()
		m = left.(Model)
	}
	// A decision of this session's own holds the panel, and the manager is a
	// takeover: one panel holds one thing
	// (docs/interface/principles.md#one-interaction-panel). The key used to
	// do nothing at all here, which reads exactly like a key that is broken —
	// so the refusal is said, and it names what is holding the panel and the
	// two keys that free it
	// (docs/interface/surfaces.md#the-agent-manager).
	if holder := m.panelHolder(); holder != "" {
		m.appendEntry(entry{kind: entrySystem, text: holder +
			", and the panel holds one thing at a time. Answer it or press esc, then the agents open."})
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
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

// panelHolder names the session's own decision standing in the panel, in the
// words the notice is built on, or "" where nothing of the session's is. It
// is the three states the manager cannot open over: each of them is a card
// the reader has to answer or set aside, and none of them is a child's — a
// routed card steps aside for the list and comes back when it closes
// (updateChildAsk).
func (m Model) panelHolder() string {
	switch m.state {
	case stateConfirmRun:
		return "A command is waiting for an answer"
	case statePlanApprove:
		return "A plan is waiting for an answer"
	case stateQuestion:
		return "A question is waiting for an answer"
	}
	return ""
}

// buildAgentRows assembles the live rows — orchestrator first, then
// blocked-on-approval children, then the rest in spawn order, each followed
// by the agents it spawned — and the parallel agent-name index ("" is the
// orchestrator).
func (m Model) buildAgentRows() ([]components.AgentRow, []string) {
	rows := []components.AgentRow{m.orchestratorRow()}
	names := []string{""}
	var nested []subagent.Status
	var depth map[string]int
	if m.subagents != nil {
		nested, depth = m.nestAgents(m.subagents.Snapshot())
	}
	for _, st := range nested {
		// The row draws the child's progress through the fan-out lane's
		// renderer, so the manager and the transcript say the same thing
		// about the same child.
		progress := m.childProgress(st)
		row := components.AgentRow{
			Name:     st.Name,
			Depth:    depth[st.Name],
			Task:     firstLine(st.Task),
			Status:   st.Detail,
			Progress: &progress,
			Note:     childNote(st),
			Handoff:  st.Handoff,
			// A blocked child can be answered here only while its request is
			// still queued; a failed one can be run again on its task.
			Answerable: st.State == subagent.StateBlocked && m.pendingAskFor(st.Name) != nil,
			Retryable:  st.State == subagent.StateFailed,
			PatchKept:  st.PatchKept,
			// A child that has answered is asked again from here too.
			TakesFollowUp: st.TakesFollowUp,
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
	// The rows that are not agents. First the roles this session can spawn:
	// the manager is where a person goes to see what this session has, and
	// until now a role was named nowhere a reader could reach
	// (docs/interface/surfaces.md#the-agent-manager). A role with a file
	// behind it is one enter opens; the ones shhh ships have none.
	for _, role := range m.spawnableRoles() {
		rows = append(rows, components.AgentRow{
			State:    components.AgentRole,
			Name:     role.Name,
			Task:     role.Description,
			Status:   role.Scope,
			Editable: role.Path != "",
		})
		names = append(names, role.Name)
	}
	// Then the answer "none of these" gets somewhere to go. It is offered
	// only where drafting is wired, because a row that opened a surface
	// saying no model can draft would be an offer that is not one.
	if m.personas.Enabled {
		rows = append(rows, components.AgentRow{
			State:  components.AgentOffer,
			Name:   "draft a new profile",
			Status: personaCommandName,
		})
		names = append(names, "")
	}
	return rows, names
}

// spawnableRoles is the roles this session can spawn, or none where the
// session wired no list — a surface built without one draws the agents and
// stops there.
func (m Model) spawnableRoles() []SpawnableRole {
	if m.personas.Roles == nil {
		return nil
	}
	return m.personas.Roles()
}

// openRoleEditor hands a role's own file to the reader's editor, which is
// what /memory edit does with an entry: the file is the profile, so there is
// nothing to write out first. What the editor leaves is read back on the way
// out (roleEditorFinished), so the edit is the running session's.
//
// The manager opens over a running turn and the editor takes the terminal
// with it, so the one refusal reachable from here is the turn's own. The list
// has already closed by then, which is what makes the notice worth writing:
// it lands where the reader is looking rather than behind a takeover.
func (m Model) openRoleEditor(name string) (tea.Model, tea.Cmd) {
	if reason, refused := m.editorRefusal(); refused {
		return m.surfaceNotice(reason)
	}
	path := ""
	for _, role := range m.spawnableRoles() {
		if role.Name == name {
			path = role.Path
		}
	}
	if path == "" {
		return m, nil
	}
	argv := editorArgv(editorCommand(), path, 1, 1)
	proc := exec.Command(argv[0], argv[1:]...)
	return m, tea.ExecProcess(proc, func(err error) tea.Msg {
		return roleEditorDoneMsg{name: name, path: path, err: err}
	})
}

// roleEditorDoneMsg is the editor's exit over a role's file.
type roleEditorDoneMsg struct {
	name string
	path string
	err  error
}

// roleEditorFinished says what became of the edit. There is nothing to save:
// the editor wrote the file. What is left is to read it again through the
// same registration a drafted profile's save ends on, so the next spawn is
// the role as the file now reads
// (docs/capabilities/subagents.md#a-profile-is-a-file). A file the loader
// refuses leaves the running role as it was, and the note says which of the
// two the reader now has.
func (m Model) roleEditorFinished(msg roleEditorDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.surfaceNotice("the editor exited with an error, so " + msg.name + " is as it was — " + msg.err.Error())
	}
	if m.personas.Reload != nil {
		if err := m.personas.Reload(msg.path); err != nil {
			return m.surfaceNotice("the edit did not load, so this session spawns " + msg.name + " as it was — " + err.Error())
		}
	}
	return m.systemNotice("Edited " + msg.path + ". The next " + msg.name + " this session spawns is the file as it now reads.")
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
	case stateRetryWait:
		// A turn waiting out a retry is still a turn in flight, so the map
		// draws this row as running. Without a word of its own it would be a
		// spinner beside "ready", which is the one reading that is wrong.
		status = "waiting to retry…"
	case stateCloseGate:
		// And so is a turn whose work is finished and whose checks are not.
		status = "running the checks…"
	case stateConfirmRun, statePlanApprove, stateQuestion:
		status = "waiting on you"
	}
	if r := m.agent.Rounds(); r > 0 && m.state != stateInput {
		status = fmt.Sprintf("round %d · %s", r, status)
	}
	return components.AgentRow{
		State:  state,
		Name:   "orchestrator",
		Status: status,
		// The orchestrator's own spend as it was priced request by request,
		// cache split and all — the row sits beside children whose figures
		// are estimates, and the one figure this session actually billed is
		// not one of the estimates.
		Spend: m.totalsLabel(m.mainSpend()),
	}
}

// freshRateLabel prices a bare token pair against the session's model at the
// full input rate: dollars when the pricing table knows the model, a token
// count otherwise, empty before anything was spent.
//
// It is an upper bound, not a bill. Every token the provider served from its
// prompt cache is charged here as if it had been read fresh, because a pair
// carries no cache split to charge at the cache rate — and on a coding
// session, whose prompt prefix is re-sent every round, that is most of the
// input (docs/capabilities/providers.md#the-prompt-prefix-is-paid-for-once).
// So it is for the spends nothing priced as it went: a child's token
// counters, a live turn's interpolated figures. A caller holding a
// meter.Totals — the ledger's, the vitals' — reports totalsLabel instead, and
// gets what was actually billed.
func (m Model) freshRateLabel(in, out int64) string {
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
// never render at once.
func (m Model) agentListLines() []string {
	if ask := m.listAnswerAsk(); ask != nil {
		return strings.Split(m.listAnswerCard(ask).View(m.contentWidth()), "\n")
	}
	rows, _ := m.buildAgentRows()
	m.agentList.Rows = rows
	m.agentList.MaxLines = m.maxConfirmPanelHeight()
	m.agentList.Spawned, m.agentList.SpawnLimit = m.subagents.Spawned()
	if m.agentList.Focus >= len(rows) {
		m.agentList.Focus = max(len(rows)-1, 0)
	}
	lines := strings.Split(m.agentList.View(m.contentWidth()), "\n")
	if m.killConfirm != nil {
		lines = append(lines, m.killConfirm.View(m.contentWidth()))
	}
	return lines
}

// updateAgentList routes keys while the agent list is open: enter attaches,
// x cancels the focused agent's turn, X arms the inline kill confirm, esc
// dismisses the list.
func (m Model) updateAgentList(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// An answer in progress owns the keys: the card is over the list, and
	// answering it (either way) hands the list back.
	if ask := m.listAnswerAsk(); ask != nil {
		return m.updateListAnswer(msg, ask)
	}
	if m.killConfirm != nil {
		done, yes := m.killConfirm.Update(msg)
		if !done {
			return m, nil
		}
		targets := m.killTargets
		m.killConfirm = nil
		m.killTargets = nil
		if yes {
			m.killChildren(targets)
		}
		m.syncViewport()
		return m, nil
	}

	rows, names := m.buildAgentRows()
	m.agentList.Rows = rows
	if m.agentList.Focus >= len(rows) {
		m.agentList.Focus = max(len(rows)-1, 0)
	}
	done, res := m.agentList.Update(msg)
	if res.Action == components.AgentNone {
		// The redirect's field opens and closes under a row without any
		// action reaching the host, and it is two lines the panel did not
		// have — so the frame it opens on is already the taller one.
		m.syncViewport()
		return m, nil
	}
	if done && res.Action == components.AgentDraft {
		m.agentList = nil
		m.answerAgent = ""
		m.syncViewport()
		return m.startPersona("")
	}
	if done && res.Action == components.AgentBack {
		m.agentList = nil
		m.answerAgent = ""
		m.syncViewport()
		return m, nil
	}
	// The one action that is about the list and not about a row, so it is
	// answered before the index is read: it carries none.
	if res.Action == components.AgentKillAll {
		return m.armKillAll()
	}
	if res.Index < 0 || res.Index >= len(names) {
		return m, nil
	}
	name := names[res.Index]
	switch res.Action {
	case components.AgentOpenRole:
		// The editor takes the terminal, so the list goes first: coming back
		// to a takeover that was drawn before the file was edited is coming
		// back to a stale screen.
		m.agentList = nil
		m.answerAgent = ""
		m.syncViewport()
		return m.openRoleEditor(name)
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
				m.viewport.SetLines(m.renderHistoryLines())
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
		// The card renders over the list and comes back to it: opening
		// the manager because something needs you should not then send you
		// into that child's session to say yes.
		if name == "" || m.pendingAskFor(name) == nil {
			return m, nil
		}
		m.answerAgent = name
		m.syncViewport()
		return m, nil
	case components.AgentSteer:
		// The same message the child's own lane sends, from the row the
		// reader was already looking at: opening the manager because a child
		// has drifted should not then send you into its session to say so
		// (docs/capabilities/subagents.md#three-can-steer-a-child-and-none-of-them-can-end-it).
		if name == "" {
			return m, nil // the session's own turn is steered by typing at it
		}
		if err := m.subagents.Steer(name, res.Text, subagent.SteerFromLane); err != nil {
			m.noteChild(name, "Cannot steer: "+err.Error())
		}
		m.syncViewport()
		return m, nil
	case components.AgentRetry:
		if name == "" {
			return m, nil // the orchestrator's turn is re-run by asking again
		}
		if err := m.subagents.Retry(name); err != nil {
			m.noteChild(name, err.Error())
		} else {
			// A review of the attempt the retry replaced is a decision about
			// work nobody is asking for any more, so it goes with it.
			m.purgeChildAsks(name)
			m.appendEntry(entry{kind: entrySystem, text: "Retrying " + name + " on its original task."})
			m.viewport.SetLines(m.renderHistoryLines())
			m.viewport.GotoBottom()
		}
		return m, nil
	case components.AgentReview:
		if name == "" {
			return m, nil
		}
		return m.reviewKeptPatch(name)
	case components.AgentKill:
		if name == "" {
			return m, nil // the orchestrator is quit with Ctrl+D, never killed from here
		}
		m.killConfirm = &components.Confirm{Prompt: m.killPrompt(name)}
		m.killTargets = []string{name}
		m.syncViewport()
		return m, nil
	}
	return m, nil
}

// reviewKeptPatch is [p] on a row holding a kept patch: the patch opens full
// screen on the surface [d] opens from a live card, headed with whose it is,
// and the card behind it is the one a finishing writer's patch is put on —
// apply and decline, over the list, the way [a] answers a blocked child from
// here. Pressing it again finds the card already out rather than putting out
// a second one over the same work
// (docs/capabilities/subagents.md#a-failed-child-leaves-a-handoff).
func (m Model) reviewKeptPatch(name string) (tea.Model, tea.Cmd) {
	if m.subagents == nil {
		return m, nil
	}
	ask, err := m.subagents.ReviewKept(name)
	if err != nil {
		m.noteChild(name, err.Error())
		return m, nil
	}
	if !slices.Contains(m.childAsks, ask) {
		m.childAsks = append(m.childAsks, ask)
		// Read once, where it arrives, for the reason a routed request's is
		// (handleSubagentEvent): the card is rebuilt every frame.
		if m.childBlast == nil {
			m.childBlast = map[*subagent.Ask]blastRadius{}
		}
		m.childBlast[ask] = m.childRadius(ask)
	}
	m.answerAgent = name
	m.syncViewport()
	return m.openChildDiff(ask)
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
// The hints drop [g] and the manager's chord: the manager is already what is underneath,
// and answering here is the whole point of being here.
func (m Model) listAnswerCard(ask *subagent.Ask) *components.ApprovalCard {
	card := m.childAskCard(ask)
	// A card picked off the list is not one that arrived: the reader opened
	// the manager and named this decision, which is the opposite of the case
	// an arrival holds the keyboard for. So it claims every key it has —
	// there is no draft under the list for a letter to belong to — and the
	// one hint it keeps is the way back, which a card claiming less would
	// have suppressed along with the rest
	// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
	card.HeldOnArrival = false
	// Esc is the card's own row rather than an offer beside the run, and what
	// it does here is leave, not decline. It used to decline, argued from
	// there being no draft under the list to hand the keyboard back to — but
	// the surface a reader lands back on is the manager, and the row they
	// came from goes on saying `⚠ needs you`, so the decision is as visibly
	// still there as it is behind a draft. Escape never abandons work
	// (docs/interface/principles.md#esc-is-always-the-safe-answer), and a key
	// that answered no because the reader wanted to look at the list again
	// was the one place in the product where it did.
	card.ExtraHints = nil
	card.Return = "back to the agents — the decision stays waiting"
	return card
}

// updateListAnswer routes keys to the card over the list. Either answer
// resolves the request and returns to the list; [n] declines, because a
// routed request is never silently dropped, and esc returns to the list
// without answering.
func (m Model) updateListAnswer(msg tea.KeyPressMsg, ask *subagent.Ask) (tea.Model, tea.Cmd) {
	// Esc before the card reads it, because the card's own deny binds the
	// same keystroke under the letter `n` and the two are different acts
	// here: [n] answers the child no, esc puts the reader back on the list
	// with the request still queued and its row still saying it needs them
	// (docs/interface/principles.md#esc-is-always-the-safe-answer).
	if keys.Match(msg, keys.Select.Cancel) {
		m.answerAgent = ""
		m.syncViewport()
		return m, nil
	}
	card := m.listAnswerCard(ask)
	// The card over the list is bounded like the card anywhere else, so it
	// counts what the bound swallowed — and the chord it counts it behind
	// moves the body here too.
	if keys.Match(msg, keys.Decision.ScrollUp, keys.Decision.ScrollDown,
		keys.Decision.PanLeft, keys.Decision.PanRight) {
		return m.scrollCard(msg, card)
	}
	done, result := card.Update(msg)
	if !done {
		return m, nil
	}
	if result == components.ApprovalFullDiff {
		// The whole change, full screen, with the request still waiting
		// behind it — the same door the card offers anywhere else. Esc comes
		// back here, to the list with the card still over it.
		return m.openChildDiff(ask)
	}
	if result == components.ApprovalAlways {
		// The same grant the card makes anywhere else: this is one card drawn
		// in two places, and a key that meant a different thing depending on
		// how the reader got to it would be two keys (subagents.go).
		m.grantChildCommand(ask)
		result = components.ApprovalApprove
	}
	approved, ok := askAnswer(result)
	if !ok {
		return m, nil
	}
	m.answerAgent = ""
	for i, queued := range m.childAsks {
		if queued == ask {
			m.childAsks = append(m.childAsks[:i], m.childAsks[i+1:]...)
			break
		}
	}
	m.forgetChildBlast(ask)
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

// --- attached-view interaction ---

// attachedSubmit handles Enter while attached: scoped slash commands run
// against the child, anything else is queued mid-turn steering (
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
	if err := m.subagents.Steer(m.attachedTo, text, subagent.SteerFromLane); err != nil {
		m.noteChild(m.attachedTo, "Cannot steer: "+err.Error())
	}
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	m.atBottom = true
	return m, nil
}

// attachedCommand runs one child-scoped slash command.
func (m Model) attachedCommand(parts []string) (tea.Model, tea.Cmd) {
	name := m.attachedTo
	switch parts[0] {
	case "/exit":
		// Ending a child has one name, and it is the manager's kill. This
		// command was a second one, spelled the same as the command that
		// quits the whole session everywhere else in the product — so a
		// reader who typed it to leave a child's surface ended the child
		// instead (docs/capabilities/subagents.md#three-can-steer-a-child-and-none-of-them-can-end-it).
		m.noteChild(name, "Ending an agent is "+keys.Bracket(keys.Agent.Kill)+
			" in the agent manager ("+keys.Shown(keys.Draft.Agents)+
			"). Esc detaches without ending anything.")
	case "/stats":
		m.noteChild(name, m.childStatsReport(name))
	case "/diff":
		m.attachedDiff(name)
	case "/permissions", "/perms", "/mode":
		m.attachedModeCommand(parts)
	case "/agents":
		return m.openAgentList()
	case "/attach":
		// Hop straight to another agent without going through the list
		//; bare /attach opens it.
		return m.attachCommand(parts)
	case "/detach":
		m.detachOne()
	default:
		m.noteChild(name, "Commands while attached: /stats, /diff, /permissions [name], /agents, /attach <name>, /detach. Plain text steers the agent; esc detaches. Ending it is "+keys.Bracket(keys.Agent.Kill)+" in the agent manager.")
	}
	m.viewport.SetLines(m.renderHistoryLines())
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
	cycle := m.policy.cycle
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
	m.viewport.SetLines(m.renderHistoryLines())
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
			m.viewport.SetLines(m.renderHistoryLines())
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
	spend := fmt.Sprintf("  spend:      ↑%s ↓%s tokens", formatTokenCount(st.Spend.In), formatTokenCount(st.Spend.Out))
	if label := m.childSpendLabel(st); strings.HasPrefix(label, "$") {
		spend += "  " + label
	}
	sb.WriteString(spend)
	if q := m.subagents.QueuedSteering(name); q > 0 {
		fmt.Fprintf(&sb, "\n  queued steering: %d", q)
	}
	return sb.String()
}

// childModeStatus is /permissions with no argument, scoped to the attached
// child.
func (m Model) childModeStatus(name string) string {
	mode, ok := m.subagents.AgentMode(name)
	if !ok {
		return "No agent named " + name + "."
	}
	ceiling := m.subagents.ParentMode()
	cycle := m.policy.cycle
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

// attachedDetail is what the attached frame says the child is doing. A parked
// child says `held`, the word its row on the rail's map says, and nothing
// about the release: the supervisor's sentence names a release the chord
// cannot give from here, since attached the chord keeps its textarea meaning
// and the key is named on the orchestrator's frame where it is live
// (docs/interface/surfaces.md#the-agent-manager).
func attachedDetail(st subagent.Status) string {
	if st.Reseeding {
		return "reseeding"
	}
	// A child waiting for a check slot was not parked by the reader, and
	// nothing here releases it: the sentence says what it waits for, as its
	// lane and its rail row do (docs/capabilities/subagents.md#what-they-share).
	if st.SlotWait > 0 {
		return st.Detail
	}
	if st.Held {
		return "held"
	}
	return st.Detail
}

// renderChildStatusBar is the status bar scoped to the attached child.
func (m Model) renderChildStatusBar(width int) string {
	name := m.attachedTo
	st, ok := m.subagents.Get(name)
	if !ok {
		return sty.StatusBar.Render(name)
	}
	mode, _ := m.subagents.AgentMode(name)
	parts := []string{childModeSegment(mode), sty.StatusBar.Render(attachedDetail(st))}
	if st.State == subagent.StateBlocked {
		parts[1] = sty.CtxAlert.Render(st.Detail)
	}
	if spend := m.childSpendLabel(st); spend != "" {
		parts = append(parts, sty.StatusBar.Render(spend))
	}
	if q := m.subagents.QueuedSteering(name); q > 0 {
		parts = append(parts, sty.StatusBar.Render(fmt.Sprintf("queued %d", q)))
	}
	left := strings.Join(parts, "  ")
	right := sty.StatusBar.Render(name)
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

// childModeSegment mirrors the orchestrator's mode segment — the same words
// and the same marks, because a child's permission mode is read for the same
// reason the parent's is.
func childModeSegment(mode agent.Mode) string {
	name := modeWord(mode)
	switch mode {
	case agent.ModeAcceptEdits, agent.ModeAuto:
		return sty.ModePermissive.Render("⏵⏵ " + name)
	default:
		return sty.ModeGated.Render("⏸ " + name)
	}
}
