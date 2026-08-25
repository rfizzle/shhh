package changeset

// Undo's mechanics (S-100): what a plan says about the workspace, and what
// applying one leaves behind. Everything here runs against a real temporary
// directory — undo's whole point is that it writes files, and asserting on
// anything less would be asserting on the plan rather than the effect.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wrote records an edit that turned before into after, and puts after on
// disk — the state the turn left the workspace in.
func wrote(t *testing.T, dir, name, before, after string) Record {
	t.Helper()
	path := filepath.Join(dir, name)
	if after != "" {
		if err := os.WriteFile(path, []byte(after), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return Record{
		Path:         path,
		Before:       before,
		After:        after,
		BeforeExists: before != "",
		AfterExists:  after != "",
		Agent:        MainAgent,
		Origin:       Approved,
		Track:        TrackTracked,
	}
}

func read(t *testing.T, path string) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data), true
}

// turnOf builds a store turn from records, so the plan sees the same shape
// the session would hand it.
func turnOf(t *testing.T, records ...Record) Turn {
	t.Helper()
	s := New(0)
	for _, r := range records {
		s.Add(1, r)
	}
	turn, ok := s.Turn(1)
	if !ok {
		t.Fatal("the records should have made a turn")
	}
	return turn
}

func TestUndo_RestoresTheBeforeContent(t *testing.T) {
	dir := t.TempDir()
	turn := turnOf(t, wrote(t, dir, "a.go", "one\n", "one\ntwo\n"))

	plan := PlanUndo(turn, nil)
	if len(plan.Drifted()) != 0 {
		t.Fatalf("a workspace still holding what the turn wrote has not drifted, got %v", plan.Drifted())
	}
	if plan.Restores() != 1 || plan.Removes() != 0 {
		t.Fatalf("expected one restore and no deletion, got %d/%d", plan.Restores(), plan.Removes())
	}

	out := plan.Apply(false)
	if len(out.Records) != 1 || len(out.Skipped) != 0 || len(out.Failed) != 0 {
		t.Fatalf("a clean undo restores everything and skips nothing, got %+v", out)
	}
	if got, _ := read(t, filepath.Join(dir, "a.go")); got != "one\n" {
		t.Fatalf("the file should be back to what it was, got %q", got)
	}
	// The reverse record is an ordinary edit: from what was found to what
	// the turn had replaced, which is what makes the undo itself undoable.
	r := out.Records[0]
	if r.Before != "one\ntwo\n" || r.After != "one\n" || !r.BeforeExists || !r.AfterExists {
		t.Fatalf("the reverse record should describe the undo as an edit, got %+v", r)
	}
}

// A file changed since the turn holds something the record never saw, so the
// default answer leaves it exactly as it is.
func TestUndo_DriftIsListedAndSkipped(t *testing.T) {
	dir := t.TempDir()
	turn := turnOf(t,
		wrote(t, dir, "a.go", "one\n", "one\ntwo\n"),
		wrote(t, dir, "b.go", "x\n", "x\ny\n"))
	drifted := filepath.Join(dir, "a.go")
	if err := os.WriteFile(drifted, []byte("someone else was here\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan := PlanUndo(turn, nil)
	if got := plan.Drifted(); len(got) != 1 || got[0] != drifted {
		t.Fatalf("the changed file should be the only drift, got %v", got)
	}
	if plan.Touches(false) != 1 || plan.Touches(true) != 2 {
		t.Fatalf("the default answer touches one file and force both, got %d/%d",
			plan.Touches(false), plan.Touches(true))
	}

	out := plan.Apply(false)
	if len(out.Skipped) != 1 || out.Skipped[0] != drifted {
		t.Fatalf("the drifted file should be skipped by name, got %v", out.Skipped)
	}
	if got, _ := read(t, drifted); got != "someone else was here\n" {
		t.Fatalf("a skipped file must be untouched, got %q", got)
	}
	if got, _ := read(t, filepath.Join(dir, "b.go")); got != "x\n" {
		t.Fatalf("the rest of the turn still comes back, got %q", got)
	}
}

// Force is the deliberate second answer: it takes the drifted file back too,
// and records what it discarded so that is itself recoverable.
func TestUndo_ForceOverwritesDrift(t *testing.T) {
	dir := t.TempDir()
	turn := turnOf(t, wrote(t, dir, "a.go", "one\n", "one\ntwo\n"))
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := PlanUndo(turn, nil).Apply(true)
	if len(out.Skipped) != 0 || len(out.Records) != 1 {
		t.Fatalf("force restores the drifted file too, got %+v", out)
	}
	if got, _ := read(t, path); got != "one\n" {
		t.Fatalf("the before-content should have won, got %q", got)
	}
	if out.Records[0].Before != "mine\n" {
		t.Fatalf("the record should hold what force discarded, got %q", out.Records[0].Before)
	}
}

func TestUndo_DeletesWhatTheTurnCreated(t *testing.T) {
	dir := t.TempDir()
	turn := turnOf(t, wrote(t, dir, "new.go", "", "package main\n"))

	plan := PlanUndo(turn, nil)
	if plan.Removes() != 1 || plan.Restores() != 0 {
		t.Fatalf("a created file is deleted, not rewritten, got %d/%d", plan.Removes(), plan.Restores())
	}
	out := plan.Apply(false)
	if len(out.Records) != 1 {
		t.Fatalf("the deletion should be recorded, got %+v", out)
	}
	if _, ok := read(t, filepath.Join(dir, "new.go")); ok {
		t.Fatal("a file the turn created should be gone after undo")
	}
	if r := out.Records[0]; !r.Deleted() {
		t.Fatalf("the reverse record of a creation is a deletion, got %+v", r)
	}
}

func TestUndo_RestoresWhatTheTurnDeleted(t *testing.T) {
	dir := t.TempDir()
	// The turn deleted the file, so nothing is on disk to read back.
	turn := turnOf(t, wrote(t, dir, "gone.go", "package main\n", ""))

	plan := PlanUndo(turn, nil)
	if len(plan.Drifted()) != 0 {
		t.Fatalf("an absent file is what the turn left behind, not drift: %v", plan.Drifted())
	}
	out := plan.Apply(false)
	if len(out.Failed) != 0 {
		t.Fatalf("restoring a deleted file should not fail, got %+v", out.Failed)
	}
	got, ok := read(t, filepath.Join(dir, "gone.go"))
	if !ok || got != "package main\n" {
		t.Fatalf("the deleted file should be back, got %q (exists=%v)", got, ok)
	}
}

// A restore recreates the directories the turn's deletion emptied.
func TestUndo_RecreatesMissingDirectories(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "sub")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	turn := turnOf(t, wrote(t, nested, "gone.go", "x\n", ""))
	if err := os.RemoveAll(nested); err != nil {
		t.Fatal(err)
	}

	out := PlanUndo(turn, nil).Apply(false)
	if len(out.Failed) != 0 {
		t.Fatalf("the restore should have made the directory, got %+v", out.Failed)
	}
	if got, _ := read(t, filepath.Join(nested, "gone.go")); got != "x\n" {
		t.Fatalf("the file should be back under a recreated directory, got %q", got)
	}
}

// A selection undoes only what it names — what review stages.
func TestUndo_PlanHonoursASelection(t *testing.T) {
	dir := t.TempDir()
	turn := turnOf(t,
		wrote(t, dir, "a.go", "one\n", "one\ntwo\n"),
		wrote(t, dir, "b.go", "x\n", "x\ny\n"))

	plan := PlanUndo(turn, []string{filepath.Join(dir, "b.go")})
	if len(plan.Files) != 1 || plan.Files[0].Path() != filepath.Join(dir, "b.go") {
		t.Fatalf("the plan should cover only the selection, got %+v", plan.Files)
	}
	plan.Apply(false)
	if got, _ := read(t, filepath.Join(dir, "a.go")); got != "one\ntwo\n" {
		t.Fatalf("the unselected file should be untouched, got %q", got)
	}
}

// An evicted turn is a gap, not an empty one: the store says which it is, so
// undo can refuse with an explanation rather than a shrug.
func TestUndo_EvictedTurnIsRefusableApart(t *testing.T) {
	s := New(64)
	s.Add(1, Record{Path: "a.go", After: strings.Repeat("a", 40), AfterExists: true})
	s.Add(2, Record{Path: "b.go", After: strings.Repeat("b", 40), AfterExists: true})

	if _, ok := s.Turn(1); ok {
		t.Fatal("the oldest turn should have been evicted by the bound")
	}
	if !s.WasEvicted(1) {
		t.Fatal("eviction should be reportable, which is what undo refuses with")
	}
	if s.WasEvicted(9) {
		t.Fatal("a turn that never existed was not evicted")
	}
}
