package changeset

import (
	"fmt"
	"strings"
	"testing"
)

func rec(path, before, after string) Record {
	return Record{
		Path:         path,
		Before:       before,
		After:        after,
		BeforeExists: before != "",
		AfterExists:  true,
		Agent:        MainAgent,
		Origin:       Approved,
		Track:        TrackTracked,
	}
}

func TestAdd_ComputesHunksAndCounts(t *testing.T) {
	s := New(0)
	s.Add(1, rec("a.go", "one\ntwo\n", "one\ntwo\nthree\n"))

	turn, ok := s.Turn(1)
	if !ok {
		t.Fatal("turn 1 should be recorded")
	}
	if turn.Files() != 1 {
		t.Fatalf("expected 1 file, got %d", turn.Files())
	}
	r := turn.Records[0]
	if len(r.Hunks) == 0 {
		t.Fatal("the record should carry computed hunks")
	}
	if r.Added != 1 || r.Removed != 0 {
		t.Fatalf("expected +1 −0, got +%d −%d", r.Added, r.Removed)
	}
	if r.Created() || r.Deleted() {
		t.Fatal("an edit of an existing file is neither a creation nor a deletion")
	}
}

func TestAdd_IgnoresAChangeThatChangedNothing(t *testing.T) {
	s := New(0)
	s.Add(1, rec("a.go", "same\n", "same\n"))
	if _, ok := s.Turn(1); ok {
		t.Fatal("a byte-identical write is not a change")
	}
}

func TestTurn_Aggregates(t *testing.T) {
	s := New(0)
	s.Add(7, rec("agent/loop.go", "a\n", "a\nb\nc\n"))
	s.Add(7, rec("ui/model.go", "x\ny\n", "x\n"))
	child := rec("docs/loop.md", "", "hello\n")
	child.Agent = "writer-1"
	child.Origin = ChildPatch
	child.Track = TrackUntracked
	s.Add(7, child)

	turn, _ := s.Turn(7)
	if turn.Files() != 3 {
		t.Fatalf("expected 3 files, got %d", turn.Files())
	}
	if turn.Added != 3 || turn.Removed != 1 {
		t.Fatalf("expected +3 −1, got +%d −%d", turn.Added, turn.Removed)
	}
	if turn.Hunks != 3 {
		t.Fatalf("expected 3 hunks, got %d", turn.Hunks)
	}
	if len(turn.Agents) != 2 {
		t.Fatalf("expected two agents attributed, got %+v", turn.Agents)
	}
	if turn.Agents[0].Name != MainAgent || turn.Agents[0].Files != 2 {
		t.Fatalf("main should own 2 files, got %+v", turn.Agents[0])
	}
	if turn.Agents[1].Name != "writer-1" || turn.Agents[1].Added != 1 {
		t.Fatalf("writer-1 should own its one added line, got %+v", turn.Agents[1])
	}
	if r, _ := turn.Record("docs/loop.md"); !r.Created() || r.Track != TrackUntracked {
		t.Fatalf("a new file from a child patch should read as created and untracked, got %+v", r)
	}
	if r, _ := turn.Record("docs/loop.md"); r.Origin != ChildPatch || r.Origin.String() != "child patch" {
		t.Fatalf("origin should survive the record, got %v", r.Origin)
	}
}

func TestAdd_RepeatedEditsCollapseToOneNetRecord(t *testing.T) {
	s := New(0)
	s.Add(2, rec("a.go", "one\n", "one\ntwo\n"))
	s.Add(2, rec("a.go", "one\ntwo\n", "one\ntwo\nthree\n"))

	turn, _ := s.Turn(2)
	if turn.Files() != 1 {
		t.Fatalf("two edits of one file are one record, got %d", turn.Files())
	}
	r := turn.Records[0]
	if r.Before != "one\n" || r.After != "one\ntwo\nthree\n" {
		t.Fatalf("the net record should span both edits, got before=%q after=%q", r.Before, r.After)
	}
	if r.Added != 2 || r.Removed != 0 {
		t.Fatalf("expected the net +2 −0, got +%d −%d", r.Added, r.Removed)
	}
	if turn.Added != 2 {
		t.Fatalf("the turn's aggregate should follow the net record, got +%d", turn.Added)
	}
}

func TestAdd_EditedBackToWhereItStartedDropsTheRecord(t *testing.T) {
	s := New(0)
	s.Add(3, rec("a.go", "one\n", "two\n"))
	s.Add(3, rec("a.go", "two\n", "one\n"))

	turn, ok := s.Turn(3)
	if ok && turn.Files() != 0 {
		t.Fatalf("a file edited back to its starting content changed nothing, got %+v", turn.Records)
	}
}

func TestAdd_CreationCollapsesWithItsLaterEdit(t *testing.T) {
	s := New(0)
	created := Record{Path: "new.go", After: "package main\n", AfterExists: true}
	s.Add(4, created)
	s.Add(4, rec("new.go", "package main\n", "package main\n\nfunc main() {}\n"))

	turn, _ := s.Turn(4)
	if turn.Files() != 1 {
		t.Fatalf("expected one record, got %d", turn.Files())
	}
	if r := turn.Records[0]; !r.Created() {
		t.Fatalf("the net record should still read as a creation, got %+v", r)
	}
	if turn.Records[0].Agent != MainAgent {
		t.Fatalf("an unattributed record defaults to the session's own agent, got %q", turn.Records[0].Agent)
	}
}

func TestEviction_DropsOldestTurnsAndSaysSo(t *testing.T) {
	big := strings.Repeat("x\n", 400) // ~800 bytes per side
	s := New(3000)
	for turn := int64(1); turn <= 3; turn++ {
		s.Add(turn, rec(fmt.Sprintf("f%d.go", turn), big, big+"tail\n"))
	}
	if evicted := s.Add(4, rec("f4.go", big, big+"tail\n")); len(evicted) == 0 {
		t.Fatal("a write past the bound should report what it evicted")
	}
	if _, ok := s.Turn(1); ok {
		t.Fatal("the oldest turn should have been evicted")
	}
	if !s.WasEvicted(1) {
		t.Fatal("eviction should be visible, not silent")
	}
	if _, ok := s.Turn(4); !ok {
		t.Fatal("the newest turn must survive its own write")
	}
	if s.Bytes() > 3000 {
		t.Fatalf("the store should be inside its bound, holding %d bytes", s.Bytes())
	}
	if got := s.Evicted(); len(got) == 0 || got[0] != 1 {
		t.Fatalf("evicted turns should be listed oldest first, got %v", got)
	}
}

func TestEviction_NeverDropsTheOnlyTurn(t *testing.T) {
	s := New(16)
	s.Add(1, rec("a.go", "", strings.Repeat("y\n", 100)))
	if _, ok := s.Turn(1); !ok {
		t.Fatal("an oversized single turn is kept rather than leaving the session with no record")
	}
}

func TestUntrackedAndUnknownAreDifferentAnswers(t *testing.T) {
	s := New(0)
	untracked := rec("scratch.txt", "", "note\n")
	untracked.Track = TrackUntracked
	s.Add(1, untracked)
	unknown := rec("nogit.txt", "", "note\n")
	unknown.Track = TrackUnknown
	s.Add(1, unknown)

	turn, _ := s.Turn(1)
	a, _ := turn.Record("scratch.txt")
	b, _ := turn.Record("nogit.txt")
	if a.Track != TrackUntracked || a.Track.String() != "untracked" {
		t.Fatalf("expected untracked, got %v", a.Track)
	}
	if b.Track != TrackUnknown || b.Track.String() != "unknown" {
		t.Fatalf("outside a repository the answer is unknown, got %v", b.Track)
	}
}

func TestSession_CollapsesTurnsIntoOneDiffPerFile(t *testing.T) {
	s := New(0)
	s.Add(1, rec("a.go", "one\n", "one\ntwo\n"))
	s.Add(2, rec("a.go", "one\ntwo\n", "one\ntwo\nthree\n"))
	s.Add(2, rec("b.go", "keep\n", "keep\nmore\n"))

	files := s.Session()
	if len(files) != 2 {
		t.Fatalf("expected 2 files in the session diff, got %d", len(files))
	}
	if files[0].Path != "a.go" || files[1].Path != "b.go" {
		t.Fatalf("session files should be in first-edit order, got %+v", files)
	}
	filesTouched, added, removed := s.Totals()
	if filesTouched != 2 || added != 3 || removed != 0 {
		t.Fatalf("expected 2 files +3 −0, got %d files +%d −%d", filesTouched, added, removed)
	}
	var text strings.Builder
	for _, l := range files[0].Hunks[0].Lines {
		text.WriteString(l.Text + "\n")
	}
	if !strings.Contains(text.String(), "three") {
		t.Fatalf("the session diff should span both turns:\n%s", text.String())
	}
}

func TestNilStoreIsUsable(t *testing.T) {
	var s *Store
	if evicted := s.Add(1, rec("a.go", "", "x\n")); evicted != nil {
		t.Fatal("a nil store swallows writes")
	}
	if _, ok := s.Turn(1); ok {
		t.Fatal("a nil store has no turns")
	}
	if _, ok := s.Latest(); ok {
		t.Fatal("a nil store has no latest turn")
	}
	if s.Session() != nil || s.Evicted() != nil || s.Bytes() != 0 || s.WasEvicted(1) {
		t.Fatal("a nil store reads as empty")
	}
}

func TestLatest(t *testing.T) {
	s := New(0)
	s.Add(1, rec("a.go", "", "x\n"))
	s.Add(5, rec("b.go", "", "y\n"))
	turn, ok := s.Latest()
	if !ok || turn.N != 5 {
		t.Fatalf("expected turn 5 as the latest, got %+v", turn)
	}
}
