package storage

// The listings an ORDER BY on a timestamp column answers, and the invariant
// under them: a stamp is written at one width, so the text SQLite compares
// runs in the order the instants did, and where two rows still share a stamp
// the id settles it. Three rows written in one tick is the case that used to
// come back in the wrong order about once in a hundred runs.

import (
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/changeset"
	"github.com/rfizzle/shhh/internal/notebook"
	"github.com/rfizzle/shhh/internal/provider"
)

// TestStampOrdersAsText pins the write format against the reason it was
// chosen. SQLite orders a listing by comparing these columns as text, so a
// format whose width varies with the value sorts a later entry first, and an
// id tie-break never fires because the two strings are not equal.
func TestStampOrdersAsText(t *testing.T) {
	base := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	// Trailing zeros are what a variable-width format drops: .500000000
	// renders as ".5" and outsorts the later ".512".
	earlier := base.Add(500 * time.Millisecond)
	later := base.Add(512 * time.Millisecond)

	if stamp(earlier) >= stamp(later) {
		t.Fatalf("a later instant must sort after an earlier one as text: %q >= %q",
			stamp(earlier), stamp(later))
	}
	if len(stamp(earlier)) != len(stamp(later)) {
		t.Fatalf("the stamp width must not vary with the value: %q vs %q",
			stamp(earlier), stamp(later))
	}

	// The reader parses with RFC3339Nano, so the two have to agree.
	back, err := time.Parse(time.RFC3339Nano, stamp(later))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !back.Equal(later) {
		t.Fatalf("round-trip mismatch: %s want %s", back, later)
	}
}

// TestStoredStampsAreWrittenAtOneWidth is the guard on every column the
// listings below order by: one row per table, written the way the product
// writes it, and each stamp read back has to parse against the fixed-width
// layout. time.RFC3339Nano renders a stamp that will not, which is how a
// write slipping back to it is caught here rather than in a flake.
func TestStoredStampsAreWrittenAtOneWidth(t *testing.T) {
	db := openTestDB(t)
	slot := changeSlot(t, db, "stamps")

	if _, err := db.AddMemory("global", "lesson", "read the error first", "user"); err != nil {
		t.Fatalf("add memory: %v", err)
	}
	if err := db.SaveSnippet("deploy", "kubectl apply -f deploy.yaml"); err != nil {
		t.Fatalf("save snippet: %v", err)
	}
	if err := db.SaveChat(slot, []provider.Message{{Role: provider.RoleUser, Content: "q"}}); err != nil {
		t.Fatalf("save chat: %v", err)
	}
	if _, err := db.SaveNote(slot, notebook.Note{Author: "researcher-1", Title: "t", Body: "b", Written: time.Now()}); err != nil {
		t.Fatalf("save note: %v", err)
	}
	saveTurn(t, db, slot, 1, changeset.Record{
		Path: "main.go", After: "fresh\n", AfterExists: true,
		Agent: changeset.MainAgent, At: time.Now(),
	})

	for _, c := range []struct{ column, query string }{
		{"memories.created_at", `SELECT created_at FROM memories`},
		{"memories.updated_at", `SELECT updated_at FROM memories`},
		{"snippets.created_at", `SELECT created_at FROM snippets`},
		{"snippets.updated_at", `SELECT updated_at FROM snippets`},
		{"chat_sessions.created_at", `SELECT created_at FROM chat_sessions`},
		{"chat_sessions.updated_at", `SELECT updated_at FROM chat_sessions`},
		{"notes.written_at", `SELECT written_at FROM notes`},
		{"changes.at", `SELECT at FROM changes`},
	} {
		var got string
		if err := db.sql.QueryRow(c.query).Scan(&got); err != nil {
			t.Fatalf("read %s: %v", c.column, err)
		}
		if _, err := time.Parse(stampLayout, got); err != nil {
			t.Errorf("%s holds %q, which is not the one width: %v", c.column, got, err)
		}
	}
}

// TestStampsReadBackAtEitherWidth is why nothing has to rewrite the rows that
// are already on disk. The reader parses with time.RFC3339Nano, which accepts
// a stamp of any fractional width, so a row written before the store settled
// on one width comes back as the instant it was written at. Two stamps a
// second or more apart also still sort by their seconds, whatever their
// fractional widths.
func TestStampsReadBackAtEitherWidth(t *testing.T) {
	db := openTestDB(t)
	old := time.Date(2026, 9, 13, 10, 0, 0, 500000000, time.UTC)
	fresh := old.Add(time.Second + 12*time.Millisecond)

	// The old row goes in as the store used to write it: RFC3339Nano, which
	// renders this instant as ".5Z" and not ".500000000Z".
	if _, err := db.sql.Exec(
		`INSERT INTO snippets (name, command, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		"old", "echo old", old.Format(time.RFC3339Nano), old.Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert old-width row: %v", err)
	}
	if _, err := db.sql.Exec(
		`INSERT INTO snippets (name, command, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		"fresh", "echo fresh", stamp(fresh), stamp(fresh),
	); err != nil {
		t.Fatalf("insert fixed-width row: %v", err)
	}

	got, err := db.ListSnippets()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected both rows, got %d", len(got))
	}
	if got[0].Name != "fresh" || got[1].Name != "old" {
		t.Fatalf("expected the later row first, got %q then %q", got[0].Name, got[1].Name)
	}
	if !got[1].UpdatedAt.Equal(old) {
		t.Errorf("the old-width stamp read back as %s, want %s", got[1].UpdatedAt, old)
	}
	if !got[0].UpdatedAt.Equal(fresh) {
		t.Errorf("the fixed-width stamp read back as %s, want %s", got[0].UpdatedAt, fresh)
	}
	one, err := db.GetSnippet("old")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !one.UpdatedAt.Equal(old) {
		t.Errorf("read by name gave %s, want %s", one.UpdatedAt, old)
	}
}

// tieStamps gives every row in a column the same stamp, so the tie the clock
// produces only sometimes is the one the listing is then asked to break.
func tieStamps(t *testing.T, db *DB, query string) {
	t.Helper()
	if _, err := db.sql.Exec(query, stamp(time.Now())); err != nil {
		t.Fatalf("tie stamps: %v", err)
	}
}

func TestListSnippets_SavedInOneTickListNewestFirst(t *testing.T) {
	db := openTestDB(t)
	for _, name := range []string{"first", "second", "third"} {
		if err := db.SaveSnippet(name, "echo "+name); err != nil {
			t.Fatalf("save %s: %v", name, err)
		}
	}
	want := []string{"third", "second", "first"}

	list := func(when string) {
		t.Helper()
		got, err := db.ListSnippets()
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		names := make([]string, len(got))
		for i, s := range got {
			names[i] = s.Name
		}
		if len(names) != len(want) {
			t.Fatalf("%s: expected %d snippets, got %v", when, len(want), names)
		}
		for i := range want {
			if names[i] != want[i] {
				t.Fatalf("%s: expected newest first %v, got %v", when, want, names)
			}
		}
	}

	list("as saved")
	tieStamps(t, db, `UPDATE snippets SET updated_at = ?`)
	list("with the stamps tied")
}

func TestListChats_SavedInOneTickListNewestFirst(t *testing.T) {
	db := openTestDB(t)
	for _, name := range []string{"first", "second", "third"} {
		if err := db.SaveChat(name, []provider.Message{{Role: provider.RoleUser, Content: name}}); err != nil {
			t.Fatalf("save %s: %v", name, err)
		}
	}
	want := []string{"third", "second", "first"}

	list := func(when string) {
		t.Helper()
		got, err := db.ListChats()
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		names := make([]string, len(got))
		for i, e := range got {
			names[i] = e.Name
		}
		if len(names) != len(want) {
			t.Fatalf("%s: expected %d chats, got %v", when, len(want), names)
		}
		for i := range want {
			if names[i] != want[i] {
				t.Fatalf("%s: expected newest first %v, got %v", when, want, names)
			}
		}
	}

	list("as saved")
	tieStamps(t, db, `UPDATE chat_sessions SET updated_at = ?`)
	list("with the stamps tied")
}

func TestListHistory_RecordedInOneTickListNewestFirst(t *testing.T) {
	db := openTestDB(t)
	// The column is SQLite's own default and it has millisecond resolution,
	// so three rows recorded in one tick tie outright and only the id
	// tie-break can order them.
	for _, prompt := range []string{"first", "second", "third"} {
		if _, err := db.RecordRequest(RequestRecord{
			Provider: "openai", Model: "gpt-4o", Prompt: prompt, Command: "echo " + prompt,
			Action: "run", Success: true,
		}); err != nil {
			t.Fatalf("record %s: %v", prompt, err)
		}
	}
	want := []string{"third", "second", "first"}

	list := func(when string, f HistoryFilter) {
		t.Helper()
		got, err := db.ListHistory(f)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		prompts := make([]string, len(got))
		for i, e := range got {
			prompts[i] = e.Prompt
		}
		if len(prompts) != len(want) {
			t.Fatalf("%s: expected %d entries, got %v", when, len(want), prompts)
		}
		for i := range want {
			if prompts[i] != want[i] {
				t.Fatalf("%s: expected newest first %v, got %v", when, want, prompts)
			}
		}
	}

	list("unfiltered", HistoryFilter{Limit: 10})
	// The searched listing is a second query over the same rows, ordered the
	// same way: "echo" is in every command.
	list("searched", HistoryFilter{Search: "echo", Limit: 10})
	tieStamps(t, db, `UPDATE requests SET created_at = ?`)
	list("with the stamps tied", HistoryFilter{Limit: 10})
}

func TestLoadNotes_WrittenInOneTickLoadInWriteOrder(t *testing.T) {
	db := openTestDB(t)
	written := time.Now()
	for _, title := range []string{"first", "second", "third"} {
		if _, err := db.SaveNote("slot-a", notebook.Note{
			Author: "researcher-1", Title: title, Body: "b", Turn: 1, Written: written,
		}); err != nil {
			t.Fatalf("save %s: %v", title, err)
		}
	}

	got, err := db.LoadNotes("slot-a")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := []string{"first", "second", "third"}
	if len(got) != len(want) {
		t.Fatalf("expected %d notes, got %d", len(want), len(got))
	}
	for i, title := range want {
		if got[i].Title != title {
			t.Fatalf("expected oldest first %v, got %q at %d", want, got[i].Title, i)
		}
		if !got[i].Written.Equal(written) {
			t.Errorf("note %d came back written at %s, want %s", i, got[i].Written, written.UTC())
		}
	}
}

func TestLoadChanges_SavedInOneTickLoadInSaveOrder(t *testing.T) {
	db := openTestDB(t)
	slot := changeSlot(t, db, "work")
	at := time.Now()
	saveTurn(t, db, slot, 1,
		changeset.Record{Path: "first.go", After: "1\n", AfterExists: true, Agent: changeset.MainAgent, At: at},
		changeset.Record{Path: "second.go", After: "2\n", AfterExists: true, Agent: changeset.MainAgent, At: at},
		changeset.Record{Path: "third.go", After: "3\n", AfterExists: true, Agent: changeset.MainAgent, At: at},
	)

	turns, err := db.LoadChanges(slot, 1, 0)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("expected one turn, got %d", len(turns))
	}
	want := []string{"first.go", "second.go", "third.go"}
	if len(turns[0].Records) != len(want) {
		t.Fatalf("expected %d records, got %d", len(want), len(turns[0].Records))
	}
	for i, path := range want {
		r := turns[0].Records[i]
		if r.Path != path {
			t.Fatalf("expected save order %v, got %q at %d", want, r.Path, i)
		}
		if !r.At.Equal(at) {
			t.Errorf("record %d came back at %s, want %s", i, r.At, at.UTC())
		}
	}
}
