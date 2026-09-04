package cli

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/storage"
)

func TestPruneStoreOnce_RunsOncePerProcessOnTheGivenConnection(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Rows are stamped by the store, so they are backdated after the fact.
	old := time.Now().UTC().AddDate(0, 0, -400).Format(time.RFC3339Nano)
	addOld := func() {
		id, err := db.RecordRequest(storage.RequestRecord{Provider: "p", Model: "m", Prompt: "old", Command: "ls"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.SQL().Exec(`UPDATE requests SET created_at = ? WHERE id = ?`, old, id)
		must(t, err)
	}
	addOld()
	addOld()
	count := func() int {
		var n int
		must(t, db.SQL().QueryRow(`SELECT COUNT(*) FROM requests`).Scan(&n))
		return n
	}

	purge.once, purge.days, purge.chatDays, purge.observeDays = sync.Once{}, 0, 0, 0
	pruneStoreOnce(db)
	if count() != 2 {
		t.Fatal("with no retention set nothing is purged")
	}

	setHistoryRetention(90)
	pruneStoreOnce(db)
	deadline := time.Now().Add(5 * time.Second)
	for count() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if count() != 0 {
		t.Fatal("the first open should purge the old rows")
	}

	addOld()
	pruneStoreOnce(db)
	time.Sleep(50 * time.Millisecond)
	if count() != 1 {
		t.Fatal("a second open in the same process purges nothing more")
	}
}

// The record's window rides the same first open history's does. It is a
// separate window and a separate table, and the failure it guards against is
// the quiet one: a store that grows forever because the only thing that would
// have trimmed it is a command nobody runs.
func TestPruneStoreOnce_PrunesTheRecordOnTheSameOpen(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	id, err := db.StartAgentSession("chat", "openai", "gpt-test")
	must(t, err)
	must(t, db.RecordAgentEvent(id, storage.AgentEvent{Kind: storage.AgentEventTool, Tool: "read_file", Outcome: "ok"}))
	old := time.Now().UTC().AddDate(0, 0, -400).Format("2006-01-02T15:04:05.000Z")
	_, err = db.SQL().Exec(`UPDATE agent_sessions SET started_at = ?, ended_at = ? WHERE id = ?`, old, old, id)
	must(t, err)
	sessions := func() int {
		var n int
		must(t, db.SQL().QueryRow(`SELECT COUNT(*) FROM agent_sessions`).Scan(&n))
		return n
	}

	purge.once, purge.days, purge.chatDays, purge.observeDays = sync.Once{}, 0, 0, 0
	pruneStoreOnce(db)
	time.Sleep(50 * time.Millisecond)
	if sessions() != 1 {
		t.Fatal("with no window set the record is left alone")
	}

	purge.once = sync.Once{}
	setObserveRetention(180)
	pruneStoreOnce(db)
	deadline := time.Now().Add(5 * time.Second)
	for sessions() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if sessions() != 0 {
		t.Fatal("the first open should prune the session past the window")
	}
	var events int
	must(t, db.SQL().QueryRow(`SELECT COUNT(*) FROM agent_events`).Scan(&events))
	if events != 0 {
		t.Fatalf("%d events outlived the session they belong to", events)
	}
	setObserveRetention(0)
}

// Saved chats have a window of their own, and it is the one that is off until
// somebody sets it: the other three tables hold what a session left behind,
// and this one holds the session.
func TestPruneStoreOnce_PrunesSavedChatsOnlyWhenAWindowIsSet(t *testing.T) {
	db, err := storage.OpenPath(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	must(t, db.SaveChat("ancient", []provider.Message{{Role: provider.RoleUser, Content: "hello"}}))
	old := time.Now().UTC().AddDate(0, 0, -400).Format(time.RFC3339Nano)
	_, err = db.SQL().Exec(`UPDATE chat_sessions SET updated_at = ?`, old)
	must(t, err)
	chats := func() int {
		var n int
		must(t, db.SQL().QueryRow(`SELECT COUNT(*) FROM chat_sessions`).Scan(&n))
		return n
	}

	purge.once, purge.days, purge.chatDays, purge.observeDays = sync.Once{}, 0, 0, 0
	pruneStoreOnce(db)
	time.Sleep(50 * time.Millisecond)
	if chats() != 1 {
		t.Fatal("with no window set a saved chat is kept whatever its age")
	}

	purge.once = sync.Once{}
	setChatsRetention(90)
	pruneStoreOnce(db)
	deadline := time.Now().Add(5 * time.Second)
	for chats() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if chats() != 0 {
		t.Fatal("the first open should prune the chat past the window")
	}
	setChatsRetention(0)
}
