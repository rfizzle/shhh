package cli

import (
	"sync"
	"time"

	"github.com/rfizzle/shhh/internal/storage"
)

// openStore is how every command opens the store. It is storage.Open plus
// the retention windows: old request rows, saved conversations and recorded
// sessions past their window are deleted at most once a day, on the first
// connection a command opens after that day has turned, in the background on
// that same connection. The root used to open a connection of its own for the
// purge beside the command's, which was a second opener on every invocation
// for a job that needs none — the pool serialises this one behind whatever the
// command is doing, and a command that never touches the store leaves the
// purge to the next one that does.
func openStore() (*storage.DB, error) {
	db, err := storage.Open()
	if err != nil {
		return nil, err
	}
	pruneStoreOnce(db)
	return db, nil
}

// purge is the once-per-process guard and the retentions the root read from
// the config; zero days on any of them means the table is left alone —
// because the root has not run (a test), or, for saved chats, because nobody
// has asked for a window at all.
var purge struct {
	once        sync.Once
	days        int
	chatDays    int
	observeDays int
}

// setHistoryRetention is the root's half: the retention window, read once
// the config has loaded.
func setHistoryRetention(days int) { purge.days = days }

// setChatsRetention is the same for the saved conversations, which have no
// window until somebody writes one down
// (docs/capabilities/sessions-and-memory.md#a-conversation-is-kept-for-a-window).
func setChatsRetention(days int) { purge.chatDays = days }

// setObserveRetention is the same for the session record, which has a window
// of its own and a longer one.
func setObserveRetention(days int) { purge.observeDays = days }

// pruneInterval is how often the sweeps are worth running. A window is
// measured in days, so the oldest row a sweep would delete moves once a day
// and a second sweep before then has nothing to find.
const pruneInterval = 24 * time.Hour

// pruneStoreOnce schedules the prunes on db the first time it is called in
// the process, and only where the store says nobody has swept today. Best
// effort: a prune that fails is retried by the next command after the day
// turns, and there is nobody here to tell.
//
// The guard is two halves because there are two ways to sweep too often. The
// sync.Once is this process asking twice, which a command opening the store
// on two paths does; the stamp in the store is every other process on the
// machine, which is the half that matters on a machine driving a backlog —
// `shhh todo run` is dozens of processes an hour, and each of them paying for
// four full-table sweeps is work nobody asked for on a table whose oldest row
// moves once a day. The stamp is claimed rather than read (storage.ClaimPrune)
// so two runs starting in the same second do not both read it as stale.
//
// The prunes share one guard and one goroutine, and the record's prune runs
// last. The store is one connection, so two goroutines would only queue
// behind each other anyway, and running them in a fixed order keeps a slow
// prune from arriving in the middle of whatever the command is doing twice
// over.
func pruneStoreOnce(db *storage.DB) {
	if purge.days <= 0 && purge.chatDays <= 0 && purge.observeDays <= 0 {
		return
	}
	purge.once.Do(func() {
		days, chatDays, observeDays := purge.days, purge.chatDays, purge.observeDays
		go func() {
			// The claim is made on the goroutine and not in front of it: it
			// is a write, and the caller here is a command that has not
			// started doing its own work yet.
			if mine, _ := db.ClaimPrune(pruneInterval); !mine {
				return
			}
			if days > 0 {
				_, _ = db.PurgeOldHistory(days)
			}
			if chatDays > 0 {
				_, _ = db.PruneOldChats(chatDays)
				// The record of what a turn changed is part of the
				// conversation it belongs to, so it keeps the conversation's
				// window rather than one of its own. A conversation the line
				// above deleted took its records with it; this is for the
				// records under conversations still here.
				_, _ = db.PruneOldChanges(chatDays)
			}
			if observeDays > 0 {
				_, _ = db.PruneAgentObservability(observeDays)
			}
		}()
	})
}
