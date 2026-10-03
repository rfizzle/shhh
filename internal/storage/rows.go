package storage

import "fmt"

// rowScanner is what a single row and an open cursor have in common, so one
// scan function reads a row either way.
type rowScanner interface{ Scan(...any) error }

// queryRows runs query and reads every row it returns through scan, in the
// order the query returns them. No rows is a nil slice, not an empty one.
//
// The cursor is closed before it returns. The store runs on one connection,
// so a caller that writes after reading must not still be holding the cursor:
// the write would wait on a cursor that is waiting on it.
func queryRows[T any](db *DB, scan func(rowScanner) (T, error), query string, args ...any) ([]T, error) {
	rows, err := db.sql.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// queryScoped is queryRows over an aggregate written against a scope: scope
// fills the query's one %s, and lead binds the placeholders the query names
// before it — an event kind, a tool — ahead of the scope's own args.
func queryScoped[T any](db *DB, scan func(rowScanner) (T, error), query, scope string, lead, args []any) ([]T, error) {
	return queryRows(db, scan, fmt.Sprintf(query, scope), append(lead, args...)...)
}

// scanFields is a scan that reads a row straight into the fields dest names,
// in the order the query selects them.
func scanFields[T any](dest func(*T) []any) func(rowScanner) (T, error) {
	return func(r rowScanner) (T, error) {
		var v T
		err := r.Scan(dest(&v)...)
		return v, err
	}
}
