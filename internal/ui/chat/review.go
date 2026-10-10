package chat

// Review mode (docs/interface/surfaces.md#the-turns-close): the
// surface `/review` and a turn's changeset row, opened, open — every file the
// turn touched with its hunks, and the turn's verdict pinned beside the files.
//
// It reads the session's own changeset, which is what makes it work
// in a directory that was never a repository and what makes the review of an
// old turn possible at all. The hunks it shows are the ones the store
// computed when the edit was applied, rendered by the same component the
// approval card, the transcript row and /diff go through — review is
// a layout around that renderer, not a second one.
//
// Nothing here writes to the workspace, and nothing here stages: it is a
// reading, as /diff's is. Taking the turn back is /undo, and a file of it is
// asked of the model, so the surface names the way back on screen rather
// than being a second path to it.

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
		return m.systemNotice("usage: /review [turn]")
	}
	if len(parts) == 2 {
		var n int64
		if _, err := fmt.Sscanf(parts[1], "%d", &n); err != nil || n <= 0 {
			return m.systemNotice("usage: /review [turn] — the turn number from its close row")
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
	// Recall and not Turn: a turn this process evicted, and a turn from a
	// sitting that has already ended, are both still on record, and a turn
	// that can be undone and not looked at first would be the wrong half of
	// the pair to offer.
	t, ok := m.runChangeset(m.closeFrom(n), n, m.changes.Recall)
	if !ok {
		if m.changes.WasEvicted(n) {
			return m.systemNotice(fmt.Sprintf(
				"turn %d's records were dropped to stay inside the changeset store's size limit; there is nothing left to review", n))
		}
		return m.systemNotice(fmt.Sprintf("turn %d changed no files", n))
	}
	v := &components.ReviewView{
		Title:        fmt.Sprintf("turn %d", n),
		Files:        reviewFiles(t),
		Verdict:      m.reviewVerdict(n),
		Shield:       "nothing is committed",
		ShieldDetail: reviewShieldDetail(n, t, m.runWriters(m.closeFrom(n), n, m.changes.Recall)),
	}
	return m.showReview(v, n)
}

// showReview gives review the screen. turn is the turn it is reviewing, or 0
// for a review of something else (the cumulative session diff). Esc goes
// back to focus mode when that is what opened it, and to the input
// otherwise — the turn underneath keeps running either way.
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

// reviewFiles turns a turn's records into the review's file list.
func reviewFiles(t changeset.Turn) []components.ReviewFile {
	files := make([]components.ReviewFile, 0, len(t.Records))
	for _, r := range t.Records {
		f := components.ReviewFile{
			Path:   r.Path,
			Hunks:  r.Hunks,
			Syntax: diffSyntax(r.Path),
			Mode:   r.ModeChange(),
		}
		// The session's own edits need no attribution; a child's do.
		if r.Agent != changeset.MainAgent {
			f.Agent = r.Agent
		}
		files = append(files, f)
	}
	return files
}

// reviewShieldDetail is the second line of the standing "nothing is
// committed" note: how the turn is taken back, and what that restores from.
func reviewShieldDetail(n int64, t changeset.Turn, writers []int64) string {
	if len(writers) > 1 {
		return fmt.Sprintf("%s restores the %s this run wrote", undoSpan(writers), plural(t.Files(), "file"))
	}
	return fmt.Sprintf("/undo %d restores the %s this turn wrote", n, plural(t.Files(), "file"))
}

// reviewVerdict pins the turn's own verdict beside its files: the checks it
// ran and, where they failed, the first lines of what they said. It reads
// the same rows the turn closed with rather than deciding again what
// counts as a check.
func (m Model) reviewVerdict(n int64) *components.ReviewVerdict {
	es := m.entriesForTurn(n)
	checks := m.turnChecks(n, es)
	if checks == nil {
		return nil
	}
	v := &components.ReviewVerdict{Failed: checks.Failed, Label: checks.Label}
	if checks.Counts != "" {
		v.Label += " · " + checks.Counts
	}
	if checks.Superseded > 0 {
		v.Label += " · " + plural(checks.Superseded, "earlier failure") + " since passed"
	}
	if checks.Failed {
		v.Detail = failureLines(es)
	}
	return v
}

// turnChecks is the verdict a turn ended with: the block it closed with,
// where it has one, and the rows themselves for a turn still in flight.
//
// The stored block is the reading the turn actually reported, and taking it
// rather than parsing the rows again is what keeps a verdict from
// disappearing weeks into a session: a check's output is a tool result like
// any other, and a trim some turns later takes the body a verdict would have
// to be read back out of (context.go). The row survives that; what it said
// about how many checks passed does not survive being parsed out of the
// placeholder.
func (m Model) turnChecks(n int64, es []entry) *components.TurnChecks {
	for _, e := range m.transcript {
		if e.kind == entryTurnClose && e.turn == n && e.close != nil {
			return e.close.Checks
		}
	}
	return turnChecksRow(es, m.wiring.Gate.Manage != nil)
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
//
// A failure the repository's own suite has since answered is skipped: it is
// the wrong failure to pin beside a verdict that is about something else
// (resolved.go).
func failureLines(es []entry) []string {
	verified := lastVerification(es)
	for i, e := range es {
		if e.exitCode == 0 || verified.settled(i) {
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

// updateReview routes keys to the surface. Its one exit changes nothing.
func (m Model) updateReview(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.review == nil {
		return m.closeReview()
	}
	m.review.Height = m.viewportHeight()
	if !m.review.Update(msg) {
		return m, nil
	}
	return m.closeReview()
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
		m.viewport.SetLines(m.renderHistoryLines())
		m.viewport.GotoBottom()
	}
	return m, nil
}

// renderContextHint is the context surface's bottom panel: it holds the
// keyboard, so the panel states the way out and nothing else.
func (m Model) renderContextHint() string {
	return sty.SystemMsg.Render("context · ") + contextKeyHint()
}

// renderReviewHint fills the input area while review has the screen. The
// surface's own footer carries the keys; this says where esc goes.
func (m Model) renderReviewHint() string {
	label := segAs(keys.Review.Back, "back")
	if m.reviewReturn == stateFocus {
		label = segAs(keys.Review.Back, "back to the transcript")
	}
	return sty.SystemMsg.Render("review · ") + label.render()
}
