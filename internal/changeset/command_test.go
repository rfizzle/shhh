package changeset

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A command's record has no before side, and nothing reads one into it: it
// is neither a creation nor a deletion, it counts no lines, and an undo plan
// names it apart from the files it can put back rather than deleting a file
// it took for new.
func TestCommandRecordHasNoBeforeSide(t *testing.T) {
	dir := t.TempDir()
	gen := filepath.Join(dir, "gen.pb.go")
	if err := os.WriteFile(gen, []byte("package gen\n\nvar X = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Record{Path: gen, After: "package gen\n\nvar X = 1\n", AfterExists: true,
		Origin: ByCommand, Evidence: "make gen", Generated: true}
	if !r.BeforeUnknown() || r.Created() || r.Deleted() || !r.Changed() {
		t.Fatalf("unknown=%v created=%v deleted=%v changed=%v", r.BeforeUnknown(), r.Created(), r.Deleted(), r.Changed())
	}
	if r.Origin.String() != "by a command" || ByClaim.String() != "claimed" {
		t.Fatalf("origin words: %q, %q", r.Origin, ByClaim)
	}

	s := New(0)
	s.Add(1, Record{Path: filepath.Join(dir, "edit.go"), Before: "a\n", After: "b\n", BeforeExists: true, AfterExists: true})
	s.Add(1, r)
	turn, _ := s.Turn(1)
	got, _ := turn.Record(gen)
	if got.Evidence != "make gen" || !got.Generated || got.Added != 0 || len(got.Hunks) != 0 {
		t.Fatalf("the store kept %+v", got)
	}
	if turn.Added != 1 || turn.Removed != 1 {
		t.Fatalf("the turn counts lines nobody read: +%d −%d", turn.Added, turn.Removed)
	}
	if !slices.Contains(s.Paths(), gen) {
		t.Fatalf("the stager's paths leave the command's file out: %v", s.Paths())
	}
	for _, f := range s.SessionFiles() {
		if f.Path == gen {
			t.Fatal("a file with no before side has no session diff to draw")
		}
	}
	if s.Origins()[gen] != "generated" {
		t.Fatalf("origins = %v", s.Origins())
	}

	plan := PlanUndo(turn, nil)
	if len(plan.Files) != 1 || len(plan.Kept) != 1 || plan.KeptPaths()[0] != gen || plan.Removes() != 0 {
		t.Fatalf("plan files=%d kept=%v removes=%d", len(plan.Files), plan.KeptPaths(), plan.Removes())
	}
	out := plan.Apply(true)
	if len(out.Records) != 1 {
		t.Fatalf("undo restored %d records", len(out.Records))
	}
	if _, err := os.Stat(gen); err != nil {
		t.Fatalf("undo removed the command's file: %v", err)
	}
}

// A file a write recorded with both sides is not recorded again by a command
// in the same turn, and a file a command recorded stays one with no before
// side when a write lands on it after — in a turn and across a fold.
func TestCommandRecordMergesKeepTheMissingBeforeSide(t *testing.T) {
	s := New(0)
	edit := Record{Path: "a.go", Before: "x\n", After: "y\n", BeforeExists: true, AfterExists: true}
	s.Add(1, edit)
	s.Add(1, Record{Path: "a.go", After: "z\n", AfterExists: true, Origin: ByCommand, Evidence: "gofmt -w ."})
	turn, _ := s.Turn(1)
	if r, _ := turn.Record("a.go"); r.Origin != Approved || r.After != "y\n" {
		t.Fatalf("the command record took over an edit: %+v", r)
	}

	s.Add(2, Record{Path: "b.go", After: "1\n", AfterExists: true, Origin: ByCommand, Evidence: "make gen"})
	s.Add(2, Record{Path: "b.go", Before: "1\n", After: "2\n", BeforeExists: true, AfterExists: true})
	turn, _ = s.Turn(2)
	if r, _ := turn.Record("b.go"); !r.BeforeUnknown() || r.Evidence != "make gen" || r.After != "2\n" {
		t.Fatalf("a write after a command lost the missing before side: %+v", r)
	}

	t1 := Turn{N: 1, Records: []Record{{Path: "c.go", Before: "a\n", After: "b\n", BeforeExists: true, AfterExists: true}}}
	t2 := Turn{N: 2, Records: []Record{{Path: "c.go", After: "c\n", AfterExists: true, Origin: ByCommand}}}
	if r, _ := Fold([]Turn{t1, t2}).Record("c.go"); !r.BeforeUnknown() {
		t.Fatalf("a fold over a command's record restores content nobody read: %+v", r)
	}
}

// A link a command made is recorded as its target's text, which is what git
// stores for it, never as the content of whatever it points at.
func TestCommandBracketRecordsALinkAsItsTarget(t *testing.T) {
	root, elsewhere := t.TempDir(), t.TempDir()
	secret := filepath.Join(elsewhere, "id_ed25519")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "key")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	recs, _ := CommandBracket{Root: root}.Records([]string{"key"}, "ln -s", nil)
	if len(recs) != 1 || recs[0].After != secret {
		t.Fatalf("the link was recorded as %+v", recs)
	}
}

// The bracket: what became dirty while the command ran is its work; what was
// dirty already is not, whoever changed it; a path a write already holds is
// not recorded twice; and a tree past the bound records nothing.
func TestCommandBracketRecordsWhatBecameDirty(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{"new.go": "package x\n", "theirs.go": "mine\n", "edited.go": "e\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b := CommandBracket{Root: root, Before: []string{"theirs.go"}}
	held := func(abs string) bool { return abs == filepath.Join(root, "edited.go") }
	recs, over := b.Records([]string{"theirs.go", "new.go", "edited.go", "gone.go"}, "make gen", held)
	if over || len(recs) != 2 {
		t.Fatalf("records = %+v, over = %v", recs, over)
	}
	if recs[0].Path != filepath.Join(root, "new.go") || !recs[0].AfterExists || recs[0].After != "package x\n" ||
		recs[0].Origin != ByCommand || recs[0].Evidence != "make gen" {
		t.Fatalf("created file: %+v", recs[0])
	}
	if recs[1].Path != filepath.Join(root, "gone.go") || recs[1].AfterExists {
		t.Fatalf("deleted file: %+v", recs[1])
	}
	if recs, _ := b.Records([]string{"theirs.go"}, "true", nil); len(recs) != 0 {
		t.Fatalf("a command that changed nothing new recorded %+v", recs)
	}
	b.Over = func(paths int, _ int64) bool { return paths > 1 }
	if recs, over := b.Records([]string{"new.go", "gone.go"}, "make gen", nil); !over || recs != nil {
		t.Fatalf("past the bound: %+v, over = %v", recs, over)
	}
}
