package chat

// The transcript search over the entries rather than over the pane
// (docs/interface/surfaces.md#reading-mode). Every test here is about a row
// nothing is drawing: the count that has to include it, the fold row that has
// to say so, and the key that has to reach it.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// foldedSearchModel is reading mode over the golden transcript, whose first
// step has finished — so it is drawn as its header alone, with a run of four
// read-only calls folded inside that. `context.go` is read exactly once, by a
// call two folds down, and is on nothing the pane is drawing.
func foldedSearchModel(t *testing.T) Model {
	t.Helper()
	m := frameModel(t, 110, 40)
	m.transcript = goldenTranscript()
	m.invalidateRenderCache()
	next, _ := m.enterFocusMode()
	rm := next.(Model)
	if !rm.headerOf(t, 1).Folded {
		t.Fatal("the fixture wants a step that has finished and folded")
	}
	return rm
}

// headerOf is the header the step titled by the entry at idx draws.
func (m Model) headerOf(t *testing.T, idx int) stepHeader {
	t.Helper()
	es := *m.entries()
	blk, ok := m.stepBlockAt(es, idx)
	if !ok {
		t.Fatalf("no step is titled by entry %d", idx)
	}
	return m.headerFor(blk, es)
}

// A count taken from the rendered lines is a count of the rows that happened
// to be open. This is the whole story: the one occurrence is two folds down
// and the session still says it is there.
func TestSearch_CountsWhatTheFoldsAreCovering(t *testing.T) {
	m := foldedSearchModel(t)
	if visible := m.searchTranscript("context.go"); visible != 0 {
		t.Fatalf("the fixture wants nothing on screen to match, the pane found %d", visible)
	}
	at, total := m.searchPosition()
	if total != 1 {
		t.Fatalf("the session holds one occurrence, the count says %d", total)
	}
	if at != 0 {
		t.Fatalf("the pointer is on none of them until a fold opens, got %d", at)
	}
	if got := m.searchNotice(); got != "0/1" {
		t.Fatalf("notice = %q, want the session's count and not the pane's", got)
	}
}

// The fold that is covering it says so on its own row, with the key that
// opens it — a fold states what it swallowed (invariant 4), and while a
// search is up what it swallowed includes the answer.
func TestSearch_TheFoldRowCountsAndOffersTheKey(t *testing.T) {
	m := foldedSearchModel(t)
	m.searchTranscript("context.go")
	h := m.headerOf(t, 1)
	if h.Matches != 1 {
		t.Fatalf("the header counts %d, want the one occurrence behind it", h.Matches)
	}
	row := h.View(m.transcriptWidth())
	if !strings.Contains(row, "1 match inside") {
		t.Fatalf("the header row says nothing about it:\n%s", row)
	}
	if !strings.Contains(row, "open to the first") {
		t.Fatalf("the count has no way to be reached from the row:\n%s", row)
	}
	// And a step covering nothing the query wants says nothing about it.
	if other := m.headerOf(t, 6); other.Matches != 0 {
		t.Fatalf("the other step counted %d", other.Matches)
	}
}

// [enter] on the fold row opens it and lands the cursor inside: a search that
// says one match and walks to none is the count lying twice.
func TestSearch_EnterOpensTheFoldOntoTheMatch(t *testing.T) {
	m := foldedSearchModel(t)
	m.focusIdx = 1
	m.refreshFocusView()
	m, _ = pressKey(t, m, slashKey)
	m = typeChars(t, m, "context.go")
	// Enter closes the query row; the next one is the fold's.
	m, _ = pressKey(t, m, enter)
	m, _ = pressKey(t, m, enter)

	es := *m.entries()
	if es[1].stepFold != foldSearch {
		t.Fatalf("the step's fold is %v, want the search's own", es[1].stepFold)
	}
	if m.headerOf(t, 1).Folded {
		t.Fatal("the step should be open")
	}
	// One level: what is under it now is the run of read-only calls, still
	// folded and still counting the occurrence it is standing in for.
	if m.focusIdx != 2 {
		t.Fatalf("the cursor is on %d, want the run that holds the match", m.focusIdx)
	}
	row := m.groupRowFor(es, slot{idx: 2, span: 4, group: true}).View(m.transcriptWidth())
	if !strings.Contains(row, "1 match inside") {
		t.Fatalf("the run says nothing about what it is covering:\n%s", row)
	}

	// And again: the run opens and the row itself is what the cursor is on.
	m, _ = pressKey(t, m, enter)
	if got := (*m.entries())[2].groupFold; got != foldSearch {
		t.Fatalf("the run's fold is %v, want the search's own", got)
	}
	if at, total := m.searchPosition(); at != 1 || total != 1 {
		t.Fatalf("position %d/%d, want the one occurrence reached", at, total)
	}
}

// A fold the search opened was opened to answer a question that is now over,
// so it goes back. A fold the reader opened is theirs.
func TestSearch_ClearingTheQueryPutsBackOnlyTheFoldsItOpened(t *testing.T) {
	m := foldedSearchModel(t)
	// The reader's own: the second step, folded by hand before any search.
	m.focusIdx = 6
	m.refreshFocusView()
	m, _ = pressKey(t, m, enter)
	if got := (*m.entries())[6].stepFold; got != foldClosed {
		t.Fatalf("the second step's fold is %v, want the reader's own", got)
	}

	m.focusIdx = 1
	m.refreshFocusView()
	m, _ = pressKey(t, m, slashKey)
	m = typeChars(t, m, "context.go")
	m, _ = pressKey(t, m, enter)
	m, _ = pressKey(t, m, enter)
	if got := (*m.entries())[1].stepFold; got != foldSearch {
		t.Fatalf("the first step's fold is %v, want the search's own", got)
	}

	// Esc clears the query, and the fold the search opened goes with it.
	m, _ = pressKey(t, m, slashKey)
	m, _ = pressKey(t, m, escK)
	es := *m.entries()
	if es[1].stepFold != foldAuto {
		t.Fatalf("the search's fold is %v, want it back at rest", es[1].stepFold)
	}
	if es[6].stepFold != foldClosed {
		t.Fatalf("the reader's fold is %v, want it left alone", es[6].stepFold)
	}
}

// Leaving the mode clears the search, and a fold left standing behind a
// cleared search would be a row opened by a question nothing on screen still
// asks.
func TestSearch_LeavingTheModePutsTheSearchsFoldsBack(t *testing.T) {
	m := foldedSearchModel(t)
	m.focusIdx = 1
	m.refreshFocusView()
	m, _ = pressKey(t, m, slashKey)
	m = typeChars(t, m, "context.go")
	m, _ = pressKey(t, m, enter)
	m, _ = pressKey(t, m, enter)

	m, _ = pressKey(t, m, tea.KeyPressMsg{Code: 'q', Text: "q"})
	if m.state == stateFocus {
		t.Fatal("[q] should leave reading mode")
	}
	if got := (*m.entries())[1].stepFold; got != foldAuto {
		t.Fatalf("the search's fold is %v, want it back at rest", got)
	}
}

// The rail names the surface and the query, so what the count is a count of
// is on the screen beside it.
func TestSearch_TheRailNamesTheSearchAndTheQuery(t *testing.T) {
	m := foldedSearchModel(t)
	if got := m.readingLabel(); !strings.HasPrefix(got, "READING") {
		t.Fatalf("rail = %q, want the reading label before any search", got)
	}
	m, _ = pressKey(t, m, slashKey)
	if got := m.readingLabel(); got != "SEARCH" {
		t.Fatalf("rail = %q, want the surface's name with nothing typed yet", got)
	}
	m = typeChars(t, m, "context.go")
	if got, want := m.readingLabel(), "SEARCH · context.go · 0/1"; got != want {
		t.Fatalf("rail = %q, want %q", got, want)
	}
}

// "No match" is only worth reading beside the size of what was read, and the
// row says where the answer could still be.
func TestSearch_TheEmptyResultSaysWhatWasSearched(t *testing.T) {
	m := foldedSearchModel(t)
	m, _ = pressKey(t, m, slashKey)
	m = typeChars(t, m, "round_test.go")
	if at, total := m.searchPosition(); at != 0 || total != 0 {
		t.Fatalf("position %d/%d, want nothing found anywhere", at, total)
	}
	lines := strings.Join(m.transcriptSearchLines(m.contentWidth()), "\n")
	rows := m.searchedRows()
	if rows != len(*m.entries()) {
		t.Fatalf("searched %d rows over a transcript of %d", rows, len(*m.entries()))
	}
	for _, want := range []string{
		"no match in this session",
		"rows searched, folds and pastes included",
		"shhh history --grep round_test.go",
	} {
		if !strings.Contains(lines, want) {
			t.Fatalf("the empty result never says %q:\n%s", want, lines)
		}
	}
}

// The census is kept between frames, and what it is a census of is on the
// record beside it: a row landing under a standing query is counted, and a
// fold opening moves an occurrence from covered to drawn without changing how
// many there are — neither needs the reader to touch the query.
func TestSearch_TheCountFollowsTheSessionUnderAStandingQuery(t *testing.T) {
	m := foldedSearchModel(t)
	m.searchTranscript("context.go")
	if _, total := m.searchPosition(); total != 1 {
		t.Fatalf("total %d before anything moved", total)
	}
	// Asked twice with nothing changed, which is what a frame does.
	if _, total := m.searchPosition(); total != 1 {
		t.Fatalf("total %d asked again", total)
	}
	m.transcript = append(m.transcript, entry{kind: entryTool, toolName: "read_file",
		toolArgs: `{"path":"internal/agent/context.go"}`, toolResult: "a"})
	m.invalidateRenderCache()
	m.refreshFocusView()
	if _, total := m.searchPosition(); total != 2 {
		t.Fatalf("total %d after a matching row landed", total)
	}
	(*m.entries())[1].stepFold = foldOpen
	m.invalidateRenderCache()
	m.refreshFocusView()
	if _, total := m.searchPosition(); total != 2 {
		t.Fatalf("total %d after the fold opened", total)
	}
}

// The mark is the picker's, verbatim: bold, and nothing else. A tint would
// spend a fourth background on a screen that has three, and underline and
// reverse were two treatments for one fact.
func TestSearch_TheMarkIsBoldAndNothingElse(t *testing.T) {
	if !matchStyle.GetBold() {
		t.Fatal("a match is bold")
	}
	if matchStyle.GetUnderline() || matchStyle.GetReverse() {
		t.Fatal("bold is the whole of the mark")
	}
}
