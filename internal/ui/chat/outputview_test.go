package chat

// The three depths of a row's output (
// docs/interface/surfaces.md#the-activity-row): the bounded body with its
// counted tail, the wider in-place window, and the full screen — plus the
// command card's [d], which opens the same host on the card's own facts.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/provider"
)

// longOutputModel is a session whose last row is a command with n output
// lines, opened in reading mode with the cursor on it.
func longOutputModel(t *testing.T, n int) Model {
	t.Helper()
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	var out strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&out, "line %d\n", i)
	}
	m.appendEntry(entry{kind: entryUser, text: "run the suite"})
	m.appendEntry(entry{kind: entryCommand, text: "go test ./...",
		toolResult: strings.TrimRight(out.String(), "\n"), exitCode: 1})
	m.viewport.SetLines(m.renderHistoryLines())
	updated, _ = m.Update(readingChord())
	return updated.(Model)
}

func TestOutputDepths_BoundedWindowFullScreen(t *testing.T) {
	m := longOutputModel(t, 200)
	es := *m.entries()
	if es[m.focusIdx].kind != entryCommand {
		t.Fatalf("the cursor should land on the command row, got kind %d", es[m.focusIdx].kind)
	}

	// The failed row auto-expands to the 8-line cap, and the cap counts what
	// it swallowed.
	body, _, _ := m.renderFocusHistory()
	plain := ansi.Strip(body)
	if !strings.Contains(plain, "line 8") || strings.Contains(plain, "line 9") {
		t.Fatalf("the bounded body should stop at the cap:\n%s", plain)
	}
	if !strings.Contains(plain, "… 192 more lines") {
		t.Fatalf("the cap should count what it swallowed:\n%s", plain)
	}

	// [enter] opens the in-place window: cap × 4, still counted.
	updated, _ := m.updateFocus(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	body, _, _ = m.renderFocusHistory()
	plain = ansi.Strip(body)
	if !strings.Contains(plain, "line 32") || strings.Contains(plain, "line 33") {
		t.Fatalf("the in-place window should show cap × 4 lines:\n%s", plain)
	}
	if !strings.Contains(plain, "… 168 more lines") {
		t.Fatalf("the window should count what it swallowed:\n%s", plain)
	}

	// [enter] again takes the whole output full screen, scrollable.
	updated, _ = m.updateFocus(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.state != stateOutputFull || m.fullOutput == nil {
		t.Fatalf("the depth past the window is the full screen, got state %d", m.state)
	}
	if len(m.fullOutput.Lines) != 200 {
		t.Fatalf("the full screen holds the whole output, got %d lines", len(m.fullOutput.Lines))
	}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "$ go test ./...") || !strings.Contains(view, "200 lines") {
		t.Fatalf("the full screen should carry the row's own words and count:\n%s", view)
	}
	before := m.fullOutput.Offset
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = updated.(Model)
	if m.fullOutput.Offset <= before {
		t.Fatalf("j should scroll the full screen, offset %d → %d", before, m.fullOutput.Offset)
	}

	// Esc returns to reading mode with the row still open in place.
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.state != stateFocus {
		t.Fatalf("esc should return to reading mode, got state %d", m.state)
	}
	if es := *m.entries(); !es[m.focusIdx].expanded {
		t.Fatal("esc never destroys: the row stays open in place")
	}
}

// [enter] on the full screen is the depth past it: the row folds on the way
// out, mirroring the diff viewer's cycle.
func TestOutputDepths_EnterOnFullScreenCollapses(t *testing.T) {
	m := longOutputModel(t, 200)
	updated, _ := m.updateFocus(tea.KeyPressMsg{Code: tea.KeyEnter})
	updated, _ = updated.(Model).updateFocus(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.state != stateOutputFull {
		t.Fatalf("expected the full screen, got state %d", m.state)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.state != stateFocus {
		t.Fatalf("enter should close the full screen, got state %d", m.state)
	}
	if es := *m.entries(); es[m.focusIdx].expanded {
		t.Fatal("the depth past full screen is closed")
	}
}

// An output the in-place window already shows whole skips the full-screen
// step: the next press closes rather than changing nothing.
func TestOutputDepths_ShortOutputSkipsFullScreen(t *testing.T) {
	m := longOutputModel(t, 20)
	updated, _ := m.updateFocus(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if es := *m.entries(); !es[m.focusIdx].expanded {
		t.Fatal("the first press opens the window")
	}
	updated, _ = m.updateFocus(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.state != stateFocus {
		t.Fatalf("a window already showing everything has no full step, got state %d", m.state)
	}
	if es := *m.entries(); es[m.focusIdx].expanded {
		t.Fatal("the press past a whole window closes the row")
	}
}

// A read row's file content opens through the same depths as a command's
// output.
func TestOutputDepths_ReadRowsOpenTheSameWay(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	var out strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&out, "content %d\n", i)
	}
	m.appendEntry(entry{kind: entryTool, toolName: "read_file",
		toolArgs: `{"path":"internal/agent/loop.go"}`, toolResult: strings.TrimRight(out.String(), "\n")})
	m.viewport.SetLines(m.renderHistoryLines())
	updated, _ = m.Update(readingChord())
	m = updated.(Model)

	updated, _ = m.updateFocus(tea.KeyPressMsg{Code: tea.KeyEnter})
	updated, _ = updated.(Model).updateFocus(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.state != stateOutputFull || m.fullOutput == nil {
		t.Fatalf("a read row past its window wants the screen too, got state %d", m.state)
	}
	if !strings.Contains(m.fullOutput.Title, "read") ||
		!strings.Contains(m.fullOutput.Title, "internal/agent/loop.go") {
		t.Fatalf("the view is titled by the row's own words, got %q", m.fullOutput.Title)
	}
}

// Every arrival resets the scroll, including the ones that never touch the
// approval queue — a !bang confirm inherits nothing from the card the reader
// panned before it.
func TestApprovalCard_ScrollResetsOnABangConfirm(t *testing.T) {
	m := runCapableModel("no blocks here")
	m.cardScroll, m.cardPan = 7, 25
	m = sendText(t, m, "!ls")
	if m.state != stateConfirmRun {
		t.Fatalf("expected the /run confirm, got state %d", m.state)
	}
	if m.cardScroll != 0 || m.cardPan != 0 {
		t.Fatalf("a new card starts unscrolled, got %d/%d", m.cardScroll, m.cardPan)
	}
}

// The scroll describes one card's body, so it resets when the card changes.
func TestApprovalCard_ScrollResetsWhenTheCardChanges(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "run it"},
	}
	m := New(msgs, mockStream).WithRunner(func(_ context.Context, _ string) (string, int) { return "", 0 })
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(Model)
	m.state = stateStreaming
	m.cardScroll, m.cardPan = 7, 15
	m = runExecApproval(t, m)
	if m.cardScroll != 0 || m.cardPan != 0 {
		t.Fatalf("a new card starts unscrolled, got %d/%d", m.cardScroll, m.cardPan)
	}
}

// The command card's [d] opens the card's own facts on the same host, and
// esc returns to the card with the decision still pending.
func TestApprovalCard_FullViewForCommands(t *testing.T) {
	var ran []string
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "run it"},
	}
	m := New(msgs, mockStream).WithRunner(func(_ context.Context, cmd string) (string, int) {
		ran = append(ran, cmd)
		return "", 0
	})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(Model)
	m.state = stateStreaming
	m = runExecApproval(t, m)

	updated, _ = m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	m = updated.(Model)
	if m.state != stateOutputFull || m.fullOutput == nil {
		t.Fatalf("[d] on a command card should open the full view, got state %d", m.state)
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "echo hi") {
		t.Fatalf("the full view should carry the command text:\n%s", view)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.state != stateConfirmRun {
		t.Fatalf("esc should return to the card with the decision pending, got state %d", m.state)
	}
	if len(ran) != 0 {
		t.Fatal("looking at the full view decides nothing")
	}
}
