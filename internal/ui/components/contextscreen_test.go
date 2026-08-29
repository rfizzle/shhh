package components

import (
	"strings"
	"testing"
)

// goldenContextScreen is the fixture every test here reads: a large window a
// third full, itemised into two groups.
func goldenContextScreen() ContextScreen {
	return ContextScreen{
		Model:    "claude-opus-5[1m]",
		Provider: "anthropic",
		Window:   "1m",
		Tokens:   "~312.4k",
		Pct:      31,
		Warn:     60,
		Alert:    80,
		Source:   "provider-reported",
		Categories: []ContextCategory{
			{Label: "system prompt", Tokens: "3.8k", Pct: "0.4%", Share: 0.38, Tone: ContextPrompt},
			{Label: "project context", Tokens: "61", Pct: "0.0%", Share: 0.006, Tone: ContextProject},
			{Label: "tool definitions", Tokens: "22.3k", Pct: "2.2%", Share: 2.23, Tone: ContextTools},
			{Label: "messages", Tokens: "283.3k", Pct: "28.3%", Share: 28.33, Tone: ContextMessages},
			{Label: "tool results", Tokens: "2.9k", Pct: "0.3%", Share: 0.29, Tone: ContextOutput},
			{Label: "free space", Tokens: "687.6k", Pct: "68.8%", Share: 68.76, Tone: ContextFree},
		},
		Groups: []ContextGroup{
			{
				Label:   "tool definitions",
				Summary: "24 tools · 22.3k",
				Items: []ContextItem{
					{Label: "execute_command", Share: 18, Tokens: "4.1k", Pct: "18.4%"},
					{Label: "edit_file", Share: 14, Tokens: "3.2k", Pct: "14.3%"},
					{Label: "search", Share: 12, Tokens: "2.6k", Pct: "11.7%"},
				},
				More: "↓ 21 more · 12.4k together",
			},
			{Label: "tool results", Summary: "9 tools · 2.9k"},
		},
	}
}

// TestContextGridIsOneMeterCutIntoRows checks the grid fills in reading
// order: the last filled cell lands where the occupancy puts it, not at the
// end of whichever row it fell in.
func TestContextGridIsOneMeterCutIntoRows(t *testing.T) {
	cases := []struct {
		name   string
		shares []float64
		filled int
	}{
		{"empty", []float64{0, 0, 0, 0, 0}, 0},
		// Three of the five are under a cell's worth and take one each. The
		// run after the first two absorbs their borrowing — the boundaries
		// are measured from the start of the grid — so the whole fixture
		// draws one cell more than the proportion alone would, not three.
		{"the fixture", []float64{0.38, 0.006, 2.23, 28.33, 0.29}, 69},
		{"half", []float64{50, 0, 0, 0, 0}, 110},
		{"full", []float64{100, 0, 0, 0, 0}, MeterCellsRail * contextGridRows},
	}
	for _, c := range cases {
		screen := goldenContextScreen()
		for i := range c.shares {
			screen.Categories[i].Share = c.shares[i]
		}
		got := strings.Count(stripANSI(strings.Join(screen.gridRows(), "")), "▰")
		if got != c.filled {
			t.Errorf("%s: filled %d cells, want %d", c.name, got, c.filled)
		}
	}
}

// TestContextGridRunsAreContiguousAndInLegendOrder checks the grid reads as
// the legend does: each category takes one unbroken run, in the order the
// rows are listed, so a reader can find a category in the grid by counting
// down the legend.
func TestContextGridRunsAreContiguousAndInLegendOrder(t *testing.T) {
	screen := goldenContextScreen()
	cells := screen.gridCells()
	var order []ContextTone
	for i, tone := range cells {
		if i == 0 || tone != cells[i-1] {
			order = append(order, tone)
		}
	}
	want := []ContextTone{ContextPrompt, ContextProject, ContextTools, ContextMessages, ContextOutput, ContextFree}
	if len(order) != len(want) {
		t.Fatalf("grid has %d runs %v, want %d %v", len(order), order, len(want), want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("run %d is tone %d, want %d", i, order[i], want[i])
		}
	}
}

// TestContextGridShowsACategoryTooSmallToMeasure: project context is six
// thousandths of a percent of this window, far less than a cell. It still
// gets one, for the reason meterFill gives — a category named in the legend
// with nothing of it in the grid reads as one that is not in the window.
func TestContextGridShowsACategoryTooSmallToMeasure(t *testing.T) {
	screen := goldenContextScreen()
	n := 0
	for _, tone := range screen.gridCells() {
		if tone == ContextProject {
			n++
		}
	}
	if n != 1 {
		t.Errorf("a category under one cell got %d cells, want exactly 1", n)
	}
}

// TestContextGridNeverShowsACategoryWithNothingInIt is the other half of that
// rule: the floor is for a category that costs something, not for one the
// session has none of.
func TestContextGridNeverShowsACategoryWithNothingInIt(t *testing.T) {
	screen := goldenContextScreen()
	screen.Categories[1].Share = 0
	for _, tone := range screen.gridCells() {
		if tone == ContextProject {
			t.Fatal("a category costing nothing was given a cell")
		}
	}
}

// TestContextLegendPairsEveryTintWithItsWord is invariant 1 on this surface:
// the grid is tinted, so every tone in it has to be named somewhere in the
// same colour.
func TestContextLegendPairsEveryTintWithItsWord(t *testing.T) {
	screen := goldenContextScreen()
	named := map[ContextTone]bool{}
	for _, cat := range screen.Categories {
		named[cat.Tone] = true
	}
	for _, tone := range screen.gridCells() {
		if !named[tone] {
			t.Errorf("the grid draws tone %d, which no legend row names", tone)
		}
	}
}

// TestContextGridIsAlwaysTheWholeWindow keeps the grid a fixed rectangle: it
// is a picture of the window, so a window that is nearly empty must still
// draw all of it.
func TestContextGridIsAlwaysTheWholeWindow(t *testing.T) {
	for _, pct := range []int{0, 31, 100} {
		screen := goldenContextScreen()
		screen.Pct = pct
		rows := screen.gridRows()
		if len(rows) != contextGridRows {
			t.Fatalf("%d%%: %d rows, want %d", pct, len(rows), contextGridRows)
		}
		for i, row := range rows {
			if n := len([]rune(stripANSI(row))); n != MeterCellsRail {
				t.Errorf("%d%% row %d: %d cells, want %d", pct, i, n, MeterCellsRail)
			}
		}
	}
}

// TestContextFoldCountsWhatItSwallowed is invariant 4 on this surface: a
// folded group states its count and its total, so the breakdown is an answer
// before it is opened.
func TestContextFoldCountsWhatItSwallowed(t *testing.T) {
	screen := goldenContextScreen()
	out := stripANSI(screen.View(130))
	for _, want := range []string{"▸ tool definitions", "24 tools · 22.3k", "▸ tool results", "9 tools · 2.9k"} {
		if !strings.Contains(out, want) {
			t.Errorf("folded surface is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "execute_command") {
		t.Error("a folded group drew its items")
	}
}

// TestContextExpandOpensTheGroupUnderTheCursor checks the one key that both
// folds and unfolds, and that it acts on the row the cursor is on.
func TestContextExpandOpensTheGroupUnderTheCursor(t *testing.T) {
	screen := goldenContextScreen()
	if done, _ := screen.Update(key("enter")); done {
		t.Fatal("enter left the surface")
	}
	if !screen.Groups[0].Open {
		t.Fatal("enter did not open the group under the cursor")
	}
	out := stripANSI(screen.View(130))
	if !strings.Contains(out, "▾ tool definitions") || !strings.Contains(out, "execute_command") {
		t.Errorf("opened group did not draw its items:\n%s", out)
	}
	if !strings.Contains(out, "↓ 21 more · 12.4k together") {
		t.Error("the items that were not named are not counted")
	}
	if done, _ := screen.Update(key("enter")); done || screen.Groups[0].Open {
		t.Error("enter did not fold the group again")
	}
}

// TestContextCursorStopsAtBothEnds checks the cursor does not wrap: a list
// this short has no scroll to lose your place in, and a cursor that
// reappeared at the top would read as a key that did nothing.
func TestContextCursorStopsAtBothEnds(t *testing.T) {
	screen := goldenContextScreen()
	for range 5 {
		screen.Update(key("down"))
	}
	if screen.Cursor != len(screen.Groups)-1 {
		t.Errorf("cursor ran to %d, want %d", screen.Cursor, len(screen.Groups)-1)
	}
	for range 5 {
		screen.Update(key("up"))
	}
	if screen.Cursor != 0 {
		t.Errorf("cursor ran to %d, want 0", screen.Cursor)
	}
}

// TestContextEscLeaves is invariant 3: the safe answer is the one that gives
// the keyboard back, and this surface has no other kind of answer.
func TestContextEscLeaves(t *testing.T) {
	for _, pressed := range []string{"esc", "q", "ctrl+c"} {
		screen := goldenContextScreen()
		if done, _ := screen.Update(key(pressed)); !done {
			t.Errorf("%q did not leave the surface", pressed)
		}
	}
}

// TestContextStatesItsWayOut is invariant 5: a takeover that did not say how
// to give the keyboard back would be holding it silently.
func TestContextStatesItsWayOut(t *testing.T) {
	screen := goldenContextScreen()
	if out := stripANSI(screen.View(130)); !strings.Contains(out, "[q]") {
		t.Errorf("the surface does not state its way out:\n%s", out)
	}
}

// TestContextStacksRatherThanTruncating checks the narrow layout: below the
// width the two panels fit side by side, the legend goes under the grid with
// its numbers intact rather than being clipped into them.
func TestContextStacksRatherThanTruncating(t *testing.T) {
	screen := goldenContextScreen()
	out := stripANSI(screen.View(60))
	for _, want := range []string{"687.6k", "68.8%", "283.3k"} {
		if !strings.Contains(out, want) {
			t.Errorf("narrow surface lost %q:\n%s", want, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if len([]rune(line)) > 60 {
			t.Errorf("line overflows 60 columns: %q", line)
		}
	}
}

// TestContextKeysListSwapsInPlace checks `?` opens the whole register where
// the compact key row was, which is the idiom every takeover shares.
func TestContextKeysListSwapsInPlace(t *testing.T) {
	screen := goldenContextScreen()
	screen.Update(key("?"))
	if !screen.ShowKeys {
		t.Fatal("? did not open the key list")
	}
	if out := stripANSI(screen.View(130)); !strings.Contains(out, "hide the keys") {
		t.Errorf("the open list does not offer the key that closes it:\n%s", out)
	}
}
