package storage

// The flake ledger. A quality check that failed and then passed on its rerun
// is a flake, and one flake is a busy machine while the ninth is a check that
// needs fixing — so each one is counted, keyed on the checkout it happened in,
// and the count is what tells the two apart. It lives in the data directory
// and not the checkout because it is a reading of how this machine ran the
// checks rather than a fact about the code
// (docs/capabilities/testing.md#a-flake-is-counted-where-it-happened).

import "time"

// flakeTime is how the ledger writes a moment, in the shape the store's own
// timestamp defaults write theirs.
const flakeTime = "2006-01-02T15:04:05.000Z"

// Flake is one check's row in a checkout's ledger.
type Flake struct {
	Root    string
	Suite   string
	Check   string
	Command string
	// Seen is how many times the check has flaked under Root.
	Seen int
	// FirstExit is the exit code the failing first run gave, the last time
	// it flaked: the result reports the check as passed, so this is the one
	// place the failure's code is kept.
	FirstExit int
	// FirstAt and LastAt are when the check first and last flaked here.
	FirstAt, LastAt time.Time
	// LastSession names the session the last flake happened in.
	LastSession string
}

// RecordFlake counts one flake of a check under f.Root, at the moment at, and
// reports how many times that check had flaked there before this one. The
// command, the exit code and the session are the latest flake's. Seen,
// FirstAt and LastAt are the ledger's to keep and are not read from f.
func (db *DB) RecordFlake(f Flake, at time.Time) (before int, err error) {
	stamp := at.UTC().Format(flakeTime)
	err = retryBusy(func() error {
		var seen int
		if err := db.sql.QueryRow(
			`INSERT INTO gate_flakes (root, suite, "check", command, seen, first_exit, first_at, last_at, last_session)
			 VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?)
			 ON CONFLICT(root, suite, "check") DO UPDATE SET
			   seen = seen + 1, command = excluded.command, first_exit = excluded.first_exit,
			   last_at = excluded.last_at, last_session = excluded.last_session
			 RETURNING seen`,
			f.Root, f.Suite, f.Check, f.Command, f.FirstExit, stamp, stamp, f.LastSession).Scan(&seen); err != nil {
			return err
		}
		before = seen - 1
		return nil
	})
	return before, err
}

// FlakesFor is the ledger of the checkout under root, the check that flaked
// most recently first.
func (db *DB) FlakesFor(root string) ([]Flake, error) {
	rows, err := db.sql.Query(
		`SELECT root, suite, "check", command, seen, first_exit, first_at, last_at, last_session
		 FROM gate_flakes WHERE root = ? ORDER BY last_at DESC, suite, "check"`, root)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Flake
	for rows.Next() {
		var f Flake
		var first, last string
		if err := rows.Scan(&f.Root, &f.Suite, &f.Check, &f.Command, &f.Seen, &f.FirstExit,
			&first, &last, &f.LastSession); err != nil {
			return nil, err
		}
		f.FirstAt, f.LastAt = parseFlakeTime(first), parseFlakeTime(last)
		out = append(out, f)
	}
	return out, rows.Err()
}

// parseFlakeTime reads a ledger moment back; one that will not parse is the
// zero time, which a reader says it does not know.
func parseFlakeTime(s string) time.Time {
	t, err := time.Parse(flakeTime, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
