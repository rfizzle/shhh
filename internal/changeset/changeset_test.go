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

// The collapsed record's before side has to describe one moment: the mode
// travels with the content it belongs to, or an undo restores the first edit's
// text with the second edit's permissions.
func TestAdd_CollapseKeepsTheEarliestBeforeMode(t *testing.T) {
	s := New(0)
	first := rec("a.sh", "one\n", "one\ntwo\n")
	first.BeforeMode = 0o755
	second := rec("a.sh", "one\ntwo\n", "one\ntwo\nthree\n")
	second.BeforeMode = 0o644
	s.Add(2, first)
	s.Add(2, second)

	turn, _ := s.Turn(2)
	if r := turn.Records[0]; r.BeforeMode != 0o755 {
		t.Fatalf("the net record should carry the mode the turn found, got %v", r.BeforeMode)
	}
}

// A patch can carry a change of permissions and nothing else — git writes one
// as a header with no hunk — and the two modes are then the whole of what
// happened. A store keyed on bytes and existence alone drops that record, and
// the turn goes on to say it changed nothing.
func TestAdd_AModeAloneIsAChange(t *testing.T) {
	s := New(0)
	r := rec("a.sh", "one\n", "one\n")
	r.BeforeMode, r.AfterMode = 0o644, 0o755

	s.Add(3, r)
	turn, ok := s.Turn(3)
	if !ok || turn.Files() != 1 {
		t.Fatalf("a change of mode should be a record, got %+v", turn)
	}
	if got := turn.Records[0].ModeChange(); got != "mode 0644 → 0755" {
		t.Fatalf("the record should state its mode change, got %q", got)
	}
	if !turn.Records[0].ModeOnly() {
		t.Fatal("a record with the same bytes on both sides changed nothing else")
	}
	if got := turn.ModeChange(); got != "mode 0644 → 0755" {
		t.Fatalf("the turn's row should state the mode where its counts would be, got %q", got)
	}
}

// Zero is "nobody read this", not mode 000. Every record made by a path that
// never stats carries it, so reading it as a mode would turn every one of
// them into a change of permissions.
func TestAdd_AnUnknownModeIsNotAChange(t *testing.T) {
	s := New(0)
	unknownAfter := rec("a.sh", "one\n", "one\n")
	unknownAfter.BeforeMode = 0o755
	s.Add(1, unknownAfter)
	unknownBefore := rec("b.sh", "one\n", "one\n")
	unknownBefore.AfterMode = 0o755
	s.Add(1, unknownBefore)

	if turn, ok := s.Turn(1); ok {
		t.Fatalf("a record with a mode on one side only says nothing about permissions, got %+v", turn)
	}
}

// A turn that also moved bytes has counts of its own to state, and a turn
// with two mode changes in it has two things to say where the row says one.
func TestTurn_ModeChangeOnlyStatesASingleFilesPermissions(t *testing.T) {
	s := New(0)
	withContent := rec("a.sh", "one\n", "one\ntwo\n")
	withContent.BeforeMode, withContent.AfterMode = 0o644, 0o755
	s.Add(1, withContent)
	if turn, _ := s.Turn(1); turn.ModeChange() != "" {
		t.Fatalf("a turn with lines to count states them, got %q", turn.ModeChange())
	}

	second := rec("b.sh", "one\n", "one\n")
	second.BeforeMode, second.AfterMode = 0o644, 0o755
	s.Add(2, second)
	third := rec("c.sh", "one\n", "one\n")
	third.BeforeMode, third.AfterMode = 0o600, 0o700
	s.Add(2, third)
	if turn, _ := s.Turn(2); turn.ModeChange() != "" {
		t.Fatalf("two changes are not one row's worth, got %q", turn.ModeChange())
	}
}

// The second edit of a file does not read modes — the tools that write files
// do not change them — so the mode the first edit left is still the one on
// disk, and the net record has to keep it or the change disappears.
func TestAdd_CollapseKeepsTheLatestKnownAfterMode(t *testing.T) {
	s := New(0)
	chmod := rec("a.sh", "one\n", "one\n")
	chmod.BeforeMode, chmod.AfterMode = 0o644, 0o755
	s.Add(2, chmod)
	s.Add(2, rec("a.sh", "one\n", "one\ntwo\n"))

	turn, _ := s.Turn(2)
	if r := turn.Records[0]; r.AfterMode != 0o755 || !r.ModeChanged() {
		t.Fatalf("the net record lost the mode the turn set, got %+v", r)
	}
}

// A turn that chmod'd a file and then deleted it has one net change and it is
// the deletion. Carrying the mode forward onto the gone side would leave the
// record claiming permissions changed on a file that is not there, which is
// a chmod undo would go looking for somewhere to apply.
func TestAdd_AFileDeletedAfterAChmodIsADeletion(t *testing.T) {
	s := New(0)
	chmod := rec("a.sh", "one\n", "one\n")
	chmod.BeforeMode, chmod.AfterMode = 0o644, 0o755
	s.Add(4, chmod)
	removal := rec("a.sh", "one\n", "")
	removal.AfterExists = false
	s.Add(4, removal)

	turn, _ := s.Turn(4)
	r := turn.Records[0]
	if !r.Deleted() {
		t.Fatalf("the net record should be the deletion, got %+v", r)
	}
	if r.ModeChanged() || r.ModeChange() != "" {
		t.Fatalf("a file that is gone has no permissions to have changed, got %q", r.ModeChange())
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

// A session boundary empties the store: the next conversation numbers its
// turns from one, and turn 1 must not answer with the last one's edits.
func TestReset_EmptiesTheStoreAndKeepsItUsable(t *testing.T) {
	s := New(0)
	s.Add(1, rec("a.go", "one\n", "two\n"))

	s.Reset()

	if _, ok := s.Turn(1); ok {
		t.Fatal("the turns should be gone")
	}
	if files, added, removed := s.Totals(); files != 0 || added != 0 || removed != 0 {
		t.Fatalf("the totals should be zero, got %d files +%d -%d", files, added, removed)
	}
	if s.Bytes() != 0 || s.Session() != nil || s.Evicted() != nil || s.WasEvicted(1) {
		t.Fatal("an emptied store reads as a new one")
	}
	s.Add(1, rec("b.go", "", "new\n"))
	turn, ok := s.Turn(1)
	if !ok || len(turn.Records) != 1 || turn.Records[0].Path != "b.go" {
		t.Fatalf("turn 1 belongs to the new session, got %+v", turn)
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
	s.Reset()
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

// SessionFiles is Session with the attribution the inspector rail needs: one
// net row per path, in first-edit order, carrying how many turns produced it
// (docs/interface/surfaces.md#the-inspector-rail).
func TestSessionFilesCarryTurnAttribution(t *testing.T) {
	s := New(DefaultMaxBytes)
	s.Add(1, Record{Path: "loop.go", Before: "a\n", After: "a\nb\n", BeforeExists: true, AfterExists: true})
	s.Add(2, Record{Path: "model.go", Before: "x\n", After: "y\n", BeforeExists: true, AfterExists: true})
	s.Add(3, Record{Path: "loop.go", Before: "a\nb\n", After: "a\nb\nc\n", BeforeExists: true, AfterExists: true})

	files := s.SessionFiles()
	if len(files) != 2 {
		t.Fatalf("one row per path: %+v", files)
	}
	loop := files[0]
	if loop.Path != "loop.go" {
		t.Fatalf("first-edit order: %+v", files)
	}
	if loop.Added != 2 || loop.Removed != 0 {
		t.Fatalf("the net change across both turns: %+v", loop)
	}
	if loop.Turns != 2 || loop.Last != 3 {
		t.Fatalf("turn attribution: %+v", loop)
	}
	if files[1].Turns != 1 || files[1].Last != 2 {
		t.Fatalf("a path edited once says one turn: %+v", files[1])
	}

	// A later turn putting a file back is state, not history: the path drops
	// out of the session's net change entirely.
	s.Add(4, Record{Path: "model.go", Before: "y\n", After: "x\n", BeforeExists: true, AfterExists: true})
	files = s.SessionFiles()
	if len(files) != 1 || files[0].Path != "loop.go" {
		t.Fatalf("a file edited back to where it started nets out: %+v", files)
	}
	if n, added, removed := s.Totals(); n != 1 || added != 2 || removed != 0 {
		t.Fatalf("Totals reads the same aggregation: %d files, +%d −%d", n, added, removed)
	}
}

// The collapsed session diff is held between writes. Every surface that asks
// is drawn from a render loop rather than from the edit that changed
// something, so the same answer is handed out many times per edit — and a
// write is the only thing that can make it wrong.
func TestSessionFiles_HeldUntilAWriteMakesItWrong(t *testing.T) {
	s := New(DefaultMaxBytes)
	s.Add(1, Record{Path: "loop.go", Before: "a\n", After: "a\nb\n", BeforeExists: true, AfterExists: true})

	first := s.SessionFiles()
	if again := s.SessionFiles(); &again[0].Hunks[0] != &first[0].Hunks[0] {
		t.Fatal("a second ask without a write recomputed the walk")
	}
	// What is handed out is a copy, so a caller cannot edit the held answer.
	first[0].Path = "scribbled"
	if s.SessionFiles()[0].Path != "loop.go" {
		t.Fatal("a caller wrote through to what is held")
	}

	s.Add(2, Record{Path: "model.go", Before: "x\n", After: "y\n", BeforeExists: true, AfterExists: true})
	after := s.SessionFiles()
	if len(after) != 2 {
		t.Fatalf("a write clears what was held: %+v", after)
	}
}
