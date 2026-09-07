package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rfizzle/shhh/internal/evidence"
	"github.com/rfizzle/shhh/internal/storage"
)

// openEvidence opens a fresh session's evidence store under shhh's
// state dir. Failure disables tool-output reduction for the session with a
// warning instead of blocking it.
func openEvidence() *evidence.Reducer {
	base, err := storage.Dir()
	if err == nil {
		var store *evidence.Store
		if store, err = evidence.Open(filepath.Join(base, "evidence"), evidence.NewSessionID()); err == nil {
			return evidence.NewReducer(store)
		}
	}
	fmt.Fprintf(os.Stderr, "warning: evidence store unavailable, tool-output reduction disabled: %v\n", err)
	return nil
}

// evidenceReader is how a surface reads a stored entry back: the opening
// bytes of it, with a line saying what is past them where the entry is
// longer than what was asked for. False is an entry the store no longer
// holds — purged, pruned, or from a session whose store has gone — which the
// sources screen reports on the row rather than as a failure.
func evidenceReader(red *evidence.Reducer) func(id string, limit int) (string, bool) {
	return func(id string, limit int) (string, bool) {
		data, meta, err := red.Store().Read(id, 0, limit)
		if err != nil {
			return "", false
		}
		out := string(data)
		if meta.Size > int64(len(data)) {
			out += fmt.Sprintf("\n\n… (%d of %d bytes shown; the rest is in the store as %s)",
				len(data), meta.Size, id)
		}
		return out, true
	}
}

// evidenceManager backs the /evidence slash command: status by default,
// "purge" deletes the session's stored originals.
func evidenceManager(red *evidence.Reducer) func(args []string) string {
	return func(args []string) string {
		switch {
		case len(args) == 0:
			return red.StatusReport()
		case len(args) == 1 && args[0] == "purge":
			if err := red.Store().Purge(); err != nil {
				return "Error purging evidence: " + err.Error()
			}
			return "Evidence store purged: the stored originals are deleted; ids already in the transcript can no longer be retrieved."
		}
		return "Usage: /evidence [purge]"
	}
}
