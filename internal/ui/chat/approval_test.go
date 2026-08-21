package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/provider"
)

// writeFilePreview mimics a future write_file tool: diff of oldText against
// the content argument.
func writeFilePreview(oldText string) GatedPreviewFunc {
	return func(raw json.RawMessage) (GatedPreview, error) {
		var args struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return GatedPreview{}, err
		}
		return GatedPreview{Action: "write", Path: args.Path, OldText: oldText, NewText: args.Content}, nil
	}
}

func gatedModel(t *testing.T, executor ToolExecutor, gated map[string]GatedPreviewFunc) Model {
	t.Helper()
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "change it"},
	}
	m := New(msgs, mockStream).WithToolExecutor(executor).WithGatedTools(gated)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.state = stateStreaming
	return m
}

func TestGatedTool_DiffApprovalFlow(t *testing.T) {
	var executed []string
	executor := func(name string, args json.RawMessage) (string, error) {
		executed = append(executed, name)
		return "wrote 2 lines", nil
	}
	m := gatedModel(t, executor, map[string]GatedPreviewFunc{
		"write_file": writeFilePreview("line one\n"),
	})

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_w", Name: "write_file", Arguments: `{"path":"main.go","content":"line one\nline two\n"}`},
	}})
	m = updated.(Model)

	if m.state != stateConfirmRun {
		t.Fatalf("gated tool call should enter confirm state, got %d", m.state)
	}
	if len(executed) != 0 {
		t.Fatal("gated tool must not run before approval")
	}
	view := m.View()
	if !strings.Contains(view, "Assistant wants to write main.go") {
		t.Fatal("confirm prompt should describe the file action")
	}
	if !strings.Contains(view, "+line two") {
		t.Fatal("confirm prompt should show the added line as a diff")
	}
	if !strings.Contains(view, "@@") {
		t.Fatal("diff preview should include a hunk header")
	}
	if !strings.Contains(view, "[y/N]") {
		t.Fatal("confirm prompt should offer y/N")
	}

	// Approve.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(Model)
	if m.state != stateRunningCmd {
		t.Fatalf("expected running state while the tool executes, got %d", m.state)
	}
	var done approvedToolDoneMsg
	found := false
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(approvedToolDoneMsg); ok {
			done = msg
			found = true
		}
	}
	if !found {
		t.Fatal("expected approvedToolDoneMsg from the approval cmd")
	}
	if len(executed) != 1 || executed[0] != "write_file" {
		t.Fatalf("executor should have run write_file once, got %v", executed)
	}

	updated, restream := m.Update(done)
	m = updated.(Model)
	last := m.Messages()[len(m.Messages())-1]
	if last.Role != provider.RoleTool || last.ToolCallID != "call_w" || last.Content != "wrote 2 lines" {
		t.Fatalf("expected tool result for call_w, got %+v", last)
	}
	if m.state != stateStreaming || restream == nil {
		t.Fatal("stream should resume after the approved tool completes")
	}
}

func TestGatedTool_Declined(t *testing.T) {
	executor := func(name string, args json.RawMessage) (string, error) {
		t.Fatal("executor must not be called on decline")
		return "", nil
	}
	m := gatedModel(t, executor, map[string]GatedPreviewFunc{
		"write_file": writeFilePreview(""),
	})

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_w", Name: "write_file", Arguments: `{"path":"main.go","content":"x\n"}`},
	}})
	m = updated.(Model)

	updated, restream := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = updated.(Model)

	last := m.Messages()[len(m.Messages())-1]
	if last.Role != provider.RoleTool || last.ToolCallID != "call_w" || !strings.Contains(last.Content, "declined") {
		t.Fatalf("declined call should produce an error tool result, got %+v", last)
	}
	if m.state != stateStreaming || restream == nil {
		t.Fatal("stream should resume after decline so the model can react")
	}
	found := false
	for _, e := range m.transcript {
		if e.kind == entrySystem && strings.Contains(e.text, "Declined: write main.go") {
			found = true
		}
	}
	if !found {
		t.Fatal("transcript should note the declined action")
	}
}

func TestGatedTool_QueueMixedWithExec(t *testing.T) {
	executor := func(name string, args json.RawMessage) (string, error) { return "ok", nil }
	m := gatedModel(t, executor, map[string]GatedPreviewFunc{
		"write_file": writeFilePreview(""),
	})
	m = m.WithRunner(func(ctx context.Context, cmd string) (string, int) { return "ran", 0 })

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_x", Name: "execute_command", Arguments: `{"command":"echo hi"}`},
		{ID: "call_w", Name: "write_file", Arguments: `{"path":"a.txt","content":"x\n"}`},
	}})
	m = updated.(Model)

	// Exec approval first, with its command preview and safety-checked prompt.
	if m.state != stateConfirmRun || m.pendingApproval == nil || m.pendingApproval.kind != approvalExec {
		t.Fatalf("expected exec approval first, got state=%d", m.state)
	}
	if m.pendingRun != "echo hi" {
		t.Fatalf("expected pending command 'echo hi', got %q", m.pendingRun)
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(Model)
	var done cmdDoneMsg
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(cmdDoneMsg); ok {
			done = msg
		}
	}
	updated, _ = m.Update(done)
	m = updated.(Model)

	// The queue continues straight into the write_file diff approval.
	if m.state != stateConfirmRun || m.pendingApproval == nil || m.pendingApproval.kind != approvalDiff {
		t.Fatalf("expected diff approval after exec completes, got state=%d", m.state)
	}
	if !strings.Contains(m.View(), "write a.txt") {
		t.Fatal("second approval should preview the file write")
	}

	// Both tool results are recorded in order once the second is declined.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = updated.(Model)
	var ids []string
	for _, msg := range m.Messages() {
		if msg.Role == provider.RoleTool {
			ids = append(ids, msg.ToolCallID)
		}
	}
	if len(ids) != 2 || ids[0] != "call_x" || ids[1] != "call_w" {
		t.Fatalf("expected tool results for call_x then call_w, got %v", ids)
	}
}

func TestGatedTool_InvalidPreviewSkipped(t *testing.T) {
	executor := func(name string, args json.RawMessage) (string, error) { return "ok", nil }
	m := gatedModel(t, executor, map[string]GatedPreviewFunc{
		"write_file": func(raw json.RawMessage) (GatedPreview, error) {
			return GatedPreview{}, errors.New("path is required")
		},
	})

	updated, restream := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_w", Name: "write_file", Arguments: `{}`},
	}})
	m = updated.(Model)

	last := m.Messages()[len(m.Messages())-1]
	if last.Role != provider.RoleTool || !strings.Contains(last.Content, "invalid arguments") {
		t.Fatalf("invalid preview should produce an error tool result, got %+v", last)
	}
	if m.state != stateStreaming || restream == nil {
		t.Fatal("stream should resume after skipping the invalid call")
	}
}

func TestGatedTool_GenericPreview(t *testing.T) {
	executor := func(name string, args json.RawMessage) (string, error) { return "ok", nil }
	m := gatedModel(t, executor, map[string]GatedPreviewFunc{
		"my_tool": func(raw json.RawMessage) (GatedPreview, error) {
			return GatedPreview{Summary: "do the thing"}, nil
		},
	})

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_g", Name: "my_tool", Arguments: `{}`},
	}})
	m = updated.(Model)

	view := m.View()
	if !strings.Contains(view, "Assistant wants to use my_tool") {
		t.Fatal("generic approval should name the tool")
	}
	if !strings.Contains(view, "do the thing") {
		t.Fatal("generic approval should show the summary")
	}
	if !strings.Contains(view, "[y/N]") {
		t.Fatal("generic approval should offer y/N")
	}
}

func TestGatedTool_LargeDiffTruncatedAndPanelGrows(t *testing.T) {
	var content strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&content, "line %d\n", i)
	}
	executor := func(name string, args json.RawMessage) (string, error) { return "ok", nil }
	m := gatedModel(t, executor, map[string]GatedPreviewFunc{
		"write_file": writeFilePreview(""),
	})
	normalHeight := m.viewport.Height

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_w", Name: "write_file",
			Arguments: fmt.Sprintf(`{"path":"big.txt","content":%q}`, content.String())},
	}})
	m = updated.(Model)

	if !strings.Contains(m.View(), "more diff lines") {
		t.Fatal("large diff should be truncated with a notice")
	}
	// Panel is capped at 40% of terminal height (30 → 12 rows).
	if h := m.bottomPanelHeight(); h != 12 {
		t.Fatalf("expected confirm panel capped at 12 rows, got %d", h)
	}
	if m.viewport.Height != m.height-chromeHeight-12 {
		t.Fatalf("viewport should shrink for the diff preview, got %d", m.viewport.Height)
	}

	// Declining restores the normal layout.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = updated.(Model)
	if m.viewport.Height != normalHeight {
		t.Fatalf("viewport should restore after decline: got %d, want %d", m.viewport.Height, normalHeight)
	}
}
