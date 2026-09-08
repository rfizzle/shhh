package chat

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
)

// focusModel builds a ready model whose transcript holds text and expandable
// tool/command rows.
func focusModel(t *testing.T) Model {
	t.Helper()
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	var long strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&long, "result line %d\n", i)
	}
	m.appendEntry(entry{kind: entryUser, text: "look around"})
	m.appendEntry(entry{kind: entryTool, toolName: "search", toolArgs: `{"pattern":"x"}`, toolResult: strings.TrimRight(long.String(), "\n")})
	m.appendEntry(entry{kind: entryCommand, text: "go test ./...", toolResult: "ok", exitCode: 0})
	m.viewport.SetLines(m.renderHistoryLines())
	return m
}

func readingChord() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl} }

func TestFocusMode_EnterNavigateExpand(t *testing.T) {
	m := focusModel(t)

	updated, _ := m.Update(readingChord())
	m = updated.(Model)
	if m.state != stateFocus {
		t.Fatalf("ctrl+o should enter focus mode, got state %d", m.state)
	}
	// Selection starts on the most recent expandable row (the command).
	if m.focusIdx != 2 {
		t.Fatalf("focus should start on the last expandable row, got %d", m.focusIdx)
	}
	if !strings.Contains(m.View().Content, "❯") {
		t.Fatal("focus mode should render the selection pointer")
	}

	// k moves to the tool row; j moves back.
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	m = updated.(Model)
	if m.focusIdx != 1 {
		t.Fatalf("k should select the previous expandable row, got %d", m.focusIdx)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	m = updated.(Model)
	if m.focusIdx != 1 {
		t.Fatalf("k at the first expandable row should stay put, got %d", m.focusIdx)
	}

	// The long tool result is truncated until enter expands it in place.
	if strings.Contains(m.renderHistory(), "result line 19") {
		t.Fatal("tool output should be truncated before expansion")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if !m.transcript[1].expanded {
		t.Fatal("enter should expand the selected row")
	}
	if !strings.Contains(m.renderHistory(), "result line 19") {
		t.Fatal("expanded row should show the full output")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.transcript[1].expanded {
		t.Fatal("enter again should collapse the row")
	}
}

func TestFocusMode_EscReturnsToInputKeepingExpansion(t *testing.T) {
	m := focusModel(t)
	m.input.SetValue("draft in progress")

	updated, _ := m.Update(readingChord())
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.state != stateInput {
		t.Fatalf("esc should return to the input, got state %d", m.state)
	}
	// Esc never destroys: the draft and the expansion state both survive.
	if m.input.Value() != "draft in progress" {
		t.Fatalf("focus mode must not touch the input draft, got %q", m.input.Value())
	}
	if !m.transcript[1].expanded {
		t.Fatal("expansion state should survive leaving focus mode")
	}
	// One ❯ is left in the pane and it is the sent message's own mark; the
	// gutter's cursor, which stands in a column of its own, is gone.
	if n := strings.Count(m.renderHistory(), "❯"); n != 1 {
		t.Fatalf("the selection pointer should disappear outside focus mode, %d marks left", n)
	}
	if !strings.Contains(m.renderHistory(), "result line 19") {
		t.Fatal("the expanded row should stay expanded in the normal transcript")
	}
}

func TestFocusMode_NoExpandableRows(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	updated, _ = m.Update(readingChord())
	m = updated.(Model)
	if m.state != stateInput {
		t.Fatalf("without expandable rows ctrl+o should stay in input state, got %d", m.state)
	}
	last := m.transcript[len(m.transcript)-1]
	if last.kind != entrySystem || !strings.Contains(last.text, "Nothing to focus") {
		t.Fatalf("expected a notice about nothing to focus, got %+v", last)
	}
}

// Focus mode reads the transcript; it borrows the screen from a running turn
// rather than being refused while one is in flight.
func TestFocusMode_OpensOverAWorkingTurn(t *testing.T) {
	m := focusModel(t)
	m.state = stateStreaming
	updated, _ := m.Update(readingChord())
	m = updated.(Model)
	if m.state != stateFocus {
		t.Fatalf("ctrl+o should open focus mode while the agent works, got state %d", m.state)
	}
	if m.turnState() != stateStreaming || !m.working() {
		t.Fatalf("the turn must keep running underneath, got turn state %d", m.turnState())
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.state != stateStreaming {
		t.Fatalf("esc should hand the screen back to the running turn, got state %d", m.state)
	}
}

// Handing the transcript its cursor moves no text sideways. Every row the
// grid covers already holds its first two columns back for the fold mark and
// the cursor, so the cursor goes in the column it finds rather than in one
// the mode carves out of the pane — a mode that indented the page to say
// where one row was would move every line to mark one.
func TestReadingMode_DoesNotShiftTheTranscript(t *testing.T) {
	m := focusModel(t)
	before := gridRowsOf(stripANSI(m.renderHistory()))

	updated, _ := m.Update(readingChord())
	m = updated.(Model)
	if m.state != stateFocus {
		t.Fatalf("the reading chord should enter reading mode, got state %d", m.state)
	}
	after := gridRowsOf(stripANSI(m.renderHistory()))

	if len(before) == 0 || len(before) != len(after) {
		t.Fatalf("the transcript lost rows to the cursor: %d then %d", len(before), len(after))
	}
	for i, was := range before {
		now := after[i]
		// The row under the cursor takes the pointer in that same column; the
		// rest of it, and every other row, is where it was.
		if now == was || strings.TrimPrefix(now, "\u276f ") == strings.TrimPrefix(was, "  ") {
			continue
		}
		t.Fatalf("reading mode moved a row:\n  %q\n  %q", was, now)
	}
}

// gridRowsOf is the activity rows of a rendered transcript: the lines that
// carry a verb in the grid's verb column, which are the ones whose columns
// this is about.
func gridRowsOf(view string) []string {
	var rows []string
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, "search") || strings.Contains(l, "go test ./...") {
			rows = append(rows, strings.TrimRight(l, " "))
		}
	}
	return rows
}
