package chat

// Rich diff rendering (S-074, DESIGN-TUI.md §3): the full-screen diff state
// shared by transcript edit rows, the approval card's [d], and the /diff
// session diff.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// maxDiffExpandedLines bounds an applied edit's in-transcript expanded view;
// larger diffs end with the "… (+N more diff lines)" notice and open full
// screen from focus mode.
const maxDiffExpandedLines = 20

// WithChangeset wires the per-turn changeset store and the git tracker that
// answers whether a file was tracked when it was edited (S-097). Every
// session has a store already; this replaces it — with a different bound, or
// with the tracker a workspace inside a repository deserves.
func (m Model) WithChangeset(store *changeset.Store, tracker *changeset.Tracker) Model {
	if store != nil {
		m.changes = store
	}
	m.tracker = tracker
	return m
}

// systemNotice appends a system line and scrolls to it.
func (m Model) systemNotice(text string) (tea.Model, tea.Cmd) {
	m.appendEntry(entry{kind: entrySystem, text: text})
	m.viewport.SetContent(m.renderHistory())
	m.viewport.GotoBottom()
	return m, nil
}

// openSessionDiff shows what the session changed, in review mode (S-099).
// It reads the session's own changeset (S-097) rather than shelling out to
// git, so it says the same thing in a directory that was never a repository
// — and it says what this session changed, not what the working tree happens
// to hold. There is nothing to stage in a cumulative diff, so the surface
// opens read-only: the same file list and hunk pane, without the boxes.
func (m Model) openSessionDiff() (tea.Model, tea.Cmd) {
	files := m.changes.Session()
	if len(files) == 0 {
		return m.systemNotice(sessionDiffEmptyNotice(m.changes))
	}
	review := &components.ReviewView{
		Title:    "session diff",
		ReadOnly: true,
		Shield:   "nothing is committed",
	}
	// Eviction is a gap in the record, so it goes where the header keeps it
	// rather than into the title, which is what a narrow list clips first.
	if dropped := m.changes.Evicted(); len(dropped) > 0 {
		review.Note = fmt.Sprintf("%d turn(s) dropped", len(dropped))
	}
	for _, f := range files {
		review.Files = append(review.Files, components.ReviewFile{
			Path: f.Path, Hunks: f.Hunks, Syntax: diffSyntax(f.Path),
		})
	}
	return m.showReview(review, 0)
}

// sessionDiffEmptyNotice distinguishes a session that changed nothing from
// one whose records were evicted — the second is a gap, not a quiet session.
func sessionDiffEmptyNotice(store *changeset.Store) string {
	if dropped := store.Evicted(); len(dropped) > 0 {
		return fmt.Sprintf("No changes are still recorded: %d earlier turn(s) were dropped to stay inside the changeset store's size limit.", len(dropped))
	}
	return "No files have been changed this session."
}

// noteEvictedTurns says which turns the changeset store dropped to stay
// inside its bound. Eviction costs the session its ability to review or undo
// those turns, so it is a line in the transcript rather than a silent drop
// (S-097).
func (m *Model) noteEvictedTurns(evicted []int64) {
	if len(evicted) == 0 {
		return
	}
	labels := make([]string, len(evicted))
	for i, n := range evicted {
		labels[i] = fmt.Sprintf("%d", n)
	}
	m.appendEntry(entry{kind: entrySystem, text: fmt.Sprintf(
		"The changeset store is full: turn %s dropped. Those turns can no longer be reviewed or undone.",
		strings.Join(labels, ", "))})
}

// openDiffFull takes the given viewer full screen; esc returns to ret.
func (m Model) openDiffFull(d *components.DiffView, ret state) (tea.Model, tea.Cmd) {
	d.Mode = components.DiffFull
	d.Offset = 0
	m.fullDiff = d
	m.diffReturn = ret
	m.enterSurface(stateDiffFull)
	return m, nil
}

// updateDiffFull routes keys to the full-screen viewer; any key that leaves
// full-screen mode (esc, or enter's collapse) returns to where it was opened
// from. Esc never destroys.
func (m Model) updateDiffFull(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.fullDiff == nil {
		return m.closeDiffFull()
	}
	m.fullDiff.Height = m.viewportHeight()
	switch pressed := msg.String(); {
	case keys.Is(pressed, keys.Draft.Quit):
		m.quitting = true
		return m, m.quitCmd()
	case keys.Is(pressed, keys.Diff.Leave):
		m.fullDiff.Mode = components.DiffExpanded
	default:
		m.fullDiff.Update(msg)
	}
	if m.fullDiff.Mode != components.DiffFull {
		return m.closeDiffFull()
	}
	return m, nil
}

// closeDiffFull returns from the full-screen diff to wherever it was opened
// from — the confirm prompt, focus mode, or the input.
func (m Model) closeDiffFull() (tea.Model, tea.Cmd) {
	m.fullDiff = nil
	// A diff opened from focus mode goes back to it; anything else hands the
	// screen back to the turn, which may have moved on while it was up
	// (S-087).
	if m.diffReturn.isSurface() {
		m.state = m.diffReturn
	} else {
		m.leaveSurface()
	}
	m.diffReturn = stateInput
	// A transcript diff row's mode may have changed while full screen.
	m.invalidateRenderCache()
	m.syncViewport()
	switch m.state {
	case stateFocus:
		m.refreshFocusView()
	case stateConfirmRun:
		m.viewport.SetContent(m.renderHistory())
	default:
		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()
	}
	return m, nil
}

// renderDiffFullHint fills the input area while the full-screen diff shows.
func (m Model) renderDiffFullHint() string {
	label := "esc back"
	if m.diffReturn == stateConfirmRun {
		label = "esc: back to the approval prompt"
	}
	return sty.SystemMsg.Render(label) + strings.Repeat("\n", inputHeight-1)
}
