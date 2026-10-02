package chat

import (
	"fmt"
	"image/color"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// stepsModel builds a ready model whose transcript is a two-step turn: a
// batch that read and searched, then a batch that edited and broke a test.
func stepsModel(t *testing.T) Model {
	t.Helper()
	m := activityModel(t)
	m.transcript = []entry{
		{kind: entryUser, text: "fix the round limit"},
		{kind: entryAssistant, text: "Locate the round accounting"},
		{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"internal/agent/loop.go"}`,
			toolResult: "a\nb\nc", duration: 400 * time.Millisecond},
		{kind: entryTool, toolName: "search", toolArgs: `{"pattern":"ErrRoundLimit"}`,
			toolResult: searchHits, duration: 300 * time.Millisecond},
		{kind: entryAssistant, text: "Thread the sentinel through the loop"},
		{kind: entryTool, toolName: "edit_file", toolArgs: `{"path":"internal/agent/loop.go"}`,
			toolResult: "edited", duration: 1100 * time.Millisecond},
		{kind: entryCommand, text: "go test ./internal/agent/...",
			toolResult: "--- FAIL: TestRoundLimit", exitCode: 1, duration: 21400 * time.Millisecond},
	}
	m.invalidateRenderCache()
	return m
}

func stepLine(t *testing.T, view, title string) string {
	t.Helper()
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, title) {
			return line
		}
	}
	t.Fatalf("no header for %q in:\n%s", title, view)
	return ""
}

func TestSteps_ProseTitlesAGroupOfCalls(t *testing.T) {
	m := stepsModel(t)
	view := stripANSI(m.renderHistory())

	// Each batch of calls is one card: its receipt on the header, the prose
	// that titled it for a body.
	first := stepLine(t, view, "read internal/agent/loop.go")
	for _, want := range []string{"⚙", "searched ErrRoundLimit", "0.7s"} {
		if !strings.Contains(first, want) {
			t.Fatalf("the card's header should contain %q: %q", want, first)
		}
	}
	second := stepLine(t, view, "wrote internal/agent/loop.go")
	for _, want := range []string{"▎✗", "ran 1 command", "exit 1"} {
		if !strings.Contains(second, want) {
			t.Fatalf("the failed card's header should contain %q: %q", want, second)
		}
	}
	// The prose is the body, not a separate block above it.
	if strings.Count(view, "Locate the round accounting") != 1 {
		t.Fatalf("the title should render once, as the body:\n%s", view)
	}
	body := stepLine(t, view, "Locate the round accounting")
	if !strings.HasPrefix(body, strings.Repeat(" ", components.CardBodyIndent)+"Locate") {
		t.Fatalf("the body sits at the body column: %q", body)
	}
	// A finished card keeps its body and its footer; the failure is on it.
	if !strings.Contains(view, "--- FAIL: TestRoundLimit") {
		t.Fatalf("the failed card's footer is its failure:\n%s", view)
	}
}

// A run of calls nothing titled is a card too, with no body: the footer, or
// the padding row, follows its header.
func TestSteps_CallsNothingTitledAreOneCard(t *testing.T) {
	m := activityModel(t)
	m.transcript = []entry{
		{kind: entryUser, text: "look around"},
		{kind: entryTool, toolName: "search", toolArgs: `{"pattern":"x"}`, toolResult: "a\nb"},
		{kind: entryCommand, text: "go build ./...", toolResult: "ok"},
	}
	m.invalidateRenderCache()
	view := stripANSI(m.renderHistory())
	header := stepLine(t, view, "searched x")
	if !strings.Contains(header, "▎$ searched x · ran go build ./...") {
		t.Fatalf("the two calls are one card's receipt: %q", header)
	}
	if strings.Count(view, "go build") != 1 {
		t.Fatalf("the calls are stated once, on the card:\n%s", view)
	}
	if blocks := m.blocksOf(m.transcript); len(blocks) != 2 || blocks[1].step != nil {
		t.Fatalf("a card nothing titled is not a step of the outline: %+v", blocks)
	}
}

func TestSteps_ProseThatIsNotATitleKeepsItsBlock(t *testing.T) {
	m := activityModel(t)
	m.transcript = []entry{
		{kind: entryAssistant, text: "Here is what I found.\n\nAnd then some more."},
		{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"a.go"}`, toolResult: "x"},
	}
	m.invalidateRenderCache()
	view := stripANSI(m.renderHistory())
	if blocks := m.blocksOf(m.transcript); blocks[0].step != nil {
		t.Fatalf("prose of several lines is a passage, not a title:\n%s", view)
	}
	if !strings.Contains(view, "Here is what I found.") || !strings.Contains(view, "And then some more.") {
		t.Fatalf("the prose keeps its own block:\n%s", view)
	}
	if !strings.Contains(view, "⚙ read a.go") {
		t.Fatalf("the call after it is a card nothing titled:\n%s", view)
	}
}

func TestSteps_LiveStepRunsOpen(t *testing.T) {
	m := stepsModel(t)
	// Drop the failure so the last step would otherwise read as done.
	m.transcript[6].exitCode = 0
	m.transcript[6].toolResult = "ok"
	m.setTurnState(stateStreaming)
	m.invalidateRenderCache()

	view := stripANSI(m.renderHistory())
	live := stepLine(t, view, "ran go test")
	if !strings.Contains(live, "▎✎ ") || strings.ContainsAny(live, brailleFrames) {
		t.Fatalf("the live card keeps its own glyph, still: %q", live)
	}
	if !strings.Contains(view, "Thread the sentinel through the loop") {
		t.Fatalf("a running card shows its body:\n%s", view)
	}

	m.setTurnState(stateInput)
	m.invalidateRenderCache()
	view = stripANSI(m.renderHistory())
	done := stepLine(t, view, "ran go test")
	if !strings.Contains(done, "▎✎") {
		t.Fatalf("a finished card takes its own glyph: %q", done)
	}
	if !strings.Contains(view, "Thread the sentinel through the loop") {
		t.Fatalf("a finished card does not fold on its own:\n%s", view)
	}
}

func TestStepHeader_GridAndClipping(t *testing.T) {
	h := stepHeader{Ordinal: 1, Title: "Locate the round accounting",
		State: stepDone, Tools: 4, Duration: 6200 * time.Millisecond}
	for _, width := range []int{40, 60, 80, 120} {
		line := stripANSI(h.View(width))
		if got := lipgloss.Width(line); got != width {
			t.Fatalf("width %d: header should fill the grid, got %d: %q", width, got, line)
		}
		// The title starts in the verb column, so headers and rows share one
		// left edge.
		if !strings.HasPrefix(line, "▾ 1  Loc") {
			t.Fatalf("width %d: title should start in the verb column: %q", width, line)
		}
		if !strings.HasSuffix(line, "6.2s") {
			t.Fatalf("width %d: the duration owns the right edge: %q", width, line)
		}
		if !strings.Contains(line, "─") {
			t.Fatalf("width %d: the rule never disappears: %q", width, line)
		}
	}
	// Narrow enough and the title clips — the stats never do.
	narrow := stripANSI(h.View(34))
	if !strings.Contains(narrow, "…") || !strings.Contains(narrow, "4 tools") {
		t.Fatalf("the title clips before the stats: %q", narrow)
	}
}

// A duration that fills its whole field is the case the reserved gap is for:
// without it `4 tools` and `12345s` read as one word, which is the defect the
// activity row's own gap was added for, on the same grid.
func TestStepHeader_AFullDurationDoesNotAbutTheCount(t *testing.T) {
	h := stepHeader{Ordinal: 1, Title: "Locate the round accounting",
		State: stepDone, Tools: 4, Duration: 12345 * time.Second}
	for _, width := range []int{60, 80} {
		line := stripANSI(h.View(width))
		if !strings.HasSuffix(line, "4 tools 12345s") {
			t.Fatalf("width %d: the count and a six-column duration should be one column apart: %q", width, line)
		}
		if got := lipgloss.Width(line); got != width {
			t.Fatalf("width %d: header should fill the grid, got %d: %q", width, got, line)
		}
	}
}

func TestStepHeader_StatesAndCounts(t *testing.T) {
	cases := []struct {
		state stepState
		want  []string
	}{
		{stepDone, []string{"✓", "1 tool"}},
		{stepFailed, []string{"✗", "1 tool"}},
		{stepRunning, []string{"▸", "1 tool"}},
		{stepQueued, []string{"·", "queued", "—"}},
	}
	for _, tc := range cases {
		h := stepHeader{Ordinal: 3, Title: "Re-run the agent suite", State: tc.state, Tools: 1}
		line := stripANSI(h.View(72))
		for _, want := range tc.want {
			if !strings.Contains(line, want) {
				t.Fatalf("state %d should render %q: %q", tc.state, want, line)
			}
		}
	}
}

func TestSteps_SurviveResizeAndCaching(t *testing.T) {
	m := stepsModel(t)
	first := m.renderHistory()
	if second := m.renderHistory(); second != first {
		t.Fatal("a second render from the cache must match the first")
	}

	// A resize re-renders every card from the stored raw entries.
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 64, Height: 30})
	m = updated.(Model)
	narrow := m.renderHistory()
	cold := m
	cold.invalidateRenderCache()
	if narrow != cold.renderHistory() {
		t.Fatal("the resized render drifted from a cold one")
	}
	for _, line := range strings.Split(stripANSI(narrow), "\n") {
		if lipgloss.Width(line) > m.transcriptWidth() {
			t.Fatalf("a card line runs past the new width %d: %q", m.transcriptWidth(), line)
		}
	}

	// A row landing in the last card restates its header, cache or no cache,
	// at a width with room for the whole receipt.
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m = updated.(Model)
	_ = m.renderHistory()
	m.appendEntry(entry{kind: entryTool, toolName: "read_file",
		toolArgs: `{"path":"b.go"}`, toolResult: "x", duration: time.Second})
	grown := stepLine(t, stripANSI(m.renderHistory()), "wrote internal/agent/loop.go")
	if !strings.Contains(grown, "read b.go") && !strings.Contains(grown, "read 1 file") {
		t.Fatalf("the header should state the call that just landed: %q", grown)
	}
}

// TestSteps_BatchAfterANoticeJoinsTheStepAbove drives the order the rows
// actually arrive in: a titled step, a notice after its last call, and then
// the batch of calls the same step made with no prose over them. The notice
// is trimmed off the step, so for one frame the step is not the final block —
// and freezing it there drew the whole step again when the batch landed: one
// title and one ordinal, twice.
func TestSteps_BatchAfterANoticeJoinsTheStepAbove(t *testing.T) {
	m := activityModel(t)
	m.setTurnState(stateStreaming)
	arrive := func(e entry) {
		m.appendEntry(e)
		// The frame the row landed in, which is what freezes the blocks
		// above it.
		_ = m.renderHistory()
	}
	arrive(entry{kind: entryUser, text: "fix the round limit"})
	arrive(entry{kind: entryAssistant, text: "Locate the round accounting"})
	arrive(entry{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"loop.go"}`,
		toolResult: "a\nb", duration: 400 * time.Millisecond})
	arrive(entry{kind: entrySystem, text: "auto-allowed by policy"})
	arrive(entry{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"round.go"}`,
		toolResult: "a", duration: 300 * time.Millisecond})
	arrive(entry{kind: entryTool, toolName: "search", toolArgs: `{"pattern":"ErrRoundLimit"}`,
		toolResult: searchHits, duration: 200 * time.Millisecond})

	view := stripANSI(m.renderHistory())
	if got := strings.Count(view, "Locate the round accounting"); got != 1 {
		t.Fatalf("the step should be one card, got %d bodies:\n%s", got, view)
	}
	// The batch is the step's, so the one card's receipt counts it.
	header := stepLine(t, view, "searched ErrRoundLimit")
	if !strings.Contains(header, "read 2 files") {
		t.Fatalf("the one card should state the whole step: %q", header)
	}
	// And the frame is what the same entries render as from cold: a frozen
	// block that changed is exactly the difference between the two.
	fresh := activityModel(t)
	fresh.setTurnState(stateStreaming)
	fresh.transcript = m.transcript
	fresh.invalidateRenderCache()
	if want := stripANSI(fresh.renderHistory()); view != want {
		t.Fatalf("the cached frame drifted from a cold render:\n%s\nwant:\n%s", view, want)
	}
}

func TestSteps_FocusFoldsAndUnfolds(t *testing.T) {
	m := stepsModel(t)
	m.viewport.SetLines(m.renderHistoryLines())

	updated, _ := m.Update(readingChord())
	m = updated.(Model)
	if m.state != stateFocus {
		t.Fatalf("ctrl+o should enter focus mode, got state %d", m.state)
	}
	// A card is one target, and a card that is not open offers no rows.
	want := []int{1, 4}
	if got := m.expandableIndices(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("focus targets should be %v, got %v", want, got)
	}

	// Enter on a card opens it onto its calls in place.
	m.focusIdx = 1
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if !strings.Contains(stripANSI(m.renderHistory()), "⚙ ErrRoundLimit") {
		t.Fatalf("enter on a card should open it onto its calls:\n%s", stripANSI(m.renderHistory()))
	}
	if got := m.expandableIndices(); fmt.Sprint(got) != fmt.Sprint([]int{1, 2, 3, 4}) {
		t.Fatalf("an open card offers its rows too, got %v", got)
	}

	// Enter again closes it to the card, whose header still says what it
	// holds; there is no third depth.
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	view := stripANSI(m.renderHistory())
	if strings.Contains(view, "⚙ ErrRoundLimit") || !strings.Contains(view, "Locate the round accounting") {
		t.Fatalf("enter again should close the card to its header and body:\n%s", view)
	}
	if !strings.Contains(stepLine(t, view, "searched ErrRoundLimit"), "read internal/agent/loop.go") {
		t.Fatal("the closed card's header still states what it holds")
	}
	if got := m.expandableIndices(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("a closed card offers no rows, got %v", got)
	}
}

func TestSteps_FocusPointerOnHeader(t *testing.T) {
	m := stepsModel(t)
	m.enterSurface(stateFocus)
	m.focusIdx = 1
	content, start, count := m.renderFocusHistory()
	lines := strings.Split(stripANSI(content), "\n")
	if start < 0 || start+1 >= len(lines) {
		t.Fatalf("selected card line %d out of range (%d lines)", start, len(lines))
	}
	// The card opens on its padding row; the cursor is on the header under
	// it, in the card's own pointer column.
	if !strings.HasPrefix(lines[start+1], "❯▎") && !strings.HasPrefix(lines[start+1], "❯ ") {
		t.Fatalf("the pointer should sit on the selected card's header: %q", lines[start+1])
	}
	if !strings.Contains(lines[start+1], "read internal/agent/loop.go") {
		t.Fatalf("the pointer should sit on the selected card's header: %q", lines[start+1])
	}
	if count != 4 {
		t.Fatalf("a card with a body and no footer is four lines, got %d", count)
	}
}

// TestStepHeader_TonesFollowTheDesignSystem pins the colors the design
// system's StepGroup component assigns: a running step goes spin in the
// pointer and the duration and brightens its title, a finished one is body
// text, a queued one is dim throughout (invariant 1 — the glyph and the words
// carry the state too, so color is emphasis, never the only signal).
func TestStepHeader_TonesFollowTheDesignSystem(t *testing.T) {
	cases := []struct {
		state           stepState
		ptr, title, dur components.Token
	}{
		{stepRunning, components.Palette.Spin, components.Palette.Bright, components.Palette.Spin},
		{stepDone, components.Palette.Dim, components.Palette.Body, components.Palette.Dim},
		{stepFailed, components.Palette.Dim, components.Palette.Body, components.Palette.Dim},
		{stepQueued, components.Palette.Dim, components.Palette.Dim, components.Palette.Dim},
	}
	for _, tc := range cases {
		ptr, title, dur := stepHeader{State: tc.state}.tones()
		got := []color.Color{ptr.GetForeground(), title.GetForeground(), dur.GetForeground()}
		want := []color.Color{tc.ptr.Color(), tc.title.Color(), tc.dur.Color()}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("state %d tone %d: got %v, want %v", tc.state, i, got[i], want[i])
			}
		}
	}
	// The rule is the faint one, and the stats sit in dim beside their glyph.
	if sty.Step.Rule.GetForeground() != components.Palette.Dim.Color() ||
		sty.Step.Stats.GetForeground() != components.Palette.Dim.Color() {
		t.Fatal("the stretched rule and the tool count are dim")
	}
}
