package chat

// The key route: one press, and which surface answers it.
//
// The ladder used to name every mode twice — once as the state it is in and
// once as the handler that answers it — in a rung of its own each, so a mode
// reached the keyboard only if somebody remembered to write one. It asks the
// register instead (overlay.go): which mode the session is in, whether that
// mode answers the quit chord itself, and what it does with the key are all
// one row there.
//
// What is left is the order the routes are tried in, which is the part that
// is genuinely about keys and not about modes: the two chords answered above
// every surface, the open history search, the full-screen viewers, the
// handover and its grace window, then the modes, then the draft.

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// updateKey routes one key press. handled is false when nothing on the
// surface claimed it: the key is then the start of a sentence and belongs to
// the draft, so the session comes back stamped either way — the arrival
// clock, the two-press window and the grace settle all happen on the way in,
// whoever ends up answering.
func (m Model) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	// Every key stamps the clock a decision's arrival reads (
	// interrupt.go). The open grace window is settled first, against the
	// quiet before this key — the key that ends the quiet must not count
	// as part of it. Stamped here rather than on the draft's own path
	// because the question is whether the reader is at the keyboard, not
	// which surface they were talking to.
	m.settleGrace(time.Now())
	m.lastKeypress = time.Now()
	// And every key consumes an armed two-press window (cancel.go),
	// whichever surface answers it: a reader who went on typing — or
	// answered a card — was not confirming anything. The draft's own
	// handlers below read the captured value and re-arm as their answer.
	armed := m.armed
	m.disarm()
	// Mouse reporting is the one setting with a chord of its own (
	// reading mode), and the only key answered before the surfaces are: what it
	// costs — the terminal's own click-drag selection — is discovered at
	// the moment of wanting to copy something, with a mouse already in
	// hand and no appetite for a slash command. That moment arrives just
	// as often over the full-screen diff or a transcript being read as it
	// does over the draft, so the chord is answered above all of them.
	// Nothing else claims it, so nothing is taken away by that.
	if keys.Match(msg, keys.Draft.Mouse) {
		return answered(m.toggleMouse())
	}
	// The redraw is answered here for a related reason (terminal.go): a
	// screen written over by a notification or by whatever ran during a
	// suspend is a screen the reader wants back wherever they are, and
	// the surfaces most worth repainting — the full-screen diff, review
	// mode, a pager — are exactly the ones a key routed below this would
	// never reach. It takes nothing from them: it repaints from the model
	// and changes nothing in it, and no sentence can produce the chord.
	if keys.Match(msg, keys.Draft.Redraw) {
		return answered(m.redraw())
	}
	// The open history search reads every key first: typing filters
	// rather than edits, which is the whole difference between the search
	// and the draft it sits under (historysearch.go). Only while the
	// draft actually holds the keyboard — a surface that arrived on its
	// own mid-search (an approval that landed on a quiet draft, the retry
	// countdown) has taken it, and a search left open under one would eat
	// the surface's keys invisibly. The search closes and gives the draft
	// back as it was, and the key goes to whatever is really listening.
	if m.historySearching() {
		if m.inputLive() {
			return answered(m.updateHistorySearch(msg))
		}
		m.closeHistorySearch(true)
	}
	// The full-screen viewers answer before the handover chord and the
	// grace window below: two of them are where a decision card's own [d]
	// goes, so the reader is inside the card's detail and a chord that would
	// gate the card behind it is not what the key means there
	// (the register's aboveDecision, overlay.go).
	if o := overlayFor(m.state); o != nil && o.aboveDecision {
		return answered(m.routeOverlay(o, msg))
	}
	// The handover means one thing in both states a decision can be in:
	// give the card the whole keyboard. From ungated it is the mid-sentence
	// rule's transfer
	// — every letter belonged to the draft, and now none do. From a card
	// holding the keyboard by arrival it buys the keys that card left
	// alone on purpose ([a], [d], [A]).
	if m.interruptShowing() && keys.Match(msg, keys.Draft.Answer) && (m.decisionUngated() || m.heldOnArrival) {
		return answered(m.gateDecision())
	}
	// The grace window on a card that took the keyboard by arriving on a
	// warm keyboard (interrupt.go): the keys that would answer it are
	// discarded until the keyboard has been quiet for a beat, because a
	// key this soon after typing was part of the typing. The discard
	// still moved the window's end, so the repaint is rescheduled off
	// the bumped sequence (graceTickCmd).
	if m.graceHolds() && m.graceDiscards(msg.String()) {
		m.graceSeq++
		return m, nil, true
	}
	// A decision that arrived on top of a sentence is inert until it holds the
	// keyboard
	// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard):
	// ungated, the handover above is the only key that is its own, and every
	// letter belongs to the draft.
	if !m.decisionUngated() {
		if keys.Match(msg, keys.Draft.Clear) && m.escLeavesWaiting() {
			// Esc leaves the decision waiting rather than denying it; [n]
			// is how you say no.
			return answered(m.ungateDecision())
		}
		// The two decision cards, which are the only modes the
		// register places floating: they answer only once the handover
		// has given them the keyboard.
		if o := overlayFor(m.state); o != nil && o.place == placeFloating {
			return answered(m.routeOverlay(o, msg))
		}
	}
	// Every other mode. Which of them the state is in, whether it answers
	// the quit chord itself and what it does with the key are all the
	// register's answer now, so a mode added to it is routed without a
	// rung of its own here (overlay.go).
	//
	// A draining retry countdown is one of them: it owns the keyboard the
	// way the confirm prompt does — nothing is streaming, the input is
	// not live, and the wait offers two keys that both end it.
	if o := overlayFor(m.state); o != nil && o.place != placeFloating {
		return answered(m.routeOverlay(o, msg))
	}
	// The agent manager and a child agent's routed approval cover
	// whatever mode the state is in, so they are found on the session
	// rather than in it (coverOverlay). Both take over the bottom panel
	// and defer to the parent's own prompts above; like every other
	// decision that arrives unbidden the child's ask is inert until the
	// handover gives it the keyboard, which is why the ask is only
	// reached while the keyboard is held.
	if m.agentList != nil || m.decisionHeld {
		if o := m.coverOverlay(); o != nil {
			return answered(m.routeOverlay(o, msg))
		}
	}
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Draft.Quit):
		// Quitting over a live turn is a question, not a chord: the
		// confirm names what it would cancel and what the autosave
		// keeps (docs/interface/surfaces.md#the-inline-confirm).
		if m.working() {
			return answered(m.openQuitConfirm())
		}
		if armed.open(armQuit) {
			return m, m.quitNow(), true
		}
		return m, m.armPress(armQuit, keys.Shown(keys.Draft.Quit)), true
	case keys.Is(pressed, keys.Draft.Cancel):
		// While attached, Ctrl+C acts on the child: cancel its turn.
		if m.attachedTo != "" {
			return answered(m.attachedCancel())
		}
		if m.state == stateClassifying {
			// Skip the classifier check and ask the user directly. The
			// card arrives through the one door every decision arrives
			// through, so it takes an empty draft's keyboard like any
			// other arrival instead of showing keys nobody can press.
			if m.classifierCancel != nil {
				m.classifierCancel()
				m.classifierCancel = nil
			}
			m.setTurnState(stateConfirmRun)
			m.syncViewport()
			return m, nil, true
		}
		if m.state == stateRunningCmd {
			if m.runCancel != nil {
				m.runCancel()
			}
			return m, nil, true
		}
		if m.state == stateStreaming || m.state == stateCloseGate {
			// The first press arms the cancel and the rails say so;
			// only a second within the window abandons the turn
			// (docs/interface/surfaces.md#the-input-frame). The
			// scoped cancels — a child, the classifier, a running
			// command, and a compaction, which costs one summary
			// request and keeps the conversation — stay single-press:
			// those are reversible acts, not minutes of work.
			if m.compacting || armed.open(armCancel) {
				return answered(m.cancelTurnNow())
			}
			return m, m.armPress(armCancel, keys.Shown(keys.Draft.Cancel)), true
		}
		if m.heldAtBoundary() {
			// A held turn is what the chord has to reach next: the turn
			// is parked rather than finished, and without this the two
			// presses fall through to the empty idle draft below and
			// quit the session instead of giving the turn up (hold.go).
			if armed.open(armCancel) {
				m.dropHold()
				m.setTurnState(stateStreaming)
				return answered(m.cancelTurnNow())
			}
			return m, m.armPress(armCancel, keys.Shown(keys.Draft.Cancel)), true
		}
		if m.decisionUngated() {
			// Ctrl+C keeps the meaning the card has always given it: it
			// answers the decision no. No draft can produce the chord, so
			// leaving it live is what keeps a waiting decision endable
			// without first taking the keyboard.
			m.decisionHeld = true
			return answered(m.routeDecision(msg))
		}
		if strings.TrimSpace(m.input.Value()) != "" {
			m.input.Reset()
			m.historyIdx = len(m.inputHistory)
			return m, nil, true
		}
		// An empty idle draft: the chord means quit, and quitting is
		// two presses like every other way of walking away.
		if armed.open(armQuit) {
			return m, m.quitNow(), true
		}
		return m, m.armPress(armQuit, keys.Shown(keys.Draft.Cancel)), true
	case keys.Is(pressed, keys.Draft.Mode):
		// Cycle the permission mode; attached, it cycles the
		// child's mode clamped to the orchestrator's ceiling.
		if m.attachedTo != "" {
			return answered(m.cycleAttachedMode())
		}
		m.applyMode(agent.NextMode(m.policy.cycle, m.policy.mode))
		return m, nil, true
	case keys.Is(pressed, keys.Draft.Agents):
		// Agent manager; without a supervisor the key keeps its
		// textarea meaning (character back).
		if m.subagents != nil {
			return answered(m.openAgentList())
		}
	case keys.Is(pressed, keys.Draft.NextAgent):
		// One step along the rail's session map, which is where the map
		// is walked from: opening the manager to move between two
		// sessions is a surface in the way of a look, and the rail is
		// already showing the row being moved to (attach.go).
		return answered(m.cycleAgent(1))
	case keys.Is(pressed, keys.Draft.PrevAgent):
		return answered(m.cycleAgent(-1))
	case keys.Is(pressed, keys.Draft.Attach):
		// Ctrl+V used to be the textarea's own text paste. It reads the
		// clipboard properly now: a screenshot or a copied file is
		// staged as an attachment, and plain text still lands in the
		// draft (attachments.go). Attached to a child, the
		// orchestrator's staging area is not what the keyboard is
		// pointed at, so the key keeps its textarea meaning there.
		if m.inputLive() && m.attachedTo == "" {
			return m, readClipboardCmd(), true
		}
	case keys.Is(pressed, keys.Draft.Editor):
		// The draft goes out to the reader's own editor and comes back
		// (editor.go). It is the one key here that stops the world:
		// the program is suspended while the editor has the terminal, so
		// it is refused with a notice rather than queued whenever
		// something is still happening on this screen.
		//
		// It costs the textarea's own ctrl+g, which selected everything
		// in the box. Nothing in shhh offered that key or said it was
		// there, and what it selected could only be deleted or replaced
		// — which is what the editor is for.
		return answered(m.openEditor())
	case keys.Is(pressed, keys.Draft.Suspend):
		// The shell's own chord for a foreground job, answered here
		// because a terminal in raw mode will not act on it — and
		// refused while work is in flight, because a stopped process
		// cannot keep a stream open (terminal.go).
		return answered(m.suspend())
	case keys.Is(pressed, keys.Draft.Reasoning):
		// Reasoning effort: the level the next request asks for.
		// It changes nothing about the conversation and nothing about the
		// turn in flight, so like the rest of the live surfaces it is
		// answered while one runs — and it is a chord, so the draft below
		// keeps every letter it has.
		if m.inputLive() && m.attachedTo == "" {
			next, note := m.cycleReasoning()
			m = next
			m.appendEntry(entry{kind: entrySystem, text: note})
			m.syncViewport()
			return m, nil, true
		}
	case keys.Is(pressed, keys.Draft.HistorySearch):
		// Reverse search over the input ring: the shell's own key,
		// doing in the draft what it does at the shell. Attached, the
		// orchestrator's history is not what the keyboard is pointed at,
		// so the chord is inert there — the textarea claims it for
		// nothing either.
		if m.inputLive() && m.attachedTo == "" {
			return answered(m.openHistorySearch())
		}
	case keys.Is(pressed, keys.Draft.Palette):
		// The command palette: one prompt over the commands, the
		// saved chats and the files this session has touched. It reads
		// the session without touching the conversation, so it opens
		// over a running turn like the rest of the live surfaces — and
		// over a draft, because tab writes the answer *into* the draft.
		// The chord costs the textarea's own line-up, which the arrows
		// still do; input history keeps ↑/↓ and never claimed this key.
		// Attached, the orchestrator's commands are not what the keyboard
		// is pointed at, so the key keeps its textarea meaning there.
		if m.inputLive() && m.attachedTo == "" {
			return answered(m.openPalette())
		}
	case keys.Is(pressed, keys.Draft.Pause):
		// Hold the turn at its next round boundary, or let a held one go
		// (hold.go). Attached, the keyboard is pointed at a child and
		// the parent's turn is not what the chord is about, so it keeps
		// its textarea meaning there — as it did when the palette had it.
		if m.inputLive() && m.attachedTo == "" {
			return answered(m.toggleHold())
		}
	case keys.Is(pressed, keys.Draft.Reading):
		// Focus mode: navigate and expand transcript rows; scoped
		// to whichever agent is focused. It reads the transcript
		// without touching the conversation, so it opens over a running
		// turn too — the turn keeps streaming underneath.
		if m.inputLive() {
			return answered(m.enterFocusMode())
		}
	case keys.Is(pressed, keys.Draft.KeyList):
		// `?` on an empty draft prints the keys as a system row — the
		// door Claude Code taught. With any text in the box it is a
		// letter, because the input owns every ordinary key the moment
		// there is a draft
		// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
		if m.inputLive() && m.attachedTo == "" && !m.completionActive() &&
			strings.TrimSpace(m.input.Value()) == "" && !m.browsingHistory() {
			return answered(m.systemNotice(helpKeysText()))
		}
	case keys.Is(pressed, keys.Draft.PageUp, keys.Draft.PageDown):
		// The pager keys read the transcript and leave the keyboard in
		// the draft. Reading is not a decision — the wheel
		// has always said so — and the reader scrolling back to check a
		// path mid-sentence is not asking to stop writing the sentence.
		// Paging used to hand the keyboard over, which took the draft off
		// the screen to answer a question about the pane above it.
		//
		// No draft can produce these keys, which is what makes them safe
		// to spend here and why the letters bubbles binds to the same job
		// (j/k/u/d/f/b and the spacebar) are still not offered at all.
		if m.inputLive() {
			dir := -1
			if keys.Is(pressed, keys.Draft.PageDown) {
				dir = 1
			}
			m.scrollPage(dir)
			return m, nil, true
		}
	case keys.Is(pressed, keys.Draft.ScrollUp):
		// The same job by a line rather than a page. Both
		// chords are bound because terminals disagree about which they
		// report: the textarea underneath claims neither, and neither is
		// reachable by typing.
		if m.inputLive() {
			m.scrollLines(-keyScrollLines)
			return m, nil, true
		}
	case keys.Is(pressed, keys.Draft.ScrollDown):
		if m.inputLive() {
			m.scrollLines(keyScrollLines)
			return m, nil, true
		}
	case keys.Is(pressed, keys.Draft.Clear):
		// A visible selection is what esc cancels first.
		// It is the only thing on the surface esc could mean while one
		// is lit, and it says so without touching the draft — a reader
		// who selected the wrong six screens has not also abandoned the
		// sentence they were writing.
		if m.cancelSelection() {
			m.refreshTranscript()
			return m, nil, true
		}
		// With the completion menu open, esc only dismisses the menu; the
		// draft survives and further typing re-opens it.
		if m.completionActive() {
			m.dismissCompletions()
			m.syncViewport()
			return m, nil, true
		}
		// The input is live in every non-confirm state, so esc
		// clears the draft; attached with an empty draft it pops one
		// breadcrumb level.
		if m.attachedTo != "" && strings.TrimSpace(m.input.Value()) == "" {
			m.detachOne()
			return m, nil, true
		}
		// Empty draft while the turn streams: nothing at all. Esc is
		// the key that leaves whatever is open, and the reflex that
		// closes a diff or drops a selection arrives at a draft that
		// happens to be empty often enough that letting it interrupt
		// costs a turn nobody meant to stop. The two-press window is fed
		// by the cancel chord alone
		// (docs/interface/surfaces.md#the-input-frame).
		//
		// Inert here means inert: whatever two-press window was open is
		// put back, because every key consumes one on the way in
		// (cancel.go) and a key that does nothing must not be the thing
		// that changes what the next press means.
		if strings.TrimSpace(m.input.Value()) == "" && m.state == stateStreaming {
			m.armed = armed
			return m, nil, true
		}
		// Esc esc on an empty idle draft opens the rewind picker — the
		// gesture three of the five harnesses share, over the surface
		// /rewind already owns (docs/interface/surfaces.md#the-input-frame).
		// Idle only: attached, the press above was the detach, and while
		// a turn is live it was nothing — a picker offering to unwind a
		// conversation the model is still writing into would be asking
		// about a shape that has not settled. The window is short because
		// the two presses are one gesture, not a press and a decision. A
		// session with nothing to rewind to arms nothing: esc on an empty
		// idle draft has always meant nothing, no rail advertises the
		// gesture, and the alternative was a reflexive double-esc
		// spending the start screen on a notice about a picker that
		// cannot open.
		if strings.TrimSpace(m.input.Value()) == "" && m.state == stateInput &&
			len(m.checkpoints) > 0 {
			if armed.open(armRewind) {
				return answered(m.openRewindPick())
			}
			return m, m.armPressFor(armRewind, keys.Shown(keys.Draft.Clear), rewindPressWindow), true
		}
		m.input.Reset()
		m.historyIdx = len(m.inputHistory)
		return m, nil, true
	case keys.Is(pressed, keys.Draft.Complete):
		// Tab writes the focused completion into the input. A file
		// mention also stages an image, exactly as enter would
		// (mention.go).
		if m.completionActive() {
			if m.complete.files {
				return answered(m.insertMention())
			}
			m.acceptCompletion()
			m.syncViewport()
			return m, nil, true
		}
	case keys.Is(pressed, keys.Draft.HistoryPrev):
		if m.completionActive() {
			if m.complete.idx > 0 {
				m.complete.idx--
			}
			m.complete.moved = true
			return m, nil, true
		}
		// The start screen's suggestion list claims ↑↓ only while it is
		// live: an empty draft on a session that has not started yet,
		// which is also the only time the input history has nothing to
		// browse.
		if next, claimed := m.startKey("up"); claimed {
			return next, nil, true
		}
		// Recall is the draft's, wherever the draft has the keyboard
		//. It used to be the idle turn's: ↑ did nothing
		// while the agent streamed, ran a command or waited on the
		// classifier, and nothing under an approval card that had not
		// been given the keyboard — the four states steering and the
		// mid-sentence rule exist
		// to keep the sentence live in. A key that is the input's in
		// every state and a frame that is live but cannot recall
		// were two claims about the same three lines, and the code was
		// answering the older one.
		if m.inputLive() && len(m.inputHistory) > 0 &&
			(m.browsingHistory() || strings.TrimSpace(m.input.Value()) == "") {
			if m.historyIdx > 0 {
				m.historyIdx--
				m.input.SetValue(m.inputHistory[m.historyIdx])
			}
			return m, nil, true
		}
		// ↑ used to hand the keyboard to the transcript on an empty draft
		// with no history to recall. It is the input's key in every other
		// state, and a key that changes surface depending on how much
		// history a session happens to have is one nobody can learn
		//. Alternate scroll made it worse than unlearnable: on a
		// terminal that synthesises arrows for the wheel, a flick opened
		// reading mode (altscroll.go). Scrolling has its own keys now.
	case keys.Is(pressed, keys.Draft.HistoryNext):
		if m.completionActive() {
			if m.complete.idx < len(m.complete.items)-1 {
				m.complete.idx++
			}
			m.complete.moved = true
			return m, nil, true
		}
		if next, claimed := m.startKey("down"); claimed {
			return next, nil, true
		}
		if m.inputLive() && m.browsingHistory() {
			m.historyIdx++
			if m.historyIdx >= len(m.inputHistory) {
				m.historyIdx = len(m.inputHistory)
				m.input.Reset()
			} else {
				m.input.SetValue(m.inputHistory[m.historyIdx])
			}
			return m, nil, true
		}
	case keys.Is(pressed, keys.Draft.FollowUp):
		// Alt+enter with a turn live queues the draft for after it
		// (followup.go); everywhere else — idle, attached, an empty
		// box — it falls through to the textarea's newline.
		if next, cmd, claimed := m.queueFollowUp(); claimed {
			return next, cmd, true
		}
	case keys.Is(pressed, keys.Draft.PullQueued):
		// Alt+↑ takes the newest queued message — follow-up first,
		// else steering — back into the draft.
		if next, cmd, claimed := m.pullQueued(); claimed {
			return next, cmd, true
		}
	case keys.Is(pressed, keys.Draft.Send):
		// A trailing backslash turns this enter into a newline, the
		// shell's own continuation (continuation.go).
		if m.holdSend() {
			return m, nil, true
		}
		// While attached, Enter acts on the child: scoped commands and
		// mid-turn steering.
		if m.attachedTo != "" {
			return answered(m.attachedSubmit())
		}
		// Enter on a live start screen types the focused suggestion and
		// submits it, so choosing an offer and typing it are the same
		// act down to the dispatch.
		if action := m.startAction(); action != "" {
			m.input.SetValue(action)
			return answered(m.submitInput())
		}
		// One submit path for every state that keeps the input live
		// (command.go): commands run, plain text is a message when
		// idle and steering while the agent works.
		if m.inputLive() {
			return answered(m.submitInput())
		}
	}
	return m, nil, false
}
