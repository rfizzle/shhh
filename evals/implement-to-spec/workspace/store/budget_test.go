package store

import (
	"errors"
	"testing"
)

func TestBudgetAccumulates(t *testing.T) {
	b := &Budget{Ceiling: 1000}
	for _, e := range []Entry{{"coffee", 350}, {"bun", 275}} {
		if err := b.Add(e); err != nil {
			t.Fatalf("Add(%v): %v", e, err)
		}
	}
	if got := b.Total(); got != 625 {
		t.Errorf("Total() = %d, want 625", got)
	}
	if got := b.Remaining(); got != 375 {
		t.Errorf("Remaining() = %d, want 375", got)
	}
	if got := b.Entries(); len(got) != 2 || got[0].Label != "coffee" || got[1].Label != "bun" {
		t.Errorf("Entries() = %v, want coffee then bun", got)
	}
}

// An entry that would breach the ceiling is refused, and refusing it changes
// nothing: the total, the remainder and the list are as they were.
func TestBudgetRefusesAnEntryOverTheCeiling(t *testing.T) {
	b := &Budget{Ceiling: 500}
	if err := b.Add(Entry{"lunch", 400}); err != nil {
		t.Fatal(err)
	}
	err := b.Add(Entry{"dinner", 200})
	if !errors.Is(err, ErrOverBudget) {
		t.Fatalf("Add over the ceiling: want ErrOverBudget, got %v", err)
	}
	if got := b.Total(); got != 400 {
		t.Errorf("a refused entry must not be counted: Total() = %d, want 400", got)
	}
	if got := len(b.Entries()); got != 1 {
		t.Errorf("a refused entry must not be recorded: %d entries, want 1", got)
	}
}

// An entry that lands exactly on the ceiling is within it.
func TestBudgetAcceptsAnEntryOnTheCeiling(t *testing.T) {
	b := &Budget{Ceiling: 500}
	if err := b.Add(Entry{"exact", 500}); err != nil {
		t.Fatalf("an entry equal to the ceiling is not over it: %v", err)
	}
	if got := b.Remaining(); got != 0 {
		t.Errorf("Remaining() = %d, want 0", got)
	}
}

func TestBudgetWithoutACeilingAcceptsAnything(t *testing.T) {
	b := &Budget{}
	if err := b.Add(Entry{"vast", 1 << 40}); err != nil {
		t.Fatalf("a budget with no ceiling refuses nothing: %v", err)
	}
	if got := b.Remaining(); got != 0 {
		t.Errorf("a budget with no ceiling has no remainder: got %d, want 0", got)
	}
}
