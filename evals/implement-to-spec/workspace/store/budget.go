package store

// Add records an entry against the budget.
func (b *Budget) Add(e Entry) error {
	panic("not implemented")
}

// Total is the sum of every entry that was accepted.
func (b *Budget) Total() int64 {
	panic("not implemented")
}

// Remaining is what is left under the ceiling. A budget with no ceiling has
// no meaningful remainder and reports zero.
func (b *Budget) Remaining() int64 {
	panic("not implemented")
}

// Entries is what was accepted, in the order it was added.
func (b *Budget) Entries() []Entry {
	panic("not implemented")
}
