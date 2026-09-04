package chat

// Undo a turn (docs/interface/surfaces.md#the-turns-close). `[u]` on a
// changeset row and `/undo [turn]` put back what a turn wrote, reading the
// session's own records rather than git: it works in a directory that
// was never a repository, and it never touches the user's index or stash.
//
// Undo asks first. The confirm states what it would do, and when a file has
// changed since the turn it says so and leaves that file alone — the drifted
// content is something the record never saw, so overwriting it is the second,
// deliberate answer rather than the default.
//
// An undo is an edit like any other, so it is recorded as its own changeset
// and closes with the same row a turn does. That is what makes it appear in
// the transcript, reviewable with `[v]`, and undoable in turn.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// undoCommand handles `/undo [turn]`: bare undoes the most recent turn that
// changed anything, a number undoes that turn.
func (m Model) undoCommand(parts []string) (tea.Model, tea.Cmd) {
	if len(parts) > 2 {
		return m.systemNotice("Usage: /undo [turn]")
	}
	if len(parts) == 2 {
		var n int64
		if _, err := fmt.Sscanf(parts[1], "%d", &n); err != nil || n <= 0 {
			return m.systemNotice("Usage: /undo [turn] — the turn number from its close row.")
		}
		return m.undoTurn(n, nil)
	}
	t, ok := m.changes.Latest()
	if !ok {
		return m.systemNotice(sessionDiffEmptyNotice(m.changes))
	}
	return m.undoTurn(t.N, nil)
}

// undoSubject is what an armed undo is taking back, in the wordings the
// surfaces around the confirm need: the noun the drift is measured against,
// the note the close row carries, and the command that offers the same undo a
// second time — which is the only way back to [f] once it has been declined.
// A rewind's file restore comes through the same confirm as a turn's undo and
// is none of those things, so the words travel with the plan rather than
// being derived from a turn number at each of the three places.
type undoSubject struct {
	since string
	note  string
	again string
}

// undoOf is the subject for taking one turn back.
func undoOf(n int64) undoSubject {
	return undoSubject{
		since: fmt.Sprintf("turn %d", n),
		note:  fmt.Sprintf("undo of turn %d", n),
		again: fmt.Sprintf("/undo %d", n),
	}
}

// undoTurn arms the confirm for turn n, restricted to files when review
// staged a selection and covering the whole turn otherwise. Nothing is
// written here: this reads the workspace and asks.
func (m Model) undoTurn(n int64, files []string) (tea.Model, tea.Cmd) {
	// Recall and not Turn: a turn evicted to stay inside the byte bound, and
	// a turn from a sitting that has already ended, are both still on record
	// and both still undoable. The size limit is a bound on what this process
	// holds at once, not on what can be put back.
	t, ok := m.changes.Recall(n)
	if !ok && m.changes.WasEvicted(n) {
		return m.systemNotice(fmt.Sprintf(
			"Turn %d's records were dropped to stay inside the changeset store's size limit; it can no longer be undone.", n))
	}
	if !ok {
		return m.systemNotice(fmt.Sprintf("Turn %d changed no files; there is nothing to undo.", n))
	}
	plan := changeset.PlanUndo(t, files)
	if plan.Empty() {
		return m.systemNotice(fmt.Sprintf("Nothing in turn %d matched what was selected; nothing to undo.", n))
	}
	ret := m.state
	if ret.isSurface() && ret != stateFocus {
		ret = stateInput
	}
	m.armUndo(plan, undoOf(n), n, ret)
	return m, nil
}

// armUndo puts the confirm up for a plan already built. confirmTurn is the
// turn the question names, which for a rewind is the earliest turn being taken
// back — the one the workspace is being returned to the far side of.
func (m *Model) armUndo(plan changeset.UndoPlan, of undoSubject, confirmTurn int64, ret state) {
	m.undoPlan = plan
	m.undoSubject = of
	m.undoAsk = &components.UndoConfirm{
		Turn:     confirmTurn,
		Restores: plan.Restores() - driftedIn(plan, false),
		Removes:  plan.Removes() - driftedIn(plan, true),
		Drifted:  plan.Drifted(),
	}
	m.undoReturn = ret
	m.enterSurface(stateUndoConfirm)
	m.syncViewport()
}

// driftedIn counts the drifted files of one kind — the ones the plan would
// delete when removes is true, the ones it would write back otherwise. The
// confirm states what [y] does, and [y] does not touch a drifted file.
func driftedIn(plan changeset.UndoPlan, removes bool) int {
	n := 0
	for _, f := range plan.Files {
		if f.Drifted && f.Removes() == removes {
			n++
		}
	}
	return n
}

// updateUndoConfirm routes keys while the confirm is up. Declining writes
// nothing and hands the screen back to whatever offered the undo.
func (m Model) updateUndoConfirm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.undoAsk == nil {
		return m.closeUndoConfirm()
	}
	done, result := m.undoAsk.Update(msg)
	if !done {
		return m, nil
	}
	decision, _ := result.(components.UndoDecision)
	plan, of := m.undoPlan, m.undoSubject
	updated, cmd := m.closeUndoConfirm()
	next := updated.(Model)
	if decision == components.UndoCancel {
		return next, cmd
	}
	return next.applyUndo(plan, of, decision == components.UndoForce)
}

// closeUndoConfirm takes the prompt down and gives the screen back to where
// the undo was offered from — focus mode on the row, or the input.
func (m Model) closeUndoConfirm() (tea.Model, tea.Cmd) {
	m.undoAsk = nil
	m.undoPlan = changeset.UndoPlan{}
	m.undoSubject = undoSubject{}
	if m.undoReturn.isSurface() {
		m.state = m.undoReturn
	} else {
		m.leaveSurface()
	}
	m.undoReturn = stateInput
	m.syncViewport()
	if m.state == stateFocus {
		m.refreshFocusView()
	}
	return m, nil
}

// applyUndo writes the plan back and records having done so. The reverse
// edits become a changeset of their own under a fresh turn number, which is
// what puts the undo in the transcript and makes it reviewable — and, since
// the records describe an ordinary edit, undoable in turn.
func (m Model) applyUndo(plan changeset.UndoPlan, of undoSubject, force bool) (tea.Model, tea.Cmd) {
	out := plan.Apply(force)
	if len(out.Records) > 0 {
		m.turnCount++
		m.signal(observe.SignalUndo, "")
		var evicted []int64
		for _, r := range out.Records {
			evicted = append(evicted, m.changes.Add(m.turnCount, r)...)
		}
		m.appendEntry(entry{
			kind:  entryTurnClose,
			turn:  m.turnCount,
			close: m.undoCloseData(of.note),
		})
		m.noteEvictedTurns(evicted)
	}
	if note := undoOutcomeNotice(of, out); note != "" {
		m.appendEntry(entry{kind: entrySystem, text: note})
	}
	m.invalidateRenderCache()
	m.syncViewport()
	if m.state == stateFocus {
		m.refreshFocusView()
	} else {
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
	}
	return m, nil
}

// undoCloseData is the close block the undo appends: the same rows a turn
// ends with, so `[v]` and `[u]` work on it exactly as they do on the turn it
// took back. The note says what that was.
func (m Model) undoCloseData(note string) *components.TurnClose {
	return &components.TurnClose{
		State:   components.TurnDone,
		Note:    note,
		Changes: m.turnChangesRow(),
	}
}

// undoOutcomeNotice reports what the undo did not do: the drifted files it
// left alone, and any it could not write. An undo that did everything asked
// of it says nothing here — the close row already said what changed.
func undoOutcomeNotice(of undoSubject, out changeset.UndoOutcome) string {
	var parts []string
	if len(out.Skipped) > 0 {
		parts = append(parts, fmt.Sprintf(
			"%s changed since %s and %s left alone: %s. %s again and answer [f] to overwrite.",
			plural(len(out.Skipped), "file"), of.since, wasWere(len(out.Skipped)),
			strings.Join(out.Skipped, ", "), of.again))
	}
	for _, f := range out.Failed {
		parts = append(parts, fmt.Sprintf("%s could not be restored: %v.", f.Path, f.Err))
	}
	if len(out.Records) == 0 && len(parts) > 0 {
		parts = append([]string{"Nothing was put back."}, parts...)
	}
	return strings.Join(parts, " ")
}

// wasWere agrees the verb with the count, so the notices read as sentences.
func wasWere(n int) string {
	if n == 1 {
		return "was"
	}
	return "were"
}

// undoConfirmLines renders the confirm, one row per line.
func (m Model) undoConfirmLines() []string {
	if m.undoAsk == nil {
		return nil
	}
	return strings.Split(m.undoAsk.View(m.contentWidth()), "\n")
}
