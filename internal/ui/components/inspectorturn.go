package components

// The rail's THIS TURN block: how far through its work the turn is. It is a
// file of its own because it is what the rail's turn-versus-session
// distinction turns on — CHANGES and everything under it count the session,
// and this is the block they are being told apart from.

import (
	"fmt"
	"strings"
)

// InspectorTurn is the THIS TURN block: how far through its steps the turn
// is, how many tools it has spent, and what it has changed.
//
// It states no elapsed. The turn's clock belongs to the frame while the turn
// runs and to the transcript once it has stopped, and this block is up only
// above the two-pane rung — a span here is the same figure a second time, a
// few columns apart and on wide terminals only
// (docs/interface/surfaces.md#the-input-frame).
type InspectorTurn struct {
	// Step and Steps drive the progress meter and the "step 3 of 4" heading.
	// Steps == 0 means the turn declared none, so no ratio is fabricated —
	// the block states its tool count alone.
	Step, Steps int
	Tools       int
	// Files and its counts are what this turn changed — the turn-scoped half of
	// the scoped pair, and the reason the row says "this turn" in words rather
	// than printing a bare count beside CHANGES' session total.
	Files          int
	Added, Removed int
	// Running says the turn is still in flight, which is what lights the
	// progress meter's current cell. Whether the turn is still moving, and
	// how long it has been at it, are the live turn status's to answer.
	Running bool
}

func (r InspectorRail) turnBlock(width int) (railBlock, bool) {
	t := r.Turn
	if t == nil {
		return railBlock{}, false
	}
	meta := ""
	if t.Steps <= 0 && t.Step > 0 {
		// Steps observed, none declared: the ordinal is true, the ratio would
		// not be, so no denominator and no meter.
		meta = fmt.Sprintf("step %d", t.Step)
	}
	b := railBlock{heading: railHeading("THIS TURN", meta, sty.Dim, width)}
	if m, ok := StepMeter(t.Step, t.Steps, railCells(MeterCellsRail, width), t.Running); ok {
		// The count sits beside the bar rather than in the heading, because a
		// bar is never the only carrier of its value.
		b.add(indentRow(m.View(), width))
	}
	// "3 files this turn" rather than "3 files": CHANGES counts files too, and
	// the two are different questions, so both say their scope in words
	//. A turn that wrote nothing still says so — that is the fact.
	files := sty.Dim.Render(plural(t.Files, "file") + " this turn")
	if t.Files > 0 {
		files += " " + DiffStat(t.Added, t.Removed)
	}
	stats := []string{files}
	// A turn that called no tools reports no tool count: that zero was never
	// measured, and a gap is legible as a gap where a fabricated zero is not
	// (docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out).
	// The file count is the exception and says so in words — "0 files this
	// turn" is the answer to a question the block is being asked, which is
	// what CHANGES beneath it is being told apart from.
	if t.Tools > 0 {
		stats = append(stats, sty.Dim.Render(plural(t.Tools, "tool")))
	}
	b.add(indentRow(strings.Join(stats, sty.Dim.Render(" · ")), width))
	return b, true
}
