package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type DB struct {
	sql *sql.DB
}

func Open() (*DB, error) {
	dir, err := dataDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return OpenPath(filepath.Join(dir, "shhh.db"))
}

func OpenPath(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=foreign_keys(on)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	conn.SetMaxOpenConns(1)

	db := &DB{sql: conn}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

func (db *DB) Close() error {
	return db.sql.Close()
}

func (db *DB) SQL() *sql.DB {
	return db.sql
}

// Dir returns shhh's data directory without creating it, so other packages
// (e.g. the sandbox deny mask) can reference the state location.
func Dir() (string, error) {
	return dataDir()
}

// dataDir is one layout on every platform: XDG_DATA_HOME if it is set, then
// ~/.local/share/shhh. macOS used to put the store under ~/Library/Application
// Support alongside the config file, which mixed two kinds of state — settings
// a person edits and a database they never touch — into one directory.
// See docs/capabilities/configuration.md#one-layout-everywhere. A machine
// still holding the old directory is detected by `shhh doctor`, which offers
// to move it (internal/migrate).
func dataDir() (string, error) {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "shhh"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "shhh"), nil
}

// Recorded reports whether the store at this path holds anything at all.
//
// It exists for the migration that moves a store from an older location. The
// first shhh command run after such a move opens the store, which creates an
// empty one at the new path — so by the time anyone looks, there is a real
// database in the old place and a brand-new one in the new place, and a
// migration that treated the new one as a file worth keeping would ask every
// user on earth to delete it by hand.
//
// "Holds anything" is asked of the schema rather than of a list of tables, so
// a table added later is counted without this needing to hear about it. A
// store that will not open, or will not answer, reports an error: the caller
// must be able to tell "there is nothing in it" from "I could not tell".
func Recorded(path string) (bool, error) {
	db, err := OpenPath(path)
	if err != nil {
		return false, err
	}
	defer db.Close()

	rows, err := db.sql.Query(
		`SELECT name FROM sqlite_master WHERE type = 'table'
		   AND name NOT LIKE 'sqlite_%' AND name <> 'schema_version'`)
	if err != nil {
		return false, err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return false, err
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, err
	}
	rows.Close()

	for _, table := range tables {
		var any int
		// The table name cannot be a parameter, and it did not come from
		// anywhere but this database's own schema.
		if err := db.sql.QueryRow(`SELECT EXISTS(SELECT 1 FROM "` + table + `")`).Scan(&any); err != nil {
			return false, err
		}
		if any == 1 {
			return true, nil
		}
	}
	return false, nil
}
