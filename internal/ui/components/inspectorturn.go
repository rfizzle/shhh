package components

// The rail's THIS TURN block: how far through its work the turn is. It is a
// file of its own because it is what the rail's turn-versus-session
// distinction turns on — CHANGES and everything under it count the session,
// and this is the block they are being told apart from.

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
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
	b := railBlock{heading: railHeading("THIS TURN", meta, sty.dim, width), class: railLive,
		collapsedHeading: railHeading("THIS TURN", t.collapsedMeta(width), sty.dim, width)}
	if t.Running {
		b.class = railAsks
	}
	if m, ok := stepMeter(t.Step, t.Steps, railCells(meterCellsRail, width), t.Running); ok {
		// The count sits beside the bar rather than in the heading, because a
		// bar is never the only carrier of its value.
		b.rows = append(b.rows, railLine{text: indentRow(m.View(), width), stated: true})
	}
	// "3 files this turn" rather than "3 files": CHANGES counts files too, and
	// the two are different questions, so both say their scope in words.
	files := sty.dim.Render(plural(t.Files, "file") + " this turn")
	if t.Files > 0 {
		files += " " + DiffStat(t.Added, t.Removed)
	}
	stats := []string{files}
	// A turn that called no tools reports no tool count: that zero was never
	// measured, and a gap is legible as a gap where a fabricated zero is not
	// (docs/interface/principles.md#a-stat-that-cannot-be-reported-is-left-out).
	// The file count is kept at zero because that zero was measured, and
	// "0 files this turn" is the answer the block is asked for
	// (docs/interface/departures.md#a-turn-that-changed-no-files-says-so).
	if t.Tools > 0 {
		stats = append(stats, sty.dim.Render(plural(t.Tools, "tool")))
	}
	b.rows = append(b.rows, railLine{text: indentRow(strings.Join(stats, sty.dim.Render(" · ")), width), stated: true})
	return b, true
}

// collapsedMeta is the turn on its heading alone: the step it is on, the
// files it has changed, and their counts where the row has room for them.
func (t InspectorTurn) collapsedMeta(width int) string {
	var parts []string
	switch {
	case t.Steps > 0:
		parts = append(parts, fmt.Sprintf("step %d of %d", t.Step, t.Steps))
	case t.Step > 0:
		parts = append(parts, fmt.Sprintf("step %d", t.Step))
	}
	parts = append(parts, plural(t.Files, "file"))
	meta := sty.dim.Render(strings.Join(parts, " · "))
	if t.Files > 0 {
		counted := meta + " " + DiffStat(t.Added, t.Removed)
		if lipgloss.Width(counted) <= railRoom(width, "", inspectorIndent)-lipgloss.Width("THIS TURN")-1 {
			meta = counted
		}
	}
	return meta
}
