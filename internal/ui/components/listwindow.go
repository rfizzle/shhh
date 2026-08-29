package components

// The sliding window every long list in the product scrolls through
// (docs/interface/surfaces.md#selectors). S-116 gave it to the selector and
// left it there; S-124 lifts it out so the two lists that own their own Focus
// — the multi-select and the agent manager — scroll through the same code
// rather than growing a second and a third implementation of it. The design
// says as much on both component pages: "if a real multi-select outgrows its
// card, window it with Select's rules", and "when it happens, window it with
// Select's rules and keep every blocked child above the window".
//
// What is shared is the arithmetic and the markers, not the rows: each list
// still renders itself, and hands this the shape of what it is rendering.

import "fmt"

// listGeometry is what a list tells the window about itself: how many items
// it holds, where the pointer is, how tall each item renders, and which of
// them a key can land on. Everything else the window needs it can compute.
type listGeometry struct {
	// n is the number of items in the list.
	n int
	// focus is the item the pointer is on, or -1 when the pointer is
	// somewhere this window does not cover — the agent list's pinned
	// blocked children, which are above the window rather than inside it.
	focus int
	// height reports how many rows item i renders to: one for most, two for
	// an item carrying a description or a note underneath.
	height func(i int) int
	// counts reports whether item i is something the markers should count.
	// The selector's group rails are labels for options rather than options,
	// so a run that hid only rails keeps a bare … (invariant 4).
	counts func(i int) bool
}

// rows is how many screen rows items[lo:hi) render to.
func (g listGeometry) rows(lo, hi int) int {
	n := 0
	for i := lo; i < hi; i++ {
		n += g.height(i)
	}
	return n
}

// countIn counts the items in [lo:hi) a key could land on — what a marker
// says it is hiding, and what the title rail says is showing.
func (g listGeometry) countIn(lo, hi int) int {
	n := 0
	for i := lo; i < hi; i++ {
		if g.counts(i) {
			n++
		}
	}
	return n
}

// end is the exclusive end of the run starting at lo that fits budget body
// rows, counting the overflow markers the run itself makes necessary: a
// window that starts past the top spends a row saying so, and one that stops
// short of the end spends another.
func (g listGeometry) end(lo, budget int) int {
	avail := budget
	if lo > 0 {
		avail--
	}
	if g.rows(lo, g.n) <= avail {
		return g.n
	}
	avail--
	hi, used := lo, 0
	for hi < g.n {
		h := g.height(hi)
		if used+h > avail {
			break
		}
		used, hi = used+h, hi+1
	}
	// A budget too small for even one item still shows one: a card with no
	// rows on it is worse than a card that overruns by a line, and boundRows
	// is what holds the height contract in that corner.
	return max(hi, lo+1)
}

// listWindow is where a list remembers the window it is showing. It is state
// rather than arithmetic on the focus because the window has to stay still
// while the pointer moves inside it — a list that re-centres on every
// keystroke is unreadable — and it self-heals, so no host has to reset it: a
// list that got shorter clamps it, and a focus outside the window pulls it
// back.
type listWindow struct {
	scroll int
}

// rangeFor is the half-open range of items the card shows for a body budget.
// The pointer above the window pulls it up to meet it and the pointer below
// pushes it down, one item at a time; inside it, the window does not move at
// all. It is therefore path-dependent, and deliberately so.
func (w *listWindow) rangeFor(g listGeometry, budget int) (lo, hi int) {
	if g.n == 0 {
		w.scroll = 0
		return 0, 0
	}
	if budget <= 0 || g.rows(0, g.n) <= budget {
		// Everything fits: there is no window, and nothing to remember about
		// where one was.
		w.scroll = 0
		return 0, g.n
	}
	focus := g.focus
	if focus >= g.n {
		focus = g.n - 1
	}
	lo = min(max(w.scroll, 0), g.n-1)
	if focus >= 0 && focus < lo {
		lo = focus
	}
	for {
		hi = g.end(lo, budget)
		if focus < 0 || hi > focus || lo >= g.n-1 {
			break
		}
		lo++
	}
	w.scroll = lo
	return lo, hi
}

// bodyBudget is how many rows a card of maxLines has left for its list once
// the frame and everything pinned has been taken off it (the query line,
// the key hints and the note field come off the budget first, and the window
// may never buy itself a row). 0 — an unbounded card — windows nothing, which
// is what a test or a surface that sizes itself gets.
func bodyBudget(maxLines, pinned int) int {
	if maxLines <= 0 {
		return 0
	}
	return max(maxLines-2-pinned, 1)
}

// listOverflowRow is the marker on a windowed list's edge. It counts what it
// is hiding rather than only marking that something is (invariant 4) — the
// form the queue strip's own overflowRow uses about a different list, which
// `ui_kits/cockpit/Lists.html` keeps for this one, so the borrowing S-116
// made with nothing to check against is now the decision. A run that hid
// nothing selectable keeps the bare …, because writing ↑ 1 more there would
// promise an option that does not exist.
//
// note is what a particular list has to add about what it is hiding, and is
// empty for most of them. The multi-select uses it to say how many of the
// hidden rows are ticked, because a count you cannot see is a count you
// cannot trust (S-124).
func listOverflowRow(arrow string, n int, note string, width int) string {
	label := "…"
	if n > 0 {
		label = fmt.Sprintf("%s %d more", arrow, n)
	}
	if note != "" {
		label += " · " + note
	}
	return sty.Dim.Render(clip(label, width-cardFrameWidth))
}
