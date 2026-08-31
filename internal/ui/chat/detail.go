package chat

// Step detail (docs/interface/surfaces.md#the-step): /step opens the
// detail bodies of one step's rows — `/ui verbosity high` scoped to a single
// step, and nothing else on the screen moves.
//
// The transcript had two ways to see what a call actually returned and a gap
// between them. Reading mode's [enter] opens one row, unbounded, and costs a
// keyboard handover to reach; `/ui verbosity high` opens every row of every
// step in the session and is a setting rather than a gesture. What was
// missing is the question a reader actually asks — *what did this step do* —
// and the moment they ask it is usually mid-turn, with a half-written
// sentence in the box.
//
// It was a chord once; the chord went to reading mode, which is where the
// other harnesses' readers expect to find the transcript, and the command
// stayed because the question stayed: /step opens the step in flight, which
// is the one being watched, and the draft keeps the keyboard while it does.
// Under the cursor, reading mode's [enter] opens any one row whole.
//
// The override lives on the entry that titles the step, beside stepFold and
// groupFold, so steps still hold no layout state of their own and
// re-render from stored raw entries on resize. It is resolved at render time
// rather than stamped onto the rows, which is what lets a call that lands
// after the command was run arrive already open — a step in flight is a
// step still growing.
//
// The bodies are bounded to maxToolResultLines, as high verbosity's are. A
// step is nine calls wide often enough that unbounded detail would push the
// header off the screen, and the row that wants its whole output still has
// [enter] on it.

import (
	tea "charm.land/bubbletea/v2"
)

// noStepDetailNotice is what /step says when the transcript has no step
// to open. It names what carries detail rather than reporting the refusal,
// because the reader who ran it has already worked out that nothing
// happened (invariant 4).
const noStepDetailNotice = "Nothing to expand yet — /step opens the detail of a step's rows."

// stepDetailOpen reports whether a step is showing its rows' detail bodies.
// Your own answer overrides the verbosity, the same order stepFolded and
// groupFolded read their overrides in; with no answer on record, high
// verbosity is the setting that has already said yes to every step.
func (m Model) stepDetailOpen(g *stepGroup, es []entry) bool {
	if g == nil || g.titleIdx == stepNoTitle || g.titleIdx >= len(es) {
		// A declared step nobody has started has no rows to open and no entry
		// to record an answer on.
		return false
	}
	switch es[g.titleIdx].detailFold {
	case foldOpen:
		return true
	case foldClosed:
		return false
	}
	return m.verbosity == verbosityHigh
}

// stepAt finds the step the entry at idx heads or belongs to. It is the one
// walk behind every "which step is this" question — the cursor's position on
// the hint bar, the group fold inside an opened step, and /step itself —
// so those three can never disagree about where a row lives.
func (m Model) stepAt(es []entry, idx int) (*stepGroup, bool) {
	if idx < 0 {
		return nil, false
	}
	for _, blk := range m.blocksOf(es) {
		g := blk.step
		if g == nil || g.queued() {
			continue
		}
		if idx == g.titleIdx || (idx >= g.start && idx < g.end) {
			return g, true
		}
	}
	return nil, false
}

// stepDetailAt answers stepDetailOpen for a reader holding only an index —
// the group-fold check on the row under the cursor, which knows where it is
// but not which block it is in.
func (m Model) stepDetailAt(es []entry, idx int) bool {
	g, ok := m.stepAt(es, idx)
	if !ok {
		return false
	}
	return m.stepDetailOpen(g, es)
}

// draftStep is the step /step opens from the input: the last one with rows
// in it. While a turn works that is the step in flight — the one being
// watched, and the reason the command is answered beside a live draft at all —
// and once the turn ends it is the step that just finished, which is the one
// still on screen. Steps a plan declared but the run has not reached have no
// rows to open, so they are not it.
func (m Model) draftStep(es []entry) (*stepGroup, bool) {
	blocks := m.blocksOf(es)
	for i := len(blocks) - 1; i >= 0; i-- {
		if g := blocks[i].step; g != nil && !g.queued() {
			return g, true
		}
	}
	return nil, false
}

// toggleStepDetail flips a step's detail, recording the answer on the entry
// that titles it.
//
// Opening detail unfolds the step as well, because a folded step is its
// header and nothing else: opening the detail of rows that are not on screen
// would be an answer that reports success and shows nothing. Closing it leaves
// the fold alone — the reader unfolded that step, one way or the other, and
// the command that closed a body did not ask to hide the rows too.
func (m *Model) toggleStepDetail(g *stepGroup) {
	es := *m.entries()
	if g == nil || g.titleIdx == stepNoTitle || g.titleIdx >= len(es) {
		return
	}
	if m.stepDetailOpen(g, es) {
		es[g.titleIdx].detailFold = foldClosed
		return
	}
	es[g.titleIdx].detailFold = foldOpen
	es[g.titleIdx].stepFold = foldOpen
}

// detailFromDraft is /step beside a live input: it opens the step
// in flight without taking the keyboard, so the sentence in the box survives
// being curious. It is a reading, and reading is not a focus transfer — the
// same rule the wheel follows.
func (m Model) detailFromDraft() (tea.Model, tea.Cmd) {
	es := *m.entries()
	g, ok := m.draftStep(es)
	if !ok {
		if m.startScreenShowing() {
			// First contact is the one screen with nothing behind it. A
			// notice there would spend the screen to say the obvious.
			return m, nil
		}
		if m.saidNoStepDetail(es) {
			// A refusal that fires on every keypress teaches a reader to stop
			// reading refusals. It is said once, and the next press of
			// a command that still has nothing to open is silent.
			return m, nil
		}
		if m.attachedTo != "" {
			m.noteChild(m.attachedTo, noStepDetailNotice)
			m.viewport.SetLines(m.renderHistoryLines())
			m.viewport.GotoBottom()
			return m, nil
		}
		return m.systemNotice(noStepDetailNotice)
	}
	m.toggleStepDetail(g)
	m.invalidateRenderCache()
	m.viewport.SetLines(m.renderHistoryLines())
	if m.atBottom {
		// A reader watching a running step is at the bottom, and the rows the
		// command just opened are what pushed it up. A reader who had scrolled
		// away is left where they were.
		m.viewport.GotoBottom()
	}
	return m, nil
}

// saidNoStepDetail reports whether the notice is already the last thing in
// the transcript, which is the only place a second press could put it.
func (m Model) saidNoStepDetail(es []entry) bool {
	if len(es) == 0 {
		return false
	}
	last := es[len(es)-1]
	return last.kind == entrySystem && last.text == noStepDetailNotice
}
