package chat

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
			toolResult: "x\ny", duration: 300 * time.Millisecond},
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

	// Each batch of calls folds under an ordinal, a state glyph, a rule, a
	// tool count and a duration (§13).
	first := stepLine(t, view, "Locate the round accounting")
	for _, want := range []string{"1 ", "✓", "2 tools", "0.7s", "─"} {
		if !strings.Contains(first, want) {
			t.Fatalf("step header should contain %q: %q", want, first)
		}
	}
	second := stepLine(t, view, "Thread the sentinel")
	for _, want := range []string{"2 ", "✗", "2 tools", "22s"} {
		if !strings.Contains(second, want) {
			t.Fatalf("failed step header should contain %q: %q", want, second)
		}
	}
	// The prose is the title, not a separate block above it.
	if strings.Count(view, "Locate the round accounting") != 1 {
		t.Fatalf("the title should render once, as the header:\n%s", view)
	}
	// A completed step collapses to its header; the broken one stays open.
	if strings.Contains(view, "internal/agent/loop.go") && strings.Contains(view, "ErrRoundLimit") {
		t.Fatalf("a finished step should collapse to its header:\n%s", view)
	}
	if !strings.Contains(view, "exit 1") {
		t.Fatalf("a step containing a failure stays open:\n%s", view)
	}
}

func TestSteps_FlatWithoutTitles(t *testing.T) {
	m := activityModel(t)
	m.transcript = []entry{
		{kind: entryUser, text: "look around"},
		{kind: entryTool, toolName: "search", toolArgs: `{"pattern":"x"}`, toolResult: "a\nb"},
		{kind: entryCommand, text: "go build ./...", toolResult: "ok"},
	}
	m.invalidateRenderCache()

	w := m.contentWidth()
	var want string
	for i, e := range m.transcript {
		if i > 0 {
			want += separatorBefore(m.transcript[i-1], e)
		}
		want += m.renderEntry(e, w)
	}
	if got := m.renderHistory(); got != want {
		t.Fatalf("a turn with no steps must render exactly as a flat list:\n%q\nwant:\n%q", got, want)
	}
	if strings.Contains(stripANSI(m.renderHistory()), "─") {
		t.Fatal("no step chrome without steps")
	}
}

func TestSteps_ProseThatIsNotATitleKeepsItsBlock(t *testing.T) {
	long := strings.Repeat("a very long explanation ", 10)
	cases := map[string]string{
		"multi-line": "Here is what I found.\n\nAnd then some more.",
		"too long":   long,
	}
	for name, prose := range cases {
		t.Run(name, func(t *testing.T) {
			m := activityModel(t)
			m.transcript = []entry{
				{kind: entryAssistant, text: prose},
				{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"a.go"}`, toolResult: "x"},
			}
			m.invalidateRenderCache()
			view := stripANSI(m.renderHistory())
			if strings.Contains(view, "─") {
				t.Fatalf("prose that is an explanation must not become a title:\n%s", view)
			}
			if !strings.Contains(view, "Assistant") {
				t.Fatalf("the prose keeps its own block:\n%s", view)
			}
		})
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
	live := stepLine(t, view, "Thread the sentinel")
	if !strings.Contains(live, "▾") || !strings.Contains(live, "▸") {
		t.Fatalf("the live step is running and open: %q", live)
	}
	if !strings.Contains(view, "go test ./internal/agent/...") {
		t.Fatalf("a running step shows its rows:\n%s", view)
	}

	m.setTurnState(stateInput)
	m.invalidateRenderCache()
	done := stepLine(t, stripANSI(m.renderHistory()), "Thread the sentinel")
	if !strings.Contains(done, "▸") || !strings.Contains(done, "✓") {
		t.Fatalf("a finished step folds to its header: %q", done)
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
		// left edge (§6a).
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

	// A resize re-renders every step from the stored raw entries.
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 64, Height: 30})
	m = updated.(Model)
	narrow := stripANSI(m.renderHistory())
	line := stepLine(t, narrow, "Locate the round accounting")
	if got := lipgloss.Width(line); got != m.contentWidth() {
		t.Fatalf("resized header should fill the new width %d, got %d: %q", m.contentWidth(), got, line)
	}

	// A row landing in the open step restates its header, cache or no cache.
	m.appendEntry(entry{kind: entryTool, toolName: "read_file",
		toolArgs: `{"path":"b.go"}`, toolResult: "x", duration: time.Second})
	grown := stepLine(t, stripANSI(m.renderHistory()), "Thread the sentinel")
	if !strings.Contains(grown, "3 tools") {
		t.Fatalf("the header should count the row that just landed: %q", grown)
	}
}

func TestSteps_FocusFoldsAndUnfolds(t *testing.T) {
	m := stepsModel(t)
	m.viewport.SetContent(m.renderHistory())

	updated, _ := m.Update(ctrlE())
	m = updated.(Model)
	if m.state != stateFocus {
		t.Fatalf("ctrl+e should enter focus mode, got state %d", m.state)
	}
	// Headers are selection targets alongside rows: header 1 (folded, so no
	// rows), header 2 and its two rows.
	want := []int{1, 4, 5, 6}
	if got := m.expandableIndices(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("focus targets should be %v, got %v", want, got)
	}

	// Enter on a folded header unfolds the group in place.
	m.focusIdx = 1
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if !strings.Contains(stripANSI(m.renderHistory()), "ErrRoundLimit") {
		t.Fatalf("enter on a folded header should unfold it:\n%s", stripANSI(m.renderHistory()))
	}
	if got := m.expandableIndices(); fmt.Sprint(got) != fmt.Sprint([]int{1, 2, 3, 4, 5, 6}) {
		t.Fatalf("an unfolded step offers its rows too, got %v", got)
	}

	// And enter again folds it back, hiding nothing the header does not say.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	view := stripANSI(m.renderHistory())
	if strings.Contains(view, "ErrRoundLimit") {
		t.Fatalf("enter should fold the group back:\n%s", view)
	}
	if !strings.Contains(stepLine(t, view, "Locate the round accounting"), "2 tools") {
		t.Fatal("a folded header still states what it swallowed")
	}
}

func TestSteps_FocusPointerOnHeader(t *testing.T) {
	m := stepsModel(t)
	m.enterSurface(stateFocus)
	m.focusIdx = 1
	content, start, count := m.renderFocusHistory()
	lines := strings.Split(stripANSI(content), "\n")
	if start < 0 || start >= len(lines) {
		t.Fatalf("selected header line %d out of range (%d lines)", start, len(lines))
	}
	if !strings.Contains(lines[start], "❯") || !strings.Contains(lines[start], "Locate the round accounting") {
		t.Fatalf("the pointer should sit on the selected header: %q", lines[start])
	}
	if count != 1 {
		t.Fatalf("a folded header is one line, got %d", count)
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
		ptr, title, dur lipgloss.Color
	}{
		{stepRunning, components.Palette.Spin, components.Palette.Bright, components.Palette.Spin},
		{stepDone, components.Palette.Dim, components.Palette.Body, components.Palette.Dim},
		{stepFailed, components.Palette.Dim, components.Palette.Body, components.Palette.Dim},
		{stepQueued, components.Palette.Dim, components.Palette.Dim, components.Palette.Dim},
	}
	for _, tc := range cases {
		ptr, title, dur := stepHeader{State: tc.state}.tones()
		got := []lipgloss.TerminalColor{ptr.GetForeground(), title.GetForeground(), dur.GetForeground()}
		want := []lipgloss.TerminalColor{tc.ptr, tc.title, tc.dur}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("state %d tone %d: got %v, want %v", tc.state, i, got[i], want[i])
			}
		}
	}
	// The rule is the faint one, and the stats sit in dim beside their glyph.
	if stepRuleStyle.GetForeground() != components.Palette.Dim ||
		stepStatsStyle.GetForeground() != components.Palette.Dim {
		t.Fatal("the stretched rule and the tool count are dim")
	}
}
