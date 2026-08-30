package cli

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/storage"
)

func TestPurgeHistoryOnce_RunsOncePerProcessOnTheGivenConnection(t *testing.T) {
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

	purge.once, purge.days = sync.Once{}, 0
	purgeHistoryOnce(db)
	if count() != 2 {
		t.Fatal("with no retention set nothing is purged")
	}

	setHistoryRetention(90)
	purgeHistoryOnce(db)
	deadline := time.Now().Add(5 * time.Second)
	for count() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if count() != 0 {
		t.Fatal("the first open should purge the old rows")
	}

	addOld()
	purgeHistoryOnce(db)
	time.Sleep(50 * time.Millisecond)
	if count() != 1 {
		t.Fatal("a second open in the same process purges nothing more")
	}
}
