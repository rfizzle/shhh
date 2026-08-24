package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// appliedEditModel drives a write_file call through approval so the applied
// edit lands in the transcript.
func appliedEditModel(t *testing.T) Model {
	t.Helper()
	m := gatedModel(t, nil, nil)
	path := filepath.Join(t.TempDir(), "code.go")

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_w", Name: "write_file",
			Arguments: fmt.Sprintf(`{"path":%q,"content":"package main\n"}`, path)},
	}})
	m = updated.(Model)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(Model)
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(approvedToolDoneMsg); ok {
			updated, _ = m.Update(msg)
			m = updated.(Model)
		}
	}
	return m
}

func TestAppliedEdit_LandsAsCollapsedDiffRow(t *testing.T) {
	m := appliedEditModel(t)

	var d *components.DiffView
	for _, e := range m.transcript {
		if e.kind == entryDiff {
			d = e.diff
		}
	}
	if d == nil {
		t.Fatal("applied edit should land as an entryDiff row")
	}
	if d.Mode != components.DiffCollapsed {
		t.Fatalf("applied edit should start collapsed, got mode %d", d.Mode)
	}
	row := d.RowView(200)
	for _, want := range []string{"✎ write", "+1 −0", "[enter] expand"} {
		if !strings.Contains(row, want) {
			t.Fatalf("collapsed row should contain %q:\n%s", want, row)
		}
	}
	// The model still received the plain tool result.
	last := m.Messages()[len(m.Messages())-1]
	if last.Role != provider.RoleTool || !strings.Contains(last.Content, "Created") {
		t.Fatalf("tool result should confirm the write, got %+v", last)
	}
}

func TestAppliedEdit_ErrorKeepsToolBlock(t *testing.T) {
	m := gatedModel(t, nil, nil)
	dir := t.TempDir()
	path := filepath.Join(dir, "code.go")
	if err := os.WriteFile(path, []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_e", Name: "edit_file",
			Arguments: fmt.Sprintf(`{"path":%q,"old_text":"alpha","new_text":"beta"}`, path)},
	}})
	m = updated.(Model)
	// Make the approved edit fail by removing the file after the preview.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(Model)
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(approvedToolDoneMsg); ok {
			updated, _ = m.Update(msg)
			m = updated.(Model)
		}
	}

	for _, e := range m.transcript {
		if e.kind == entryDiff {
			t.Fatal("a failed edit must not render as an applied-diff row")
		}
	}
	found := false
	for _, e := range m.transcript {
		if e.kind == entryTool && strings.HasPrefix(e.toolResult, "error") {
			found = true
		}
	}
	if !found {
		t.Fatal("failed edit should keep the plain tool block with the error")
	}
}

func TestFocusMode_DiffRowCyclesToFullScreen(t *testing.T) {
	m := appliedEditModel(t)
	m.state = stateInput // the resumed stream is irrelevant to focus mode

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	m = updated.(Model)
	if m.state != stateFocus {
		t.Fatalf("ctrl+e should enter focus mode, got state %d", m.state)
	}

	// Enter expands the collapsed diff row in place.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	d := m.transcript[m.focusIdx].diff
	if d == nil || d.Mode != components.DiffExpanded {
		t.Fatalf("enter should expand the diff row, got %+v", d)
	}

	// A second enter opens the full-screen view.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.state != stateDiffFull || m.fullDiff != d {
		t.Fatalf("second enter should open the diff full screen, got state %d", m.state)
	}
	view := m.View()
	if !strings.Contains(view, "j/k scroll") {
		t.Fatal("full-screen view should show its key hints")
	}

	// Esc steps back to the expanded row in focus mode — nothing is lost.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = updated.(Model)
	if m.state != stateFocus || m.fullDiff != nil {
		t.Fatalf("esc should return to focus mode, got state %d", m.state)
	}
	if d.Mode != components.DiffExpanded {
		t.Fatalf("diff row should be back to expanded, got mode %d", d.Mode)
	}
}

func TestApprovalFullDiff_RoundTrips(t *testing.T) {
	m := gatedModel(t, nil, nil)
	path := filepath.Join(t.TempDir(), "code.go")

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_w", Name: "write_file",
			Arguments: fmt.Sprintf(`{"path":%q,"content":"package main\n"}`, path)},
	}})
	m = updated.(Model)
	if !strings.Contains(m.View(), "d: full diff") {
		t.Fatal("edit approval should hint the full-diff key")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = updated.(Model)
	if m.state != stateDiffFull || m.fullDiff == nil {
		t.Fatalf("d should open the pending edit full screen, got state %d", m.state)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = updated.(Model)
	if m.state != stateConfirmRun || m.pendingApproval == nil {
		t.Fatalf("esc should return to the approval with it still pending, got state %d", m.state)
	}
}

func TestSessionDiff_Unavailable(t *testing.T) {
	m := gatedModel(t, nil, nil)
	m.state = stateInput
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model) // consume no-op enter on the empty input
	m.input.SetValue("/diff")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	found := false
	for _, e := range m.transcript {
		if e.kind == entrySystem && strings.Contains(e.text, "only available inside a git repository") {
			found = true
		}
	}
	if !found {
		t.Fatal("/diff without git wiring should say it is unavailable")
	}
}

func TestSessionDiff_OpensFullScreen(t *testing.T) {
	patch := "diff --git a/a.go b/a.go\n" +
		"--- a/a.go\n+++ b/a.go\n" +
		"@@ -1,1 +1,1 @@\n-old\n+new\n"
	m := gatedModel(t, nil, nil).WithSessionDiff(func() (string, error) { return patch, nil })
	m.state = stateInput
	m.input.SetValue("/diff")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.state != stateDiffFull || m.fullDiff == nil {
		t.Fatalf("/diff should open the session diff full screen, got state %d", m.state)
	}
	view := m.View()
	for _, want := range []string{"session diff", "a.go", "- 1  old", "+ 1  new"} {
		if !strings.Contains(view, want) {
			t.Fatalf("session diff view should contain %q:\n%s", want, view)
		}
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = updated.(Model)
	if m.state != stateInput || m.fullDiff != nil {
		t.Fatalf("esc should close the session diff, got state %d", m.state)
	}
}

func TestSessionDiff_Empty(t *testing.T) {
	m := gatedModel(t, nil, nil).WithSessionDiff(func() (string, error) { return "", nil })
	m.state = stateInput
	m.input.SetValue("/diff")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.state != stateInput {
		t.Fatalf("an empty session diff should stay in input state, got %d", m.state)
	}
	found := false
	for _, e := range m.transcript {
		if e.kind == entrySystem && strings.Contains(e.text, "No changes since the session started") {
			found = true
		}
	}
	if !found {
		t.Fatal("an empty session diff should note there are no changes")
	}
}
