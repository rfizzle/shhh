package store

import "errors"

// ErrOverBudget is returned by Budget.Add when an entry would take the
// running total past the ceiling.
var ErrOverBudget = errors.New("over budget")

// Entry is one line of spend.
type Entry struct {
	Label string
	Cents int64
}

// Budget accumulates entries up to a ceiling.
type Budget struct {
	// Ceiling is the most the budget may total, in cents. A ceiling of zero
	// means no ceiling.
	Ceiling int64

	entries []Entry
	total   int64
}
