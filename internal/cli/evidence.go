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
