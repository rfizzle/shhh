package storage

// The changes table: that a turn's records survive the process that made
// them, that the same bytes are stored once however many records hold them,
// and that they go when the conversation they belong to does.

import (
	"os"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/changeset"
)

// changeSlot claims a slot to hang records off, the way a session does on the
// way in.
func changeSlot(t *testing.T, db *DB, name string) string {
	t.Helper()
	slot, err := db.ClaimChatSlot(name)
	if err != nil {
		t.Fatalf("claim slot: %v", err)
	}
	return slot
}

// saveTurn writes a whole turn's records the way the changeset does, one at a
// time in the order it holds them.
func saveTurn(t *testing.T, db *DB, slot string, turn int64, records ...changeset.Record) {
	t.Helper()
	for i, r := range records {
		if err := db.SaveChange(slot, turn, i, r); err != nil {
			t.Fatalf("save change: %v", err)
		}
	}
}

// changeCount answers a scalar query these tests read as a number.
func changeCount(t *testing.T, db *DB, query string) int {
	t.Helper()
	var n int
	if err := db.sql.QueryRow(query).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func TestChanges_SaveAndLoadRoundTrip(t *testing.T) {
	db := openTestDB(t)
	slot := changeSlot(t, db, "work")

	at := time.Now().UTC().Truncate(time.Millisecond)
	want := []changeset.Record{
		{Path: "main.go", Before: "one\n", After: "two\n",
			BeforeExists: true, AfterExists: true,
			BeforeMode: 0o644, AfterMode: 0o755,
			Agent: changeset.MainAgent, Origin: changeset.AutoApproved,
			Track: changeset.TrackTracked, At: at},
		{Path: "new.go", After: "fresh\n", AfterExists: true,
			Agent: "writer", Origin: changeset.ChildPatch,
			Track: changeset.TrackUntracked, At: at},
	}
	saveTurn(t, db, slot, 3, want...)

	got, err := db.LoadChanges(slot, 1, 0)
	if err != nil {
		t.Fatalf("load changes: %v", err)
	}
	if len(got) != 1 || got[0].Turn != 3 || len(got[0].Records) != 2 {
		t.Fatalf("expected one turn of two records, got %+v", got)
	}
	first := got[0].Records[0]
	if first.Path != "main.go" || first.Before != "one\n" || first.After != "two\n" {
		t.Fatalf("the content on both sides should come back: %+v", first)
	}
	if !first.BeforeExists || !first.AfterExists {
		t.Fatalf("existence should come back: %+v", first)
	}
	if first.BeforeMode != os.FileMode(0o644) || first.AfterMode != os.FileMode(0o755) {
		t.Fatalf("the permission bits should come back: %v → %v", first.BeforeMode, first.AfterMode)
	}
	if first.Origin != changeset.AutoApproved || first.Track != changeset.TrackTracked {
		t.Fatalf("how the edit was applied should come back: %+v", first)
	}
	if !first.At.Equal(at) {
		t.Fatalf("when it happened should come back: %v, want %v", first.At, at)
	}
	second := got[0].Records[1]
	if second.BeforeExists || second.Agent != "writer" || second.Origin != changeset.ChildPatch {
		t.Fatalf("a created file's record should come back as one: %+v", second)
	}
}

// A file edited all afternoon is many records over a handful of contents, and
// the content is what costs anything.
func TestChanges_ContentIsStoredOncePerDistinctContent(t *testing.T) {
	db := openTestDB(t)
	slot := changeSlot(t, db, "work")

	// Ten turns walking one file from "v0" to "v10": eleven distinct
	// contents across twenty record sides.
	for turn := int64(1); turn <= 10; turn++ {
		before := "v" + string(rune('0'+turn-1))
		after := "v" + string(rune('0'+turn))
		saveTurn(t, db, slot, turn, changeset.Record{
			Path: "main.go", Before: before, After: after,
			BeforeExists: true, AfterExists: true, At: time.Now(),
		})
	}
	if rows := changeCount(t, db, `SELECT COUNT(*) FROM changes`); rows != 10 {
		t.Fatalf("expected ten records, got %d", rows)
	}
	if blobs := changeCount(t, db, `SELECT COUNT(*) FROM change_blobs`); blobs != 11 {
		t.Fatalf("twenty record sides over eleven distinct contents should be eleven blobs, got %d", blobs)
	}
}

// A turn's records are its net change, so writing a path again replaces what
// was there rather than adding to it.
func TestChanges_SavingAPathAgainReplacesIt(t *testing.T) {
	db := openTestDB(t)
	slot := changeSlot(t, db, "work")

	rec := changeset.Record{Path: "a.go", After: "one\n", AfterExists: true, At: time.Now()}
	saveTurn(t, db, slot, 1, rec)
	rec.After = "two\n"
	saveTurn(t, db, slot, 1, rec)
	got, err := db.LoadChanges(slot, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Records) != 1 || got[0].Records[0].After != "two\n" {
		t.Fatalf("the later save should stand alone, got %+v", got)
	}
}

func TestChanges_LastTurnIsWhereTheNumberingGotTo(t *testing.T) {
	db := openTestDB(t)
	slot := changeSlot(t, db, "work")

	if last, err := db.LastChangeTurn(slot); err != nil || last != 0 {
		t.Fatalf("a slot with no records is at zero, got %d (%v)", last, err)
	}
	saveTurn(t, db, slot, 4, changeset.Record{Path: "a.go", After: "x", AfterExists: true, At: time.Now()})
	if last, err := db.LastChangeTurn(slot); err != nil || last != 4 {
		t.Fatalf("expected turn 4, got %d (%v)", last, err)
	}
	if last, err := db.LastChangeTurn("nobody"); err != nil || last != 0 {
		t.Fatalf("a slot nothing was written under is at zero, got %d (%v)", last, err)
	}
}

// The records belong to the conversation, so deleting it takes them.
func TestChanges_GoWithTheConversation(t *testing.T) {
	db := openTestDB(t)
	kept := changeSlot(t, db, "kept")
	going := changeSlot(t, db, "going")

	rec := changeset.Record{Path: "a.go", After: "x", AfterExists: true, At: time.Now()}
	saveTurn(t, db, kept, 1, rec)
	saveTurn(t, db, going, 1, rec)
	if err := db.DeleteChat(going); err != nil {
		t.Fatalf("delete chat: %v", err)
	}
	if rows := changeCount(t, db, `SELECT COUNT(*) FROM changes`); rows != 1 {
		t.Fatalf("the deleted conversation should have taken its records, got %d rows", rows)
	}
	got, err := db.LoadChanges(kept, 1, 0)
	if err != nil || len(got) != 1 {
		t.Fatalf("the conversation that stayed keeps its own, got %+v (%v)", got, err)
	}
}

// The window is the conversations' own, and the content nothing points at any
// more goes with the rows that pointed at it.
func TestChanges_PruneFollowsTheConversationWindow(t *testing.T) {
	db := openTestDB(t)
	slot := changeSlot(t, db, "work")

	old := time.Now().AddDate(0, 0, -40)
	stale := changeset.Record{Path: "old.go", After: "gone\n", AfterExists: true, At: old}
	fresh := changeset.Record{Path: "new.go", After: "here\n", AfterExists: true, At: time.Now()}
	saveTurn(t, db, slot, 1, stale)
	saveTurn(t, db, slot, 2, fresh)

	if n, err := db.PruneOldChanges(0); err != nil || n != 0 {
		t.Fatalf("no window means no prune, got %d (%v)", n, err)
	}
	if rows := changeCount(t, db, `SELECT COUNT(*) FROM changes`); rows != 2 {
		t.Fatalf("both records should still be there, got %d", rows)
	}

	n, err := db.PruneOldChanges(30)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected the one record past the window, got %d", n)
	}
	got, err := db.LoadChanges(slot, 1, 0)
	if err != nil || len(got) != 1 || got[0].Turn != 2 {
		t.Fatalf("the record inside the window should stay, got %+v (%v)", got, err)
	}
	// "gone\n" was only ever named by the pruned record; "here\n" and the
	// empty before side are still named by the one that stayed.
	var held int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM change_blobs WHERE content = ?`, "gone\n").Scan(&held); err != nil {
		t.Fatal(err)
	}
	if held != 0 {
		t.Fatal("content nothing names any more should be collected with the rows")
	}
	if blobs := changeCount(t, db, `SELECT COUNT(*) FROM change_blobs`); blobs != 2 {
		t.Fatalf("the surviving record's two sides should still be there, got %d blobs", blobs)
	}
}

// A slot with no row is a conversation that is already gone; there is nothing
// to key records to and that is not a failure.
func TestChanges_SavingUnderAnUnknownSlotIsANoOp(t *testing.T) {
	db := openTestDB(t)
	rec := changeset.Record{Path: "a.go", After: "x", AfterExists: true, At: time.Now()}
	if err := db.SaveChange("nobody", 1, 0, rec); err != nil {
		t.Fatalf("an unclaimed slot should not error: %v", err)
	}
	if rows := changeCount(t, db, `SELECT COUNT(*) FROM changes`); rows != 0 {
		t.Fatalf("nothing should have been written, got %d rows", rows)
	}
}

// A turn that edited a file back to where it found it has no change of it
// left, and the row has to go with the record or an undo would put back
// something nobody did.
func TestChanges_DroppingAPathTakesItsRow(t *testing.T) {
	db := openTestDB(t)
	slot := changeSlot(t, db, "work")

	saveTurn(t, db, slot, 1,
		changeset.Record{Path: "a.go", Before: "one\n", After: "two\n",
			BeforeExists: true, AfterExists: true, At: time.Now()},
		changeset.Record{Path: "b.go", After: "new\n", AfterExists: true, At: time.Now()})
	if err := db.DropChange(slot, 1, "a.go"); err != nil {
		t.Fatalf("drop change: %v", err)
	}
	got, err := db.LoadChanges(slot, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Records) != 1 || got[0].Records[0].Path != "b.go" {
		t.Fatalf("only the path that still changed something should be left, got %+v", got)
	}
	// A path nothing holds, and a slot nobody claimed, are both the state
	// asked for rather than a failure.
	if err := db.DropChange(slot, 1, "gone.go"); err != nil {
		t.Fatalf("dropping what is not there should not error: %v", err)
	}
	if err := db.DropChange("nobody", 1, "b.go"); err != nil {
		t.Fatalf("dropping under an unclaimed slot should not error: %v", err)
	}
}
