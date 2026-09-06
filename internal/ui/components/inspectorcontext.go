package components

// The rail's CONTEXT block: how much of the model's window the conversation
// occupies, and the burn behind it. It is a file of its own so the block that
// reads the window sits beside nothing else that does.

import (
	"fmt"
	"strings"
)

// InspectorContext is the CONTEXT block: occupancy of the model's window,
// the tokens behind it, and the per-round burn.
type InspectorContext struct {
	Pct              int
	Tokens, Window   int64
	Tokens1, Tokens2 string // the ↑in and ↓out labels
	// Burn is the per-round context series behind the sparkline, fed from the
	// session's vitals history. One sample is a dot, not a trend, so
	// the host sends nothing until it has two and the row says "estimated"
	// instead of drawing a flat line.
	Burn []float64
	// WarnPct/AlertPct override the meter's threshold colors (0 keeps the
	// defaults), so the rail matches the host's own trim warnings.
	WarnPct, AlertPct int
	// Estimated says the occupancy is the host's own estimate rather than a
	// provider-reported size, and the block says so in words — a
	// number nobody vouched for should not look like one that was.
	Estimated bool
	// Corrected says that estimate has been scaled by what the host measured
	// its own arithmetic to be worth against the provider's reports. It sits
	// beside the count rather than under the sparkline, because the
	// sparkline's own label takes that row for most of a session and a
	// figure whose meaning changed has to say so whenever it is on screen.
	Corrected bool
}

func (r InspectorRail) contextBlock(width int) (railBlock, bool) {
	c := r.Context
	if c == nil || c.Window <= 0 {
		return railBlock{}, false
	}
	pct := min(max(c.Pct, 0), 100)
	meter := Meter{Pct: pct, Cells: railCells(MeterCellsRail, width), Tone: MeterPressure,
		Warn: c.WarnPct, Alert: c.AlertPct}
	style := meter.Style()
	b := railBlock{heading: railHeading("CONTEXT",
		style.Render(fmt.Sprintf("%d%% of %s", pct, formatTokens(c.Window))), style, width)}
	count := formatTokens(c.Tokens)
	if c.Estimated {
		count = "~" + count
	}
	// The bar's number is the token count at the rail's right edge, in the
	// meter's own colour — the bar never carries the value alone.
	right := style.Render(count)
	if c.Corrected {
		right = sty.Dim.Render("corrected") + " " + right
	}
	b.add(railRow(meter.Bar(), right, width, inspectorIndent))
	tokens := strings.TrimSpace(c.Tokens1 + " " + c.Tokens2)
	lead := ""
	switch {
	case len(c.Burn) > 0:
		lead = Sparkline{Values: c.Burn, Cells: railCells(SparkCells, width)}.View() + " " + sty.Dim.Render("per round")
	case c.Estimated:
		// No series yet and no reported size: the block still has to say
		// where its number came from.
		lead = sty.Dim.Render("estimated")
	}
	if lead != "" || tokens != "" {
		b.add(railRow(lead, sty.Dim.Render(tokens), width, inspectorIndent))
	}
	return b, true
}
