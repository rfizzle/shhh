package chat

import (
	"slices"
	"strings"
	"testing"
)

// The contract lines.go exists to keep: writing text into the cache leaves
// exactly the lines strings.Split would have left on the concatenation of the
// same text. Every golden in this package reads renderHistory(), which joins
// these lines back up, so a drift here is a drift in every one of them.
func TestLineCache_WriteMatchesConcatenation(t *testing.T) {
	cases := [][]string{
		{},
		{"one line\n"},
		{"one line\n", "another\n"},
		{"a block\nof three\nlines\n"},
		// A separator is a newline the next block begins with (§13), so the
		// blank line lands between the two rather than inside either.
		{"first\n", "\nsecond\n"},
		// Text that does not end in a newline leaves the line open, and the
		// next write continues it — which is what the streaming tail does.
		{"header\n", "part", " and the rest\n"},
		{"", "nothing before it\n"},
	}
	for _, writes := range cases {
		var c lineCache
		c.rewind()
		for _, w := range writes {
			c.write(w)
		}
		want := strings.Split(strings.Join(writes, ""), "\n")
		if !slices.Equal(c.lines, want) {
			t.Fatalf("write(%q) = %q, concatenation splits to %q", writes, c.lines, want)
		}
	}
}

// A frozen block is rendered once. Rewinding for the next frame must give the
// tail back and nothing else: the lines below the seam are the ones the pane,
// the selection and the clipboard all keep reading (§10m).
func TestLineCache_RewindKeepsTheFrozenPrefix(t *testing.T) {
	var c lineCache
	c.rewind()
	c.write("frozen one\nfrozen two\n")
	c.freeze()
	frozen := append([]string(nil), c.lines[:c.frozen]...)

	c.write("a live tail\n")
	c.rewind()
	c.write("a different live tail\nover two lines\n")

	if !slices.Equal(c.lines[:c.frozen], frozen) {
		t.Fatalf("the frozen prefix changed: %q → %q", frozen, c.lines[:c.frozen])
	}
	want := []string{"frozen one", "frozen two", "a different live tail", "over two lines", ""}
	if !slices.Equal(c.lines, want) {
		t.Fatalf("after the rewind the lines are %q, want %q", c.lines, want)
	}
}

// The incremental cache must not be able to disagree with a render that never
// used it. This walks the golden transcript one entry at a time — freezing
// blocks as they gain successors — and checks the result against a model that
// renders the same prefix cold.
func TestRenderHistoryLines_IncrementalMatchesCold(t *testing.T) {
	full := goldenTranscript()
	warm := goldenModel(t, 110)
	warm.transcript = nil
	warm.invalidateRenderCache()

	for i := range full {
		warm.transcript = full[:i+1]
		got := strings.Join(warm.renderHistoryLines(), "\n")

		cold := goldenModel(t, 110)
		cold.transcript = full[:i+1]
		cold.invalidateRenderCache()
		want := strings.Join(cold.renderHistoryLines(), "\n")

		if got != want {
			t.Fatalf("entry %d: the incremental render drifted from a cold one\n--- got\n%s\n--- want\n%s", i, got, want)
		}
	}
	if warm.cached.count == 0 {
		t.Fatal("the walk should have frozen at least one block")
	}
}

// The lines the highlight is lit over belong to the block cache, so the
// restyle works on a copy. A selection that wrote through would corrupt the
// frozen prefix — and the clipboard reads the same lines unstyled (§7a).
func TestApplySelectionHighlight_DoesNotWriteThroughToTheCache(t *testing.T) {
	c := &clip{}
	m := tallModel(t, c)
	m.viewport.SetLines(m.renderHistoryLines())
	before := append([]string(nil), m.cached.lines...)

	m.sel = selection{on: true, width: m.transcriptWidth(),
		anchor: selPoint{line: 1, col: 0}, end: selPoint{line: 3, col: 4}}
	lit := m.renderHistoryLines()

	if !slices.Equal(m.cached.lines, before) {
		t.Fatal("the highlight rewrote the cached lines")
	}
	if slices.Equal(lit, before) {
		t.Fatal("and it should have lit something")
	}
	// The clipboard reads the same lines unstyled (§7a): the highlight is the
	// last thing applied and the first thing dropped.
	if raw := m.renderHistoryRawLines(); !slices.Equal(raw, before) {
		t.Fatal("no selection styling may reach the raw render")
	}
}
