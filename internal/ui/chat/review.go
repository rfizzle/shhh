package chat

// Review mode (S-099, DESIGN-TUI.md §16a): the surface `/review` and `[v]`
// on a turn's changeset row open — every file the turn touched with its
// hunks, staging per hunk, and the turn's verdict pinned beside the files.
//
// It reads the session's own changeset (S-097), which is what makes it work
// in a directory that was never a repository and what makes the review of an
// old turn possible at all. The hunks it shows are the ones the store
// computed when the edit was applied, rendered by the same component the
// approval card, the transcript row and /diff go through (S-074) — review is
// a layout around that renderer, not a second one.
//
// Nothing here writes to the workspace. For edits already on disk the
// checkboxes select what an undo would put back, which is S-100's work; the
// surface says so on screen rather than offering a key that quietly does
// nothing.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// reviewVerdictDetail bounds how much of a failing check's output is pinned
// under the file list. It is the shape of the failure, not the log.
const reviewVerdictDetail = 3

// reviewCommand handles `/review [turn]`: bare reviews the most recent turn
// that changed anything, a number reviews that turn.
func (m Model) reviewCommand(parts []string) (tea.Model, tea.Cmd) {
	if len(parts) > 2 {
		return m.systemNotice("Usage: /review [turn]")
	}
	if len(parts) == 2 {
		var n int64
		if _, err := fmt.Sscanf(parts[1], "%d", &n); err != nil || n <= 0 {
			return m.systemNotice("Usage: /review [turn] — the turn number from its close row.")
		}
		return m.openReview(n)
	}
	t, ok := m.changes.Latest()
	if !ok {
		return m.systemNotice(sessionDiffEmptyNotice(m.changes))
	}
	return m.openReview(t.N)
}

// openReview takes a turn's changeset into review mode. A turn with nothing
// recorded says which of the two reasons it has — it changed nothing, or its
// records were evicted — rather than opening an empty surface.
func (m Model) openReview(n int64) (tea.Model, tea.Cmd) {
	t, ok := m.changes.Turn(n)
	if !ok {
		if m.changes.WasEvicted(n) {
			return m.systemNotice(fmt.Sprintf(
				"Turn %d's records were dropped to stay inside the changeset store's size limit; there is nothing left to review.", n))
		}
		return m.systemNotice(fmt.Sprintf("Turn %d changed no files.", n))
	}
	v := &components.ReviewView{
		Title: fmt.Sprintf("turn %d", n),
		Files: reviewFiles(t),
		// The edits are already on disk, so what is staged is what an undo
		// would put back (S-100), not something to apply.
		ApplyVerb:    "undo",
		Verdict:      m.reviewVerdict(n),
		Shield:       "nothing is committed",
		ShieldDetail: reviewShieldDetail(t),
	}
	return m.showReview(v, n)
}

// showReview gives review the screen. turn is the turn it is reviewing, or 0
// for a review of something else (the cumulative session diff). Esc goes
// back to focus mode when that is what opened it, and to the input
// otherwise — the turn underneath keeps running either way (S-087).
func (m Model) showReview(v *components.ReviewView, turn int64) (tea.Model, tea.Cmd) {
	m.review = v
	m.reviewTurnN = turn
	m.reviewReturn = m.state
	if m.reviewReturn.isSurface() && m.reviewReturn != stateFocus {
		m.reviewReturn = stateInput
	}
	m.enterSurface(stateReview)
	return m, nil
}

// reviewFiles turns a turn's records into the review's file list. Everything
// starts staged: for an applied turn the selection is what undo would
// restore, and the whole turn is the answer that needs no keystrokes.
func reviewFiles(t changeset.Turn) []components.ReviewFile {
	files := make([]components.ReviewFile, 0, len(t.Records))
	for _, r := range t.Records {
		staged := make([]bool, len(r.Hunks))
		for i := range staged {
			staged[i] = true
		}
		f := components.ReviewFile{
			Path:   r.Path,
			Hunks:  r.Hunks,
			Staged: staged,
			Syntax: diffSyntax(r.Path),
		}
		// The session's own edits need no attribution; a child's do (S-097).
		if r.Agent != changeset.MainAgent {
			f.Agent = r.Agent
		}
		files = append(files, f)
	}
	return files
}

// reviewShieldDetail is the second line of the standing "nothing is
// committed" note: what taking the turn back would restore from.
func reviewShieldDetail(t changeset.Turn) string {
	return fmt.Sprintf("undo restores the %s this turn wrote", plural(t.Files(), "file"))
}

// reviewVerdict pins the turn's own verdict beside its files: the checks it
// ran and, where they failed, the first lines of what they said. It reads
// the same rows the turn closed with (S-098) rather than deciding again what
// counts as a check.
func (m Model) reviewVerdict(n int64) *components.ReviewVerdict {
	es := m.entriesForTurn(n)
	checks := turnChecksRow(es)
	if checks == nil {
		return nil
	}
	v := &components.ReviewVerdict{Failed: checks.Failed, Label: checks.Label}
	if checks.Counts != "" {
		v.Label += " · " + checks.Counts
	}
	if checks.Failed {
		v.Detail = failureLines(es)
	}
	return v
}

// entriesForTurn is the transcript slice belonging to turn n: everything
// between the previous turn's close row and this one's. A turn still in
// flight has no close row yet, so it is the live turn's entries.
func (m Model) entriesForTurn(n int64) []entry {
	end := -1
	for i, e := range m.transcript {
		if e.kind == entryTurnClose && e.turn == n {
			end = i
			break
		}
	}
	if end < 0 {
		if n == m.turnCount {
			return m.turnEntries()
		}
		return nil
	}
	start := 0
	for i := end - 1; i >= 0; i-- {
		if m.transcript[i].kind == entryTurnClose {
			start = i + 1
			break
		}
	}
	return m.transcript[start:end]
}

// failureLines are the first lines of what a failing check printed — the
// shape of the failure, beside the hunks that claim to fix it.
func failureLines(es []entry) []string {
	for _, e := range es {
		if e.exitCode == 0 {
			continue
		}
		var out []string
		for _, line := range strings.Split(e.toolResult, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			out = append(out, strings.TrimRight(line, " \t"))
			if len(out) == reviewVerdictDetail {
				break
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

// updateReview routes keys to the surface. Every exit is non-destructive:
// esc leaves with nothing chosen, and enter hands the staged selection to
// the undo path, which is what staging means for edits already applied.
func (m Model) updateReview(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.review == nil {
		return m.closeReview()
	}
	m.review.Height = m.viewportHeight()
	if keys.Match(msg, keys.Draft.Quit) {
		m.quitting = true
		return m, m.quitCmd()
	}
	done, result := m.review.Update(msg)
	if !done {
		return m, nil
	}
	staged, _ := result.(components.ReviewResult)
	files := reviewStagedPaths(m.review, staged)
	turn := m.reviewTurnN
	updated, cmd := m.closeReview()
	next := updated.(Model)
	if staged.Canceled || len(files) == 0 || turn == 0 {
		return next, cmd
	}
	return next.undoTurn(turn, files)
}

// reviewStagedPaths names the files the selection covers, in list order.
func reviewStagedPaths(v *components.ReviewView, r components.ReviewResult) []string {
	var out []string
	for _, sel := range r.Staged {
		if sel.File >= 0 && sel.File < len(v.Files) {
			out = append(out, v.Files[sel.File].Path)
		}
	}
	return out
}

// closeReview hands the screen back to where review was opened from — focus
// mode on the row that offered it, or the input — and leaves the workspace
// exactly as it found it.
func (m Model) closeReview() (tea.Model, tea.Cmd) {
	m.review = nil
	m.reviewTurnN = 0
	if m.reviewReturn.isSurface() {
		m.state = m.reviewReturn
	} else {
		m.leaveSurface()
	}
	m.reviewReturn = stateInput
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

// renderReviewHint fills the input area while review has the screen. The
// surface's own footer carries the keys; this says where esc goes.
func (m Model) renderReviewHint() string {
	label := "review · esc back"
	if m.reviewReturn == stateFocus {
		label = "review · esc: back to the transcript"
	}
	return sty.SystemMsg.Render(label) + strings.Repeat("\n", inputHeight-1)
}
