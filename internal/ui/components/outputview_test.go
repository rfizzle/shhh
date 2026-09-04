package components

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func outputFixture(n int) *OutputView {
	lines := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	return &OutputView{Title: "$ go test ./...", Lines: lines, Height: 12}
}

func TestOutputView_ScrollsAndClamps(t *testing.T) {
	v := outputFixture(100)
	plain := ansi.Strip(v.View(80))
	if !strings.Contains(plain, "$ go test ./...") || !strings.Contains(plain, "100 lines") {
		t.Fatalf("the header carries the title and the count:\n%s", plain)
	}
	if !strings.Contains(plain, "line 1") || strings.Contains(plain, "line 11") {
		t.Fatalf("the body shows its budget from the top:\n%s", plain)
	}

	v.Scroll(1000)
	end := ansi.Strip(v.View(80))
	if !strings.Contains(end, "line 100") {
		t.Fatalf("a scroll past the end clamps to it:\n%s", end)
	}
	if v.Offset != 100-v.bodyHeight() {
		t.Fatalf("the stored offset is clamped by the render, got %d", v.Offset)
	}
	v.Scroll(-1000)
	if top := ansi.Strip(v.View(80)); !strings.Contains(top, "line 1\n") && !strings.Contains(top, "line 1 ") {
		t.Fatalf("scrolling back reaches the top:\n%s", top)
	}
}

// Foreign bytes are re-painted on the way through, exactly as a detail
// body's are: no colour of the program's own reaches the screen.
func TestOutputView_RepaintsForeignOutput(t *testing.T) {
	v := &OutputView{Title: "$ go test", Height: 8,
		Lines: []string{"--- \x1b[31mFAIL\x1b[0m: TestX"}}
	view := v.View(80)
	if strings.Contains(view, "\x1b[31m") {
		t.Fatal("a program's own red should be re-painted into the palette")
	}
	if !strings.Contains(ansi.Strip(view), "--- FAIL: TestX") {
		t.Fatalf("the words survive the repaint:\n%s", view)
	}
}

// Wrap trades the clip for soft-wrapped rows, for the one view whose body is
// read whole rather than scanned by line heads.
func TestOutputView_WrapKeepsTheWholeLine(t *testing.T) {
	long := strings.Repeat("word ", 40)
	v := &OutputView{Title: "Approve command", Height: 30,
		Lines: []string{long}, Wrap: true}
	plain := ansi.Strip(v.View(40))
	if strings.Count(plain, "word") != 40 {
		t.Fatalf("wrapping should keep every word on screen, got %d of 40:\n%s",
			strings.Count(plain, "word"), plain)
	}
}

// The offset four surfaces read a long body through. What they had written
// four times is the corner cases: an offset past the end, a body shorter than
// the pane, and a pane that has to stay the same height either way.
func TestPager_HoldsTheOffsetInsideTheBody(t *testing.T) {
	p := Pager{Offset: 900, Height: 10, Total: 100}
	if got := p.Held(); got != 90 {
		t.Fatalf("an offset past the end settles on the last full pane, got %d", got)
	}
	p = Pager{Offset: -5, Height: 10, Total: 100}
	if got := p.Held(); got != 0 {
		t.Fatalf("an offset above the top settles at the top, got %d", got)
	}
	p = Pager{Offset: 4, Height: 10, Total: 3}
	if got := p.Held(); got != 0 {
		t.Fatalf("a body shorter than the pane holds at the top, got %d", got)
	}
}

// Window is the run the offset names, and it writes the held offset back so
// one press past the end costs no press to recover from.
func TestPager_WindowWritesTheHeldOffsetBack(t *testing.T) {
	rows := make([]string, 20)
	for i := range rows {
		rows[i] = fmt.Sprintf("row %d", i)
	}
	p := Pager{Offset: 99, Height: 5}
	got := p.Window(rows)
	if p.Offset != 15 {
		t.Fatalf("the overshoot should settle at 15, got %d", p.Offset)
	}
	if len(got) != 5 || got[0] != "row 15" {
		t.Fatalf("the window is the five rows from 15, got %v", got)
	}
	if p.Above() != 15 || p.Below() != 0 {
		t.Fatalf("above/below = %d/%d, want 15/0", p.Above(), p.Below())
	}
}

// Reveal is what a pane scrolled by something other than the wheel does: the
// least movement that brings a row in.
func TestPager_RevealScrollsTheLeastItCan(t *testing.T) {
	p := Pager{Offset: 10, Height: 5, Total: 100}
	p.Reveal(12)
	if p.Offset != 10 {
		t.Fatalf("a row already showing moves nothing, got %d", p.Offset)
	}
	p.Reveal(3)
	if p.Offset != 3 {
		t.Fatalf("a row above the pane pulls it up to meet it, got %d", p.Offset)
	}
	p.Reveal(20)
	if p.Offset != 16 {
		t.Fatalf("a row below the pane pushes it down to end on it, got %d", p.Offset)
	}
}

// A body too short to reach the bottom of the screen still puts the footer
// there: a viewer whose keys walk up as its content shortens is one a reader
// has to look for.
func TestPager_ScreenPadsToThePanesRows(t *testing.T) {
	p := Pager{Height: 6}
	got := strings.Split(p.Screen("head", []string{"a", "b"}, "foot"), "\n")
	if len(got) != 8 {
		t.Fatalf("header + 6 rows + footer is 8 rows, got %d: %q", len(got), got)
	}
	if got[0] != "head" || got[len(got)-1] != "foot" {
		t.Fatalf("the header and footer are the ends, got %q", got)
	}
}
