package cli

import (
	"sync"

	"github.com/rfizzle/shhh/internal/storage"
)

// openStore is how every command opens the store. It is storage.Open plus
// the two retention windows: old request rows and recorded sessions past
// their window are deleted once per process, on the first connection a
// command opens, in the background on that same connection. The root used to
// open a connection of its own for the purge beside the command's, which was
// a second opener on every invocation for a job that needs none — the pool
// serialises this one behind whatever the command is doing, and a command
// that never touches the store leaves the purge to the next one that does.
func openStore() (*storage.DB, error) {
	db, err := storage.Open()
	if err != nil {
		return nil, err
	}
	pruneStoreOnce(db)
	return db, nil
}

// purge is the once-per-process guard and the two retentions the root read
// from the config; zero days on either means the root has not run (a test)
// and that table is left alone.
var purge struct {
	once        sync.Once
	days        int
	observeDays int
}

// setHistoryRetention is the root's half: the retention window, read once
// the config has loaded.
func setHistoryRetention(days int) { purge.days = days }

// setObserveRetention is the same for the session record, which has a window
// of its own and a longer one.
func setObserveRetention(days int) { purge.observeDays = days }

// pruneStoreOnce schedules both prunes on db the first time it is called in
// the process. Best effort: a prune that fails is retried by the next
// command, and there is nobody here to tell.
//
// They share one guard and one goroutine, and the record's prune runs second.
// The store is one connection, so two goroutines would only queue behind each
// other anyway, and running them in a fixed order keeps a slow prune from
// arriving in the middle of whatever the command is doing twice over.
func pruneStoreOnce(db *storage.DB) {
	if purge.days <= 0 && purge.observeDays <= 0 {
		return
	}
	purge.once.Do(func() {
		days, observeDays := purge.days, purge.observeDays
		go func() {
			if days > 0 {
				_, _ = db.PurgeOldHistory(days)
			}
			if observeDays > 0 {
				_, _ = db.PruneAgentObservability(observeDays)
			}
		}()
	})
}
