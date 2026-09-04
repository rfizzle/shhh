package chat

// Undo's host side: that it asks before it writes, that drift
// is put to the user rather than run over, and that the undo lands in the
// transcript as a changeset of its own.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/changeset"
)

// undoPlanPaths names what a plan covers, in plan order.
func undoPlanPaths(p changeset.UndoPlan) []string {
	out := make([]string, 0, len(p.Files))
	for _, f := range p.Files {
		out = append(out, f.Path())
	}
	return out
}

// undoModel is a finished turn that created one file, ready to be undone.
func undoModel(t *testing.T) (Model, string) {
	t.Helper()
	m := turnModel(t)
	m = sendText(t, m, "write the file")
	path := filepath.Join(t.TempDir(), "main.go")
	m = applyWrite(t, m, path, "package main\n", "y")
	return finishTurn(t, m), path
}

// press sends one keystroke to the model.
func press(t *testing.T, m Model, s string) Model {
	t.Helper()
	var msg tea.KeyPressMsg
	switch s {
	case "esc":
		msg = tea.KeyPressMsg{Code: tea.KeyEscape}
	case "enter":
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	default:
		msg = tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
	}
	updated, _ := m.Update(msg)
	return updated.(Model)
}

func lastSystem(t *testing.T, m Model) string {
	t.Helper()
	for i := len(m.transcript) - 1; i >= 0; i-- {
		if m.transcript[i].kind == entrySystem {
			return m.transcript[i].text
		}
	}
	t.Fatal("expected a system notice in the transcript")
	return ""
}

// The clean case: /undo asks, y answers, and the file the turn created is
// gone.
func TestUndo_RestoresWhatTheTurnWrote(t *testing.T) {
	m, path := undoModel(t)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the turn should have written the file: %v", err)
	}

	m = sendText(t, m, "/undo")
	if m.state != stateUndoConfirm || m.undoAsk == nil {
		t.Fatalf("/undo should ask before it writes, got state %v", m.state)
	}
	prompt := ansi.Strip(m.panelView())
	if !strings.Contains(prompt, "Undo turn 1?") || !strings.Contains(prompt, "deletes 1 file") {
		t.Fatalf("the confirm should state what it would do, got %q", prompt)
	}

	m = press(t, m, "y")
	if m.state == stateUndoConfirm {
		t.Fatal("answering should take the confirm down")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a file the turn created should be gone after undo, got %v", err)
	}
}

// Esc declines: the prompt goes away and the workspace is exactly as it was.
func TestUndo_EscDeclinesAndWritesNothing(t *testing.T) {
	m, path := undoModel(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	m = sendText(t, m, "/undo")
	m = press(t, m, "esc")
	if m.state != stateInput || m.undoAsk != nil {
		t.Fatalf("esc should close the confirm back to the input, got state %v", m.state)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("declining must change nothing: %q became %q", before, after)
	}
}

// An undo is an edit like any other, so it closes with the same row a turn
// does and can be reviewed — and undone again.
func TestUndo_IsRecordedAsItsOwnChangeset(t *testing.T) {
	m, path := undoModel(t)
	m = sendText(t, m, "/undo")
	m = press(t, m, "y")

	if m.turnCount != 2 {
		t.Fatalf("the undo should take a turn number of its own, got %d", m.turnCount)
	}
	turn, ok := m.changes.Turn(2)
	if !ok || turn.Files() != 1 || turn.Records[0].Path != path {
		t.Fatalf("the undo's own changeset should record what it restored, got %+v", turn)
	}
	last := m.transcript[len(m.transcript)-1]
	if last.kind != entryTurnClose || last.turn != 2 || last.close == nil {
		t.Fatalf("the undo should close with a changeset row, got %v", last.kind)
	}
	if last.close.Changes == nil {
		t.Fatal("the close row should offer review and undo like any other turn's")
	}
	if !strings.Contains(last.close.Note, "undo of turn 1") {
		t.Fatalf("the row should say which turn it took back, got %q", last.close.Note)
	}
	// And it can be reviewed, which is what "its own changeset" buys.
	updated, _ := m.openReview(2)
	if r := updated.(Model); r.state != stateReview || r.review.Title != "turn 2" {
		t.Fatalf("the undo should be reviewable, got state %v", r.state)
	}
}

// A file changed since the turn is named, left alone, and the notice says
// how to overwrite it deliberately.
func TestUndo_DriftIsSkippedAndReported(t *testing.T) {
	m, path := undoModel(t)
	if err := os.WriteFile(path, []byte("mine now\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m = sendText(t, m, "/undo")
	prompt := ansi.Strip(m.panelView())
	if !strings.Contains(prompt, "1 file changed since the turn") {
		t.Fatalf("the confirm should state the drift, got %q", prompt)
	}
	if !strings.Contains(prompt, "[f] force") {
		t.Fatalf("force should be offered as the deliberate second answer, got %q", prompt)
	}
	// Every file drifted, so [y] has nothing to do and is not offered.
	if strings.Contains(prompt, "[y/N]") {
		t.Fatalf("a confirm with nothing for [y] to do should not offer it, got %q", prompt)
	}

	m = press(t, m, "y")
	if m.state != stateUndoConfirm {
		t.Fatal("[y] with nothing to do should leave the confirm up")
	}
	if got, _ := os.ReadFile(path); string(got) != "mine now\n" {
		t.Fatalf("the drifted file must be untouched, got %q", got)
	}
}

// With something else to restore, [y] does that and reports what it left.
func TestUndo_PartialDriftRestoresTheRest(t *testing.T) {
	m := turnModel(t)
	m = sendText(t, m, "write both")
	dir := t.TempDir()
	a := filepath.Join(dir, "a.go")
	b := filepath.Join(dir, "b.go")
	m = applyWrite(t, m, a, "package a\n", "y")
	m = applyWrite(t, m, b, "package b\n", "y")
	m = finishTurn(t, m)
	if err := os.WriteFile(a, []byte("mine now\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m = sendText(t, m, "/undo")
	m = press(t, m, "y")
	if got, _ := os.ReadFile(a); string(got) != "mine now\n" {
		t.Fatalf("the drifted file must be left alone, got %q", got)
	}
	if _, err := os.Stat(b); !os.IsNotExist(err) {
		t.Fatalf("the rest of the turn should still come back, got %v", err)
	}
	notice := lastSystem(t, m)
	if !strings.Contains(notice, a) || !strings.Contains(notice, "[f]") {
		t.Fatalf("the notice should name what was skipped and how to force it, got %q", notice)
	}
}

// Force is the second answer, and it takes the drifted file back too.
func TestUndo_ForceOverwritesDrift(t *testing.T) {
	m, path := undoModel(t)
	if err := os.WriteFile(path, []byte("mine now\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m = sendText(t, m, "/undo")
	m = press(t, m, "f")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("force should have deleted the file the turn created, got %v", err)
	}
	// What force discarded is itself recorded, so it is not lost either.
	turn, ok := m.changes.Turn(2)
	if !ok || turn.Records[0].Before != "mine now\n" {
		t.Fatalf("the undo should record what force discarded, got %+v", turn)
	}
}

// Records dropped to stay inside the store's bound cannot be restored, and
// undo says exactly that rather than failing quietly.
func TestUndo_RefusesAnEvictedTurn(t *testing.T) {
	m, _ := undoModel(t)
	// A store that has already dropped turn 1 is what undo has to answer for.
	small := changeset.New(64)
	small.Add(1, changeset.Record{Path: "a.go", After: strings.Repeat("a", 40), AfterExists: true})
	small.Add(2, changeset.Record{Path: "b.go", After: strings.Repeat("b", 40), AfterExists: true})
	m.changes = small

	updated, _ := m.undoTurn(1, nil)
	m = updated.(Model)
	if m.state == stateUndoConfirm {
		t.Fatal("an evicted turn has nothing to confirm")
	}
	notice := lastSystem(t, m)
	if !strings.Contains(notice, "dropped") || !strings.Contains(notice, "no longer be undone") {
		t.Fatalf("the refusal should explain itself, got %q", notice)
	}
}

// A turn that changed nothing is a different answer from one that was
// dropped, and undo tells them apart.
func TestUndo_RefusesATurnThatChangedNothing(t *testing.T) {
	m, _ := undoModel(t)
	updated, _ := m.undoTurn(9, nil)
	m = updated.(Model)
	if notice := lastSystem(t, m); !strings.Contains(notice, "changed no files") {
		t.Fatalf("a turn with no records is not an eviction, got %q", notice)
	}
}

// Undo writes files, so it waits for the running turn rather than editing
// underneath it.
func TestUndo_WaitsForARunningTurn(t *testing.T) {
	m, path := undoModel(t)
	m = sendText(t, m, "keep going")
	if !m.working() {
		t.Fatal("the model should be mid-turn for this case")
	}

	m = sendText(t, m, "/undo")
	if m.state == stateUndoConfirm {
		t.Fatal("undo should not open while the turn is writing")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("nothing should have been written: %v", err)
	}
}

// The records outlive the process that made them, so shutting the terminal is
// no longer the same act as accepting every edit the session made.
func TestUndo_AResumedSessionUndoesAPreRestartTurn(t *testing.T) {
	db := rewindTestDB(t)
	path := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	first := rewindChangeModel(t, db, changeset.New(0), "the work")
	slot := first.sessionName
	first = sendText(t, first, "change it")
	recordEdit(t, first, path, "before\n", "after\n")
	first = completeReply(t, first, "done")

	// A second process opening the same slot: a fresh changeset store, and
	// the conversation's own name.
	next := rewindChangeModel(t, db, changeset.New(0), "second sitting")
	next.adoptSlot(slot)
	if next.turnCount != 1 {
		t.Fatalf("the resumed session should carry on numbering past what the slot holds, got %d", next.turnCount)
	}
	if _, ok := next.changes.Turn(1); ok {
		t.Fatal("a fresh store holds nothing in memory; the record is what answers here")
	}

	updated, _ := next.undoTurn(1, nil)
	next = updated.(Model)
	if next.state != stateUndoConfirm || next.undoAsk == nil {
		t.Fatalf("a turn from before the restart should still be undoable, got state %v", next.state)
	}
	next = press(t, next, "y")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "before\n" {
		t.Fatalf("the pre-restart turn should have been put back, got %q", content)
	}
}

// The byte bound is what one process holds at once, not what can be taken
// back.
func TestUndo_ATurnEvictedFromMemoryIsStillOnRecord(t *testing.T) {
	db := rewindTestDB(t)
	path := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A bound too small to hold the first turn beside the second.
	m := rewindChangeModel(t, db, changeset.New(64), "the work")
	m = sendText(t, m, "change it")
	recordEdit(t, m, path, "before\n", strings.Repeat("x", 200))
	m = completeReply(t, m, "done")
	m = sendText(t, m, "again")
	recordEdit(t, m, path, strings.Repeat("x", 200), "after\n")
	m = completeReply(t, m, "done")

	if !m.changes.WasEvicted(1) {
		t.Fatal("turn 1 should have been evicted to stay inside the bound")
	}
	updated, _ := m.undoTurn(1, nil)
	m = updated.(Model)
	if m.state != stateUndoConfirm {
		t.Fatalf("an evicted turn is still on record and still undoable, got %q", lastSystem(t, m))
	}
}

// A turn that changed no files leaves no record, so the records alone would
// tell a resumed sitting the numbering got less far than it did — and the
// next turn would take a number an earlier one already has.
func TestUndo_ResumeNumbersPastATurnThatChangedNothing(t *testing.T) {
	db := rewindTestDB(t)
	path := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	first := rewindChangeModel(t, db, changeset.New(0), "the work")
	slot := first.sessionName
	first = sendText(t, first, "change it")
	recordEdit(t, first, path, "before\n", "after\n")
	first = completeReply(t, first, "done")
	// A second turn that only talked: no record, and the numbering still
	// moved.
	first = completeExchange(t, first, "and what does it do", "it does this")
	if first.turnCount != 2 {
		t.Fatalf("the sitting reached turn 2, got %d", first.turnCount)
	}

	next := rewindChangeModel(t, db, changeset.New(0), "second sitting")
	next.resumeConversation(slot, first.Messages())
	if next.turnCount < 2 {
		t.Fatalf("the resumed sitting must not re-use a number, got %d", next.turnCount)
	}
	// And the turn that did change something is still addressable by its own
	// number.
	updated, _ := next.undoTurn(1, nil)
	if updated.(Model).state != stateUndoConfirm {
		t.Fatal("turn 1 should still be the turn that changed the file")
	}
}
