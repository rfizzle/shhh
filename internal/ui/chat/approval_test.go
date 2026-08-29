package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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
	view := m.View().Content
	if !strings.Contains(view, "Assistant wants to write main.go") {
		t.Fatal("confirm prompt should describe the file action")
	}
	// Diff previews carry line numbers (
	// docs/interface/surfaces.md#the-approval-card).
	if !strings.Contains(view, "+ 2  line two") {
		t.Fatal("confirm prompt should show the added line as a diff")
	}
	if !strings.Contains(view, "@@") {
		t.Fatal("diff preview should include a hunk header")
	}
	// The card landed on a draft nobody was typing into, so it holds the
	// keyboard and offers the two answers; [a] waits behind the handover.
	if !strings.Contains(view, "[y/N]") {
		t.Fatal("a card holding the keyboard by arrival should offer y/N")
	}
	if !strings.Contains(view, "[ctrl+space] for [a]/[d]") {
		t.Fatal("the card should say what the handover still buys")
	}

	// Approve.
	m = handover(t, m)
	if !strings.Contains(m.View().Content, "[y/n/a]") {
		t.Fatal("after the handover the card should offer y/n/a")
	}
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
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

	m = handover(t, m)
	updated, restream := m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
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
		if e.kind == entryTool && e.toolName == "write_file" && e.deniedBy == decidedByYou {
			found = true
		}
	}
	if !found {
		t.Fatal("transcript should note the declined action")
	}
	view := stripANSI(m.renderHistory())
	if !strings.Contains(view, "⊘") || !strings.Contains(view, "denied · you") {
		t.Fatalf("a decline renders as a ⊘ row naming you as the decider:\n%s", view)
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

	m = handover(t, m)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
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
	if !strings.Contains(m.View().Content, "write a.txt") {
		t.Fatal("second approval should preview the file write")
	}

	// Both tool results are recorded in order once the second is declined.
	// Esc would hand the keyboard back and leave it waiting;
	// [n] is how a decision is denied.
	m = handover(t, m)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
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

	view := m.View().Content
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

// The real write_file/edit_file tools are intercepted natively: no
// registration needed, diff preview from disk, execution via ExecuteMutating
// rather than the session's auto-run executor.
func TestMutatingTool_WriteApprovedThroughQueue(t *testing.T) {
	executor := func(name string, args json.RawMessage) (string, error) {
		t.Errorf("session executor must not run mutating tool %s", name)
		return "", nil
	}
	m := gatedModel(t, executor, nil)
	path := filepath.Join(t.TempDir(), "hello.txt")

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_w", Name: "write_file",
			Arguments: fmt.Sprintf(`{"path":%q,"content":"hello\n"}`, path)},
	}})
	m = updated.(Model)

	if m.state != stateConfirmRun || m.pendingApproval == nil || m.pendingApproval.kind != approvalDiff {
		t.Fatalf("write_file should enter diff approval, got state=%d", m.state)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file must not exist before approval")
	}
	view := m.View().Content
	if !strings.Contains(view, "Assistant wants to write") || !strings.Contains(view, "+ 1  hello") {
		t.Fatal("confirm prompt should show the write action and diff")
	}

	m = handover(t, m)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)
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
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "hello\n" {
		t.Fatalf("approved write should create the file: content=%q err=%v", data, err)
	}

	updated, _ = m.Update(done)
	m = updated.(Model)
	last := m.Messages()[len(m.Messages())-1]
	if last.Role != provider.RoleTool || last.ToolCallID != "call_w" || !strings.Contains(last.Content, "Created") {
		t.Fatalf("tool result should confirm the write, got %+v", last)
	}
}

func TestMutatingTool_EditDeclinedLeavesFileUntouched(t *testing.T) {
	m := gatedModel(t, nil, nil)
	path := filepath.Join(t.TempDir(), "code.go")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_e", Name: "edit_file",
			Arguments: fmt.Sprintf(`{"path":%q,"old_text":"beta","new_text":"delta"}`, path)},
	}})
	m = updated.(Model)

	if m.state != stateConfirmRun || m.pendingApproval == nil || m.pendingApproval.kind != approvalDiff {
		t.Fatalf("edit_file should enter diff approval, got state=%d", m.state)
	}
	view := m.View().Content
	if !strings.Contains(view, "- 2  beta") || !strings.Contains(view, "+ 2  delta") {
		t.Fatal("confirm prompt should diff the edit")
	}

	m = handover(t, m)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = updated.(Model)
	data, _ := os.ReadFile(path)
	if string(data) != "alpha\nbeta\n" {
		t.Fatal("declined edit must leave the file untouched")
	}
	last := m.Messages()[len(m.Messages())-1]
	if last.Role != provider.RoleTool || last.ToolCallID != "call_e" || !strings.Contains(last.Content, "declined") {
		t.Fatalf("declined edit should produce an error tool result, got %+v", last)
	}
}

func TestMutatingTool_InvalidEditSkippedWithError(t *testing.T) {
	m := gatedModel(t, nil, nil)
	path := filepath.Join(t.TempDir(), "code.go")
	if err := os.WriteFile(path, []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	updated, restream := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_e", Name: "edit_file",
			Arguments: fmt.Sprintf(`{"path":%q,"old_text":"missing","new_text":"x"}`, path)},
	}})
	m = updated.(Model)

	last := m.Messages()[len(m.Messages())-1]
	if last.Role != provider.RoleTool || !strings.Contains(last.Content, "not found") {
		t.Fatalf("no-match edit should produce an error tool result, got %+v", last)
	}
	if m.state != stateStreaming || restream == nil {
		t.Fatal("stream should resume so the model can correct the edit")
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
	// The layout's answer, not the viewport's field: the fixture sets the
	// streaming state directly, and nothing has re-synced the pane to the row
	// the turn's live tail is using.
	normalHeight := m.viewportHeight()

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_w", Name: "write_file",
			Arguments: fmt.Sprintf(`{"path":"big.txt","content":%q}`, content.String())},
	}})
	m = updated.(Model)

	if !strings.Contains(m.View().Content, "more diff lines") {
		t.Fatal("large diff should be truncated with a notice")
	}
	// The card takes the panel once the decision holds the keyboard;
	// until then it rides above a live frame and the panel is the input's.
	m = handover(t, m)
	// The card is capped at 40% of terminal height (30 → 12 rows); the rail
	// that names the keyboard's owner is the row above it.
	if h := m.bottomPanelHeight(); h != 13 {
		t.Fatalf("expected confirm panel capped at 12 rows plus its rail, got %d", h)
	}
	if m.viewport.Height() != m.height-(headerHeight+dividerHeight+bottomChromeHeight)-13 {
		t.Fatalf("viewport should shrink for the diff preview, got %d", m.viewport.Height())
	}

	// Declining restores the normal layout.
	m = handover(t, m)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = updated.(Model)
	if m.viewport.Height() != normalHeight {
		t.Fatalf("viewport should restore after decline: got %d, want %d", m.viewport.Height(), normalHeight)
	}
}

func TestMutatingTool_HookAppendsDiagnosticsToResult(t *testing.T) {
	m := gatedModel(t, nil, nil)
	m = m.WithMutationHook(func(name string, args json.RawMessage, result string) string {
		if name != "write_file" {
			t.Errorf("hook should see the mutating tool name, got %q", name)
		}
		return result + "\n\nDiagnostics (fake) for hello.go:\nhello.go:1:1 error: boom"
	})
	path := filepath.Join(t.TempDir(), "hello.go")

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_w", Name: "write_file",
			Arguments: fmt.Sprintf(`{"path":%q,"content":"package main\n"}`, path)},
	}})
	m = updated.(Model)
	m = handover(t, m)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)

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
	updated, _ = m.Update(done)
	m = updated.(Model)
	last := m.Messages()[len(m.Messages())-1]
	if last.Role != provider.RoleTool || !strings.Contains(last.Content, "Created") ||
		!strings.Contains(last.Content, "Diagnostics (fake)") {
		t.Fatalf("tool result should carry write confirmation plus hook diagnostics, got %+v", last)
	}
}
