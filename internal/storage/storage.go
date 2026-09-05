package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	_ "modernc.org/sqlite"
)

type DB struct {
	sql *sql.DB
	// chatMu guards the two maps below and the chat writes that read them.
	// It is held across the whole save — transaction and record together —
	// because the connection is serialised and nothing else is: with the
	// record made after the commit and outside the lock, a second save
	// beginning in that gap reads the first one's rows as a stranger's and
	// refuses a slot this process owns.
	chatMu sync.Mutex
	// chatWrote is what this process last put in each chat slot it has
	// touched, or last read out of one — the only thing that tells this
	// session's own messages from a stranger's when two of them are open on
	// one store, and the only thing that says whether a save is a
	// continuation of that conversation or a rewriting of it. A slot missing
	// from the map is one nothing here has seen, which is why an autosave to
	// it overwrites rather than refuses.
	// See docs/capabilities/sessions-and-memory.md#a-slot-belongs-to-one-session.
	chatWrote map[string]chatWrite
	// chatMoved is where each slot this process was turned out of went, so
	// two saves refused one after the other land in one new slot rather
	// than leaving the conversation in two.
	chatMoved map[string]string
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
	// recursive_triggers is on for the sake of the full-text indexes. They
	// are kept in step by triggers, and without this SQLite skips a trigger
	// on a row a foreign key cascade removed — so deleting a conversation
	// would take its messages and leave their words in the index, which is
	// the growing-forever store the index was added to stop
	// (migrate.go). The busy timeout comes first so the pragmas behind it
	// wait for another opener too: switching the journal mode takes a lock
	// of its own.
	conn, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(wal)&_pragma=foreign_keys(on)&_pragma=recursive_triggers(on)")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	conn.SetMaxOpenConns(1)

	db := &DB{sql: conn, chatWrote: map[string]chatWrite{}, chatMoved: map[string]string{}}
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

// retentionCutoff is the moment a row is past a window of so many days, in
// the layout most of this store writes a timestamp in.
//
// It is a function rather than the same expression written out beside each
// table, because the store keeps more than one kind of row for a window and
// rows that are read together have to expire on the same reckoning. Two
// tables trimmed against two slightly different clocks part company by a day,
// and whatever joined one to the other then finds half a pair.
// See docs/capabilities/sessions-and-memory.md#a-conversation-is-kept-for-a-window.
func retentionCutoff(now time.Time, days int) string {
	return now.UTC().AddDate(0, 0, -days).Format(time.RFC3339Nano)
}

// matchTerms turns what a person typed into one expression per word for the
// full-text index, in the order they were typed. A query with no word in it
// at all yields none, which every caller reads as "matches nothing": matching
// everything would be a listing the reader did not ask for.
//
// Two things happen to every word. It is quoted, so a hyphen, a colon or a
// stray quote is text rather than one of the query language's operators — a
// person searching for `-i` is not writing a NOT clause, and a search that
// failed on punctuation would be a search nobody could type. And it is given
// a trailing star, so a half-typed word still finds the row: the index matches
// whole tokens, and a reader typing into a picker has not finished the word
// yet.
//
// The split is on everything that is not a letter or a digit, which is the
// rule the index's own tokenizer uses; a query split any other way would ask
// for terms the index never made.
func matchTerms(query string) []string {
	words := strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	terms := make([]string, 0, len(words))
	for _, word := range words {
		terms = append(terms, `"`+word+`"*`)
	}
	return terms
}

// matchQuery is matchTerms as one expression, for a search whose answer is a
// single row: every word has to be somewhere in it. That is the index's own
// default between terms, and it is what a search box is expected to do — each
// further word narrows the answer rather than widening it.
func matchQuery(query string) (string, bool) {
	terms := matchTerms(query)
	if len(terms) == 0 {
		return "", false
	}
	return strings.Join(terms, " "), true
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
//
// The full-text indexes are the one thing left out, and they are left out on
// the same principle. An index holds no content of its own — it is a
// restatement of a table already counted here — and the bookkeeping rows it
// writes itself are there from the moment the store is created, so counting
// them would make every brand-new store look like one somebody had been
// using. They are recognised by the virtual table they hang off rather than
// by name, so an index added later is left out too.
func Recorded(path string) (bool, error) {
	db, err := OpenPath(path)
	if err != nil {
		return false, err
	}
	defer db.Close()

	rows, err := db.sql.Query(
		`SELECT name, COALESCE(sql, '') FROM sqlite_master WHERE type = 'table'
		   AND name NOT LIKE 'sqlite_%' AND name <> 'schema_version'`)
	if err != nil {
		return false, err
	}
	var tables, indexes []string
	for rows.Next() {
		var name, ddl string
		if err := rows.Scan(&name, &ddl); err != nil {
			rows.Close()
			return false, err
		}
		if strings.HasPrefix(strings.ToUpper(ddl), "CREATE VIRTUAL TABLE") {
			indexes = append(indexes, name)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, err
	}
	rows.Close()

	for _, table := range tables {
		if belongsToIndex(table, indexes) {
			continue
		}
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

// belongsToIndex reports whether a table is one of the virtual tables named,
// or one of the shadow tables such a table keeps its own state in. SQLite
// names those after the virtual table with a suffix, which is the only thing
// there is to go on and is enough.
func belongsToIndex(table string, indexes []string) bool {
	for _, index := range indexes {
		if table == index || strings.HasPrefix(table, index+"_") {
			return true
		}
	}
	return false
}
