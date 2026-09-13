package chat

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/ui/components"
)

func readEntry(path string, d time.Duration) entry {
	return entry{kind: entryTool, toolName: "read_file",
		toolArgs: fmt.Sprintf(`{"path":%q}`, path), toolResult: "a\nb", duration: d}
}

func searchEntry(pattern string, d time.Duration) entry {
	return entry{kind: entryTool, toolName: "search",
		toolArgs: fmt.Sprintf(`{"pattern":%q}`, pattern), toolResult: "hit", duration: d}
}

// foldModel builds a one-step turn whose calls are six reads and two searches
// followed by an edit and a broken command — the verbosity mock, in entries.
func foldModel(t *testing.T) Model {
	t.Helper()
	m := activityModel(t)
	m.transcript = []entry{
		{kind: entryUser, text: "fix the round limit"},
		{kind: entryAssistant, text: "Thread the sentinel through the loop"},
		readEntry("internal/agent/loop.go", 400*time.Millisecond),
		readEntry("internal/agent/round.go", 200*time.Millisecond),
		readEntry("internal/agent/tool.go", 300*time.Millisecond),
		readEntry("internal/agent/mode.go", 500*time.Millisecond),
		readEntry("internal/agent/session.go", 600*time.Millisecond),
		readEntry("internal/agent/context.go", 400*time.Millisecond),
		searchEntry("ErrRoundLimit", 800*time.Millisecond),
		searchEntry("roundLimit", 700*time.Millisecond),
		{kind: entryTool, toolName: "edit_file", toolArgs: `{"path":"internal/agent/loop.go"}`,
			toolResult: "edited", duration: 1100 * time.Millisecond},
		{kind: entryCommand, text: "go test ./internal/agent/...",
			toolResult: "--- FAIL: TestRoundLimit", exitCode: 1, duration: 21400 * time.Millisecond},
	}
	m.invalidateRenderCache()
	return m
}

// The first foldable run of the fixture: entries 2–9, six reads and two
// searches taking 3.9s between them.
const (
	foldRunStart = 2
	foldRunLen   = 8
)

func TestFold_PredicateOnlyEverHidesChrome(t *testing.T) {
	m := activityModel(t)
	cases := []struct {
		name string
		e    entry
		want bool
	}{
		{"a read", readEntry("main.go", 0), true},
		{"a search", searchEntry("x", 0), true},
		{"a glob", entry{kind: entryTool, toolName: "glob", toolArgs: `{"pattern":"**/*.go"}`, toolResult: "a"}, true},
		{"an edit", entry{kind: entryTool, toolName: "edit_file", toolArgs: `{"path":"a.go"}`, toolResult: "ok"}, false},
		{"a write", entry{kind: entryTool, toolName: "write_file", toolArgs: `{"path":"a.go"}`, toolResult: "ok"}, false},
		{"a command", entry{kind: entryCommand, text: "go build ./...", toolResult: "ok"}, false},
		{"a diff", entry{kind: entryDiff, diff: &components.DiffView{}}, false},
		{"a failed read", entry{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"a.go"}`, toolResult: "error: no such file"}, false},
		{"a denied read", entry{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"a.go"}`, deniedBy: decidedByYou}, false},
		{"a running read", entry{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"a.go"}`, toolResult: pendingToolResult}, false},
		{"a sub-agent report", entry{kind: entryTool, toolName: "agent_report", toolArgs: `{"name":"writer-1"}`, toolResult: "done"}, false},
	}
	for _, tc := range cases {
		if got := m.foldableRow(tc.e); got != tc.want {
			t.Errorf("%s: foldable = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestFold_CountedLabelStatesWhatItSwallowed(t *testing.T) {
	m := foldModel(t)
	es := m.transcript

	if got := m.foldRun(es, foldRunStart, len(es)); got != foldRunLen {
		t.Fatalf("the run should stop at the edit, got %d rows", got)
	}
	sl := slot{idx: foldRunStart, span: foldRunLen, group: true}
	row := m.groupRowFor(es, sl)
	// Counted by kind, in the order the calls came (invariant 4).
	if row.Label != "6 reads · 2 searches" {
		t.Fatalf("group label should count both kinds, got %q", row.Label)
	}
	if row.Duration != "3.9s" {
		t.Fatalf("the group states what it cost, got %q", row.Duration)
	}
	view := stripANSI(row.View(80))
	for _, want := range []string{"⚙", "6 reads · 2 searches", components.GroupExpandKey, "3.9s"} {
		if !strings.Contains(view, want) {
			t.Fatalf("group row should contain %q: %q", want, view)
		}
	}

	// A single kind reads in the singular where it should.
	one := m.groupRowFor(es, slot{idx: foldRunStart, span: 3})
	if one.Label != "3 reads" {
		t.Fatalf("a single-kind group counts one way, got %q", one.Label)
	}
	if got := groupLabel([]entry{searchEntry("x", 0)}); got != "1 search" {
		t.Fatalf("singular label should read %q, got %q", "1 search", got)
	}
}

func TestFold_ShortRunsAreNotWorthFolding(t *testing.T) {
	m := activityModel(t)
	m.transcript = []entry{
		{kind: entryAssistant, text: "Look around"},
		readEntry("a.go", 0),
		searchEntry("x", 0),
		// The break keeps the step open, so its rows are on screen to count.
		{kind: entryCommand, text: "go build ./...", toolResult: "undefined: x", exitCode: 1},
	}
	m.invalidateRenderCache()
	view := stripANSI(m.renderHistory())
	for _, want := range []string{"a.go", "x"} {
		if !strings.Contains(view, want) {
			t.Fatalf("a pair of reads keeps its targets, %q missing:\n%s", want, view)
		}
	}
}

// renderAt renders the fixture at one verbosity. The fixture's step contains
// a failure, so it stays open at every level and the three levels are being
// compared on the same rows.
func renderAt(t *testing.T, v verbosity) string {
	t.Helper()
	m := foldModel(t)
	m.verbosity = v
	m.invalidateRenderCache()
	return stripANSI(m.renderHistory())
}

func TestFold_EachVerbosityLevelOfOneEntryList(t *testing.T) {
	// low: the step header and nothing under it.
	low := renderAt(t, verbosityLow)
	if !strings.Contains(low, "Thread the sentinel through the loop") {
		t.Fatalf("low keeps the step header:\n%s", low)
	}
	for _, gone := range []string{"internal/agent/round.go", "6 reads", "go test ./internal/agent/..."} {
		if strings.Contains(low, gone) {
			t.Fatalf("low shows headers only, found %q:\n%s", gone, low)
		}
	}

	// normal: reads fold into one counted row; the edit and the failure do not.
	normal := renderAt(t, verbosityNormal)
	if !strings.Contains(normal, "6 reads · 2 searches") {
		t.Fatalf("normal folds the read-only run:\n%s", normal)
	}
	if strings.Contains(normal, "internal/agent/round.go") {
		t.Fatalf("a folded read should not keep its own row:\n%s", normal)
	}
	for _, want := range []string{"edit", "go test ./internal/agent/...", "exit 1"} {
		if !strings.Contains(normal, want) {
			t.Fatalf("normal never folds %q:\n%s", want, normal)
		}
	}

	// high: every row, with its bounded detail body.
	high := renderAt(t, verbosityHigh)
	if strings.Contains(high, "6 reads · 2 searches") {
		t.Fatalf("high folds nothing:\n%s", high)
	}
	for _, want := range []string{"internal/agent/round.go", "internal/agent/context.go", "--- FAIL: TestRoundLimit"} {
		if !strings.Contains(high, want) {
			t.Fatalf("high shows %q expanded:\n%s", want, high)
		}
	}
}

func TestFold_EnterRestoresTheRowsInPlace(t *testing.T) {
	m := foldModel(t)
	m.viewport.SetLines(m.renderHistoryLines())

	updated, _ := m.Update(readingChord())
	m = updated.(Model)
	// The folded run offers one target — its group row — not the eight rows
	// inside it: header 1, the group at 2, the edit, the command.
	want := []int{1, foldRunStart, 10, 11}
	if got := m.expandableIndices(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("focus targets should be %v, got %v", want, got)
	}

	m.focusIdx = foldRunStart
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	view := stripANSI(m.renderHistory())
	if strings.Contains(view, "6 reads · 2 searches") {
		t.Fatalf("enter should expand the group:\n%s", view)
	}
	for _, want := range []string{"internal/agent/loop.go", "ErrRoundLimit", "roundLimit"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expanding restores %q in place:\n%s", want, view)
		}
	}
	if got := len(m.expandableIndices()); got != 1+foldRunLen+2 {
		t.Fatalf("every restored row is selectable, got %d targets", got)
	}

	// And enter folds it back — the fold is reversible from the keyboard.
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if view := stripANSI(m.renderHistory()); !strings.Contains(view, "6 reads · 2 searches") {
		t.Fatalf("enter should fold the group back:\n%s", view)
	}
}

// planlessModel is the turn this fold was missing: a question, then a run of
// reads and searches with no prose over them and no plan to number them, so
// there is no step for the run to fold under.
func planlessModel(t *testing.T) Model {
	t.Helper()
	m := activityModel(t)
	m.transcript = []entry{{kind: entryUser, text: "how is the round limit counted"}}
	for _, path := range []string{"loop.go", "round.go", "tool.go", "mode.go", "session.go"} {
		m.transcript = append(m.transcript, readEntry("internal/agent/"+path, 200*time.Millisecond))
	}
	m.transcript = append(m.transcript, searchEntry("ErrRoundLimit", 300*time.Millisecond))
	m.invalidateRenderCache()
	return m
}

// planlessRun is the fixture's run: entries 1–6, five reads and one search.
const (
	planlessStart = 1
	planlessLen   = 6
)

func TestFold_ARunWithNoStepOverItFoldsToo(t *testing.T) {
	m := planlessModel(t)
	view := stripANSI(m.renderHistory())
	if !strings.Contains(view, "5 reads · 1 search") {
		t.Fatalf("a run with no step over it should fold to one counted row:\n%s", view)
	}
	for _, gone := range []string{"round.go", "session.go", "ErrRoundLimit"} {
		if strings.Contains(view, gone) {
			t.Fatalf("a folded run keeps no row of its own, found %q:\n%s", gone, view)
		}
	}
	// The question above it is not a call and was never in the run.
	if !strings.Contains(view, "how is the round limit counted") {
		t.Fatalf("the fold only ever hides chrome:\n%s", view)
	}
	// One block holds the whole run, and it is nobody's step.
	blocks := m.blocksOf(m.transcript)
	if len(blocks) != 2 {
		t.Fatalf("the run tiles as one block, got %d blocks", len(blocks))
	}
	if blocks[1].step != nil {
		t.Fatal("the run has no step over it and must not be given one")
	}
	if blocks[1].start != planlessStart || blocks[1].end != planlessStart+planlessLen {
		t.Fatalf("the block should be the whole run, got [%d,%d)", blocks[1].start, blocks[1].end)
	}
}

func TestFold_ThePlanlessGroupTakesTheReadingCursor(t *testing.T) {
	m := planlessModel(t)
	m.viewport.SetLines(m.renderHistoryLines())

	updated, _ := m.Update(readingChord())
	m = updated.(Model)
	// One target: the group row, not the six rows behind it.
	if got := m.expandableIndices(); fmt.Sprint(got) != fmt.Sprint([]int{planlessStart}) {
		t.Fatalf("the folded run offers its group row and nothing else, got %v", got)
	}

	m.focusIdx = planlessStart
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	view := stripANSI(m.renderHistory())
	if strings.Contains(view, "5 reads · 1 search") {
		t.Fatalf("enter should expand the run:\n%s", view)
	}
	for _, want := range []string{"loop.go", "session.go", "ErrRoundLimit"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expanding restores %q in place:\n%s", want, view)
		}
	}
	if got := len(m.expandableIndices()); got != planlessLen {
		t.Fatalf("every restored row is selectable, got %d targets", got)
	}

	// And it folds back, which is what makes the group row a fold rather
	// than a threshold.
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if view := stripANSI(m.renderHistory()); !strings.Contains(view, "5 reads · 1 search") {
		t.Fatalf("enter should fold the run back:\n%s", view)
	}
}

func TestFold_ThePlanlessRunNeverHidesWhatIsRunning(t *testing.T) {
	m := planlessModel(t)
	// The call in flight is the last row of the run, and it has not come
	// back: the fold may not stand in for a row whose outcome nobody knows.
	live := readEntry("internal/agent/limit.go", 0)
	live.toolResult = pendingToolResult
	m.transcript = append(m.transcript, live)
	m.invalidateRenderCache()

	view := stripANSI(m.renderHistory())
	if !strings.Contains(view, "5 reads · 1 search") {
		t.Fatalf("the settled run still folds behind the live row:\n%s", view)
	}
	if !strings.Contains(view, "limit.go") {
		t.Fatalf("the call in flight keeps a row of its own:\n%s", view)
	}
	// It joins the count as soon as it lands.
	m.transcript[len(m.transcript)-1].toolResult = "package agent"
	m.invalidateRenderCache()
	if view := stripANSI(m.renderHistory()); !strings.Contains(view, "6 reads · 1 search") {
		t.Fatalf("a landed call joins the group it was standing beside:\n%s", view)
	}
}

func TestFold_ThePlanlessRunAnswersToTheVerbosityAndToYou(t *testing.T) {
	m := planlessModel(t)
	m.verbosity = verbosityHigh
	m.invalidateRenderCache()
	high := stripANSI(m.renderHistory())
	if strings.Contains(high, "5 reads · 1 search") {
		t.Fatalf("high folds nothing, step or no step:\n%s", high)
	}
	if !strings.Contains(high, "session.go") {
		t.Fatalf("high shows every row:\n%s", high)
	}

	// A fold the reader closed at high verbosity is still theirs.
	m.transcript[planlessStart].groupFold = foldClosed
	m.invalidateRenderCache()
	if view := stripANSI(m.renderHistory()); !strings.Contains(view, "5 reads · 1 search") {
		t.Fatalf("your own fold outranks the setting:\n%s", view)
	}

	// And one you opened stays open when the setting would have folded it.
	m.verbosity = verbosityNormal
	m.transcript[planlessStart].groupFold = foldOpen
	m.invalidateRenderCache()
	if view := stripANSI(m.renderHistory()); !strings.Contains(view, "session.go") {
		t.Fatalf("a run you opened stays open:\n%s", view)
	}
}

func TestFold_ThePlanlessGroupCountsWhatTheSearchFindsBehindIt(t *testing.T) {
	m := planlessModel(t)
	m.viewport.SetLines(m.renderHistoryLines())
	if visible := m.searchTranscript("session.go"); visible != 0 {
		t.Fatalf("the fixture wants the row behind the fold, the pane found %d", visible)
	}

	folds := m.transcriptFolds(m.transcript)
	if len(folds) != 1 || folds[0].idx != planlessStart {
		t.Fatalf("the run is the one fold on this transcript, got %+v", folds)
	}
	if n := m.searchMatchesIn(m.transcript, folds[0].start, folds[0].end); n != 1 {
		t.Fatalf("the fold should count the one row behind it holding the query, got %d", n)
	}
	view := stripANSI(m.renderHistory())
	if !strings.Contains(view, matchesInside(1)) {
		t.Fatalf("the group row states what the query found behind it:\n%s", view)
	}
	if !strings.Contains(view, "5 reads · 1 search") {
		t.Fatalf("the count leads and the verbs stay:\n%s", view)
	}
}

func TestFold_GroupRowSurvivesResize(t *testing.T) {
	m := foldModel(t)
	for _, w := range []int{60, 100, 60} {
		updated, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: 30})
		m = updated.(Model)
		view := stripANSI(m.renderHistory())
		// The count, not the whole label: at the narrowest rung the row
		// clips its target like every other row, and what has to survive is
		// that the run is still one row rather than six.
		if !strings.Contains(view, "6 reads") {
			t.Fatalf("width %d: the group re-renders from raw entries:\n%s", w, view)
		}
		for _, line := range strings.Split(view, "\n") {
			if got := len([]rune(line)); got > m.contentWidth() {
				t.Fatalf("width %d: line overflows to %d cells: %q", w, got, line)
			}
		}
	}
}
