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
	"github.com/rfizzle/shhh/internal/digest"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/scope"
	"github.com/rfizzle/shhh/internal/structural"
	"github.com/rfizzle/shhh/internal/subagent"
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
	return gatedModelWith(t, executor, gated, Wiring{})
}

// gatedModelWith is gatedModel built from w, whose own executor and gated
// tools win over the two named.
func gatedModelWith(t *testing.T, executor ToolExecutor, gated map[string]GatedPreviewFunc, w Wiring) Model {
	t.Helper()
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "change it"},
	}
	if w.Executor == nil {
		w.Executor = executor
	}
	if w.GatedTools == nil {
		w.GatedTools = gated
	}
	m := New(msgs, mockStream, w)
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
	for _, want := range []string{"[y] apply the change", "[n] deny"} {
		if !strings.Contains(ansi.Strip(view), want) {
			t.Fatalf("a card holding the keyboard by arrival offers %q:\n%s", want, view)
		}
	}
	if !strings.Contains(ansi.Strip(view), keys.Bracket(keys.Draft.Answer)+" answer it") {
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
	m.wiring.Runner = legacyRunner(func(ctx context.Context, cmd string) (string, int) { return "ran", 0 })

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_x", Name: "execute_command", Arguments: `{"command":"echo hi"}`},
		{ID: "call_w", Name: "write_file", Arguments: `{"path":"a.txt","content":"x\n"}`},
	}})
	m = updated.(Model)

	// Exec approval first, with its command preview and safety-checked prompt.
	if m.state != stateConfirmRun || m.approval.request == nil || m.approval.request.kind != approvalExec {
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
	if m.state != stateConfirmRun || m.approval.request == nil || m.approval.request.kind != approvalDiff {
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

	if m.state != stateConfirmRun || m.approval.request == nil || m.approval.request.kind != approvalDiff {
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

	if m.state != stateConfirmRun || m.approval.request == nil || m.approval.request.kind != approvalDiff {
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

	if !strings.Contains(m.View().Content, "more lines · [shift+↓]") {
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
	m.wiring.MutationHook = func(name string, args json.RawMessage, result string) string {
		if name != "write_file" {
			t.Errorf("hook should see the mutating tool name, got %q", name)
		}
		return result + "\n\nDiagnostics (fake) for hello.go:\nhello.go:1:1 error: boom"
	}
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
	if m.state != stateConfirmRun || m.approval.request == nil || m.approval.request.kind != approvalDiff {
		t.Fatalf("a three-edit call should arm one diff approval, got state=%d", m.state)
	}
	// One card, and all three changes in the diff behind it: a decision put
	// up for one of them would be an approval of something else. The hunks
	// are read rather than the render, which clips a body taller than the
	// panel bound and would make this an assertion about terminal height.
	var changed []string
	for _, h := range m.approval.request.hunks {
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
	if m.approval.request != nil {
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
	if m.approval.request != nil {
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
	if m.state != stateConfirmRun || m.approval.request == nil {
		t.Fatalf("the card should still be waiting, got state %d pending %v", m.state, m.approval.request)
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
	if m.state != stateConfirmRun || m.approval.request == nil {
		t.Fatalf("esc should come back to the waiting decision, got state %d pending %v", m.state, m.approval.request)
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

// A derived form that never started says why the way the real command's row
// would, on its row and on the screen's title, and never as a negative exit.
func TestApprovalCard_DryRunThatDidNotStartKeepsItsCategory(t *testing.T) {
	var bare, contained []string
	m := containedModel(t, &bare, &contained, "contained: bwrap")
	m.containment.Run = func(context.Context, string) tools.ExecResult {
		return runner.WrapFailure("", errors.New("wrap unsupported: bwrap vanished"))
	}
	m = execApproval(t, m, "rsync --delete src/ dst/")
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	m = updated.(Model)
	updated, _ = m.Update(drainDryRun(t, cmd))
	m = updated.(Model)

	view := m.View().Content
	if !strings.Contains(view, "did not start · ") || strings.Contains(view, "exit -1") {
		t.Fatalf("the screen should name how the form ended, never as an exit status:\n%s", view)
	}
	last := m.transcript[len(m.transcript)-1]
	if last.commandResult.Prereq == "" {
		t.Fatalf("the dry run's row should keep the prerequisite, got %+v", last.commandResult)
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
	if m.state != stateConfirmRun || m.approval.request == nil {
		t.Fatalf("the card should still be waiting, got state %d pending %v", m.state, m.approval.request)
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
	if m.approval.request == nil || m.approval.request.kind != approvalExec {
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
	m := gatedModel(t, nil, nil)
	m.wiring.Runner = legacyRunner(func(context.Context, string) (string, int) { return "FAIL\tinternal/calc", 1 })
	m.wiring.Repeats = agent.NewRepeatDetector()

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
func TestApproval_FailedCommandIsAnErrorResultForEveryConsumer(t *testing.T) {
	m := gatedModel(t, nil, nil)
	m.wiring.Runner = legacyRunner(func(context.Context, string) (string, int) { return "compiler: undefined symbol", 1 })
	m.wiring.Repeats = agent.NewRepeatDetector()

	_, result := runOnce(t, m, "go test ./internal/chat")
	if !strings.HasPrefix(result, "error:") {
		t.Fatalf("failed command must be an error result, got %q", result)
	}
	if !strings.Contains(result, "compiler: undefined symbol") {
		t.Fatalf("failed command must retain stderr, got %q", result)
	}
	if got := digest.Outcome(result); got != digest.OutcomeError {
		t.Fatalf("digest outcome = %q, want error", got)
	}
}

func TestApproval_ALocalRunIsNeverARepeat(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream, Wiring{
		Runner:  legacyRunner(func(context.Context, string) (string, int) { return "ok", 0 }),
		Repeats: agent.NewRepeatDetector(),
	})
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

	m := gatedModel(t, nil, map[string]GatedPreviewFunc{"write_file": writeFilePreview("")})
	m.wiring.Repeats = agent.NewRepeatDetector()
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
				return GatedPreview{Title: "stage 20 files", Summary: strings.Join(paths, ", ")}, nil
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

// spawnPreview is the session's own spawn preview in miniature
// (cli/session.go): the plan the supervisor resolves, as the block's fields
// and the row the card draws a child with.
func spawnPreview(raw json.RawMessage) (GatedPreview, error) {
	plan, err := subagent.SpawnPlan(nil, raw)
	if err != nil {
		return GatedPreview{}, err
	}
	return GatedPreview{
		Action: "spawn", Summary: "start a " + string(plan.Role),
		Title: "spawn " + plan.Name,
		Fields: []GatedField{
			{Label: SpawnTouchesLabel, Value: plan.Scope, Open: plan.Writer},
			{Label: "budget", Value: plan.Budget},
		},
		Spawn: &components.SpawnRow{
			Role: string(plan.Role), Name: plan.Name, About: plan.About,
			Task: plan.Task, Touches: plan.Scope, Writer: plan.Writer,
		},
	}, nil
}

// spawnModel is a session at a spawn card, with the calls the round asked for
// already queued and the keyboard handed to the card.
func spawnModel(t *testing.T, calls ...provider.ToolCall) Model {
	t.Helper()
	m := tallPanel(t, gatedModel(t, func(string, json.RawMessage) (string, error) {
		return "started", nil
	}, map[string]GatedPreviewFunc{subagent.SpawnToolName: spawnPreview}))
	updated, _ := m.Update(toolCallsMsg{calls: calls})
	return handover(t, updated.(Model))
}

func spawnCall(id, args string) provider.ToolCall {
	return provider.ToolCall{ID: id, Name: subagent.SpawnToolName, Arguments: args}
}

// TestSpawnCard_ARoundIsOneDecision: three children asked for in one round
// are one card with a row per child, answered by one [y] for the set — not
// three cards reading "Approve tool (1 of 3)" with nothing to grant on any of
// them (docs/capabilities/subagents.md#spawning-is-a-decision).
func TestSpawnCard_ARoundIsOneDecision(t *testing.T) {
	m := spawnModel(t,
		spawnCall("s1", `{"role":"researcher","task":"say where the counter is read","name":"researcher-1"}`),
		spawnCall("s2", `{"role":"researcher","task":"say where the limit is set","name":"researcher-2"}`),
		spawnCall("s3", `{"role":"researcher","task":"say where the loop exits","name":"researcher-3"}`),
	)
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{
		"Start 3 researchers",
		"◇ researcher-1 · say where the counter is read",
		"◇ researcher-2 · say where the limit is set",
		"◇ researcher-3 · say where the loop exits",
		"reads only — a researcher changes nothing",
		"[y] start all 3",
		"[n] deny all 3",
		"[a] allow researchers for this session, or pick which of the 3 to start",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the fan-out card does not say %q:\n%s", want, view)
		}
	}
	// The card is the whole of what is waiting, so nothing counts the three
	// children a second time above it or in its title.
	if strings.Contains(view, "(1 of 3)") || len(m.approval.strip.Items) != 0 {
		t.Errorf("a fan-out card is not also a queue of three:\n%s", view)
	}
	// One decision, so the plain answer answers the set: the two behind the
	// head are marked and carried out as each reaches it (queue.go).
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(Model)
	for _, id := range []string{"s2", "s3"} {
		if allow, ok := m.approval.batchAnswered[id]; !ok || !allow {
			t.Fatalf("[y] on the fan-out card must answer %s too: %v", id, m.approval.batchAnswered)
		}
	}
}

// TestSpawnCard_ABatchStatesItsReasonFromTheRowsItDraws: a fan-out card takes
// the scope off the block and puts it on each child's row, so the level's
// reason names it as the rows' — not as a block row the card no longer draws —
// while a single writer's card keeps the block's own wording.
func TestSpawnCard_ABatchStatesItsReasonFromTheRowsItDraws(t *testing.T) {
	m := spawnModel(t,
		spawnCall("s1", `{"role":"writer","task":"add the flag","name":"writer-1","paths":["internal/cli/**"]}`),
		spawnCall("s2", `{"role":"writer","task":"read the flag","name":"writer-2","paths":["internal/agent/**"]}`),
	)
	card := m.approvalCard()
	if len(card.Spawns) != 2 {
		t.Fatalf("two writers asked for together are one card of two rows: %+v", card.Spawns)
	}
	if want := "each child's touches not limited"; card.SeverityReason != want {
		t.Errorf("batched reason = %q, want %q", card.SeverityReason, want)
	}
	for _, f := range card.Fields {
		if f.Label == SpawnTouchesLabel {
			t.Errorf("the block still draws the scope the rows carry: %+v", card.Fields)
		}
	}

	single := spawnModel(t, spawnCall("s1",
		`{"role":"writer","task":"add the flag","name":"writer-1","paths":["internal/cli/**"]}`))
	if want := "touches not limited"; single.approvalCard().SeverityReason != want {
		t.Errorf("single reason = %q, want %q", single.approvalCard().SeverityReason, want)
	}
}

// TestSpawnCard_AnAnsweredChildIsNotAskedAgain: a row the queue list already
// answered is neither drawn on the next card nor swept back into its set. A
// hook can put a card in front of the reader again mid-round, and a batch
// rebuilt from the queue's contents alone would let the plain key overrule a
// denial the reader gave a row one card earlier (queue.go).
func TestSpawnCard_AnAnsweredChildIsNotAskedAgain(t *testing.T) {
	m := tallPanel(t, gatedModel(t, func(string, json.RawMessage) (string, error) {
		return "started", nil
	}, map[string]GatedPreviewFunc{subagent.SpawnToolName: spawnPreview}))
	// The list denied the third child and allowed the second; the head has
	// run and the second is the card now.
	m.approval.batchAnswered = map[string]bool{"s3": false}
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		spawnCall("s2", `{"role":"researcher","task":"say where the limit is set","name":"researcher-2"}`),
		spawnCall("s3", `{"role":"researcher","task":"say where the loop exits","name":"researcher-3"}`),
	}})
	m = handover(t, updated.(Model))
	if slices.Contains(m.approval.batch, "s3") {
		t.Fatalf("an answered child is not part of the next decision: %v", m.approval.batch)
	}
	// It is still a queued decision, so the strip above the card still counts
	// it; what it is not is a row of this decision.
	if rows := m.approvalCard().Spawns; len(rows) != 1 || rows[0].Name != "researcher-2" {
		t.Errorf("an answered child is not drawn as a row again: %+v", rows)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if allow := updated.(Model).approval.batchAnswered["s3"]; allow {
		t.Fatal("the plain key must not overrule a denial the reader already gave")
	}
}

// TestSpawnCard_ASingleSpawnKeepsItsCard: one child is one row, the profile's
// clause under it, and the scope back in the block where every other card
// answers that question.
func TestSpawnCard_ASingleSpawnKeepsItsCard(t *testing.T) {
	m := spawnModel(t, spawnCall("s1",
		`{"role":"writer","task":"add the flag","name":"writer-1","paths":["internal/cli/**"]}`))
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{
		"Start a writer",
		"◇ writer-1 · add the flag",
		"full tools against an isolated copy of the workspace",
		"touches   its own worktree · claims internal/cli/**",
		"[y] start it",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the spawn card does not say %q:\n%s", want, view)
		}
	}
	// A writer's spawn never offers the session grant: its patch is the
	// decision that matters and this card is where the claim is read.
	if strings.Contains(view, "[a] allow writers") {
		t.Errorf("a writer's spawn must not offer a role grant:\n%s", view)
	}
	if strings.Contains(view, "[A] ") {
		t.Errorf("one child is not a queue to pick down:\n%s", view)
	}
}

// TestSpawnCard_AReadOnlyRoleIsGrantedForTheSession: [a] on a researcher's
// card opens the one grant it can make, and taking it is what stops the next
// fan-out of researchers being the same card again
// (docs/capabilities/approvals-and-safety.md#a-read-only-role-is-granted-once).
func TestSpawnCard_AReadOnlyRoleIsGrantedForTheSession(t *testing.T) {
	m := spawnModel(t, spawnCall("s1", `{"role":"researcher","task":"read it","name":"researcher-1"}`))
	if !strings.Contains(ansi.Strip(m.View().Content), "[a] allow researchers for this session") {
		t.Fatalf("a read-only role's card offers the session grant:\n%s", m.View().Content)
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	m = updated.(Model)
	if m.approval.grant == nil || len(m.approval.grant.offers) != 1 {
		t.Fatalf("a role grant is already exact, so the list is the one length: %+v", m.approval.grant)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if !m.roleGranted("researcher") {
		t.Fatalf("taking the row grants the role: %v", m.policy.roles)
	}
	if listing := m.grantStatus(); !strings.Contains(listing, "agents     researchers") {
		t.Errorf("/permissions grants does not list the role grant:\n%s", listing)
	}
	if got := m.policyLabel(); !strings.Contains(got, "1 role") {
		t.Errorf("the status line does not count the role grant: %q", got)
	}
	if out := m.revokeCommand([]string{"agents"}); !strings.Contains(out, "researchers") {
		t.Errorf("revoke does not say what went: %q", out)
	}
	if m.roleGranted("researcher") {
		t.Fatal("revoke leaves nothing granted")
	}
}

// gitWriteCard is one git write as its card is asked: the call and the
// preview the session builds for it.
type gitWriteCard struct {
	label, args string
	preview     GatedPreview
}

// gitWriteCards are the four verbs' previews written out as the session
// builds them. The function that builds them lives with the session's wiring
// above this package, so what is held here is the shape it hands over: the
// fields and the deny line together; the write tier is the classifier's,
// read off the name.
func gitWriteCards() []gitWriteCard {
	stages := GatedField{Label: "stages", Value: "this session's files only", Detail: "work that was already in the tree is never staged"}
	push := GatedField{Label: "push", Value: "no", Detail: "shhh never pushes; the remote is yours"}
	cards := []gitWriteCard{
		{"stage · the session's files, and nothing pushed", `{"verb":"add","paths":["internal/agent/loop.go"]}`,
			GatedPreview{Title: "stage 1 file", Action: "add", Summary: "internal/agent/loop.go",
				Fields: []GatedField{stages, push}}},
		{"commit · the hooks, and git revert as the way back", `{"verb":"commit","message":"feat(agent): cap rounds at the limit"}`,
			GatedPreview{Title: "commit", Action: "commit", Summary: "feat(agent): cap rounds at the limit",
				Fields: []GatedField{stages, push,
					{Label: "hooks", Value: "run", Detail: "the checkout's own commit hooks; a failure cancels and changes nothing"},
					{Label: "undo", Value: "git revert", Detail: components.CommitUndoNote}}}},
		{"commit · an untrusted checkout, and the two hooks it holds back", `{"verb":"commit","message":"feat(agent): cap rounds at the limit"}`,
			GatedPreview{Title: "commit", Action: "commit", Summary: "feat(agent): cap rounds at the limit",
				Fields: []GatedField{stages, push,
					{Label: "hooks", Value: "pre-commit and commit-msg skipped", Detail: "the checkout is not trusted; its other hooks still run"},
					{Label: "undo", Value: "git revert", Detail: components.CommitUndoNote}}}},
		{"branch · deleting it is the way back", `{"verb":"branch","branch":"topic"}`,
			GatedPreview{Title: "branch topic", Action: "branch", Summary: "create the branch topic",
				Fields: []GatedField{stages, push,
					{Label: "undo", Value: "git branch -d", Detail: "a new branch moves no file and holds no work; deleting it is a line you type"}}}},
		{"switch · switching back is the way back", `{"verb":"switch","branch":"topic"}`,
			GatedPreview{Title: "switch to topic", Action: "switch", Summary: "switch to the branch topic",
				Fields: []GatedField{stages, push,
					{Label: "undo", Value: "git switch -", Detail: "the tree becomes the other branch's, and every file this session had read is re-read"}}}},
	}
	for i := range cards {
		cards[i].preview.DenyLine = "git " + cards[i].preview.Action
	}
	return cards
}

// armGitWrite puts one git write's card up in a manual session of the given
// size and returns the panel it draws.
func armGitWrite(t *testing.T, width, height int, c gitWriteCard) string {
	t.Helper()
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "commit that change"},
	}, mockStream, Wiring{
		GatedTools: map[string]GatedPreviewFunc{
			structural.GitWriteToolName: func(json.RawMessage) (GatedPreview, error) { return c.preview, nil },
		},
	})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m = updated.(Model)
	m.state = stateStreaming
	updated, _ = m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_gw", Name: structural.GitWriteToolName, Arguments: c.args},
	}})
	m = updated.(Model)
	if m.approval.request == nil {
		t.Fatalf("%s: the git write should arm a decision", c.label)
	}
	return strings.Join(m.confirmLines(), "\n")
}

// A git write carries a command line only for the deny list to match. The
// card is the tool's own fields, never a shell's reading of that line, which
// could say only that it knew neither what the act touches nor how it is
// undone.
func TestGitWriteCard_DrawsThePreviewsOwnFields(t *testing.T) {
	for _, c := range gitWriteCards() {
		plain := ansi.Strip(armGitWrite(t, 130, 48, c))
		if strings.Contains(plain, "unknown") {
			t.Fatalf("%s: the card read the deny line as a shell command:\n%s", c.label, plain)
		}
		for _, f := range c.preview.Fields {
			if !strings.Contains(plain, f.Label) || !strings.Contains(plain, f.Value) {
				t.Fatalf("%s: the card should draw the preview's %s field:\n%s", c.label, f.Label, plain)
			}
		}
	}
}

// A session that requires containment on a host with none refuses the
// assistant's commands before a card, and a git write is not one: its deny
// line is not a command the assistant wrote, so every verb reaches the same
// card it draws without the requirement, and nothing is answered for it.
// See docs/capabilities/containment.md#a-git-write-is-not-a-command.
func TestGitWrite_RequiredContainmentPutsItToTheCard(t *testing.T) {
	const refusal = "error: this session requires containment and no mechanism is in force: " +
		"bubblewrap (bwrap) not found on PATH"
	unconfined := Containment{
		Status:  "unconfined — bubblewrap (bwrap) not found on PATH",
		Detail:  "bubblewrap (bwrap) not found on PATH",
		Network: true,
	}
	arm := func(c gitWriteCard, contain Containment) Model {
		t.Helper()
		m := New([]provider.Message{
			{Role: provider.RoleSystem, Content: "sys"},
			{Role: provider.RoleUser, Content: "commit that change"},
		}, mockStream, Wiring{
			GatedTools: map[string]GatedPreviewFunc{
				structural.GitWriteToolName: func(json.RawMessage) (GatedPreview, error) { return c.preview, nil },
			},
			Containment: contain,
		})
		updated, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 48})
		m = updated.(Model)
		m.state = stateStreaming
		updated, _ = m.Update(toolCallsMsg{calls: []provider.ToolCall{
			{ID: "call_gw", Name: structural.GitWriteToolName, Arguments: c.args},
		}})
		return updated.(Model)
	}
	required := unconfined
	required.Refusal = refusal
	for _, c := range gitWriteCards() {
		m := arm(c, required)
		if m.approval.request == nil {
			t.Fatalf("%s: a git write under a required session should be put to its card", c.label)
		}
		for _, msg := range m.Messages() {
			if msg.Role == provider.RoleTool {
				t.Fatalf("%s: nothing should be answered for the call before the card, got %q", c.label, msg.Content)
			}
		}
		want := strings.Join(arm(c, unconfined).confirmLines(), "\n")
		if got := strings.Join(m.confirmLines(), "\n"); got != want {
			t.Fatalf("%s: the requirement should not change the card:\n got:\n%s\nwant:\n%s", c.label, got, want)
		}
	}
}

// A write that adds a credential shape asks in the modes that would have
// run it, with the shape named by kind and line on the card and the value
// nowhere on it
// (docs/capabilities/approvals-and-safety.md#a-write-that-adds-a-secret-is-always-asked).
func TestApproval_AWriteWithASecretAsks(t *testing.T) {
	const key = "sk-ant-api03-" + "AbCdEfGhIjKlMnOpQrStUvWxYz0123456789AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-AbCdEfGh-AA"
	secretly := "# dev\n# a\nKEY=" + key + "\n"
	write := func(mode agent.Mode, grant bool, content string) Model {
		var ran []string
		m := execModel(t, &ran)
		m.policy.mode, m.policy.allEdits = mode, grant
		path := filepath.Join(t.TempDir(), "dev.env")
		updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
			{ID: "call_w", Name: "write_file", Arguments: fmt.Sprintf(`{"path":%q,"content":%q}`, path, content)},
		}})
		return updated.(Model)
	}
	for _, mode := range []agent.Mode{agent.ModeAcceptEdits, agent.ModeAuto} {
		m := write(mode, false, secretly)
		if m.state != stateConfirmRun {
			t.Fatalf("a write with a secret must wait for the reader in %s mode", mode)
		}
		card := strings.Join(m.confirmLines(), "\n")
		if !strings.Contains(card, "adds 1 anthropic key · line 3") {
			t.Fatalf("%s: the card should name the kind and line, got:\n%s", mode, card)
		}
		if strings.Contains(card, "AbCdEfGh") {
			t.Fatalf("%s: the card must not carry the value, got:\n%s", mode, card)
		}
	}
	if write(agent.ModeManual, true, secretly).state != stateConfirmRun {
		t.Fatal("a session grant must not answer a write with a secret")
	}
	if write(agent.ModeAcceptEdits, false, "KEY=none\n").state == stateConfirmRun {
		t.Fatal("a clean write should still run unasked in accept-edits mode")
	}
}

// In auto mode a write that adds a credential shape is never put to the
// classifier, whatever it would have said: the policy's ask is a card
// recorded as a safety ask, as the child's gate answers it
// (docs/capabilities/approvals-and-safety.md#a-write-that-adds-a-secret-is-always-asked).
func TestApproval_AWriteWithASecretSkipsTheClassifier(t *testing.T) {
	const key = "sk-ant-api03-" + "AbCdEfGhIjKlMnOpQrStUvWxYz0123456789AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-AbCdEfGh-AA"
	// Ordinary directories, not the test process's TMPDIR, which production
	// classifies as sensitive and the classifier is never asked about.
	base, err := os.MkdirTemp(".", ".secret-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	root, outside := filepath.Join(base, "root"), filepath.Join(base, "outside")
	for _, d := range []string{root, outside} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	sc, problems := scope.New(root)
	if sc == nil {
		t.Fatalf("scope.New(%q): %v", root, problems)
	}
	write := func(content string) (Model, *verdictProvider, *[][2]string) {
		var ran []string
		judge := &verdictProvider{decision: "allow", reason: "a fixture"}
		decisions := &[][2]string{}
		m := recordingDecisions(classifierModel(t, &ran, judge), decisions)
		m.wiring.Scope = sc
		path := filepath.Join(outside, "dev.env")
		updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
			{ID: "call_w", Name: "write_file", Arguments: fmt.Sprintf(`{"path":%q,"content":%q}`, path, content)},
		}})
		return updated.(Model), judge, decisions
	}
	// Control: the same out-of-scope write without a secret does reach the
	// classifier, so what follows proves a skip and not an unreachable path.
	if m, _, _ := write("KEY=none\n"); m.state != stateClassifying {
		t.Fatalf("a clean out-of-scope write should be classified in auto mode, got state %d", m.state)
	}
	m, judge, decisions := write("# dev\n# a\nKEY=" + key + "\n")
	if m.state != stateConfirmRun {
		t.Fatalf("a write with a secret must wait for the reader in auto mode, got state %d", m.state)
	}
	if judge.calls != 0 {
		t.Fatalf("the classifier must not be asked about a write with a secret, asked %d times", judge.calls)
	}
	want := [2]string{observe.DecisionAsk, observe.ReasonSafety}
	if len(*decisions) != 1 || (*decisions)[0] != want {
		t.Fatalf("the ask should be recorded as a safety ask, got %v", *decisions)
	}
}
