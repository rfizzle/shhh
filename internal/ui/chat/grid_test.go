package chat

// The transcript's leading columns
// (docs/interface/surfaces.md#the-leading-columns): one fixture holding every
// kind of entry that has a left edge of its own, measured at every width the
// surface has a rung for, with and without the inspector rail beside the
// pane.

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// gridWidths are goldenWidths plus one terminal past the inspector's rung, so
// the same fixture is measured with the rail beside the pane and without it.
// The rail splits the surface at 130 and not below it
// (components.InspectorMinContentWidth), so the five widths are three
// single-pane terminals and two split ones.
var gridWidths = append(append([]int{}, goldenWidths...), 160)

// gridTranscript is one turn holding every entry the grid has a column rule
// for: the reader's own message, the model's prose, an activity row, the
// public status a silent run was asked for, the interval's check-in, a steer
// the reader can still take back, the tree reading, an error the session is
// reporting about itself, a reading of the round and the block the turn
// closes on.
func gridTranscript() []entry {
	return []entry{
		{kind: entryUser, turn: 1, text: "hold the round limit in one place and run the tests"},
		{kind: entryAssistant, turn: 1, text: "The constant is declared in three files, so the loop is the only one of them that can own it."},
		{kind: entryTool, turn: 1, toolName: "read_file", toolArgs: `{"path":"internal/agent/loop.go"}`,
			toolResult: "a\nb\nc", duration: 400 * time.Millisecond},
		{kind: entryCommand, turn: 1, text: "go test ./internal/agent/...",
			toolResult: "--- FAIL: TestRoundLimit", exitCode: 1, duration: 21400 * time.Millisecond},
		{kind: entryAssistant, turn: 1, checkpoint: true,
			text: "The objective is one home for the round limit. The evidence is the failure above, " +
				"which reads the constant from the loop and not from the caller. Next I will move " +
				"the declaration and run the suite again."},
		{kind: entrySystem, turn: 1,
			text: "Check-in — 36 rounds used. Taking stock, then carrying on."},
		{kind: entrySystem, turn: 1,
			text: "Steered — the reading says the edits have moved outside the round loop. Say what you meant if that is wrong.",
			intervened: &interveneRow{
				iv:  agent.Intervention{Kind: agent.InterveneSteer, Notice: "Steered", Reason: "edits outside the round loop"},
				row: "round 36 · steered · edits outside the round loop",
			}},
		{kind: entrySystem, turn: 1,
			text: "tree moved — 14 paths changed outside this session · 5,811 ignored"},
		{kind: entryError, turn: 1, text: "the notebook is read-only, so nothing was written down"},
		{kind: entrySummary, turn: 1, reading: &summaryReading{
			verdict: agent.SummaryVerdict{Round: 36, State: agent.SummaryOnTarget,
				Text: "The constant has one home and the caller no longer passes its own."},
		}},
		{kind: entryTurnClose, turn: 1, close: &components.TurnClose{
			Steps: 2, Tools: 6, Elapsed: "24.7s", Spend: "$0.14", Note: "round 36/50",
			Changes: &components.TurnChanges{
				Files: 1, Added: 12, Removed: 4,
				Keys: []components.TurnKey{rowOffer(keys.Row.Review, "review"), rowOffer(keys.Row.Undo, "undo turn")},
				Note: "all tracked in git",
			},
			Checks: &components.TurnChecks{Failed: true, Label: "go test ./internal/agent/...", Counts: "exit 1 · 21s"},
		}},
	}
}

// gridModel is that transcript on a live turn, mid-answer: the steer's offer
// stands only while the turn it interrupted is open (intervene.go), and the
// arriving reply is the last thing the pane draws.
func gridModel(t testing.TB, width, height int) Model {
	t.Helper()
	m := frameModel(t, width, height)
	m.transcript = gridTranscript()
	m.turnCount, m.turnOpen = 1, true
	m.state = stateStreaming
	m.streaming = "Re-running the suite now that the constant has one home."
	m.invalidateRenderCache()
	return m
}

// contentColumn is where an entry's own text begins, ignoring the marker
// gutter: -1 for a blank line, and the count of leading spaces otherwise.
func contentColumn(line string) int {
	plain := ansi.Strip(line)
	if strings.TrimSpace(plain) == "" {
		return -1
	}
	return len(plain) - len(strings.TrimLeft(plain, " "))
}

// TestTranscriptGrid_NoEntryStartsInsideTheMarkerGutter is the whole of the
// rule in one assertion: the gutter is for marks about an entry — the
// reader's `❯`, a step's fold caret, reading mode's cursor — and no entry's
// own words may start in it
// (docs/interface/surfaces.md#the-leading-columns).
func TestTranscriptGrid_NoEntryStartsInsideTheMarkerGutter(t *testing.T) {
	// The marks that are allowed to stand there, and the rule the reader's
	// message and a step header are drawn under: the mark takes the gutter
	// and the words start past it.
	marks := "❯▸▾"
	for _, width := range gridWidths {
		m := gridModel(t, width, 40)
		for i, line := range strings.Split(ansi.Strip(m.renderHistory()), "\n") {
			col := contentColumn(line)
			if col < 0 || col >= components.GridPointerWidth {
				continue
			}
			runes := []rune(line)
			if strings.ContainsRune(marks, runes[col]) {
				// A mark in the gutter is what the gutter is for; what
				// follows it has to clear the gutter all the same.
				after := col + 1
				for after < len(runes) && runes[after] == ' ' {
					after++
				}
				if after < components.GridPointerWidth {
					t.Errorf("w%d line %d: %q starts its words in the gutter", width, i, line)
				}
				continue
			}
			// The rule under a sent message runs the pane rather than the
			// document, because what it separates is the reader's sentence
			// from everything the session did about it (render.go).
			if strings.Trim(strings.TrimSpace(line), "─") == "" {
				continue
			}
			t.Errorf("w%d line %d begins in the marker gutter: %q", width, i, line)
		}
	}
}

// TestTranscriptGrid_EveryKindSharesOneLeftEdge measures the entries the
// story of a turn is told in — a public status note, a check-in, a steer, the
// tree reading, an error, an arriving reply and the mutation rail a turn
// closes on — and requires every one of them to begin in the same column at
// every width.
func TestTranscriptGrid_EveryKindSharesOneLeftEdge(t *testing.T) {
	// Each probe is a substring only one entry's first line carries, short
	// enough to survive the narrowest pane's clip.
	probes := map[string]string{
		"the public status note": "The objective is one home",
		"the check-in":           "Check-in —",
		"the steer":              "Steered —",
		"the tree reading":       "tree moved —",
		"the error":              "Error: the notebook",
		"the arriving reply":     "Re-running the suite",
		"the changed-files rail": "▎",
	}
	for _, width := range gridWidths {
		m := gridModel(t, width, 40)
		lines := strings.Split(ansi.Strip(m.renderHistory()), "\n")
		for what, probe := range probes {
			line, ok := lineWith(lines, probe)
			if !ok {
				t.Fatalf("w%d: the fixture no longer draws %s", width, what)
			}
			if col := contentColumn(line); col != components.GridPointerWidth {
				t.Errorf("w%d: %s starts at column %d, not %d: %q",
					width, what, col, components.GridPointerWidth, line)
			}
		}
		// The reader's own message is the one entry with a mark of its own.
		// It stands in the gutter, and the words start where every other
		// entry's do rather than beside the mark.
		line, ok := lineWith(lines, "hold the round limit")
		if !ok {
			t.Fatalf("w%d: the fixture no longer draws the reader's message", width)
		}
		want := "❯" + strings.Repeat(" ", components.GridPointerWidth-1) + "hold the round limit"
		if !strings.HasPrefix(line, want) {
			t.Errorf("w%d: the steer's mark and its words: %q", width, line)
		}
	}
}

// TestTranscriptGrid_WrappedProseReturnsToTheSameColumn is the other half of
// one edge: a notice long enough to wrap puts its continuation under its own
// first word rather than at the pane's edge.
func TestTranscriptGrid_WrappedProseReturnsToTheSameColumn(t *testing.T) {
	for _, width := range gridWidths {
		m := frameModel(t, width, 40)
		m.transcript = []entry{{kind: entrySystem, text: strings.Repeat(
			"a tree reading naming every path it found would be a notice nobody reads. ", 3)}}
		m.invalidateRenderCache()
		lines := strings.Split(strings.TrimRight(ansi.Strip(m.renderHistory()), "\n"), "\n")
		var drawn int
		for _, l := range lines {
			col := contentColumn(l)
			if col < 0 {
				continue
			}
			drawn++
			if col != components.GridPointerWidth {
				t.Errorf("w%d: a wrapped notice line starts at column %d: %q", width, col, l)
			}
		}
		if drawn < 2 {
			t.Fatalf("w%d: the fixture stopped wrapping — it is measuring nothing", width)
		}
	}
}

// lineWith is the first rendered line carrying a probe.
func lineWith(lines []string, probe string) (string, bool) {
	for _, l := range lines {
		if strings.Contains(ansi.Strip(l), probe) {
			return l, true
		}
	}
	return "", false
}
