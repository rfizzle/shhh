package chat

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
	m.viewport.SetContent(m.renderHistory())
	return m
}

func ctrlE() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyCtrlE} }

func TestFocusMode_EnterNavigateExpand(t *testing.T) {
	m := focusModel(t)

	updated, _ := m.Update(ctrlE())
	m = updated.(Model)
	if m.state != stateFocus {
		t.Fatalf("ctrl+e should enter focus mode, got state %d", m.state)
	}
	// Selection starts on the most recent expandable row (the command).
	if m.focusIdx != 2 {
		t.Fatalf("focus should start on the last expandable row, got %d", m.focusIdx)
	}
	if !strings.Contains(m.View(), "❯") {
		t.Fatal("focus mode should render the selection pointer")
	}

	// k moves to the tool row; j moves back.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = updated.(Model)
	if m.focusIdx != 1 {
		t.Fatalf("k should select the previous expandable row, got %d", m.focusIdx)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = updated.(Model)
	if m.focusIdx != 1 {
		t.Fatalf("k at the first expandable row should stay put, got %d", m.focusIdx)
	}

	// The long tool result is truncated until enter expands it in place.
	if strings.Contains(m.renderHistory(), "result line 19") {
		t.Fatal("tool output should be truncated before expansion")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if !m.transcript[1].expanded {
		t.Fatal("enter should expand the selected row")
	}
	if !strings.Contains(m.renderHistory(), "result line 19") {
		t.Fatal("expanded row should show the full output")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.transcript[1].expanded {
		t.Fatal("enter again should collapse the row")
	}
}

func TestFocusMode_EscReturnsToInputKeepingExpansion(t *testing.T) {
	m := focusModel(t)
	m.input.SetValue("draft in progress")

	updated, _ := m.Update(ctrlE())
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
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
	if strings.Contains(m.renderHistory(), "❯") {
		t.Fatal("the selection pointer should disappear outside focus mode")
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

	updated, _ = m.Update(ctrlE())
	m = updated.(Model)
	if m.state != stateInput {
		t.Fatalf("without expandable rows ctrl+e should stay in input state, got %d", m.state)
	}
	last := m.transcript[len(m.transcript)-1]
	if last.kind != entrySystem || !strings.Contains(last.text, "Nothing to focus") {
		t.Fatalf("expected a notice about nothing to focus, got %+v", last)
	}
}

// Focus mode reads the transcript; it borrows the screen from a running turn
// rather than being refused while one is in flight (S-087).
func TestFocusMode_OpensOverAWorkingTurn(t *testing.T) {
	m := focusModel(t)
	m.state = stateStreaming
	updated, _ := m.Update(ctrlE())
	m = updated.(Model)
	if m.state != stateFocus {
		t.Fatalf("ctrl+e should open focus mode while the agent works, got state %d", m.state)
	}
	if m.turnState() != stateStreaming || !m.working() {
		t.Fatalf("the turn must keep running underneath, got turn state %d", m.turnState())
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.state != stateStreaming {
		t.Fatalf("esc should hand the screen back to the running turn, got state %d", m.state)
	}
}
