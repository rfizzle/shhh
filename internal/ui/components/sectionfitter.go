package components

// Fitting a screen's sections into the rows it was given
// (docs/interface/principles.md#fold-never-hide).
//
// Two take-over screens have a body made of whole blocks and a budget that
// may not hold all of them: the diagnostic's checks and the metrics screen's
// readings. Both drop whole blocks rather than cutting one in half, both
// leave a row behind naming what went, and both had written the loop that
// decides how many go — one of them reserving the marker's row from the first
// pass and the other from the second, which is the same answer arrived at
// twice and is exactly the kind of difference that survives review.
//
// What differs between them is not the loop but the order, and the order is a
// fact about the screen: the diagnostic drops what has least to say, because
// a check that passed is reassurance and a check that failed is why the
// screen was opened; the metrics screen drops from the bottom, because its
// blocks are already in the order they are worth reading. So the order is
// what a screen supplies and the arithmetic is here.

import "slices"

// SectionFitter decides how many of a body's sections fit a row budget.
type SectionFitter struct {
	// Rows is how many screen rows section i renders to.
	Rows func(i int) int
	// Next is which of the sections still kept goes next, as a position in
	// kept rather than a section number — the caller is choosing among what
	// is left. nil drops the last one, which is the right answer for a body
	// already in the order it is worth reading.
	Next func(kept []int) int
}

// Fit is the sections that fit, in order. Everything fits an unbounded
// budget, and a body that already fits keeps every section and spends no row
// on a marker.
//
// Once anything is dropped the marker naming it takes a row, and that row
// comes off the budget before the next section does: a fold that overran the
// screen to say what it folded would push the row under it off the bottom.
// One section always survives, however small the budget — a screen with no
// body is worse than one that overruns by a line, and the caller's own cut is
// what holds the height contract in that corner.
func (f SectionFitter) Fit(n, budget int) []int {
	kept := make([]int, n)
	for i := range kept {
		kept[i] = i
	}
	if budget <= 0 || f.cost(kept) <= budget {
		return kept
	}
	for len(kept) > 1 && f.cost(kept)+1 > budget {
		at := len(kept) - 1
		if f.Next != nil {
			at = f.Next(kept)
		}
		kept = slices.Delete(kept, at, at+1)
	}
	return kept
}

// cost is what a set of kept sections renders to.
func (f SectionFitter) cost(kept []int) int {
	n := 0
	for _, i := range kept {
		n += f.Rows(i)
	}
	return n
}

// Dropped is the sections Fit left out, in the order they were in. It is what
// the marker names: a row that only said "4 more" would leave the reader
// guessing which four the screen is sitting on, which on a diagnostic is the
// whole question (invariant 4).
func (f SectionFitter) Dropped(n int, kept []int) []int {
	shown := make(map[int]bool, len(kept))
	for _, i := range kept {
		shown[i] = true
	}
	var out []int
	for i := range n {
		if !shown[i] {
			out = append(out, i)
		}
	}
	return out
}
