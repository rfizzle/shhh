package chat

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func resizeTestModel(t testing.TB, rows int) Model {
	t.Helper()
	updated, _ := New(nil, mockStream).Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m := updated.(Model)
	for i := 0; i < rows; i++ {
		m.appendEntry(entry{kind: entrySystem, text: fmt.Sprintf("row %d: a sentence long enough to wrap somewhere once the pane narrows under it", i)})
	}
	m.viewport.SetLines(m.renderHistoryLines())
	return m
}

func countHistoryRenders(t testing.TB) *int {
	t.Helper()
	n := new(int)
	testHookRenderHistory = func() { *n++ }
	t.Cleanup(func() { testHookRenderHistory = nil })
	return n
}

// A burst of sizes re-wraps the history once, when the last one's settle
// window closes; the settles scheduled mid-burst recognise themselves as
// stale.
func TestResizeBurstRendersOnce(t *testing.T) {
	m := resizeTestModel(t, 40)
	renders := countHistoryRenders(t)

	var stale []int
	for i := 0; i < 5; i++ {
		updated, _ := m.Update(tea.WindowSizeMsg{Width: 90 - i*5, Height: 40})
		m = updated.(Model)
		stale = append(stale, m.resizeSeq)
	}
	if *renders != 0 {
		t.Fatalf("history rendered %d times during the burst, want 0", *renders)
	}
	for _, seq := range stale[:4] {
		updated, _ := m.Update(resizeSettledMsg{seq: seq})
		m = updated.(Model)
	}
	if *renders != 0 {
		t.Fatalf("a stale settle rendered: %d renders", *renders)
	}
	updated, _ := m.Update(resizeSettledMsg{seq: stale[4]})
	m = updated.(Model)
	if *renders != 1 {
		t.Fatalf("history rendered %d times after settling, want 1", *renders)
	}
}

// After the settle the pane holds lines wrapped at the final width — not the
// width the drag passed through, and none wider than the pane.
func TestResizeSettleRendersFinalWidth(t *testing.T) {
	m := resizeTestModel(t, 20)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	m = updated.(Model)
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 40})
	m = updated.(Model)
	updated, _ = m.Update(resizeSettledMsg{seq: m.resizeSeq})
	m = updated.(Model)

	want := m.renderHistoryLines()
	if got := strings.Join(m.viewport.lines, "\n"); got != strings.Join(want, "\n") {
		t.Fatal("settled pane does not hold the final-width render")
	}
}

// During the settle window the rectangles are already the new shape: the
// viewport reports the new size even though its lines are the old render.
func TestResizeMovesRectanglesImmediately(t *testing.T) {
	m := resizeTestModel(t, 20)
	renders := countHistoryRenders(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m = updated.(Model)
	if *renders != 0 {
		t.Fatalf("resize rendered before settling")
	}
	// The pane's columns move with the message; its rows were set from the
	// same split (the bottom panel re-measures itself against the pane on
	// the next sync, so only the width is stable enough to pin here).
	if m.viewport.Width() != m.transcriptWidth() {
		t.Fatalf("viewport width %d, want %d", m.viewport.Width(), m.transcriptWidth())
	}
	if m.viewport.Height() >= 40 {
		t.Fatalf("viewport height %d still sized for the old terminal", m.viewport.Height())
	}
}

// The first width change still cancels a selection: its coordinates were
// taken over lines the new width no longer describes.
func TestResizeClearsSelection(t *testing.T) {
	m := resizeTestModel(t, 20)
	m.sel = selection{on: true, width: m.transcriptWidth()}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 70, Height: 40})
	m = updated.(Model)
	if m.sel.on {
		t.Fatal("selection survived the width change")
	}
}

// Thirty sizes over a long history cost at most two full renders (the settle
// after the burst; nothing during it).
func BenchmarkResizeBurst(b *testing.B) {
	m := resizeTestModel(b, 2000)
	renders := countHistoryRenders(b)
	for b.Loop() {
		mm := m
		before := *renders
		for i := 0; i < 30; i++ {
			updated, _ := mm.Update(tea.WindowSizeMsg{Width: 70 + i%20, Height: 40})
			mm = updated.(Model)
		}
		updated, _ := mm.Update(resizeSettledMsg{seq: mm.resizeSeq})
		mm = updated.(Model)
		if got := *renders - before; got > 2 {
			b.Fatalf("%d history renders for 30 resizes, want at most 2", got)
		}
	}
}
