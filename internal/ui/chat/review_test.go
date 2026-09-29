package chat

// Review mode's host side: what opens it, what it reads, and
// that leaving it — by any route — changes nothing on disk.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/ui/components"
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

// reviewSplitModel is a recorded turn that changed one file in two separate
// places, so the review has two hunks to move between.
// The record is filed directly rather than driven through the tool: an
// overwrite of a file the session never read is refused before it reaches a
// card, and what these tests need is the two hunks, not the route.
func reviewSplitModel(t *testing.T) (Model, string) {
	t.Helper()
	before := "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\neleven\ntwelve\n"
	after := strings.Replace(before, "two\n", "TWO\n", 1)
	after = strings.Replace(after, "eleven\n", "ELEVEN\n", 1)

	path := filepath.Join(t.TempDir(), "loop.go")
	if err := os.WriteFile(path, []byte(after), 0o600); err != nil {
		t.Fatal(err)
	}
	m := turnModel(t)
	m.turnCount = 1
	m.changes.Add(1, changeset.Record{
		Path: path, Before: before, After: after,
		BeforeExists: true, AfterExists: true,
	})
	return m, path
}

// openSplitReview takes that turn into review.
func openSplitReview(t *testing.T, m Model) Model {
	t.Helper()
	updated, _ := m.openReview(1)
	m = updated.(Model)
	if m.review == nil {
		t.Fatalf("the turn should open in review, got state %v", m.state)
	}
	if got := len(m.review.Files[0].Hunks); got != 2 {
		t.Fatalf("the fixture needs a two-hunk file, got %d", got)
	}
	return m
}

// A turn's review is a reading: the keys that once staged an undo stage
// nothing, enter arms no confirm, and the way to take the turn back is named
// on the surface instead (docs/interface/surfaces.md#the-turns-close).
func TestReview_ATurnsReviewStagesNothing(t *testing.T) {
	m, _ := reviewSplitModel(t)
	m = openSplitReview(t, m)
	for _, press := range []tea.KeyPressMsg{
		{Code: 's', Text: "s"}, {Code: 'A', Text: "A"}, {Code: tea.KeyEnter},
	} {
		updated, _ := m.Update(press)
		m = updated.(Model)
		if m.undoAsk != nil || m.state != stateReview {
			t.Fatalf("%v should stage and arm nothing, got state %v", press, m.state)
		}
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "/undo 1 restores") {
		t.Fatalf("the surface should name the way back:\n%s", view)
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if updated.(Model).state == stateReview {
		t.Fatal("esc should leave the review")
	}
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
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"REVIEW", "turn 1", "nothing is committed"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the review surface should show %q:\n%s", want, view)
		}
	}
}

// Review is a takeover: full width, no rail, no prompt frame.
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
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
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

// A turn whose records were evicted is a gap in the record, not a quiet
// turn, and review says which of the two it is.
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
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)

	m = sendText(t, m, "/review later")
	if m.state == stateReview {
		t.Fatal("a non-numeric turn should not open a surface")
	}
	if got := m.transcript[len(m.transcript)-1].text; !strings.Contains(got, "usage: /review") {
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
	if !strings.Contains(ansi.Strip(m.View().Content), "--- FAIL: TestRoundLimit") {
		t.Fatalf("the failure belongs beside the files:\n%s", ansi.Strip(m.View().Content))
	}
}

// A child's patch is attributed to the child that wrote it.
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
// the input: esc returns to where it was opened from.
func TestReview_ReturnsToFocusMode(t *testing.T) {
	m, _ := reviewModel(t)
	updated, _ := m.enterFocusMode()
	m = updated.(Model)
	updated, _ = m.updateFocus(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.state != stateReview {
		t.Fatalf("enter on the changeset row should open review mode, got state %v", m.state)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.state != stateFocus {
		t.Fatalf("esc should hand the screen back to focus mode, got state %v", m.state)
	}
}

// A surface opened mid-turn borrows the screen, not the turn.
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
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.state != stateStreaming {
		t.Fatalf("esc should hand the screen back to the running turn, got %v", m.state)
	}
}
