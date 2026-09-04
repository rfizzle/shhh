package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/hook"
	"github.com/rfizzle/shhh/internal/provider"
)

// hookRunner is a session's hooks with a fake command behind them: every hook
// answers with the same stdout and exit code, and what each was told is
// recorded.
func hookRunner(t *testing.T, entries map[string]hook.Entry, stdout string, code int, seen *[]hook.Payload) *hook.Runner {
	t.Helper()
	set := hook.Load(entries, "config.toml", "")
	if len(set.Diagnostics) > 0 {
		t.Fatalf("entries should have loaded: %v", set.Diagnostics)
	}
	exec := func(_ context.Context, _ string, stdin []byte) (string, int, error) {
		var p hook.Payload
		if err := json.Unmarshal(stdin, &p); err != nil {
			t.Errorf("a hook was sent something that is not the payload: %v", err)
		}
		if seen != nil {
			*seen = append(*seen, p)
		}
		return stdout, code, nil
	}
	r := hook.NewRunner(set, exec, time.Second, "/work")
	if r == nil {
		t.Fatal("a set with hooks in it should build a runner")
	}
	return r
}

// drivePreToolHook runs the command a queued gated call produced and feeds the
// answer back, which is what the program's own loop does.
func drivePreToolHook(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(preToolHookMsg); ok {
			updated, _ := m.Update(msg)
			return updated.(Model)
		}
	}
	t.Fatal("a gated call with a hook on it should have produced a hook cmd")
	return m
}

// A hook's refusal is a rule denial, not the user's: the reader's next act is
// to edit a hook rather than to change their mind, and the row has to say so.
func TestHook_PreToolDenyDrawsTheRuleDenialRow(t *testing.T) {
	var ran []string
	m := execModel(t, &ran)
	m.hooks = hookRunner(t, map[string]hook.Entry{
		"guard": {Event: hook.PreTool, Command: "guard"},
	}, "vendor is off limits\n", 2, nil)
	// Nothing about the session may reach the decision: the blanket grant is
	// on and the command is on the allowlist.
	m = m.WithCommandAllowlist([]string{"go build"})
	m.policy.allCommands = true

	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_h", Name: "execute_command", Arguments: `{"command":"go build"}`},
	}})
	m = drivePreToolHook(t, updated.(Model), cmd)

	if len(ran) != 0 {
		t.Fatalf("a refused command should not run: %v", ran)
	}
	if m.pendingApproval != nil {
		t.Fatal("a refused command should not reach a card")
	}
	refused := false
	for _, msg := range m.Messages() {
		if msg.Role == provider.RoleTool && strings.Contains(msg.Content, "guard") {
			refused = true
		}
	}
	if !refused {
		t.Fatal("the model was not told why the command did not run")
	}
	view := stripANSI(m.renderHistory())
	for _, want := range []string{"⊘", "denied · auto · hook guard"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the row should name the rule, want %q:\n%s", want, view)
		}
	}
	if !strings.Contains(m.lastDenial, "guard") {
		t.Fatalf("/permissions why should name the hook: %q", m.lastDenial)
	}
}

// A hook that broke on a call there is somebody to ask about is a card, and
// it out-ranks every standing yes the session holds. Nothing decides yes on a
// failure.
func TestHook_AFailureOnAGatedCallAsks(t *testing.T) {
	var ran []string
	m := execModel(t, &ran).WithCommandAllowlist([]string{"go build"})
	m.policy.allCommands = true
	m.hooks = hookRunner(t, map[string]hook.Entry{
		"guard": {Event: hook.PreTool, Command: "guard"},
	}, "boom\n", 1, nil)

	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_h", Name: "execute_command", Arguments: `{"command":"go build"}`},
	}})
	m = drivePreToolHook(t, updated.(Model), cmd)

	if len(ran) != 0 {
		t.Fatalf("a broken hook must not let a command through: %v", ran)
	}
	if m.state != stateConfirmRun || m.pendingApproval == nil {
		t.Fatalf("a failed hook should put the call to the reader, state %d", m.state)
	}
	view := stripANSI(m.renderHistory())
	if !strings.Contains(view, "exited 1") {
		t.Fatalf("the reader should be told the hook broke:\n%s", view)
	}
}

// A rewrite is what will actually run, so the card is built again from it.
func TestHook_UpdatedInputRebuildsThePreview(t *testing.T) {
	m := gatedModel(t, nil, map[string]GatedPreviewFunc{
		"write_file": writeFilePreview("line one\n"),
	})
	m.hooks = hookRunner(t, map[string]hook.Entry{
		"rewrite": {Event: hook.PreTool, Matcher: "write_file", Command: "rewrite"},
	}, `{"updated_input":{"path":"other.go","content":"rewritten\n"}}`, 0, nil)

	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_w", Name: "write_file", Arguments: `{"path":"main.go","content":"line two\n"}`},
	}})
	m = drivePreToolHook(t, updated.(Model), cmd)

	if m.pendingApproval == nil {
		t.Fatal("the rewritten call should still be put to the reader")
	}
	if got := m.pendingApproval.path; got != "other.go" {
		t.Fatalf("the card should preview the rewritten call, got %q", got)
	}
	if !strings.Contains(m.pendingApproval.call.Arguments, "rewritten") {
		t.Fatalf("the call itself should carry the rewrite: %s", m.pendingApproval.call.Arguments)
	}
}

// The tier rule. A hook on a read may refuse it or add to it and can never
// turn it into a write: the call that runs is the call that was dispatched,
// through the dispatcher that was asked.
func TestHook_CannotTurnAReadIntoAWrite(t *testing.T) {
	var dispatched []string
	next := func(name string, args json.RawMessage) (string, error) {
		dispatched = append(dispatched, name+" "+string(args))
		return "one line", nil
	}
	m := readyModel(t)
	m.hooks = hookRunner(t, map[string]hook.Entry{
		"climb": {Event: hook.PreTool, Command: "climb"},
	}, `{"decision":"allow","tool":"write_file","name":"write_file","updated_input":{"path":"b.go"}}`, 0, nil)

	out, err := m.hookExecutor(next)("read_file", json.RawMessage(`{"path":"a.go"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatched) != 1 || !strings.HasPrefix(dispatched[0], "read_file ") {
		t.Fatalf("the call must stay a read: %v", dispatched)
	}
	if !strings.Contains(dispatched[0], "b.go") {
		t.Fatalf("its arguments are what a hook may change: %v", dispatched)
	}
	if out != "one line" {
		t.Fatalf("the read's own result should stand, got %q", out)
	}
}

// A hook on a read has nobody to ask, so a broken one costs a note and not
// the read.
func TestHook_AFailureOnAReadKeepsTheRead(t *testing.T) {
	next := func(string, json.RawMessage) (string, error) { return "one line", nil }
	m := readyModel(t)
	m.hooks = hookRunner(t, map[string]hook.Entry{
		"guard": {Event: hook.PreTool, Command: "guard"},
	}, "boom\n", 1, nil)

	out, err := m.hookExecutor(next)("read_file", json.RawMessage(`{"path":"a.go"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "one line") {
		t.Fatalf("the read should still have happened: %q", out)
	}
	if !strings.Contains(out, "exited 1") {
		t.Fatalf("the model should be told the hook broke: %q", out)
	}
}

// A call the session puts to a person is answered at the queue, so the
// executor chain does not ask about it a second time. The seam behind the
// call is not a tier and fires for everything.
func TestHook_TheChainSkipsWhatTheQueueWillAsk(t *testing.T) {
	var seen []hook.Payload
	next := func(string, json.RawMessage) (string, error) { return "wrote it", nil }
	m := gatedModel(t, nil, map[string]GatedPreviewFunc{
		"write_file": writeFilePreview(""),
	})
	m.hooks = hookRunner(t, map[string]hook.Entry{
		"before": {Event: hook.PreTool, Command: "before"},
		"after":  {Event: hook.PostTool, Command: "after"},
	}, "", 0, &seen)

	if _, err := m.hookExecutor(next)("write_file", json.RawMessage(`{"path":"a.go"}`)); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0].Event != hook.PostTool {
		t.Fatalf("a gated call should meet only the seam behind it: %+v", seen)
	}
}

// The turn's close is a seam, and there is exactly one of them per turn.
func TestHook_TurnCloseFiresOnceAsTheTurnEnds(t *testing.T) {
	var seen []hook.Payload
	m := readyModel(t)
	m.hooks = hookRunner(t, map[string]hook.Entry{
		"done": {Event: hook.TurnClose, Command: "done"},
	}, `{"note":"checked"}`, 0, &seen)
	m.turnOpen, m.turnCount = true, 4

	m.appendTurnClose()

	if len(seen) != 1 || seen[0].Event != hook.TurnClose || seen[0].Turn != 4 {
		t.Fatalf("the turn's close should fire once, with its turn: %+v", seen)
	}
	m.appendTurnClose()
	if len(seen) != 1 {
		t.Fatalf("a turn already closed does not close again: %+v", seen)
	}
	view := stripANSI(m.renderHistory())
	if !strings.Contains(view, "checked") {
		t.Fatalf("what the hook said should reach the transcript:\n%s", view)
	}
}

// The session's hooks are part of what makes this session different from the
// one beside it, so `/status` says what they are.
func TestHook_StatusNamesTheHooks(t *testing.T) {
	m := readyModel(t)
	if got := m.hooksStatus(); got != "" {
		t.Fatalf("a session with no hooks has nothing to say: %q", got)
	}
	m.hooks = hookRunner(t, map[string]hook.Entry{
		"fmt": {Event: hook.PostTool, Matcher: "edit_file", Command: "gofmt -l ."},
	}, "", 0, nil)
	got := m.hooksStatus()
	for _, want := range []string{"Hooks", "fmt", "post_tool", "edit_file"} {
		if !strings.Contains(got, want) {
			t.Fatalf("/status should name %q:\n%s", want, got)
		}
	}
}
