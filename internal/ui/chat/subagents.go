package chat

// Sub-agent orchestration surface: the parent session renders child
// activity as compact progress rows, routes detached children's approval
// requests through the same approval-card surface (labeled with the agent
// name), and cancels the whole child tree with the turn.

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/observe"
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
	sup.SetParentMode(m.policy.mode)
	sup.SetParentGrants(m.liveGrants())
	if m.conversation {
		sup.SetConversationPolicy()
	}
	return m
}

// syncGrants pushes the session's [a] grants outward, to everything that
// decides on their strength. The supervisor takes them because a category
// the user waved through for the session is waved through for children too,
// instead of being re-asked once per agent. The fetcher takes the hosts
// because it is the only place a redirect off a granted host is visible
// (policy.go, WithHostGrants).
//
// What it pushes is every grant standing right now, the ones that end with
// the turn included: a child running under a turn grant is doing the thing
// that was granted, for as long as it was granted for. That the grant is
// short is not a fact either reader can act on — neither of them can see a
// turn — so the expiry reaches them the way the grant did, by this being
// called again at the turn's close (close.go).
func (m *Model) syncGrants() {
	g := m.liveGrants()
	if m.subagents != nil {
		m.subagents.SetParentGrants(g)
	}
	if m.hostGrants != nil {
		m.hostGrants(concatGrants(m.hostAllowlist(), m.policy.turn.Hosts))
	}
}

// applyMode changes the session's permission mode, keeping the sub-agent
// ceiling in sync (children are never more permissive than the parent).
func (m *Model) applyMode(mode agent.Mode) {
	if mode != m.policy.mode {
		m.signal(observe.SignalMode, mode.String())
	}
	m.policy.mode = mode
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
		// The block is read here, once, and not in the card: a routed card is
		// rebuilt every frame, and this stats the filesystem and asks git
		// (radius.go).
		if m.childBlast == nil {
			m.childBlast = map[*subagent.Ask]blastRadius{}
		}
		m.childBlast[ev.Ask] = m.childRadius(ev.Ask)
		if m.activeChildAsk() == ev.Ask {
			// It is the card on screen now, and its body is not the one the
			// stored offsets describe — the reset every arrival at a decision
			// gets (setTurnState, turn.go). One arriving behind another card
			// takes them from nobody, so it leaves them where they are: they
			// still belong to whatever the reader is reading.
			m.cardScroll, m.cardPan = 0, 0
		}
		// A routed approval arrives the way every other decision does: on
		// screen, and holding the keyboard only if there is no sentence for
		// its letters to belong to. It arms itself because it is
		// a queue rather than a turn state, so setTurnState never sees it.
		m.armArrival()
	case subagent.EventDone:
		// A finished child can no longer act on its asks.
		m.purgeChildAsks(ev.Status.Name)
		// The child says how it ended on its own record, where its budget
		// and its spend already are (internal/observe, SignalSubagent). A
		// second row here would say the same thing about the same attempt,
		// and every rate over a window would count a fan-out's children
		// twice.
		//
		// On screen the ending is said once as well. A child with a lane
		// settles into the same words in place — the outcome in the lane's
		// state field and the first line of its report under it — so a
		// notice two rows below the block would be the second drawing of an
		// ending the reader is already looking at. A child that ran alone
		// has no lane, only the row its spawn left, and there this is the
		// one thing that says it finished
		// (docs/interface/surfaces.md#the-input-frame).
		if !m.childHasLane(ev.Status) {
			m.appendEntry(entry{kind: entrySystem, text: fmt.Sprintf("Agent %s: %s", ev.Status.Name, ev.Status.Detail)})
		}
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
	m.reopenFrozenLane(ev.Status)
	m.syncViewport()
	m.viewport.SetLines(m.renderHistoryLines())
	if m.atBottom {
		m.viewport.GotoBottom()
	}
	return m, listenSubagents(m.subagents.Events())
}

// reopenFrozenLane lets go of the render cache when a child whose lane was
// frozen into it has been handed a follow-up. A lane is frozen once every
// child in its block has settled, because nothing lands in the block after
// that — except this: a follow-up sets a finished child moving again with no
// row landing anywhere, and a block kept from the cache would go on drawing
// it done.
func (m *Model) reopenFrozenLane(st subagent.Status) {
	if st.FollowUp == "" || st.State == subagent.StateDone || st.State == subagent.StateFailed {
		return
	}
	for i, e := range m.transcript {
		if e.kind == entryFanout && e.fanout != nil && e.fanout.batch == st.Batch {
			if i < m.cached.count {
				m.invalidateRenderCache()
			}
			return
		}
	}
}

// childHasLane reports whether the transcript already draws this child as a
// lane, which is true of every child of a round that spawned two or more of
// them: their rows were replaced by that round's fan-out block, and the block
// reads its lanes off the supervisor, so a child's own ending arrives on its
// lane without anything being appended for it (fanout.go).
func (m Model) childHasLane(st subagent.Status) bool {
	for _, e := range m.transcript {
		if e.kind == entryFanout && e.fanout != nil && e.fanout.batch == st.Batch {
			return true
		}
	}
	return false
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
			BeforeMode:   f.BeforeMode,
			AfterMode:    f.AfterMode,
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
	case stateConfirmRun, statePlanApprove, stateQuestion:
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

// othersWaiting counts the agents with a request queued that the surface in
// front of the reader is not showing: attached, every child but the one whose
// transcript this is. It counts agents and not requests, because what the
// rail is telling the reader is that somebody else is stopped — a child that
// queued two calls is one agent to go and see
// (docs/interface/surfaces.md#the-agent-manager).
func (m Model) othersWaiting() int {
	if m.attachedTo == "" {
		return 0
	}
	seen := map[string]bool{}
	for _, ask := range m.childAsks {
		if ask.Agent != m.attachedTo {
			seen[ask.Agent] = true
		}
	}
	return len(seen)
}

// updateChildAsk routes keys to the presented child approval card. Its esc/n
// path declines — a routed request is never silently dropped or auto-denied.
// Detached, [g] jumps into the agent's attached view instead of answering
// (docs/interface/surfaces.md#the-agent-manager).
func (m Model) updateChildAsk(msg tea.KeyPressMsg, ask *subagent.Ask) (tea.Model, tea.Cmd) {
	// [g] is a bare letter, so it belongs to the card only while the card was
	// handed the keyboard. One holding it by arrival claims nothing but its
	// answers, and "go ahead, but…" is a sentence.
	if keys.Match(msg, keys.Agent.Go) && !m.heldOnArrival && m.attachedTo != ask.Agent {
		m.attach(ask.Agent)
		return m, nil
	}
	// The manager is reachable from a routed approval too: the card
	// steps aside while the list is open and comes back when it closes.
	if keys.Match(msg, keys.Draft.Agents) {
		return m.openAgentList()
	}
	card := m.childAskCard(ask)
	// The card's own scroll, answered before the decision keys so a held card
	// cannot read a chord as the start of a sentence — the same order the
	// session's own card answers them in (run.go). A routed card is bounded
	// like every other, and a writer's patch is the body that needs it: forty
	// hunks show eight lines and count the rest behind this chord, which
	// without a route here is a count with an offer attached to it and
	// nothing behind the offer.
	if keys.Match(msg, keys.Decision.ScrollUp, keys.Decision.ScrollDown,
		keys.Decision.PanLeft, keys.Decision.PanRight) {
		return m.scrollCard(msg, card)
	}
	done, result := card.Update(msg)
	if !done {
		return m, nil
	}
	if result == components.ApprovalRelease {
		// The card had the keyboard by arrival and this key is not one of its
		// answers: it is the start of a sentence, and the ask stays queued.
		return m.releaseToDraft(msg)
	}
	if result == components.ApprovalFullDiff {
		// [d] opens the child's change full screen with the request still
		// waiting behind it; esc comes back to the card, which keeps the
		// keyboard because the reader took it on purpose (leaveSurface).
		return m.openChildDiff(ask)
	}
	if result == components.ApprovalAlways {
		// [a] grants and then answers: the request in front of the reader is
		// the first thing the grant covers, and a key that widened the
		// permission without letting this one through would leave the child
		// waiting on a decision that had just been taken.
		m.grantChildCommand(ask)
		result = components.ApprovalApprove
	}
	approved, ok := askAnswer(result)
	if !ok {
		return m, nil
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
	// Nor can the next card inherit this one's scroll: the offsets describe a
	// body that has just been replaced, and a stale pan would blank the new
	// card's rows outright — the reset the session's own arrivals get
	// (setTurnState, turn.go).
	m.cardScroll, m.cardPan = 0, 0
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

// childAskCard builds the approval card for a routed child request, title
// prefixed with the agent name
// (docs/interface/surfaces.md#the-agent-manager). Attached to that agent, the
// prefix drops (the breadcrumb already names it) — detached, [g] offers the
// jump into its view.
//
// It carries what the session's own card carries — the blast radius, the
// severity and its reading, the containment chip, reversibility, and [d] into
// the whole diff. The person answering is the same person deciding on the
// same terms, and this is the card they have least else to go on from: the
// work happened somewhere they were not watching. The variant it matters most
// for is the patch, which writes their own files (radius.go).
func (m Model) childAskCard(ask *subagent.Ask) *components.ApprovalCard {
	card := &components.ApprovalCard{
		// The card is rebuilt every frame, so its scroll rides the model and
		// is reset whenever the presented ask changes (updateChildAsk).
		BodyOffset: m.cardScroll,
		PanOffset:  m.cardPan,
	}
	defer m.applyNotYetLive(card)
	// The blast-radius block, read where the request arrived rather than
	// resolved here: this is a render, and resolving would stat the
	// filesystem and shell out to git on every frame (handleSubagentEvent).
	// It carries the risks too, so the card states severity and warnings from
	// one source rather than two.
	m.childBlastFor(ask).applyTo(card)
	prefix := ask.Agent + " ▸ "
	if m.attachedTo == ask.Agent {
		prefix = ""
	} else {
		card.ExtraHints = []components.KeyOffer{
			{Key: keys.Bracket(keys.Agent.Go), Label: "attach to " + ask.Agent},
			{Key: keys.Bracket(keys.Draft.Agents), Label: "agents"},
		}
	}
	// The act, on a child's card as on the session's own: the glyph and the
	// thing being asked for. Which agent is asking is the title rail's, above
	// — and it is dropped there only while the reader is attached to that
	// agent, where the whole screen is already saying whose session this is
	// (docs/interface/surfaces.md#the-approval-card).
	card.Act = ask.Title
	switch ask.Kind {
	case subagent.AskCommand:
		card.Variant = components.ApprovalCommand
		card.Title = prefix + "Approve command"
		card.ActGlyph = "$"
		card.Answer = "run it once"
		// [a] here is the other half of a rule the session already keeps: a
		// grant the parent makes travels to every child (syncGrants), so a
		// reader answering the twentieth identical request from the fan-out
		// can make that grant from the card in front of them rather than
		// waiting for one of the session's own. The grant is the turn's,
		// which is the only length this key offers without a list: the reach
		// is what the reader is choosing here — one command, every agent —
		// and a standing permission chosen from a child's card is a wider
		// thing than the card is about
		// (docs/capabilities/approvals-and-safety.md#a-grant-says-when-it-ends).
		//
		// Flagged commands keep the exception they have everywhere: the key
		// is absent and the footnote says why (radius.go).
		if len(card.Warnings) == 0 {
			if grant := agent.GrantPrefix(ask.Command); grant != "" {
				card.AllowAlways = true
				card.AlwaysHint = "allow " + strconv.Quote(grant) + " for every agent until this turn ends"
			}
		}
	case subagent.AskEdit:
		card.Variant = components.ApprovalEdit
		card.Title = prefix + "Approve edit"
		card.ActGlyph = "✎"
		card.Hunks = ask.Hunks
		card.FullDiff = len(ask.Hunks) > 0
		card.Answer = "apply it in the agent's workspace"
	case subagent.AskPatch:
		card.Variant = components.ApprovalEdit
		card.Title = prefix + "Apply patch"
		card.ActGlyph = "✎"
		card.Hunks = ask.Hunks
		card.FullDiff = len(ask.Hunks) > 0
		card.Answer = "apply the patch to your workspace"
	default:
		card.Variant = components.ApprovalGeneric
		card.Title = prefix + "Approve tool"
		card.ActGlyph = "⚙"
		card.Summary = firstLine(ask.Summary)
		card.Answer = "allow it"
	}
	return card
}

// childBlastFor is the request's stashed blast-radius block. A request that
// reaches a card without one was queued by hand rather than routed — a
// front-end test does that — and is resolved on the spot instead, which is
// correct and merely not cheap.
func (m Model) childBlastFor(ask *subagent.Ask) blastRadius {
	if b, ok := m.childBlast[ask]; ok {
		return b
	}
	return m.childRadius(ask)
}

// forgetChildBlast drops the stashed blocks of requests that have left the
// queue. Their readings describe a decision nobody can make any more, and a
// session that answers a hundred of them should not still be holding a
// hundred of these.
func (m *Model) forgetChildBlast(asks ...*subagent.Ask) {
	for _, a := range asks {
		delete(m.childBlast, a)
	}
}

// askAnswer is the decision a routed card's result carries, and whether the
// result is one at all.
//
// Only the two answers resolve a request. A result the surface routes
// somewhere else — the full-screen diff — must never reach the answer below
// on its way, because everything that is not an approval there is a decline:
// a reader who pressed [d] to read a patch before deciding would have
// declined it by asking to read it, and the child would be told so.
func askAnswer(result components.ApprovalDecision) (approved, ok bool) {
	switch result {
	case components.ApprovalApprove:
		return true, true
	case components.ApprovalDeny:
		return false, true
	}
	return false, false
}

// grantChildCommand is what [a] on a routed command card makes: the turn
// grant covering the shape of the command in front of the reader. It is the
// session's own grant and not a child's, because there is no such thing as a
// child's — the supervisor is handed the session's standing grants and every
// child decides against those (syncGrants) — so the one answer this key can
// give is the one the session would have given at its own card.
//
// The transcript keeps what was granted and when it ends, the way it does for
// a grant taken off the list, because a grant nobody can see is a grant
// nobody can revoke (grant.go).
func (m *Model) grantChildCommand(ask *subagent.Ask) {
	what := m.grantCommand(ask.Command, grantOffer{length: forThisTurn})
	if what == "" {
		return
	}
	m.noteGrant(grantNote("Commands starting "+what+" will run for every agent", forThisTurn))
	m.syncGrants()
}

// openChildDiff takes a routed request's change full screen. An edit names
// its file; a patch names none, because it is a whole worktree's work and no
// one path is it — the header says whose instead, which is the fact a reader
// opening it is checking.
func (m Model) openChildDiff(ask *subagent.Ask) (tea.Model, tea.Cmd) {
	path, verb := ask.Path, "edit"
	if ask.Kind == subagent.AskPatch {
		path, verb = ask.Agent+"'s patch", "apply"
	}
	return m.openDiffFull(&components.DiffView{
		Path: path, Verb: verb, Hunks: ask.Hunks, Syntax: diffSyntax(ask.Path),
	}, m.state)
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
	m.forgetChildBlast(m.childAsks...)
	m.childAsks = nil
}

// killChildren ends the named children and drops whatever each was waiting on
// an answer for. It is one function for [X] and [K] because a kill is a kill:
// the manager's two keys differ in how many names they hand over and in
// nothing else.
func (m *Model) killChildren(names []string) {
	if m.subagents == nil {
		return
	}
	for _, name := range names {
		if err := m.subagents.Kill(name); err != nil {
			m.noteChild(name, err.Error())
			continue
		}
		m.purgeChildAsks(name)
	}
}

// killPrompt is what the confirm asks before one agent is killed. It states
// what survives as well as what does not — a kill that only names its
// casualties reads as bigger than it is — and it counts the subtree, because
// a kill takes the agents under the one named with it and a person answering
// "yes" to one name would not otherwise know how many that was
// (docs/capabilities/subagents.md#what-nesting-does-to-the-rest-of-it).
func (m Model) killPrompt(name string) string {
	var under []string
	if m.subagents != nil {
		under = m.subagents.Under(name)
	}
	if len(under) == 0 {
		return "Kill " + name + "? Its turn stops and its isolated workspace is discarded" +
			m.keptClause(" and its patch is kept", name) +
			"; its transcript stays and the other agents keep running."
	}
	return "Kill " + name + " and " + plural(len(under), "agent") + " under it? " +
		"Every turn stops and every isolated workspace is discarded" +
		m.keptClause(" and the patches in them are kept", append(under, name)...) +
		"; the transcripts stay and the other agents keep running."
}

// keptClause is what a kill confirm adds about the work that survives it: a
// writer's change is kept rather than discarded with its workspace, and the
// confirm says so only where one of the agents it names has a change to keep
// (docs/capabilities/subagents.md#a-failed-child-leaves-a-handoff).
func (m Model) keptClause(clause string, names ...string) string {
	if m.subagents == nil {
		return ""
	}
	for _, name := range names {
		if m.subagents.PatchToKeep(name) {
			return clause
		}
	}
	return ""
}

// armKillAll is [K] on the manager: the same inline confirm one child gets,
// over every child that is still going. It names the count rather than the
// children, because a prompt that listed nine names would be a prompt nobody
// reads to the end, and it states what survives for the reason the single
// kill's does — a kill that only names its casualties reads as bigger than it
// is (docs/interface/surfaces.md#the-agent-manager).
//
// What it hands the kill is the roots alone — the live agents with no live
// agent above them — because a kill takes the subtree, and naming the agents
// under a root as well would kill each of them a second time
// (docs/capabilities/subagents.md#what-nesting-does-to-the-rest-of-it). The
// count is still the whole roster: the roots and everything under them.
func (m Model) armKillAll() (tea.Model, tea.Cmd) {
	roots := m.liveRootNames()
	if len(roots) == 0 {
		return m, nil
	}
	all := append([]string(nil), roots...)
	for _, name := range roots {
		all = append(all, m.subagents.Under(name)...)
	}
	m.killConfirm = &components.Confirm{Prompt: "Kill all " + plural(len(all), "agent") +
		"? Every turn stops and every isolated workspace is discarded" +
		m.keptClause(" and the patches in them are kept", all...) +
		"; the transcripts stay and your own turn keeps going."}
	m.killTargets = roots
	m.syncViewport()
	return m, nil
}

// liveRootNames are the live children whose parent is not itself live: the
// tops of the subtrees a kill of everything has to name.
func (m Model) liveRootNames() []string {
	names := m.liveChildNames()
	live := make(map[string]bool, len(names))
	for _, name := range names {
		live[name] = true
	}
	var roots []string
	for _, name := range names {
		if parent, _ := m.subagents.Parent(name); !live[parent] {
			roots = append(roots, name)
		}
	}
	return roots
}

// liveChildNames are the children a kill can still reach: the ones that have
// not finished or broken on their own. The order is the supervisor's, which
// is spawn order.
func (m Model) liveChildNames() []string {
	if m.subagents == nil {
		return nil
	}
	var names []string
	for _, st := range m.subagents.Snapshot() {
		switch st.State {
		case subagent.StateDone, subagent.StateFailed:
		default:
			names = append(names, st.Name)
		}
	}
	return names
}

// nestAgents puts a set of agents into the order every surface that draws
// more than one of them draws them in, and says how far under the session
// each one sits: each agent is followed by the agents it spawned, and at
// every level the ones waiting on an answer come first. A parent whose
// delegate is waiting on you is a parent waiting on you, so a subtree floats
// on the request inside it and the request is drawn directly under the row it
// belongs to rather than lifted to the top of a flat list
// (docs/capabilities/subagents.md#a-child-may-delegate-to-a-configured-depth).
//
// Depth counts the session as 0 and a child of it as 1, which is what the
// map, the manager and a lane all indent by. It is counted within the set
// handed over rather than up the whole tree: a fan-out block holds one round's
// children, and a child whose parent is in an earlier block has no row here
// for its corner to hang off.
func (m Model) nestAgents(all []subagent.Status) ([]subagent.Status, map[string]int) {
	present := make(map[string]bool, len(all))
	for _, st := range all {
		present[st.Name] = true
	}
	var roots []subagent.Status
	under := map[string][]subagent.Status{}
	for _, st := range all {
		parent, _ := m.subagents.Parent(st.Name)
		if parent == "" || !present[parent] {
			roots = append(roots, st)
			continue
		}
		under[parent] = append(under[parent], st)
	}
	ordered := make([]subagent.Status, 0, len(all))
	depth := make(map[string]int, len(all))
	var walk func(group []subagent.Status, at int)
	walk = func(group []subagent.Status, at int) {
		// A parent link that ever came round in a circle would walk forever,
		// and this runs on every frame; the set is finite, so the count it
		// has already placed is the bound.
		if len(ordered) >= len(all) {
			return
		}
		for _, waiting := range []bool{true, false} {
			for _, st := range group {
				if m.agentWaiting(st, under) != waiting {
					continue
				}
				ordered = append(ordered, st)
				depth[st.Name] = at
				walk(under[st.Name], at+1)
			}
		}
	}
	walk(roots, 1)
	// Anything the walk could not reach is still an agent the surface was
	// asked to draw, so it goes at the end at the top level rather than
	// vanishing.
	for _, st := range all {
		if _, drawn := depth[st.Name]; !drawn {
			ordered = append(ordered, st)
			depth[st.Name] = 1
		}
	}
	return ordered, depth
}

// agentWaiting reports whether this agent or anything under it is waiting on
// an answer, which is what floats its whole subtree.
func (m Model) agentWaiting(st subagent.Status, under map[string][]subagent.Status) bool {
	if st.State == subagent.StateBlocked {
		return true
	}
	for _, c := range under[st.Name] {
		if m.agentWaiting(c, under) {
			return true
		}
	}
	return false
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
//
// They hide under a routed card too. The card's title rail names the child
// asking and its lane in the transcript says why it stopped, so a row
// between the two says a third time what the reader is about to answer —
// and it says it in the rows they have to look past to reach the card
// (docs/interface/surfaces.md#the-input-frame).
func (m Model) agentRowsHeight() int {
	if m.attachedTo != "" || m.agentList != nil || m.activeChildAsk() != nil {
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
// name, task, live status, and spend. The rows are in the tree order the
// fan-out block above them draws its lanes in, and a child a child spawned
// sits behind the same corner, so the two surfaces one screen apart agree
// about who spawned whom.
func (m Model) renderAgentRows(width int) string {
	statuses, depth := m.nestAgents(m.activeAgentStatuses())
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
		// The separator every other row that joins two facts joins them
		// with, and not a gap: two spaces read as a column that is not
		// there, because the names are not one width and so the tasks under
		// them never line up (docs/interface/principles.md#one-grid).
		left := components.AgentNesting(depth[st.Name]) + glyph + " " + st.Name
		if task := firstLine(st.Task); task != "" {
			left += sty.ToolArgs.Render(" · " + components.Clip(task, max(width/3, 8)))
		}
		right := detail
		if spend := st.Spend.In + st.Spend.Out; spend > 0 {
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
