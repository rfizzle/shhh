package chat

// Review mode's host side (S-099, §16a): what opens it, what it reads, and
// that leaving it — by any route — changes nothing on disk.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// reviewModel is a finished turn that wrote one file, ready to review.
func reviewModel(t *testing.T) (Model, string) {
	t.Helper()
	m := turnModel(t)
	m = sendText(t, m, "write the file")
	path := filepath.Join(t.TempDir(), "main.go")
	m = applyWrite(t, m, path, "package main\n", "y")
	return finishTurn(t, m), path
}

func TestReview_CommandOpensTheLastTurn(t *testing.T) {
	m, path := reviewModel(t)

	m = sendText(t, m, "/review")
	if m.state != stateReview || m.review == nil {
		t.Fatalf("/review should open review mode, got state %v", m.state)
	}
	if m.review.Title != "turn 1" {
		t.Fatalf("bare /review takes the last turn that changed anything, got %q", m.review.Title)
	}
	if len(m.review.Files) != 1 || m.review.Files[0].Path != path {
		t.Fatalf("the surface should carry the turn's file, got %#v", m.review.Files)
	}
	// The whole turn starts staged: for an applied turn the selection is
	// what undo would restore.
	if m.review.Files[0].Staged == nil || !m.review.Files[0].Staged[0] {
		t.Fatalf("the turn should open wholly staged, got %#v", m.review.Files[0].Staged)
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"REVIEW", "turn 1", "nothing is committed"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the review surface should show %q:\n%s", want, view)
		}
	}
}

// Review is a takeover: full width, no rail, no prompt frame (§15b).
func TestReview_IsATakeoverSurface(t *testing.T) {
	m, _ := reviewModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m = updated.(Model)
	if !m.twoPane() {
		t.Fatal("a 160-column terminal should be two-pane before review opens")
	}

	m = sendText(t, m, "/review")
	if m.twoPane() || !m.inspectorHidden() {
		t.Fatal("review should span both panes and hide the rail")
	}
	if m.frameShowing() {
		t.Fatal("a takeover surface replaces the prompt frame")
	}
	if !strings.Contains(ansi.Strip(m.renderReviewHint()), "esc") {
		t.Fatalf("the bottom panel should say where esc goes, got %q", ansi.Strip(m.renderReviewHint()))
	}
}

// Esc leaves review having changed nothing — not the file, not the record.
func TestReview_EscChangesNothing(t *testing.T) {
	m, path := reviewModel(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	m = sendText(t, m, "/review")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.state != stateInput || m.review != nil {
		t.Fatalf("esc should close review back to the input, got state %v", m.state)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("review must not touch the file: %q became %q", before, after)
	}
	if _, ok := m.changes.Turn(1); !ok {
		t.Fatal("the turn's records should survive a review")
	}
}

// Enter hands the staged selection to the undo path (S-100). Review itself
// applies nothing: what enter does is arm the undo confirm, and the file is
// untouched until that confirm is answered.
func TestReview_EnterHandsTheSelectionToUndo(t *testing.T) {
	m, path := reviewModel(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	m = sendText(t, m, "/review")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.state == stateReview {
		t.Fatal("enter with a staged selection should leave the surface")
	}
	if m.state != stateUndoConfirm || m.undoAsk == nil {
		t.Fatalf("enter should arm the undo confirm, got state %v", m.state)
	}
	if got := undoPlanPaths(m.undoPlan); len(got) != 1 || got[0] != path {
		t.Fatalf("the plan should cover the staged file, got %v", got)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("nothing in review is destructive: %q became %q", before, after)
	}
}

// Staging nothing and pressing enter is not an exit: the surface says why.
func TestReview_EnterWithNothingStagedStays(t *testing.T) {
	m, _ := reviewModel(t)
	m = sendText(t, m, "/review")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")}) // all → none
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.state != stateReview {
		t.Fatalf("enter with nothing staged should stay in review, got state %v", m.state)
	}
	if !strings.Contains(ansi.Strip(m.View()), "nothing staged") {
		t.Fatalf("the surface should say why enter did nothing:\n%s", ansi.Strip(m.View()))
	}
}

// A turn whose records were evicted is a gap in the record, not a quiet
// turn, and review says which of the two it is (S-097).
func TestReview_SaysWhyThereIsNothingToShow(t *testing.T) {
	m := turnModel(t)
	updated, _ := m.openReview(3)
	m = updated.(Model)
	if got := m.transcript[len(m.transcript)-1].text; !strings.Contains(got, "changed no files") {
		t.Fatalf("an empty turn should say so, got %q", got)
	}

	store := changeset.New(64)
	big := strings.Repeat("x\n", 200)
	store.Add(1, changeset.Record{Path: "a.go", After: big, AfterExists: true})
	store.Add(2, changeset.Record{Path: "b.go", After: big, AfterExists: true})
	m = m.WithChangeset(store, nil)
	updated, _ = m.openReview(1)
	m = updated.(Model)
	if got := m.transcript[len(m.transcript)-1].text; !strings.Contains(got, "dropped") {
		t.Fatalf("an evicted turn should say its records are gone, got %q", got)
	}
	if m.state == stateReview {
		t.Fatal("neither case opens an empty surface")
	}
}

func TestReview_NumberedTurnAndUsage(t *testing.T) {
	m, _ := reviewModel(t)

	m = sendText(t, m, "/review 1")
	if m.state != stateReview || m.review.Title != "turn 1" {
		t.Fatalf("/review 1 should open turn 1, got state %v", m.state)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)

	m = sendText(t, m, "/review later")
	if m.state == stateReview {
		t.Fatal("a non-numeric turn should not open a surface")
	}
	if got := m.transcript[len(m.transcript)-1].text; !strings.Contains(got, "Usage: /review") {
		t.Fatalf("a bad argument should answer with the usage, got %q", got)
	}
}

// The file list carries the turn's verdict — the failing check beside the
// hunks that claim to fix it — read from the rows the turn closed with.
func TestReview_CarriesTheTurnsVerdict(t *testing.T) {
	m := turnModel(t)
	m = sendText(t, m, "fix the test")
	path := filepath.Join(t.TempDir(), "main.go")
	m = applyWrite(t, m, path, "package main\n", "y")
	m.appendEntry(entry{
		kind: entryCommand, text: "go test ./internal/agent/...",
		toolResult: "--- FAIL: TestRoundLimit (0.03s)\n    loop_test.go:142", exitCode: 1,
	})
	m = finishTurn(t, m)

	m = sendText(t, m, "/review")
	if m.review == nil || m.review.Verdict == nil {
		t.Fatal("the review should carry the turn's verdict")
	}
	if !m.review.Verdict.Failed || !strings.Contains(m.review.Verdict.Label, "go test") {
		t.Fatalf("the verdict should be the failing test, got %#v", m.review.Verdict)
	}
	if len(m.review.Verdict.Detail) == 0 || !strings.Contains(m.review.Verdict.Detail[0], "FAIL") {
		t.Fatalf("the verdict should pin what the failure said, got %#v", m.review.Verdict.Detail)
	}
	if !strings.Contains(ansi.Strip(m.View()), "--- FAIL: TestRoundLimit") {
		t.Fatalf("the failure belongs beside the files:\n%s", ansi.Strip(m.View()))
	}
}

// A child's patch is attributed to the child that wrote it (S-097).
func TestReview_AttributesASubagentsFiles(t *testing.T) {
	m := turnModel(t)
	m.turnCount = 1
	m.changes.Add(1, changeset.Record{
		Path: "docs/loop.md", Before: "one\n", After: "one\ntwo\n",
		BeforeExists: true, AfterExists: true,
		Agent: "writer-1", Origin: changeset.ChildPatch,
	})
	m.changes.Add(1, changeset.Record{
		Path: "internal/agent/loop.go", Before: "a\n", After: "b\n",
		BeforeExists: true, AfterExists: true,
	})

	updated, _ := m.openReview(1)
	m = updated.(Model)
	if m.review == nil {
		t.Fatal("the turn should open in review")
	}
	byPath := map[string]components.ReviewFile{}
	for _, f := range m.review.Files {
		byPath[f.Path] = f
	}
	if got := byPath["docs/loop.md"].Agent; got != "writer-1" {
		t.Fatalf("a child's file should name the child, got %q", got)
	}
	if got := byPath["internal/agent/loop.go"].Agent; got != "" {
		t.Fatalf("the session's own edits need no attribution, got %q", got)
	}
}

// Opened from the changeset row, review goes back to that row rather than to
// the input: esc returns to where it was opened from (§7).
func TestReview_ReturnsToFocusMode(t *testing.T) {
	m, _ := reviewModel(t)
	updated, _ := m.enterFocusMode()
	m = updated.(Model)
	updated, _ = m.updateFocus(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(keys.Shown(keys.Row.Review))})
	m = updated.(Model)
	if m.state != stateReview {
		t.Fatalf("[v] should open review mode, got state %v", m.state)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.state != stateFocus {
		t.Fatalf("esc should hand the screen back to focus mode, got state %v", m.state)
	}
}

// A surface opened mid-turn borrows the screen, not the turn (S-087).
func TestReview_TurnKeepsRunningUnderneath(t *testing.T) {
	m := turnModel(t)
	m.changes.Add(1, changeset.Record{
		Path: "x.go", Before: "one\n", After: "one\ntwo\n",
		BeforeExists: true, AfterExists: true,
	})
	m.turnCount = 1
	m.setTurnState(stateStreaming)

	m = sendText(t, m, "/review")
	if m.state != stateReview {
		t.Fatalf("/review should open mid-turn, got state %v", m.state)
	}
	if m.turnState() != stateStreaming {
		t.Fatalf("the turn should still be in flight, got %v", m.turnState())
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.state != stateStreaming {
		t.Fatalf("esc should hand the screen back to the running turn, got %v", m.state)
	}
}
