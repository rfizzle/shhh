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
