package chat

// Undo a turn (S-100, DESIGN-TUI.md §16). `[u]` on a changeset row and
// `/undo [turn]` put back what a turn wrote, reading the session's own
// records (S-097) rather than git: it works in a directory that was never a
// repository, and it never touches the user's index or stash.
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

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
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

// undoTurn arms the confirm for turn n, restricted to files when review
// staged a selection and covering the whole turn otherwise. Nothing is
// written here: this reads the workspace and asks.
func (m Model) undoTurn(n int64, files []string) (tea.Model, tea.Cmd) {
	if m.changes.WasEvicted(n) {
		return m.systemNotice(fmt.Sprintf(
			"Turn %d's records were dropped to stay inside the changeset store's size limit; it can no longer be undone.", n))
	}
	t, ok := m.changes.Turn(n)
	if !ok {
		return m.systemNotice(fmt.Sprintf("Turn %d changed no files; there is nothing to undo.", n))
	}
	plan := changeset.PlanUndo(t, files)
	if plan.Empty() {
		return m.systemNotice(fmt.Sprintf("Nothing in turn %d matched what was selected; nothing to undo.", n))
	}
	m.undoPlan = plan
	m.undoAsk = &components.UndoConfirm{
		Turn:     n,
		Restores: plan.Restores() - driftedIn(plan, false),
		Removes:  plan.Removes() - driftedIn(plan, true),
		Drifted:  plan.Drifted(),
	}
	m.undoReturn = m.state
	if m.undoReturn.isSurface() && m.undoReturn != stateFocus {
		m.undoReturn = stateInput
	}
	m.enterSurface(stateUndoConfirm)
	m.syncViewport()
	return m, nil
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
func (m Model) updateUndoConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.undoAsk == nil {
		return m.closeUndoConfirm()
	}
	if keys.Match(msg, keys.Draft.Quit) {
		m.quitting = true
		return m, m.quitCmd()
	}
	done, result := m.undoAsk.Update(msg)
	if !done {
		return m, nil
	}
	decision, _ := result.(components.UndoDecision)
	plan := m.undoPlan
	updated, cmd := m.closeUndoConfirm()
	next := updated.(Model)
	if decision == components.UndoCancel {
		return next, cmd
	}
	return next.applyUndo(plan, decision == components.UndoForce)
}

// closeUndoConfirm takes the prompt down and gives the screen back to where
// the undo was offered from — focus mode on the row, or the input.
func (m Model) closeUndoConfirm() (tea.Model, tea.Cmd) {
	m.undoAsk = nil
	m.undoPlan = changeset.UndoPlan{}
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
func (m Model) applyUndo(plan changeset.UndoPlan, force bool) (tea.Model, tea.Cmd) {
	out := plan.Apply(force)
	if len(out.Records) > 0 {
		m.turnCount++
		var evicted []int64
		for _, r := range out.Records {
			evicted = append(evicted, m.changes.Add(m.turnCount, r)...)
		}
		m.appendEntry(entry{
			kind:  entryTurnClose,
			turn:  m.turnCount,
			close: m.undoCloseData(plan.Turn),
		})
		m.noteEvictedTurns(evicted)
	}
	if note := undoOutcomeNotice(plan.Turn, out); note != "" {
		m.appendEntry(entry{kind: entrySystem, text: note})
	}
	m.invalidateRenderCache()
	m.syncViewport()
	if m.state == stateFocus {
		m.refreshFocusView()
	} else {
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
	}
	return m, nil
}

// undoCloseData is the close block the undo appends: the same rows a turn
// ends with, so `[v]` and `[u]` work on it exactly as they do on the turn it
// took back. The note says which turn that was.
func (m Model) undoCloseData(of int64) *components.TurnClose {
	return &components.TurnClose{
		State:   components.TurnDone,
		Note:    fmt.Sprintf("undo of turn %d", of),
		Changes: m.turnChangesRow(),
	}
}

// undoOutcomeNotice reports what the undo did not do: the drifted files it
// left alone, and any it could not write. An undo that did everything asked
// of it says nothing here — the close row already said what changed.
func undoOutcomeNotice(turn int64, out changeset.UndoOutcome) string {
	var parts []string
	if len(out.Skipped) > 0 {
		parts = append(parts, fmt.Sprintf(
			"%s changed since turn %d and %s left alone: %s. /undo %d again and answer [f] to overwrite.",
			plural(len(out.Skipped), "file"), turn, wasWere(len(out.Skipped)),
			strings.Join(out.Skipped, ", "), turn))
	}
	for _, f := range out.Failed {
		parts = append(parts, fmt.Sprintf("%s could not be restored: %v.", f.Path, f.Err))
	}
	if len(out.Records) == 0 && len(parts) > 0 {
		parts = append([]string{fmt.Sprintf("Nothing of turn %d was undone.", turn)}, parts...)
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

// renderUndoConfirm pads the confirm to the bottom panel's height.
func (m Model) renderUndoConfirm() string {
	lines := m.undoConfirmLines()
	h := m.bottomPanelHeight()
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines[:h], "\n")
}
