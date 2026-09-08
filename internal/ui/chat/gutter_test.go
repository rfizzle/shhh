package chat

// The reading gutter's render cache, and the repaint a reader holds while
// they are standing in it (focus.go, render.go).

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// gutterTestModel is a session of announcements each carrying a couple of
// calls — the step blocks an afternoon leaves behind — sized in entries.
func gutterTestModel(t testing.TB, rows int) Model {
	t.Helper()
	updated, _ := New(nil, mockStream).Update(tea.WindowSizeMsg{Width: 130, Height: 40})
	m := updated.(Model)
	for i := 0; i < rows/4; i++ {
		// A one-line announcement and the calls under it are a step block;
		// the answer that follows is a block of its own.
		m.appendEntry(entry{kind: entryAssistant, text: fmt.Sprintf("Reading the render path, pass %d", i)})
		for _, path := range []string{"render.go", "focus.go"} {
			m.appendEntry(entry{kind: entryTool, toolName: "read_file",
				toolArgs: fmt.Sprintf(`{"path":%q}`, path), toolResult: "one\ntwo\nthree\n"})
		}
		m.appendEntry(entry{kind: entryAssistant, text: fmt.Sprintf(
			"Answer %d\n\nA paragraph with `code` in it, and a list:\n\n- one\n- two\n", i)})
	}
	m.viewport.SetLines(m.renderHistoryLines())
	return m
}

// litPointer lights the pointer on the model's last selectable row, which is
// the reader a streaming tick is measured against: the gutter is on screen
// and the draft still holds the keyboard.
func litPointer(t testing.TB, m Model) Model {
	t.Helper()
	m.movePointer(1)
	if !m.gutterShowing() {
		t.Fatal("the pointer did not light, so there is no gutter to measure")
	}
	m.atBottom = true
	return m
}

// A tick under the gutter costs what the tick without one costs: the frozen
// blocks are rendered once and the gutter is put over the lines they left,
// rather than the session being rendered again on every frame a reader is
// standing still in it.
func TestGutterTickDoesNotRerenderTheWorld(t *testing.T) {
	const rows = 600
	feed := gutterTestModel(t, rows)
	reading := litPointer(t, gutterTestModel(t, rows))
	feed.flushStream()
	reading.flushStream()

	warm := testing.AllocsPerRun(3, func() { feed.flushStream() })
	gutter := testing.AllocsPerRun(3, func() { reading.flushStream() })
	if gutter > warm*3 {
		t.Fatalf("a tick under the gutter allocates %.0f times, %.1f× the warm tick's %.0f", gutter, gutter/warm, warm)
	}
}

// The cursor is the one row the cache cannot hold, so the block it stands in
// is rendered again and every other block is not — and the render is the one
// the mode drew before the cache existed, byte for byte.
func TestGutterCacheRendersWhatTheUncachedPassDid(t *testing.T) {
	m := litPointer(t, gutterTestModel(t, 40))
	want, wantStart, wantCount := m.renderFocusHistory()
	m.gutter.reset()
	got, gotStart, gotCount := m.renderFocusHistory()
	if got != want || gotStart != wantStart || gotCount != wantCount {
		t.Fatalf("cached render differs from the cold one (start %d/%d, count %d/%d)",
			gotStart, wantStart, gotCount, wantCount)
	}
	// And the cursor moves without the cache going with it: the units it
	// holds carry no cursor.
	blocks := len(m.gutter.blocks)
	m.movePointer(-1)
	if len(m.gutter.blocks) != blocks {
		t.Fatalf("moving the cursor rebuilt the cache: %d blocks, was %d", len(m.gutter.blocks), blocks)
	}
	cold := m
	cold.gutter.reset()
	if a, _, _ := m.renderFocusHistory(); a != mustRender(&cold) {
		t.Fatal("the row the cursor moved onto is not drawn as the cold render draws it")
	}
}

func mustRender(m *Model) string {
	content, _, _ := m.renderFocusHistory()
	return content
}

// A repaint a reader cannot see is not paid for: standing in the gutter off
// the live end, the transcript is not redrawn and the owed repaint is kept,
// so the way out is what pays for it.
func TestFlushHeldWhileReadingOffTheLiveEnd(t *testing.T) {
	m := litPointer(t, gutterTestModel(t, 20))
	m.flushStream()
	before := strings.Join(m.viewport.lines, "\n")

	m.atBottom = false
	m.appendEntry(entry{kind: entrySystem, text: "a row that landed while they were reading"})
	m.streamDirty = true
	m.flushStream()
	if !m.streamDirty {
		t.Fatal("the held repaint was dropped rather than kept")
	}
	if got := strings.Join(m.viewport.lines, "\n"); got != before {
		t.Fatal("the transcript was redrawn for a reader who is not looking at the live end")
	}

	if !m.dropPointer() {
		t.Fatal("the pointer would not drop")
	}
	if m.streamDirty {
		t.Fatal("leaving the gutter did not pay for the repaint it held")
	}
	if !strings.Contains(strings.Join(m.viewport.lines, "\n"), "while they were reading") {
		t.Fatal("the row that landed is still not on screen")
	}
}

// At the live end the pointer tracks the rows as they land: the reader is
// watching the tail, and holding the repaint there would freeze the feed
// under them.
func TestPointerTracksRowsAtTheLiveEnd(t *testing.T) {
	m := litPointer(t, gutterTestModel(t, 20))
	m.flushStream()
	m.appendEntry(entry{kind: entrySystem, text: "a row that landed under the pointer"})
	m.streamDirty = true
	m.flushStream()
	if m.streamDirty {
		t.Fatal("a repaint was held for a reader standing at the live end")
	}
	if !strings.Contains(strings.Join(m.viewport.lines, "\n"), "under the pointer") {
		t.Fatal("the row that landed is not on screen")
	}
}

// Reading mode pays the same way on its way out.
func TestReadingModeFlushesOnTheWayOut(t *testing.T) {
	m := gutterTestModel(t, 20)
	next, _ := m.enterFocusMode()
	m = next.(Model)
	m.atBottom = false
	m.appendEntry(entry{kind: entrySystem, text: "a row that landed during the read"})
	m.streamDirty = true
	m.flushStream()
	if !m.streamDirty {
		t.Fatal("reading mode did not hold the repaint")
	}
	next, _ = m.exitFocusMode()
	m = next.(Model)
	if m.streamDirty {
		t.Fatal("leaving reading mode did not pay for the repaint it held")
	}
}

// The tick a reader pays for while the gutter is up, against the same tick
// without one. The measured pair is what the ratio in the test above is a
// bound on.
func BenchmarkTranscriptTick(b *testing.B) {
	const rows = 600
	b.Run("feed", func(b *testing.B) {
		m := gutterTestModel(b, rows)
		m.flushStream()
		for b.Loop() {
			m.flushStream()
		}
	})
	b.Run("reading", func(b *testing.B) {
		m := litPointer(b, gutterTestModel(b, rows))
		m.flushStream()
		for b.Loop() {
			m.flushStream()
		}
	})
}

// rowOnScreen is the membership test the pointer asks in place of building
// the whole list, so the two have to agree on every row of a transcript with
// steps, folds and rows nothing can stand on.
func TestRowOnScreenAgreesWithTheList(t *testing.T) {
	m := gutterTestModel(t, 24)
	m.appendEntry(entry{kind: entrySystem, text: "a notice with nothing under it"})
	m.appendEntry(entry{kind: entryUser, text: "a question"})
	// The first step is opened and the rest stay folded, so both answers —
	// a step's rows on screen and a step's rows not on screen — are in the
	// transcript this walks.
	m.transcript[0].stepFold = foldOpen
	on := map[int]bool{}
	for _, idx := range m.expandableIndices() {
		on[idx] = true
	}
	for i := range m.transcript {
		if got := m.rowOnScreen(i); got != on[i] {
			t.Fatalf("row %d (%v): rowOnScreen %v, the list says %v", i, m.transcript[i].kind, got, on[i])
		}
	}
}

// A row opened under the cursor renders differently from then on, and it may
// belong to a block the cache has frozen. Moving the cursor away is what
// would show a cache that kept the old lines: the row the reader just opened
// would close itself behind them.
func TestOpeningARowOutlivesTheCursorLeavingIt(t *testing.T) {
	m := gutterTestModel(t, 40)
	next, _ := m.enterFocusMode()
	m = next.(Model)
	// The first step's header, whose block the cache has long since frozen.
	idxs := m.expandableIndices()
	m.focusIdx = idxs[0]
	m.refreshFocusView()
	next, _ = m.openCursorRow(stateFocus)
	m = next.(Model)

	// The cursor leaves that block for the last row in the transcript, so
	// the block it opened is one the render takes from the cache.
	idxs = m.expandableIndices()
	m.focusIdx = idxs[len(idxs)-1]
	m.refreshFocusView()
	got, _, _ := m.renderFocusHistory()
	cold := m
	cold.gutter.reset()
	want, _, _ := cold.renderFocusHistory()
	if got != want {
		t.Fatal("the cache drew the row as it was before it was opened")
	}
}
