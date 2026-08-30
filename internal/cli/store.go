package cli

import (
	"sync"

	"github.com/rfizzle/shhh/internal/storage"
)

// openStore is how every command opens the store. It is storage.Open plus
// the history purge: old request rows are deleted once per process, on the
// first connection a command opens, in the background on that same
// connection. The root used to open a connection of its own for the purge
// beside the command's, which was a second opener on every invocation for
// a job that needs none — the pool serialises this one behind whatever the
// command is doing, and a command that never touches the store leaves the
// purge to the next one that does.
func openStore() (*storage.DB, error) {
	db, err := storage.Open()
	if err != nil {
		return nil, err
	}
	purgeHistoryOnce(db)
	return db, nil
}

// purge is the once-per-process guard and the retention the root read from
// the config; zero days means the root has not run (a test) and nothing is
// purged.
var purge struct {
	once sync.Once
	days int
}

// setHistoryRetention is the root's half: the retention window, read once
// the config has loaded.
func setHistoryRetention(days int) { purge.days = days }

// purgeHistoryOnce schedules the purge on db the first time it is called in
// the process. Best effort: a purge that fails is retried by the next
// command, and there is nobody here to tell.
func purgeHistoryOnce(db *storage.DB) {
	if purge.days <= 0 {
		return
	}
	purge.once.Do(func() {
		days := purge.days
		go func() { _, _ = db.PurgeOldHistory(days) }()
	})
}
