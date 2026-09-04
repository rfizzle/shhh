package chat

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// numbered is a transcript-shaped fixture: n lines, each naming its index, so
// a window can be checked against the offset that produced it.
func numbered(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	return lines
}

func paneLines(v viewport) []string {
	return strings.Split(v.View(), "\n")
}

// The window is the point of the file: a pane shows the lines its offset
// names and no others, however long the session is behind them.
func TestViewport_ShowsOnlyTheWindow(t *testing.T) {
	v := newViewport(20, 5)
	v.SetLines(numbered(1000))
	v.SetYOffset(400)

	got := paneLines(v)
	if len(got) != 5 {
		t.Fatalf("a 5-row pane rendered %d rows", len(got))
	}
	for i, line := range got {
		if want := fmt.Sprintf("line %d", 400+i); strings.TrimRight(line, " ") != want {
			t.Fatalf("row %d is %q, want %q", i, line, want)
		}
		if ansi.StringWidth(line) != 20 {
			t.Fatalf("row %d is %d cells wide, the pane is 20", i, ansi.StringWidth(line))
		}
	}
}

// A pane shorter than its content is padded to its own height, so the scroll
// gutter has a row to glue itself to whether or not there is a line.
func TestViewport_PadsToItsOwnHeight(t *testing.T) {
	v := newViewport(10, 6)
	v.SetLines(numbered(2))
	if got := len(paneLines(v)); got != 6 {
		t.Fatalf("a 6-row pane over 2 lines rendered %d rows", got)
	}
}

func TestViewport_ScrollAndClamp(t *testing.T) {
	v := newViewport(20, 10)
	v.SetLines(numbered(100))
	if !v.AtTop() || v.AtBottom() {
		t.Fatalf("a fresh pane should be at the top, offset %d", v.YOffset())
	}
	if got, want := v.TotalLineCount(), 100; got != want {
		t.Fatalf("TotalLineCount = %d, want %d", got, want)
	}
	if got, want := v.VisibleLineCount(), 10; got != want {
		t.Fatalf("VisibleLineCount = %d, want %d", got, want)
	}

	v.ScrollUp(5)
	if v.YOffset() != 0 {
		t.Fatalf("scrolling up from the top moved to %d", v.YOffset())
	}
	v.PageDown()
	if v.YOffset() != 10 {
		t.Fatalf("a page down from the top is %d, want 10", v.YOffset())
	}
	v.PageUp()
	if v.YOffset() != 0 {
		t.Fatalf("a page back up is %d, want 0", v.YOffset())
	}
	v.GotoBottom()
	if v.YOffset() != 90 || !v.AtBottom() {
		t.Fatalf("the bottom of 100 lines in a 10-row pane is 90, got %d", v.YOffset())
	}
	v.ScrollDown(50)
	if v.YOffset() != 90 {
		t.Fatalf("scrolling past the bottom reached %d", v.YOffset())
	}
	v.GotoTop()
	if v.YOffset() != 0 {
		t.Fatalf("GotoTop reached %d", v.YOffset())
	}
}

// Content that shrank out from under the offset — a /clear, a rewind — takes
// the pane down with it rather than leaving it looking at nothing.
func TestViewport_ShrinkingContentFollowsTheOffsetDown(t *testing.T) {
	v := newViewport(20, 10)
	v.SetLines(numbered(100))
	v.GotoBottom()
	v.SetLines(numbered(12))
	if v.YOffset() != 2 {
		t.Fatalf("offset after the transcript shrank to 12 lines is %d, want 2", v.YOffset())
	}
	v.SetLines(nil)
	if v.YOffset() != 0 || v.TotalLineCount() != 0 {
		t.Fatalf("an emptied transcript left offset %d over %d lines", v.YOffset(), v.TotalLineCount())
	}
}

// An empty render is no content, not one blank line: a session with nothing
// in it must not put a thumb in the scroll gutter.
func TestViewport_EmptyRenderIsNoLines(t *testing.T) {
	v := newViewport(20, 10)
	v.SetContent("")
	if v.TotalLineCount() != 0 {
		t.Fatalf("an empty render counted %d lines", v.TotalLineCount())
	}
	if !v.AtBottom() || !v.AtTop() {
		t.Fatal("an empty pane is at both ends of itself")
	}
}

// Nothing in the transcript should be wider than the pane, and a renderer
// that got it wrong must not be allowed to break the frame's shape — nor to
// have its line rewritten in the cache the pane is reading from.
func TestViewport_CutsOverwideLinesWithoutTouchingTheCache(t *testing.T) {
	lines := []string{"short", strings.Repeat("wide ", 20), "short again"}
	before := slices.Clone(lines)
	v := newViewport(12, 3)
	v.SetLines(lines)

	for i, row := range paneLines(v) {
		if got := ansi.StringWidth(row); got != 12 {
			t.Fatalf("row %d is %d cells wide, the pane is 12", i, got)
		}
	}
	if !slices.Equal(lines, before) {
		t.Fatalf("the render rewrote its input: %q → %q", before, lines)
	}
}

// searchable is a transcript-shaped fixture with a word to look for on known
// lines, and nothing else that could match it.
func searchable(n int, on ...int) []string {
	lines := numbered(n)
	for _, i := range on {
		lines[i] = fmt.Sprintf("line %d mentions internal/agent/loop.go here", i)
	}
	return lines
}

// The spike's outcome, tested on the pane it landed on: a search finds every
// occurrence and jumps to them, and the pane follows.
func TestViewportSearch_FindsAndJumps(t *testing.T) {
	v := newViewport(60, 5)
	v.SetLines(searchable(200, 3, 120, 199))

	if got := v.Search("loop.go"); got != 3 {
		t.Fatalf("three lines mention it, found %d", got)
	}
	at, total := v.MatchPosition()
	if at != 1 || total != 3 {
		t.Fatalf("a fresh search starts on the first occurrence, got %d/%d", at, total)
	}

	v.NextMatch()
	if at, _ := v.MatchPosition(); at != 2 {
		t.Fatalf("next should move to the second occurrence, got %d", at)
	}
	if off := v.YOffset(); off > 120 || off+v.Height() <= 120 {
		t.Fatalf("the pane should be showing line 120, offset %d", off)
	}

	v.PrevMatch()
	if at, _ := v.MatchPosition(); at != 1 {
		t.Fatalf("previous should come back to the first, got %d", at)
	}
	if off := v.YOffset(); off > 3 || off+v.Height() <= 3 {
		t.Fatalf("the pane should be showing line 3, offset %d", off)
	}

	// It wraps rather than stopping, so a reader never has to scroll back to
	// start again.
	v.PrevMatch()
	if at, _ := v.MatchPosition(); at != 3 {
		t.Fatalf("previous from the first should wrap to the last, got %d", at)
	}
}

// The half of the bubbles viewport that decided the spike: a search has to
// outlive the live tail, which during a turn is rebuilt on every frame.
func TestViewportSearch_SurvivesTheLineCacheRebuildingItsTail(t *testing.T) {
	v := newViewport(60, 5)
	v.SetLines(searchable(50, 10, 40))
	if got := v.Search("loop.go"); got != 2 {
		t.Fatalf("found %d", got)
	}

	// The tail is rebuilt and a line lands, exactly as a streaming frame
	// does it.
	next := append(searchable(50, 10, 40), "line 50 mentions internal/agent/loop.go here")
	v.SetLines(next)

	if _, total := v.MatchPosition(); total != 3 {
		t.Fatalf("the new line should have joined the matches, got %d", total)
	}
	v.NextMatch()
	if at, _ := v.MatchPosition(); at != 2 {
		t.Fatalf("next still walks the occurrences after a rebuild, got %d", at)
	}
}

// A match on screen is marked, and the one the pointer is on is marked
// differently — both structurally, so the two read apart in mono.
func TestViewportSearch_MarksWhatIsOnTheScreen(t *testing.T) {
	v := newViewport(60, 5)
	v.SetLines(searchable(20, 1, 2))
	v.Search("loop.go")
	v.SetYOffset(0)

	rows := paneLines(v)
	if !strings.Contains(rows[1], "\x1b[") {
		t.Fatalf("the occurrence the pointer is on should be marked: %q", rows[1])
	}
	if !strings.Contains(rows[2], "\x1b[") {
		t.Fatalf("the other occurrence on screen should be marked too: %q", rows[2])
	}
	if rows[1] == rows[2] {
		t.Fatal("the occurrence the pointer is on must read differently from the rest")
	}
	if strings.Contains(rows[3], "\x1b[") {
		t.Fatalf("a line with no occurrence on it is left alone: %q", rows[3])
	}
}

// The lines belong to the line cache, which rebuilds them in place: a render
// that marked them would rewrite what the next frame is about to read.
func TestViewportSearch_MarksACopyAndNotTheCache(t *testing.T) {
	lines := searchable(20, 1)
	v := newViewport(60, 5)
	v.SetLines(lines)
	v.Search("loop.go")
	held := slices.Clone(lines)

	_ = v.View()
	if !slices.Equal(lines, held) {
		t.Fatal("painting the search rewrote the cache's own lines")
	}
}

// An empty query is how a search is left, and it puts every line back.
func TestViewportSearch_AnEmptyQueryClearsIt(t *testing.T) {
	v := newViewport(60, 5)
	v.SetLines(searchable(20, 1))
	v.Search("loop.go")
	if !v.Searching() {
		t.Fatal("a query is a search")
	}
	if got := v.Search(""); got != 0 || v.Searching() {
		t.Fatalf("an empty query clears the search, found %d searching=%v", got, v.Searching())
	}
	if strings.Contains(paneLines(v)[1], "\x1b[") {
		t.Fatal("nothing is marked once the search is cleared")
	}
}
