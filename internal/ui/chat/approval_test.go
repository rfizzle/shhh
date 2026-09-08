package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/diff"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
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

// allowedOnRow is what the transcript says allowed the act aimed at target,
// read off the act's own row. There is no notice above the row to read it
// from: an auto-approval and the act it approved are one row, so what
// answered the decision is a field of the row like its outcome and its
// duration. An edit keeps it on the diff viewer, every other act on its
// activity row, which is why both are asked.
func allowedOnRow(t *testing.T, m Model, target string) string {
	t.Helper()
	for _, e := range m.transcript {
		if e.kind == entryDiff {
			if e.diff != nil && e.diff.Path == target {
				return e.diff.Allowed
			}
			continue
		}
		if row := m.activityRowFor(e); row.Target == target {
			return row.Allowed
		}
	}
	t.Fatalf("no row for %q in the transcript", target)
	return ""
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

// tallPanel gives a card room for its whole body. A card is bounded to two
// fifths of the terminal, so on thirty rows a short diff folds behind the
// counted tail — which is the fold working. A test that means to read what
// the card states asks for the rows rather than asserting about the fold.
func tallPanel(t *testing.T, m Model) Model {
	t.Helper()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 48})
	return updated.(Model)
}

func TestGatedTool_DiffApprovalFlow(t *testing.T) {
	var executed []string
	executor := func(name string, args json.RawMessage) (string, error) {
		executed = append(executed, name)
		return "wrote 2 lines", nil
	}
	m := tallPanel(t, gatedModel(t, executor, map[string]GatedPreviewFunc{
		"write_file": writeFilePreview("line one\n"),
	}))

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
	if !strings.Contains(ansi.Strip(view), "✎ write main.go") {
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
	for _, want := range []string{"[y] apply the change", "[Y] ", "[n] deny", "[N] "} {
		if !strings.Contains(ansi.Strip(view), want) {
			t.Fatalf("a card holding the keyboard by arrival offers %q:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "[ctrl+space] for [a]/[d]") {
		t.Fatal("the card should say what the handover still buys")
	}

	// Approve.
	m = handover(t, m)
	if !strings.Contains(ansi.Strip(m.View().Content), "[a] allow edits in") {
		t.Fatal("after the handover the card offers the session grant too")
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

// A call refused because the file moved is the one the person can explain,
// so it gets a row that names the file instead of the line every malformed
// call gets. The model still receives its own sentence, unchanged.
func TestGatedTool_StalePreviewNamesTheFile(t *testing.T) {
	executor := func(name string, args json.RawMessage) (string, error) { return "ok", nil }
	path := filepath.Join(t.TempDir(), "loop.go")
	m := gatedModel(t, executor, map[string]GatedPreviewFunc{
		"write_file": func(raw json.RawMessage) (GatedPreview, error) {
			return GatedPreview{}, tools.StaleError{Path: path}
		},
	})

	updated, restream := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_w", Name: "write_file", Arguments: `{"path":"loop.go"}`},
	}})
	m = updated.(Model)

	last := m.Messages()[len(m.Messages())-1]
	if last.Role != provider.RoleTool || !strings.Contains(last.Content, "read_file it again") {
		t.Fatalf("the model must still get its own sentence, got %+v", last)
	}
	if m.state != stateStreaming || restream == nil {
		t.Fatal("stream should resume after skipping the stale call")
	}
	row := m.transcript[len(m.transcript)-1]
	if row.kind != entryTool || row.text != path {
		t.Errorf("the refusal is the call's own row, about the file:\n got %v %q\nwant %v %q",
			row.kind, row.text, entryTool, path)
	}
	if row.skipped != tools.StaleReason {
		t.Errorf("row reason:\n got %q\nwant %q", row.skipped, tools.StaleReason)
	}
	if row.skipped == skippedArgsReason {
		t.Error("a stale refusal must not read as a malformed call")
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
	if !strings.Contains(ansi.Strip(view), "⚙ use my_tool") {
		t.Fatal("generic approval should name the tool")
	}
	if !strings.Contains(view, "do the thing") {
		t.Fatal("generic approval should show the summary")
	}
	if !strings.Contains(ansi.Strip(view), "[y] allow it") {
		t.Fatal("generic approval should offer its answer under the key")
	}
}

// A preview that named the act and wrote a one-line form of it puts that line
// on the card's first row instead of the tool that carries it: the reader is
// answering `GET pkg.go.dev/context`, not `use web_fetch`
// (docs/interface/surfaces.md#the-approval-card).
func TestGatedTool_APreviewThatNamesTheActLeadsWithIt(t *testing.T) {
	executor := func(name string, args json.RawMessage) (string, error) { return "ok", nil }
	m := gatedModel(t, executor, map[string]GatedPreviewFunc{
		"my_tool": func(raw json.RawMessage) (GatedPreview, error) {
			return GatedPreview{Action: "fetch", Summary: "GET pkg.go.dev/context"}, nil
		},
	})

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_g", Name: "my_tool", Arguments: `{}`},
	}})
	view := ansi.Strip(updated.(Model).View().Content)
	if !strings.Contains(view, "⚙ GET pkg.go.dev/context") {
		t.Fatalf("the act should lead the card:\n%s", view)
	}
	if strings.Contains(view, "use my_tool") {
		t.Fatalf("the mechanism should not be the card's first row:\n%s", view)
	}
	// And the line is drawn once: the summary row under it would be the same
	// sentence twice.
	if n := strings.Count(view, "GET pkg.go.dev/context"); n != 1 {
		t.Fatalf("the act is stated %d times:\n%s", n, view)
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
	if !strings.Contains(ansi.Strip(view), "✎ write") || !strings.Contains(view, "+ 1  hello") {
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
	m := tallPanel(t, gatedModel(t, nil, nil))
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

	if !strings.Contains(m.View().Content, "more lines · shift+↓") {
		t.Fatal("large diff should end in a counted, scrollable tail")
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

// Three changes to one file are one decision, one diff and one record. The
// call exists to stop the session paying a round and a card per pair, so
// what is asserted is the count: a second card here would mean the batching
// bought nothing.
func TestMutatingTool_SeveralEditsAreOneCardAndOneRecord(t *testing.T) {
	m := gatedModel(t, nil, nil)
	m.turnCount = 1
	path := filepath.Join(t.TempDir(), "loop.go")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal(map[string]any{
		"path": path,
		"edits": []map[string]string{
			{"old_text": "alpha", "new_text": "one"},
			{"old_text": "beta", "new_text": "two"},
			{"old_text": "gamma", "new_text": "three"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_e", Name: "edit_file", Arguments: string(args)},
	}})
	m = updated.(Model)
	if m.state != stateConfirmRun || m.pendingApproval == nil || m.pendingApproval.kind != approvalDiff {
		t.Fatalf("a three-edit call should arm one diff approval, got state=%d", m.state)
	}
	// One card, and all three changes in the diff behind it: a decision put
	// up for one of them would be an approval of something else. The hunks
	// are read rather than the render, which clips a body taller than the
	// panel bound and would make this an assertion about terminal height.
	var changed []string
	for _, h := range m.pendingApproval.hunks {
		for _, l := range h.Lines {
			if l.Kind != diff.Context {
				changed = append(changed, l.Text)
			}
		}
	}
	if want := []string{"alpha", "beta", "gamma", "one", "two", "three"}; !slices.Equal(changed, want) {
		t.Errorf("the card should diff every edit, got %v want %v", changed, want)
	}

	m = handover(t, m)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(approvedToolDoneMsg); ok {
			updated, _ = m.Update(msg)
			m = updated.(Model)
		}
	}
	if data, _ := os.ReadFile(path); string(data) != "one\ntwo\nthree\n" {
		t.Fatalf("the approved call should apply every edit, got %q", data)
	}
	if m.pendingApproval != nil {
		t.Fatal("one call is one decision; nothing should still be pending")
	}
	var diffs int
	for _, e := range m.transcript {
		if e.kind == entryDiff {
			diffs++
		}
	}
	if diffs != 1 {
		t.Fatalf("the applied call should leave one diff row, got %d", diffs)
	}
	turn, ok := m.changes.Turn(1)
	if !ok {
		t.Fatal("the applied call should be recorded")
	}
	if turn.Files() != 1 {
		t.Fatalf("one file changed, so one record, got %d", turn.Files())
	}
	r, _ := turn.Record(path)
	if r.Before != "alpha\nbeta\ngamma\n" || r.After != "one\ntwo\nthree\n" {
		t.Fatalf("the record should span the whole call, got before=%q after=%q", r.Before, r.After)
	}
}

// A call the shared validator refuses never becomes a card. Preview and act
// run the same check, so the alternative would be a person approving a change
// that is then refused — an approval given for nothing.
func TestMutatingTool_OverlappingEditsNeverReachACard(t *testing.T) {
	m := gatedModel(t, nil, nil)
	path := filepath.Join(t.TempDir(), "loop.go")
	if err := os.WriteFile(path, []byte("func Handle() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal(map[string]any{
		"path": path,
		"edits": []map[string]string{
			{"old_text": "func Handle() {}", "new_text": "func Serve() {}"},
			{"old_text": "Handle", "new_text": "Serve"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_e", Name: "edit_file", Arguments: string(args)},
	}})
	m = updated.(Model)
	if m.pendingApproval != nil {
		t.Fatal("a call the write would refuse must not be put to the user")
	}
	last := m.Messages()[len(m.Messages())-1]
	if last.Role != provider.RoleTool || !strings.Contains(last.Content, "overlap") {
		t.Fatalf("the model should be told the edits overlap, got %+v", last)
	}
	if data, _ := os.ReadFile(path); string(data) != "func Handle() {}\n" {
		t.Fatal("a refused call must leave the file untouched")
	}
}

// The dry run at the command card
// (docs/interface/surfaces.md#the-approval-card).

// pendingExec puts one assistant command in front of the reader, leaving the
// keyboard wherever it already was.
func pendingExec(t *testing.T, m Model, command string) Model {
	t.Helper()
	args, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		t.Fatalf("marshalling the call arguments: %v", err)
	}
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_x", Name: tools.ExecCommandName, Arguments: string(args)},
	}})
	return updated.(Model)
}

// execApproval is that call with the keyboard handed to the card, the way
// runExecApproval does it for the one command it is written around.
func execApproval(t *testing.T, m Model, command string) Model {
	t.Helper()
	return handover(t, pendingExec(t, m, command))
}

// offersDryRun reports whether the card is advertising the dry-run key.
func offersDryRun(card *components.ApprovalCard) bool {
	return slices.ContainsFunc(card.ExtraHints, func(o components.KeyOffer) bool {
		return o.Key == keys.Bracket(keys.Decision.DryRun)
	})
}

func drainDryRun(t *testing.T, cmd tea.Cmd) dryRunDoneMsg {
	t.Helper()
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(dryRunDoneMsg); ok {
			return msg
		}
	}
	t.Fatal("expected a dryRunDoneMsg from the dry-run key")
	return dryRunDoneMsg{}
}

func TestApprovalCard_DryRunRunsTheDerivedFormAndDecidesNothing(t *testing.T) {
	var bare, contained []string
	m := containedModel(t, &bare, &contained, "contained: bwrap")
	m = execApproval(t, m, "rsync --delete src/ dst/")
	if card := m.approvalCard(); !offersDryRun(card) {
		t.Fatalf("a command with a harmless form should offer the key:\n%s", m.View().Content)
	}

	updated, cmd := m.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	m = updated.(Model)
	done := drainDryRun(t, cmd)

	// The derived form ran, through the containment the real command would
	// have run in, and the real command did not run at all.
	if want := []string{"rsync --dry-run --delete src/ dst/"}; !slices.Equal(contained, want) {
		t.Fatalf("the dry run should go through the contained runner, got contained=%v bare=%v", contained, bare)
	}
	if len(bare) != 0 {
		t.Fatalf("the plain runner must not see an assistant command, got %v", bare)
	}
	// The decision is exactly where it was: still asked, still unanswered.
	if m.state != stateConfirmRun || m.pendingApproval == nil {
		t.Fatalf("the card should still be waiting, got state %d pending %v", m.state, m.pendingApproval)
	}
	if !strings.Contains(m.View().Content, "dry run — running") {
		t.Fatalf("the card should say the dry run is running:\n%s", m.View().Content)
	}

	updated, _ = m.Update(done)
	m = updated.(Model)
	if m.state != stateOutputFull {
		t.Fatalf("the output should open on the screen, got state %d", m.state)
	}
	view := m.View().Content
	if !strings.Contains(view, "dry run — rsync --dry-run --delete") || !strings.Contains(view, "contained") {
		t.Fatalf("the screen should carry the derived command and what it printed:\n%s", view)
	}
	// And esc comes back to the decision, which nothing has answered: no tool
	// result reached the conversation and the call is still pending.
	m = press(t, m, "esc")
	if m.state != stateConfirmRun || m.pendingApproval == nil {
		t.Fatalf("esc should come back to the waiting decision, got state %d pending %v", m.state, m.pendingApproval)
	}
	for _, msg := range m.Messages() {
		if msg.Role == provider.RoleTool {
			t.Fatalf("a dry run must never answer the call: %+v", msg)
		}
	}
	// The run left a row of its own, and the row says its output stayed out
	// of the conversation.
	last := m.transcript[len(m.transcript)-1]
	if last.kind != entryCommand || last.text != "rsync --dry-run --delete src/ dst/" || !last.localRun {
		t.Fatalf("the dry run should leave a local command row, got %+v", last)
	}
}

func TestApprovalCard_DryRunNotOfferedWithoutAHarmlessForm(t *testing.T) {
	var bare, contained []string
	m := containedModel(t, &bare, &contained, "contained: bwrap")
	m = execApproval(t, m, "rm -rf build")
	if card := m.approvalCard(); offersDryRun(card) {
		t.Fatalf("rm has no harmless form and the card must not offer one:\n%s", m.View().Content)
	}
	// And the key is not secretly live: nothing runs, and the decision stands.
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	m = updated.(Model)
	if cmd != nil {
		t.Fatal("the key should start nothing where there is no harmless form")
	}
	if len(contained) != 0 || len(bare) != 0 {
		t.Fatalf("nothing should have run, got contained=%v bare=%v", contained, bare)
	}
	if m.state != stateConfirmRun || m.pendingApproval == nil {
		t.Fatalf("the card should still be waiting, got state %d pending %v", m.state, m.pendingApproval)
	}
}

// runOnce drives one assistant command through the approval flow and returns
// the tool result the model was handed.
func runOnce(t *testing.T, m Model, command string) (Model, string) {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"command": command})
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "c" + command, Name: tools.ExecCommandName, Arguments: string(args)},
	}})
	m = updated.(Model)
	if m.pendingApproval == nil || m.pendingApproval.kind != approvalExec {
		t.Fatalf("expected an exec approval, got state=%d", m.state)
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
	msgs := m.agent.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == provider.RoleTool {
			return m, msgs[i].Content
		}
	}
	t.Fatal("the command produced no tool result")
	return m, ""
}

// The failure the detector was written for, on the surface a person is
// watching: a command the session runs again and again for the same answer.
// It is dispatched by the model rather than by the tool executor, so the
// detector reaches it only because the session hands the model its own.
func TestApproval_ARepeatedCommandSaysSo(t *testing.T) {
	m := gatedModel(t, nil, nil).
		WithRunner(func(context.Context, string) (string, int) { return "FAIL\tinternal/calc", 1 }).
		WithRepeats(agent.NewRepeatDetector())

	m, first := runOnce(t, m, "go test ./internal/calc")
	if agent.IsRepeatNotice(first) {
		t.Fatalf("the first run is not a repeat: %q", first)
	}
	m.state = stateStreaming
	_, second := runOnce(t, m, "go test ./internal/calc")
	if !agent.IsRepeatNotice(second) {
		t.Fatalf("the same command returning the same output should say so, got %q", second)
	}
	if !strings.Contains(second, "FAIL\tinternal/calc") {
		t.Fatalf("the output itself must survive the notice, got %q", second)
	}
}

// And the reader's own command is not the agent's: telling somebody standing
// at the keyboard that they have run this before is telling them what they
// just did.
func TestApproval_ALocalRunIsNeverARepeat(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream).
		WithRunner(func(context.Context, string) (string, int) { return "ok", 0 }).
		WithRepeats(agent.NewRepeatDetector())
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	for range 3 {
		updated, _ = m.Update(cmdDoneMsg{command: "ls", output: "ok", exitCode: 0, local: true})
		m = updated.(Model)
	}
	for _, e := range m.transcript {
		if agent.IsRepeatNotice(e.toolResult) {
			t.Fatal("a /run the reader typed is theirs, and is never called a repeat")
		}
	}
}

// The reader declining the same call twice is the other half of the gated
// circle, and the one the screen cannot answer: the ⊘ row is in front of the
// person, and the model reads only the result.
func TestApproval_ARepeatedDeclineSaysSo(t *testing.T) {
	decline := func(t *testing.T, m Model) (Model, string) {
		t.Helper()
		updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
			{ID: "call_w", Name: "write_file", Arguments: `{"path":"main.go","content":"x\n"}`},
		}})
		m = handover(t, updated.(Model))
		updated, _ = m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
		m = updated.(Model)
		msgs := m.Messages()
		return m, msgs[len(msgs)-1].Content
	}

	m := gatedModel(t, nil, map[string]GatedPreviewFunc{"write_file": writeFilePreview("")}).
		WithRepeats(agent.NewRepeatDetector())
	m, first := decline(t, m)
	if agent.IsRepeatNotice(first) {
		t.Fatalf("the first decline is not a repeat: %q", first)
	}
	m.state = stateStreaming
	_, second := decline(t, m)
	if !agent.IsRepeatNotice(second) {
		t.Fatalf("a call declined twice should say so, got %q", second)
	}
	if !strings.HasPrefix(second, "error:") || !strings.Contains(second, "declined") {
		t.Fatalf("and must still read as the decline it is, got %q", second)
	}
}

// An auto-approval is not a row. What allowed the call rides the act's own
// row, so the feed spends one row on one act rather than two, and the second
// of the two no longer states an unbounded copy of what the first bounds.
func TestAutoApproval_LeavesOneRowCarryingItsOwnAccount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "one.txt")
	var ran []string
	m := execModel(t, &ran)
	m.policy.mode = agent.ModeAcceptEdits

	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_w", Name: "write_file", Arguments: fmt.Sprintf(`{"path":%q,"content":"one\n"}`, path)},
	}})
	m = updated.(Model)
	// Nothing yet: the decision is taken and the tool is running, and the
	// approval has left no row behind for the act's row to duplicate.
	if len(m.transcript) != 0 {
		t.Fatalf("the approval should leave no row of its own, got %+v", m.transcript)
	}

	var done approvedToolDoneMsg
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(approvedToolDoneMsg); ok {
			done = msg
		}
	}
	updated, _ = m.Update(done)
	m = updated.(Model)

	if len(m.transcript) != 1 {
		t.Fatalf("one act is one row, got %+v", m.transcript)
	}
	row := m.transcript[0]
	if row.kind != entryDiff || row.diff == nil || row.diff.Path != path {
		t.Fatalf("the one row should be the edit, got %+v", row)
	}
	if row.diff.Allowed != "auto-allowed · accept-edits mode" {
		t.Fatalf("the edit's row should say what let it apply, got %q", row.diff.Allowed)
	}
}

// The staging that made this worth fixing: twenty paths were spelled in full
// on the approval's line while the row underneath it had always cut them to
// the first and a count. With the line gone there is nowhere left for the
// unbounded form to be printed.
func TestAutoApproval_NeverPrintsWhatTheRowBounds(t *testing.T) {
	paths := make([]string, 20)
	for i := range paths {
		paths[i] = fmt.Sprintf("internal/ui/chat/row%02d.go", i+1)
	}
	args, err := json.Marshal(map[string]any{"verb": "add", "paths": paths})
	if err != nil {
		t.Fatal(err)
	}
	m := gatedModel(t,
		func(string, json.RawMessage) (string, error) { return "staged 20 files", nil },
		map[string]GatedPreviewFunc{
			structural.GitWriteToolName: func(json.RawMessage) (GatedPreview, error) {
				// What the real preview builds: every path, joined.
				return GatedPreview{Title: "stage 20 files", Summary: strings.Join(paths, ", "), Write: true}, nil
			},
		})
	m.policy.mode = agent.ModeAcceptEdits

	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_gw", Name: structural.GitWriteToolName, Arguments: string(args)},
	}})
	m = updated.(Model)
	var done approvedToolDoneMsg
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(approvedToolDoneMsg); ok {
			done = msg
		}
	}
	updated, _ = m.Update(done)
	m = updated.(Model)

	if len(m.transcript) != 1 {
		t.Fatalf("the staging is one row, got %+v", m.transcript)
	}
	row := m.activityRowFor(m.transcript[0])
	if row.Target != paths[0]+" +19" {
		t.Fatalf("the row should bound the staging to the first path and a count, got %q", row.Target)
	}
	if row.Allowed != "auto-allowed · accept-edits mode" {
		t.Fatalf("the row should say what let the staging run, got %q", row.Allowed)
	}
	if last := paths[len(paths)-1]; strings.Contains(m.renderHistory(), last) {
		t.Fatalf("no row may spell the paths the staging's own row bounds away (%s)", last)
	}
}

// A reading that lands while an approved call is running lands above the
// call's row, and there is no approval row above that for it to have landed
// inside: the act and its account are one row, so nothing can come between
// them. The ordering is not repaired at render — there is no pair left to
// order.
func TestAutoApproval_ASummaryCannotLandInsideAnApprovedAct(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "one.txt")
	var ran []string
	m := execModel(t, &ran)
	m.policy.mode = agent.ModeAcceptEdits

	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_w", Name: "write_file", Arguments: fmt.Sprintf(`{"path":%q,"content":"one\n"}`, path)},
	}})
	m = updated.(Model)

	// The reading comes back while the edit is still being written.
	m.appendEntry(entry{kind: entrySummary, reading: &summaryReading{
		verdict: agent.SummaryVerdict{Round: 77, State: agent.SummaryOnTarget, Text: "editing the approval queue"},
	}})

	var done approvedToolDoneMsg
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(approvedToolDoneMsg); ok {
			done = msg
		}
	}
	updated, _ = m.Update(done)
	m = updated.(Model)

	if len(m.transcript) != 2 {
		t.Fatalf("the reading and the edit, and nothing else, got %+v", m.transcript)
	}
	if m.transcript[0].kind != entrySummary {
		t.Fatalf("the reading landed first and stays first, got %+v", m.transcript[0])
	}
	last := m.transcript[1]
	if last.kind != entryDiff || last.diff == nil || last.diff.Allowed == "" {
		t.Fatalf("the edit's row still carries its own account, got %+v", last)
	}
}
