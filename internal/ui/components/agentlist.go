package components

// The sub-agent manager (docs/interface/surfaces.md#the-agent-manager). It
// is a live list you can attach to, cancel and kill from, and it makes
// it the place a blocked child is answered. Opening the manager *because*
// something needs you and then being sent into that child's session just to
// say yes is a detour the list can spare you, so the approval card renders
// over the list and hands the list back.
//
// A row's progress is a fan-out lane's progress in list form: both read the
// same AgentProgress, so what the transcript says about a child and
// what the manager says about it cannot drift apart.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// AgentState is one agent row's lifecycle state
// (docs/interface/surfaces.md#the-agent-manager).
type AgentState int

const (
	AgentCurrent AgentState = iota // ● the agent whose surface is shown
	AgentRunning                   // ◇ working
	AgentBlocked                   // ⚠ waiting on the user
	AgentDone                      // ◇ finished, ✓ in the outcome field
	AgentFailed                    // ✗ failed
	// AgentOffer is not an agent: it is the row at the foot of the list that
	// opens the profile drafter (docs/interface/surfaces.md#the-agent-manager).
	// The manager is where a person goes to ask what this session has, which
	// makes it the one place where "and none of these is what I want" is a
	// thought somebody is already having. Its glyph is the start screen's ⚙
	// — starting something new — rather than one of its own.
	AgentOffer
	// AgentRole is not an agent either: it is one of the roles this session
	// can spawn, in the short section above the offer row
	// (docs/interface/surfaces.md#the-agent-manager). It wears the sub-agent's
	// own ◇ unlit, because that is what the row would become if somebody
	// spawned it, and the field on the right says where the file that
	// describes it lives rather than how it is doing.
	AgentRole
)

// AgentRow is one agent in the list: identity, task label, live status, and
// spend.
type AgentRow struct {
	State  AgentState
	Name   string
	Task   string
	Status string
	Spend  string
	// Progress is the child's live progress, rendered by the fan-out lane's
	// renderer. Nil for a row with no child progress to draw — the
	// orchestrator, which is not a child — and those rows fall back to
	// Status and Spend.
	Progress *AgentProgress
	// Note is the line under the row: what a blocked child is waiting for,
	// why a failed one failed. `⚠ needs you` without saying what for sends
	// the reader looking, and so does `failed`.
	Note string
	// Answerable marks a blocked row whose pending approval can be answered
	// here; Retryable marks a failed row that can be run again on its
	// original task; Editable marks a role row with a file behind it, which
	// the roles shhh ships have not got. Each gates a key, because a key
	// offered where it does nothing is not an offer.
	Answerable bool
	Retryable  bool
	Editable   bool
	// PatchKept marks a stopped writer holding a change that never reached
	// the checkout. It gates [p], and it takes the outcome field the way a
	// blocked child's `⚠ needs you` does, because it is the one thing left
	// to do about the row
	// (docs/capabilities/subagents.md#a-failed-child-leaves-a-handoff).
	PatchKept bool
	// TakesFollowUp marks a finished child that can still be spoken to. It
	// is what puts [s] on a done row: a message to a child that has
	// answered is a follow-up on its own conversation, one more turn on
	// everything it already read
	// (docs/capabilities/subagents.md#three-can-steer-a-child-and-none-of-them-can-end-it).
	TakesFollowUp bool
	// Depth is how far under the session the agent sits — 0 for the
	// orchestrator, 1 for a child it spawned, 2 for that child's own child —
	// in the numbering the rail's map and a fan-out lane use for the same
	// agent. A depth past 1 draws the row one column in behind a corner
	// (docs/capabilities/subagents.md#a-child-may-delegate-to-a-configured-depth).
	Depth int
}

// isAgent reports a row the keys that act on an agent may act on. The two
// rows at the foot of the list — a spawnable role, and the offer to draft one
// — are not agents, and every one of those keys is silent over them.
func (r AgentRow) isAgent() bool { return r.State != AgentOffer && r.State != AgentRole }

// steerable reports that this row can still take a message: a child — a row
// with progress of its own, which the orchestrator has none of — that is
// queued, running or blocked, or one that has answered and can be asked
// again. A child that failed has nothing left to redirect — running it again
// is [r] — and the supervisor refuses one, so the key is not offered over it
// (docs/capabilities/subagents.md#three-can-steer-a-child-and-none-of-them-can-end-it).
func (r AgentRow) steerable() bool {
	if r.Progress == nil {
		return false
	}
	return !r.Progress.State.settled() || r.followsUp()
}

// followsUp reports a finished row whose message would be a follow-up
// rather than a redirect, which the key and its field say in those words.
func (r AgentRow) followsUp() bool {
	return r.Progress != nil && r.Progress.State == FanoutDone && r.TakesFollowUp
}

// AgentAction is what the user asked to do with the focused row.
type AgentAction int

const (
	// AgentNone is the zero value, and it leads the list so that a key the
	// surface answered without asking the host for anything cannot be read as
	// the first action in it. The result is a value rather than an interface,
	// so there is no nil left to mean this.
	AgentNone     AgentAction = iota
	AgentAttach               // enter — attach to the agent's surface
	AgentCancel               // x — cancel its current turn
	AgentKill                 // X — kill the agent
	AgentKillAll              // K — kill every child still running
	AgentAnswer               // a — answer its pending approval in place
	AgentSteer                // s — redirect it with the note typed on its row
	AgentRetry                // r — run a failed agent again on its task
	AgentReview               // p — review a stopped writer's kept patch
	AgentDraft                // enter on the offer row — draft a profile
	AgentOpenRole             // enter on a role row — open its file in the editor
	AgentBack                 // esc — dismiss the list
)

// AgentListResult is the agent-list Update result.
type AgentListResult struct {
	Action AgentAction
	Index  int
	// Text is what was typed into the row's field, for the one action that
	// carries words: the redirect. Empty for every other action, which is
	// what a field nobody opened has to say.
	Text string
}

// AgentList is the sub-agent manager list, following the selector
// visual language. The host keeps Rows current while the list is open — it is
// a live view.
type AgentList struct {
	Rows     []AgentRow
	Focus    int
	MaxLines int
	// list is the shared pointer and window (list.go). A fan-out wide
	// enough to overflow this card is itself the problem the screen should be
	// showing, which is why the manager went unwindowed at first — but a
	// list the pointer can walk off the bottom of is worse than a wide
	// fan-out, so it scrolls now, on the same code every other list uses.
	// Blocked children never scroll: they are pinned above the window, so
	// opening the manager because something needs you always shows you the
	// thing that does. Its items are the scrolling half's own positions,
	// which is why they are indices rather than rows.
	list List[int]
	// steer is the one-line field a redirect is typed into, open under the
	// row it will reach and holding the keyboard while it is — the field the
	// question card opens, doing here what it does there. Nil when no
	// redirect is being typed.
	steer *NoteBox
	// steerAt is the name of the row the open field is aimed at, and not its
	// index: the host rebuilds and re-sorts these rows on every frame, so a
	// child that blocks while somebody is typing moves the index out from
	// under the field. The one thing a redirect must never do is reach an
	// agent nobody aimed it at.
	steerAt string
}

// split divides the rows into the ones pinned above the window and the ones
// it scrolls. The pinned run is the head of the list while it is the current
// agent or a blocked child — which, given the sort the host owes this list
// (blocked children to the top, below the orchestrator), is exactly the
// orchestrator and everyone waiting on an answer. It is the leading run
// rather than every blocked row anywhere, because the component does not sort
// its own rows: a sort that happens in here is a sort nobody can check
// against the transcript, and a blocked child that ended up below the fold is
// a host that did not sort rather than a row this one should move.
//
// A nested row is pinned with the row it hangs off, and a parent is pinned
// with the request under it: what floated to the head of the list is the
// whole group, and half a group above the window is a corner under nothing
// (docs/capabilities/subagents.md#a-child-may-delegate-to-a-configured-depth).
func (l *AgentList) split() (pinned, scrolling []int) {
	depths := make([]int, len(l.Rows))
	for i, r := range l.Rows {
		depths[i] = r.Depth
	}
	groups := depthGroups(depths)
	at := 0
	for ; at < len(groups); at++ {
		holds := false
		for _, i := range groups[at] {
			s := l.Rows[i].State
			holds = holds || s == AgentCurrent || s == AgentBlocked
		}
		if !holds {
			break
		}
		pinned = append(pinned, groups[at]...)
	}
	for ; at < len(groups); at++ {
		scrolling = append(scrolling, groups[at]...)
	}
	return pinned, scrolling
}

// focused is the row the keys act on.
func (l *AgentList) focused() AgentRow {
	if l.Focus < 0 || l.Focus >= len(l.Rows) {
		return AgentRow{}
	}
	return l.Rows[l.Focus]
}

// moved applies the manager's movement keys over the whole list, the pinned
// rows included, and reports whether the keystroke was one of them. It is
// over Rows rather than over the scrolling half the window covers, because a
// blocked child is pinned above the window and is still a row the pointer
// walks through.
func (l *AgentList) moved(pressed string) bool {
	rows := List[AgentRow]{Items: l.Rows, Focus: l.Focus}
	moved := rows.Move(pressed, keys.Agent.Move)
	l.Focus = rows.Focus
	return moved
}

// answerable is the row [a] acts on: the one under the pointer where that row
// is waiting on an answer, and otherwise the first row in the list that is.
// It returns -1 where nothing is waiting.
//
// The pointer is asked first so that a reader who walked to a particular
// blocked child answers that one. Everywhere else the key acts on the head of
// the list, which — given the sort the host owes this list — is the child that
// has been waiting longest, and is the reason the manager was opened at all
// (docs/interface/surfaces.md#the-agent-manager).
func (l *AgentList) answerable() int {
	if l.focused().Answerable {
		return l.Focus
	}
	for i, r := range l.Rows {
		if r.Answerable {
			return i
		}
	}
	return -1
}

// liveChildren counts the children that can still be killed. A child is a row
// with progress of its own — the orchestrator has none and is not killed from
// here — and it is live until its own state has settled.
func (l *AgentList) liveChildren() int {
	n := 0
	for _, r := range l.Rows {
		if r.Progress != nil && !r.Progress.State.settled() {
			n++
		}
	}
	return n
}

// Update handles list keys. Cancel, kill, answer and retry resolve with
// done=false so the list stays open over the live view (the host performs the
// action and comes back); attach and esc dismiss it. [r] is silent on a row
// that does not offer it rather than reporting a failure the row already
// predicted, and [a] and [K] are silent when the list holds nothing for them.
func (l *AgentList) Update(msg tea.KeyPressMsg) (done bool, result AgentListResult) {
	if l.steer != nil {
		if l.settleSteer(); l.steer == nil {
			// The row went away under the field between frames. The keystroke
			// goes with it rather than acting on whichever agent now stands
			// where the pointer is.
			return false, AgentListResult{}
		}
		return false, l.typeSteer(msg)
	}
	switch pressed := msg.String(); {
	case l.moved(pressed):
	case keys.Is(pressed, keys.Agent.Attach):
		switch row := l.focused(); row.State {
		case AgentOffer:
			return true, AgentListResult{Action: AgentDraft, Index: l.Focus}
		case AgentRole:
			// A role shhh ships has no file, so there is nothing for the key
			// to open and it is silent rather than saying so.
			if row.Editable {
				return true, AgentListResult{Action: AgentOpenRole, Index: l.Focus}
			}
		default:
			return true, AgentListResult{Action: AgentAttach, Index: l.Focus}
		}
	case keys.Is(pressed, keys.Agent.Answer):
		if i := l.answerable(); i >= 0 {
			return false, AgentListResult{Action: AgentAnswer, Index: i}
		}
	case keys.Is(pressed, keys.Agent.KillAll):
		// The list rather than a row, so the index says so: a host that read
		// one off this action would be killing whichever child the pointer
		// happened to be resting on as well as all of them.
		if l.liveChildren() > 1 {
			return false, AgentListResult{Action: AgentKillAll, Index: -1}
		}
	case keys.Is(pressed, keys.Agent.Steer):
		if l.focused().steerable() {
			l.openSteer()
		}
	case keys.Is(pressed, keys.Agent.Retry):
		if l.focused().Retryable {
			return false, AgentListResult{Action: AgentRetry, Index: l.Focus}
		}
	case keys.Is(pressed, keys.Agent.Review):
		if l.focused().PatchKept {
			return false, AgentListResult{Action: AgentReview, Index: l.Focus}
		}
	case keys.Is(pressed, keys.Agent.Cancel):
		// A role row and the offer row are not agents, so the keys that act
		// on one are silent over them the way [a] and [r] are silent over a
		// row that cannot take them (invariant 5).
		if !l.focused().isAgent() {
			break
		}
		return false, AgentListResult{Action: AgentCancel, Index: l.Focus}
	case keys.Is(pressed, keys.Agent.Kill):
		if !l.focused().isAgent() {
			break
		}
		return false, AgentListResult{Action: AgentKill, Index: l.Focus}
	case keys.Is(pressed, keys.Agent.Back):
		return true, AgentListResult{Action: AgentBack, Index: -1}
	}
	return false, AgentListResult{}
}

// openSteer puts the field under the focused row with the keyboard in it. It
// is named for the child it will reach rather than labelled `note`, because a
// field that opened over a list of agents has to say which of them it is
// about.
func (l *AgentList) openSteer() {
	row := l.focused()
	l.steer = NewNoteBox()
	l.steer.Label = keys.Words(keys.Agent.Steer) + " " + row.Name
	l.steer.Field.Placeholder = "what it should do instead"
	if row.followsUp() {
		l.steer.Label = followUpWord + " " + row.Name
		l.steer.Field.Placeholder = "what to ask it next"
	}
	l.steer.Open()
	l.steerAt = row.Name
}

// typeSteer routes one key while the field holds the keyboard. Enter sends
// what was typed and esc closes the field with the child untouched; every
// other key is a character of the redirect, including the letters this
// surface answers with the field shut
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
//
// An empty field is silent on enter rather than refused: nothing has been
// asked of the reader here, so there is nothing to refuse them for, and a
// redirect with no words in it is not one.
//
// settleSteer has run, so the target is a row in this list.
func (l *AgentList) typeSteer(msg tea.KeyPressMsg) AgentListResult {
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Agent.Back):
		l.closeSteer()
	case keys.Is(pressed, keys.Agent.Attach):
		text := l.steer.Value()
		if text == "" {
			break
		}
		at := l.steerTarget()
		l.closeSteer()
		return AgentListResult{Action: AgentSteer, Index: at, Text: text}
	default:
		l.steer.Update(msg)
	}
	return AgentListResult{}
}

// settleSteer puts the pointer back on the row the open field is aimed at,
// and drops the field where that row has left the list. Both are the same
// fact: the host rebuilds and re-sorts these rows on every frame, and the
// field is tied to a child rather than to a position.
//
// A pointer left at its index would light one row while the field sat under
// another — and the window is positioned on the pointer, so it would then
// scroll to the lit row and take the field off the screen with it. A field
// under no row at all is worse: it would go on holding the keyboard out of
// sight.
func (l *AgentList) settleSteer() {
	if l.steer == nil {
		return
	}
	at := l.steerTarget()
	if at < 0 {
		l.closeSteer()
		return
	}
	l.Focus = at
}

// closeSteer puts the keyboard back on the list.
func (l *AgentList) closeSteer() { l.steer, l.steerAt = nil, "" }

// steerTarget is where the open field's child sits in the list now, or -1
// where it is no longer in it.
func (l *AgentList) steerTarget() int {
	if l.steer == nil {
		return -1
	}
	for i, r := range l.Rows {
		if r.Name == l.steerAt {
			return i
		}
	}
	return -1
}

// steerRows is the field as it is drawn under row i, indented to the line the
// row's own note takes: it is about that row, and a field drawn anywhere else
// on a list of agents is a field whose target has to be worked out.
func (l *AgentList) steerRows(i, inner int) []string {
	if l.steerTarget() != i {
		return nil
	}
	indent := detailIndent + max(l.Rows[i].Depth-1, 0)
	var rows []string
	for _, r := range l.steer.Rows(max(inner-indent, 8)) {
		rows = append(rows, strings.Repeat(" ", indent)+r)
	}
	return rows
}

// stateGlyph pairs every state with a glyph so monochrome terminals stay
// usable. A row here is a row, so it keeps the outcome table's rule: the two
// states that ask something of the reader take the column from the kind
// glyph, and the one that does not leaves it alone. A blocked child leads
// with `⚠` and a broken one with `✗`; a child that is running or has finished
// keeps `◇`, in the colour its lane wears, and says `✓ done` in the field on
// the right.
//
// That is where the manager and the transcript part company, and deliberately
// (docs/interface/surfaces.md#the-agent-manager).
// Only the orchestrator's `●` is the list's own.
func (r AgentRow) stateGlyph() string {
	switch r.State {
	case AgentCurrent:
		return sty.Headline.Render("●")
	case AgentBlocked:
		return AgentProgress{State: FanoutBlocked}.rowGlyph()
	case AgentFailed:
		return AgentProgress{State: FanoutFailed}.rowGlyph()
	case AgentDone:
		return AgentProgress{State: FanoutDone}.rowGlyph()
	case AgentOffer:
		return sty.Accent.Render("⚙")
	case AgentRole:
		// The sub-agent's own mark, unlit: a role is what a child is before
		// anybody spawns one, and a lit ◇ on this list is a child that is
		// running.
		return sty.Dimmer.Render("◇")
	default:
		return AgentProgress{State: FanoutRunning}.rowGlyph()
	}
}

// rightField is what the row reports: the lane renderer's outcome field for a
// child, and the plain status and spend for a row that has no child progress.
// The two rows that are not agents report where they lead instead — the
// command the offer opens, and the place a role's file lives.
func (r AgentRow) rightField() string {
	if !r.isAgent() {
		return sty.Dimmer.Render(r.Status)
	}
	if r.Progress != nil {
		if r.PatchKept {
			return keptPatchField(*r.Progress)
		}
		return r.Progress.outcomeField()
	}
	status := r.Status
	switch r.State {
	case AgentBlocked:
		status = sty.Err.Render("⚠ " + status)
	case AgentDone:
		// ✓ never takes an activity row's glyph column, so a finished child
		// keeps ◇ there and its tick stands in the outcome field, which is
		// where the outcome table puts it (stateGlyph).
		status = sty.Add.Render("✓ " + status)
	default:
		status = sty.Dim.Render(status)
	}
	if r.Spend != "" {
		status += "  " + sty.Status.Render(r.Spend)
	}
	return status
}

// keptPatchField is the outcome field of a row holding a kept patch: what is
// left to do about it, in the place `⚠ needs you` stands on a blocked row,
// with the counts after it. The glyph column already says how the child
// ended, so the field spends itself on the offer instead.
func keptPatchField(p AgentProgress) string {
	field := keptPatchOffer()
	if stats := p.stats(); stats != "" {
		field += sty.Dim.Render(detailSep) + stats
	}
	return field
}

// keptPatchOffer is `patch kept · [p] review`, one spelling for the manager's
// row and the rail's line under the same child.
func keptPatchOffer() string {
	return sty.Dimmer.Render("patch kept") + sty.Dimmer.Render(detailSep) +
		sty.Hint.Render(keys.Bracket(keys.Agent.Review)+" "+keys.Words(keys.Agent.Review))
}

// render lays one row out across the card's inner width, with its note (if
// any) indented underneath.
func (r AgentRow) render(inner int, focused bool) []string {
	right := r.rightField()
	// The corner takes the column before the glyph, which is where the rail's
	// map puts it on the same agent: the row is what moves, so the whole of
	// it moves.
	left := AgentNesting(r.Depth) + r.stateGlyph() + " " + r.Name
	if r.Task != "" {
		// The separator and not a gap, which is what the lane above this row
		// in the transcript joins the same two facts with: two spaces read as
		// a column that is not there, because the names are not one width and
		// so the tasks under them never line up
		// (docs/interface/surfaces.md#the-agent-manager).
		left += sty.Dimmer.Render(detailSep + Clip(r.Task, max(inner/3, 8)))
	}
	gap := inner - 2 - lipgloss.Width(left) - lipgloss.Width(right)
	row := left
	if gap >= 2 {
		row += strings.Repeat(" ", gap) + right
	} else {
		row = Clip(left, max(inner-2-lipgloss.Width(right)-2, 0)) + "  " + right
	}
	if focused {
		// The pointer keeps its own colour outside the highlight and the row
		// is lit behind it, which is the pair every list draws (LitRow).
		row = sty.FocusPointer.Render("❯") + " " + LitRow(row, 0, max(inner-GridPointerWidth, 0))
	} else {
		row = PointerColumn() + row
	}
	rows := []string{row}
	if r.Note != "" {
		// Under the row's own name rather than under a sibling of its
		// parent's: the line belongs to the row above it, and the row moved.
		rows = append(rows, indented(r.Note, detailIndent+max(r.Depth-1, 0), inner))
	}
	return rows
}

// followUpWord is what the steer key does over a child that has answered:
// the message is not a redirect of work in progress but the next question on
// work that is finished.
const followUpWord = "follow up"

// managerWayOut is what esc leaves the manager for. The list is a takeover
// over a turn that is still going, and `cancel` says nothing about which of
// the several things on screen is being left
// (docs/interface/principles.md#esc-is-always-the-safe-answer).
const managerWayOut = "back to the turn"

// hints are the keys the manager offers. Two of them are about the list and
// not about the pointer: answering in place is offered whenever any child is
// waiting on an answer, and killing every child whenever more than one is
// still running. A key that appears only once the pointer has found the row
// that needs it is a key a reader has to go hunting for, and an offer nobody
// can see is not distinguishable from an offer that is not there. The keys
// that end one child stay with the row the pointer is on, because their
// target is the one thing that must never be guessed
// (docs/interface/surfaces.md#the-agent-manager).
func (l *AgentList) hints() []KeyOffer {
	if l.steer != nil {
		// The field holds the keyboard, so the list's own letters are not
		// live and are not drawn as offers. What is left is the two keys any
		// surface being typed into keeps.
		return []KeyOffer{
			keyOfferAs(keys.Agent.Attach, "send it"),
			keyOfferAs(keys.Agent.Back, "leave it unsent"),
		}
	}
	focus := l.focused()
	// The two rows that are not agents are the rows enter does something else
	// on, so the key row says which — a hint that read `enter attach` over one
	// of them would be naming an action the row does not have. The two
	// list-wide keys are still offered over them, because they still work over
	// them.
	agent := focus.isAgent()
	var segments []KeyOffer
	switch {
	case agent:
		segments = append(segments, keyOffer(keys.Agent.Attach))
	case focus.State == AgentOffer:
		segments = append(segments, keyOfferAs(keys.Agent.Attach, "draft a profile"))
	case focus.Editable:
		segments = append(segments, keyOfferAs(keys.Agent.Attach, "open its file"))
	}
	if l.answerable() >= 0 {
		segments = append(segments, keyOfferAs(keys.Agent.Answer, "answer without attaching"))
	}
	switch {
	case agent && focus.followsUp():
		segments = append(segments, keyOfferAs(keys.Agent.Steer, followUpWord))
	case agent && focus.steerable():
		segments = append(segments, keyOffer(keys.Agent.Steer))
	}
	if agent && focus.Retryable {
		segments = append(segments, keyOffer(keys.Agent.Retry))
	}
	if agent && focus.PatchKept {
		segments = append(segments, keyOffer(keys.Agent.Review))
	}
	if agent {
		segments = append(segments, keyOffer(keys.Agent.Cancel), keyOffer(keys.Agent.Kill))
	}
	if l.liveChildren() > 1 {
		// What it reaches, on the key itself: kill-all walks the whole tree,
		// and a reader counting the rows in front of them would otherwise be
		// counting one level of it
		// (docs/capabilities/subagents.md#a-child-may-delegate-to-a-configured-depth).
		segments = append(segments, keyOfferAs(keys.Agent.KillAll,
			keys.Words(keys.Agent.KillAll)+detailSep+"every level"))
	}
	return append(segments, keyOfferAs(keys.Agent.Back, managerWayOut))
}

// tally is the manager's title-rail summary: the same sentence the fan-out
// header states, about the children this list holds. The orchestrator is not
// a child and is left out of it.
func (l *AgentList) tally() string {
	var states []FanoutState
	for _, r := range l.Rows {
		if r.State != AgentOffer && r.Progress != nil {
			states = append(states, r.Progress.State)
		}
	}
	if len(states) == 0 {
		return ""
	}
	return stateTally(states)
}

// visibleRows renders the scrolling half of the list windowed to a body
// budget, with the markers the window makes necessary. An agent is one row
// plus the line under it that says what it is waiting for or why it failed,
// and every agent is a row the pointer can land on, so the markers count
// agents rather than lines.
func (l *AgentList) visibleRows(width, budget int, scrolling []int) []string {
	inner := width - cardFrameWidth
	n := len(scrolling)
	focus := -1
	for pos, i := range scrolling {
		if i == l.Focus {
			focus = pos
		}
	}
	l.list.Items, l.list.Focus = scrolling, focus
	l.list.Rows = func(pos int) int {
		lines := 1
		if l.Rows[scrolling[pos]].Note != "" {
			lines++
		}
		// The open field is part of the row it is aimed at, so the window
		// buys them together or not at all.
		lines += len(l.steerRows(scrolling[pos], inner))
		return lines
	}
	lo, hi := l.list.Range(budget)
	var rows []string
	if lo > 0 {
		rows = append(rows, ListOverflowRow("↑", lo, "", width-cardFrameWidth))
	}
	for pos := lo; pos < hi; pos++ {
		i := scrolling[pos]
		rows = append(rows, l.Rows[i].render(inner, i == l.Focus)...)
		rows = append(rows, l.steerRows(i, inner)...)
	}
	if hi < n {
		rows = append(rows, ListOverflowRow("↓", n-hi, "", width-cardFrameWidth))
	}
	return rows
}

func (l *AgentList) View(width int) string {
	l.settleSteer()
	inner := width - cardFrameWidth
	// The key hints and the pinned blocked children come off the budget
	// before the window is drawn: the list scrolls under them, and the window
	// may never buy itself a row.
	hints := hintRows(l.hints(), width)
	pinned, scrolling := l.split()
	var rows []string
	for _, i := range pinned {
		rows = append(rows, l.Rows[i].render(inner, i == l.Focus)...)
		rows = append(rows, l.steerRows(i, inner)...)
	}
	rows = append(rows, l.visibleRows(width, bodyBudget(l.MaxLines, len(hints)+len(rows)), scrolling)...)
	rows = append(rows, hints...)
	rows = boundRows(rows, l.MaxLines)
	// Info, like every other card that is waiting to be answered rather than
	// read: the manager attaches, answers, cancels and kills, and none of
	// that is rated, so there is no severity to take the frame's colour from
	// (CardTone).
	card := Card{Title: "Agents", Tone: CardDecision}
	if tally := l.tally(); tally != "" {
		card.Chips = []string{tally}
	}
	return card.Render(rows, width)
}
