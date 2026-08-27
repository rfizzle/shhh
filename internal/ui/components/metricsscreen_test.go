package components

// The metrics surface (S-129, DESIGN-TUI.md §19c). The assertions here are
// about the three rules the screen exists to keep: numeric columns are fixed
// and right-aligned so a reader scans a column rather than parsing rows, a
// sparkline is dimmer and never coloured because it is a shape rather than a
// measurement, and every ratio is a block meter with its number stated beside
// the bar.

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func metricsModels() []MetricsModel {
	return []MetricsModel{
		{Name: "gpt-5.2", Requests: "184", TokensIn: "2.9M", TokensOut: "318k",
			Spend: "$12.80", TTFT: "640ms", P95: "1.4s",
			Trend: []float64{3, 4, 4, 5, 6, 5, 8}},
		{Name: "claude-sonnet-4.6", Requests: "46", TokensIn: "1.1M", TokensOut: "96k",
			Spend: "$4.71", TTFT: "910ms", P95: "2.1s",
			Trend: []float64{1, 2, 5, 3, 2, 6, 4}},
		{Name: "house-model", Requests: "12", TokensIn: "88k", TokensOut: "7k",
			Spend: NoDuration, TTFT: "310ms", P95: NoDuration,
			Trend: []float64{0, 0, 1, 0, 0, 0, 2}},
	}
}

func metricsBlocks() []MetricsBlock {
	return []MetricsBlock{
		{Title: "where the money went", Field: "last 30 days", Bars: []MetricsBar{
			{Label: "$ run", Pct: 54, Text: "$9.94 · 54%", Note: "203 requests", Tone: MeterCategory},
			{Label: "copied", Pct: 28, Text: "$5.11 · 28%", Note: "31 requests", Tone: MeterCategory},
			{Label: "✗ no answer", Pct: 5, Text: "$0.96 · 5%", Note: "9 requests",
				NoteTone: ToneRisk, Tone: MeterUnasked},
		}},
		{Title: "how the answers came back", Field: "242 requests", Bars: []MetricsBar{
			{Label: "gpt-5.2", Pct: 94, Text: "94% answered", Note: "173 of 184", Tone: MeterCategory},
			{Label: "claude-sonnet-4.6", Pct: 100, Text: "100% answered", Note: "46 of 46", Tone: MeterCategory},
		}},
		{Title: "how the commands ran", Field: "203 runs", Bars: []MetricsBar{
			{Label: "gpt-5.2", Pct: 81, Text: "81% exited 0", Note: "164 of 203", Tone: MeterCategory},
		}},
	}
}

func metricsScreen() *MetricsScreen {
	return &MetricsScreen{
		Subject: "last 30 days · 242 requests · 3 models",
		Spend:   "$18.42",
		Models:  metricsModels(),
		Blocks:  metricsBlocks(),
	}
}

func metricsPlain(m *MetricsScreen, width int) string { return ansi.Strip(m.View(width)) }

// metricsLines is the render split into plain lines.
func metricsLines(m *MetricsScreen, width int) []string {
	return strings.Split(metricsPlain(m, width), "\n")
}

// metricsRowFor is the plain line the given text appears on.
func metricsRowFor(m *MetricsScreen, width int, text string) string {
	for _, line := range metricsLines(m, width) {
		if strings.Contains(line, text) {
			return line
		}
	}
	return ""
}

// metricsColumnEnd is the display column a field ends at, which is not its
// byte offset: a glyph outside ASCII costs three bytes and one column.
func metricsColumnEnd(line, field string) int {
	at := strings.Index(line, field)
	if at < 0 {
		return -1
	}
	return lipgloss.Width(line[:at]) + lipgloss.Width(field)
}

// The header names the command, what it is over, what it cost, and the one
// key the screen has (§19c).
func TestMetricsScreen_HeaderStatesTheSpendAndTheKey(t *testing.T) {
	head := metricsLines(metricsScreen(), 130)[0]
	for _, want := range []string{"shhh metrics", "242 requests", "$18.42", "[q] quit"} {
		if !strings.Contains(head, want) {
			t.Fatalf("the header %q does not state %q", head, want)
		}
	}
}

// It is the subject that gives ground as the terminal narrows, not the spend
// or the key: this screen has no foot key row to fall back on, so dropping
// `[q]` would leave a takeover surface with no stated way out (invariant 5).
func TestMetricsScreen_NarrowHeaderKeepsTheSpendAndTheKey(t *testing.T) {
	head := metricsLines(metricsScreen(), 50)[0]
	if !strings.Contains(head, "$18.42") || !strings.Contains(head, "[q] quit") {
		t.Fatalf("a narrow header dropped the spend or the key: %q", head)
	}
	if strings.Contains(head, "3 models") {
		t.Fatalf("a narrow header kept the whole subject: %q", head)
	}
}

// `[q]`, `[esc]` and ctrl+c close the screen; nothing else does anything,
// because there is nothing else to do.
func TestMetricsScreen_OnlyTheQuitKeysCloseIt(t *testing.T) {
	for _, k := range []string{"q", "esc", "ctrl+c"} {
		m := metricsScreen()
		if done, _ := m.Update(key(k)); !done {
			t.Fatalf("%s did not close the screen", k)
		}
	}
	for _, k := range []string{"?", "enter", "down", "/", "j"} {
		m := metricsScreen()
		if done, _ := m.Update(key(k)); done {
			t.Fatalf("%s closed a screen that has no such key", k)
		}
	}
}

// Numeric columns are right-aligned in a fixed width, so the digits line up
// down the column rather than after the longest row (§19c).
func TestMetricsScreen_NumericColumnsLineUp(t *testing.T) {
	m := metricsScreen()
	lines := metricsLines(m, 130)
	head := metricsRowFor(m, 130, "REQUESTS")
	if head == "" {
		t.Fatal("the table has no heading")
	}
	end := metricsColumnEnd(head, "REQUESTS")
	for _, tc := range []struct{ name, requests string }{
		{"gpt-5.2", "184"}, {"claude-sonnet-4.6", "46"}, {"house-model", "12"},
	} {
		row := metricsRowFor(m, 130, tc.name)
		if row == "" {
			t.Fatalf("no row for %q:\n%s", tc.name, strings.Join(lines, "\n"))
		}
		if got := metricsColumnEnd(row, tc.requests); got != end {
			t.Fatalf("%s's count ends at column %d, the column ends at %d:\n%s",
				tc.name, got, end, row)
		}
	}
}

// The heading is the same rail every group heading in the product is, and the
// numbers under it are not painted with it.
func TestMetricsScreen_HeadingIsARail(t *testing.T) {
	withColorProfile(t, termenv.ANSI256)
	m := metricsScreen()
	heading := strings.TrimSpace(metricsRowFor(m, 130, "REQUESTS"))
	if !strings.Contains(m.View(130), headlineStyle.Render(heading)) {
		t.Fatalf("the heading %q is not the group rail every other list uses", heading)
	}
}

// A sparkline is dimmer and never coloured: it is the shape, and the numbers
// beside it are the measurement (§10c, §19c).
func TestMetricsScreen_SparklineIsDimmerAndNeverColoured(t *testing.T) {
	withColorProfile(t, termenv.ANSI256)
	view := metricsScreen().View(130)
	run := sparkCells(metricsModels()[0].Trend, metricsTrendCells)
	if !strings.Contains(view, dimmerStyle.Render(run)) {
		t.Fatalf("the trend %q is not drawn in dimmer", run)
	}
	for _, style := range []lipgloss.Style{accentStyle, addStyle, delStyle, infoStyle} {
		if strings.Contains(view, style.Render(run)) {
			t.Fatal("the trend is coloured, which would imply a threshold nobody set")
		}
	}
}

// Columns give ground whole as the terminal narrows, in the stated order —
// the shape before the measurement — and MODEL and SPEND never go.
func TestMetricsScreen_ColumnsDropWholeAndInOrder(t *testing.T) {
	m := metricsScreen()
	wide := metricsRowFor(m, 130, "REQUESTS")
	for _, want := range []string{"TOK · 7d", "P95", "TTFT"} {
		if !strings.Contains(wide, want) {
			t.Fatalf("the widest table is missing %q: %q", want, wide)
		}
	}
	narrow := metricsRowFor(m, 60, "REQUESTS")
	if strings.Contains(narrow, "TOK · 7d") {
		t.Fatalf("the shape outlived the measurements at 60 columns: %q", narrow)
	}
	tight := metricsRowFor(m, 44, "MODEL")
	if !strings.Contains(tight, "MODEL") || !strings.Contains(tight, "SPEND") {
		t.Fatalf("a tight table dropped the name or the spend: %q", tight)
	}
	if strings.Contains(tight, "TTFT") || strings.Contains(tight, "P95") {
		t.Fatalf("a tight table kept a latency column: %q", tight)
	}
}

// Nothing is ever half a column: a column that does not fit is gone, not
// truncated (invariant 4).
func TestMetricsScreen_NothingOverrunsTheWidth(t *testing.T) {
	m := metricsScreen()
	for _, width := range []int{44, 60, 80, 110, 130} {
		for _, line := range metricsLines(m, width) {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("width %d: a line is %d columns: %q", width, got, line)
			}
		}
	}
}

// Every bar states its number beside it: a bar alone is a shape, not a
// number (§10c).
func TestMetricsScreen_EveryBarStatesItsNumber(t *testing.T) {
	out := metricsPlain(metricsScreen(), 130)
	for _, want := range []string{"$9.94 · 54%", "94% answered", "81% exited 0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("a bar has no number beside it — %q is missing:\n%s", want, out)
		}
	}
}

// A cost nobody asked for is del, and it says so in a glyph as well, so the
// row still reads once the colour is gone (invariant 1).
func TestMetricsScreen_TheUnaskedCostIsToldTwice(t *testing.T) {
	withColorProfile(t, termenv.ANSI256)
	m := metricsScreen()
	unasked := Meter{Pct: 5, Cells: MeterCellsRail, Tone: MeterUnasked, Text: "$0.96 · 5%"}
	if !strings.Contains(m.View(130), unasked.View()) {
		t.Fatal("the unasked cost's bar is not del")
	}
	if !strings.Contains(metricsPlain(m, 130), "✗ no answer") {
		t.Fatal("the unasked cost carries no glyph, so mono cannot tell it apart")
	}
}

// The bars share one label column across every block: the blocks are readings
// of the same models, and three columns a character apart would read as three
// different tables.
func TestMetricsScreen_BlocksShareOneLabelColumn(t *testing.T) {
	m := metricsScreen()
	at := -1
	for _, line := range metricsLines(m, 130) {
		bar := metricsColumnEnd(line, "▰")
		if bar < 0 {
			continue
		}
		if at >= 0 && bar != at {
			t.Fatalf("a meter starts at column %d where the one above starts at %d: %q", bar, at, line)
		}
		at = bar
	}
	if at < 0 {
		t.Fatal("no meters were drawn at all")
	}
}

// The note is the field that annotates the bar, and it drops before anything
// else on the row does (§16). It drops for the whole block at once: a note on
// the one short row and not on the three beside it would read as a fact about
// that row.
func TestMetricsScreen_NotesDropAsABlock(t *testing.T) {
	m := metricsScreen()
	if !strings.Contains(metricsPlain(m, 130), "203 requests") {
		t.Fatal("a wide screen dropped the notes")
	}
	out := metricsPlain(m, 56)
	for _, note := range []string{"203 requests", "31 requests", "9 requests"} {
		if strings.Contains(out, note) {
			t.Fatalf("a note survived alone at 56 columns: %q\n%s", note, out)
		}
	}
	if !strings.Contains(out, "$9.94 · 54%") {
		t.Fatalf("the number beside the bar dropped before the note did:\n%s", out)
	}
}

// A screen too short for its blocks drops them whole and names what went — a
// marker that only said "2 more" would leave the reader guessing which two
// readings the screen is sitting on (invariant 4).
func TestMetricsScreen_DroppedBlocksAreNamed(t *testing.T) {
	m := metricsScreen()
	m.MaxLines = 12
	out := metricsPlain(m, 130)
	if !strings.Contains(out, "how the commands ran") {
		t.Fatalf("the dropped block is not named:\n%s", out)
	}
	if strings.Contains(out, "81% exited 0") {
		t.Fatalf("a dropped block still drew its bars:\n%s", out)
	}
	if !strings.Contains(out, "where the money went") {
		t.Fatalf("the blocks gave ground from the wrong end:\n%s", out)
	}
}

// A screen that fits some of its blocks still names the ones it could not:
// the marker takes its row out of the budget rather than being what the
// budget squeezed out.
func TestMetricsScreen_ABlockThatFitsKeepsTheMarkerBesideIt(t *testing.T) {
	m := metricsScreen()
	m.MaxLines = 16
	out := metricsPlain(m, 130)
	if !strings.Contains(out, "$9.94 · 54%") {
		t.Fatalf("the block that fits was dropped anyway:\n%s", out)
	}
	if !strings.Contains(out, "↓ 2 more · how the answers came back · how the commands ran") {
		t.Fatalf("the blocks that went are not named beside the one that fit:\n%s", out)
	}
	if got := len(metricsLines(m, 130)); got > m.MaxLines {
		t.Fatalf("the screen is %d rows against a budget of %d:\n%s", got, m.MaxLines, out)
	}
}

// The table gives ground last, and when it has to, it says how many models it
// is holding back — and still says which blocks went with them.
func TestMetricsScreen_TheTableWindowsLast(t *testing.T) {
	m := metricsScreen()
	m.MaxLines = 6
	out := metricsPlain(m, 130)
	if !strings.Contains(out, "gpt-5.2") {
		t.Fatalf("the table was dropped before the blocks were:\n%s", out)
	}
	if !strings.Contains(out, "↓ 2 more models") {
		t.Fatalf("the windowed table did not say what it is holding back:\n%s", out)
	}
	if !strings.Contains(out, "↓ 3 more ·") {
		t.Fatalf("the blocks went silently:\n%s", out)
	}
}

// It is a takeover surface, so it draws no frame of its own (§19).
func TestMetricsScreen_DrawsNoFrame(t *testing.T) {
	out := metricsPlain(metricsScreen(), 110)
	for _, glyph := range []string{"┌", "┐", "└", "┘", "│", "╭", "╰"} {
		if strings.Contains(out, glyph) {
			t.Fatalf("the screen drew a frame glyph %q:\n%s", glyph, out)
		}
	}
}

// A table with no rows says so rather than leaving a heading over nothing.
func TestMetricsScreen_NoModelsSaysSo(t *testing.T) {
	m := &MetricsScreen{Subject: "all time · 0 requests"}
	if !strings.Contains(metricsPlain(m, 110), "no models to show") {
		t.Fatal("an empty table left its heading over nothing")
	}
}
