package components

// The rail's THIS TURN block: how far through its work the turn is. It is a
// file of its own because it is what the rail's turn-versus-session
// distinction turns on — CHANGES and everything under it count the session,
// and this is the block they are being told apart from.

import (
	"fmt"
	"strings"
	"time"
)

// InspectorTurn is the THIS TURN block: how far through its steps the turn
// is, how many tools it has spent, and how long it has been running.
type InspectorTurn struct {
	// Step and Steps drive the progress meter and the "step 3 of 4" heading.
	// Steps == 0 means the turn declared none, so no ratio is fabricated —
	// the block states its tool count and elapsed time alone.
	Step, Steps int
	Tools       int
	Elapsed     time.Duration
	// Files and its counts are what this turn changed — the turn-scoped half of
	// the scoped pair, and the reason the row says "this turn" in words rather
	// than printing a bare count beside CHANGES' session total.
	Files          int
	Added, Removed int
	// Running says the turn is still in flight, which is what lights the
	// progress meter's current cell. The row states the clock without saying
	// whether it is still moving — the live turn status is what answers that
	//, and saying it twice cost the row its file count.
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
	b.add(indentRow(strings.Join([]string{
		files,
		sty.Dim.Render(plural(t.Tools, "tool")),
		sty.Dim.Render(FormatElapsed(t.Elapsed)),
	}, sty.Dim.Render(" · ")), width))
	return b, true
}
