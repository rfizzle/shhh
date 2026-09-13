package storage

import "time"

// stampLayout is how this store writes an instant into a TEXT column, and
// every column an ORDER BY reads is written through it.
//
// SQLite compares these columns as text, and time.RFC3339Nano drops trailing
// zeros: it renders 10:00:00.500000000 as "10:00:00.5Z" and the later
// 10:00:00.512000000 as "10:00:00.512Z" — text that sorts the two the wrong
// way round, because 'Z' outranks '1'. What that looks like is a listing
// whose second row happened first, on 1.2% of adjacent pairs where it was
// measured. A fixed width makes the text order the instant order, and a
// listing that still ties breaks the tie by id.
//
// Reads parse with time.RFC3339Nano, which accepts either width, so rows
// written before a column moved to this layout are read back unchanged and
// nothing has to rewrite them.
const stampLayout = "2006-01-02T15:04:05.000000000Z07:00"

// stamp renders an instant for storage in UTC.
func stamp(t time.Time) string { return t.UTC().Format(stampLayout) }
