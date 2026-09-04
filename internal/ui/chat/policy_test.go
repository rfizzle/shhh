package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/structural"
)

func TestAllowlistMatches(t *testing.T) {
	allowlist := []string{"git status", "go test", ""}
	cases := []struct {
		command string
		want    bool
	}{
		{"git status", true},
		{"git  status", true}, // extra whitespace between words
		{"go test ./...", true},
		{"go testx", false},
		{"git", false},         // shorter than the entry
		{"gitk status", false}, // first word differs
		{"echo hi", false},
		{"git status; rm -rf ~", false},         // chained command
		{"go test && cat ~/.ssh/id_rsa", false}, // chained command
		{"go test | tee out", false},            // pipe
		{"go test $(evil)", false},              // substitution
		{"git status\nrm -rf ~", false},         // newline
	}
	for _, c := range cases {
		if got := allowlistMatches(allowlist, c.command); got != c.want {
			t.Errorf("allowlistMatches(%q) = %v, want %v", c.command, got, c.want)
		}
	}
	if allowlistMatches(nil, "git status") {
		t.Error("empty allowlist must not match anything")
	}
}

// execModel is gatedModel plus a runner that records executed commands.
func execModel(t *testing.T, ran *[]string) Model {
	t.Helper()
	m := gatedModel(t, nil, nil)
	return m.WithRunner(func(ctx context.Context, cmd string) (string, int) {
		*ran = append(*ran, cmd)
		return "ok", 0
	})
}

// driveCmdDone extracts the cmdDoneMsg produced by an exec approval cmd.
func driveCmdDone(t *testing.T, cmd tea.Cmd) cmdDoneMsg {
	t.Helper()
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(cmdDoneMsg); ok {
			return msg
		}
	}
	t.Fatal("expected cmdDoneMsg from the exec cmd")
	return cmdDoneMsg{}
}

func TestPolicy_AllowlistAutoApprovesCommand(t *testing.T) {
	var ran []string
	m := execModel(t, &ran).WithCommandAllowlist([]string{"echo"})

	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_x", Name: "execute_command", Arguments: `{"command":"echo hi"}`},
	}})
	m = updated.(Model)

	if m.state != stateRunningCmd {
		t.Fatalf("allowlisted command should run without a prompt, got state %d", m.state)
	}
	updated, restream := m.Update(driveCmdDone(t, cmd))
	m = updated.(Model)
	if len(ran) != 1 || ran[0] != "echo hi" {
		t.Fatalf("expected the command to run once, got %v", ran)
	}
	last := m.Messages()[len(m.Messages())-1]
	if last.Role != provider.RoleTool || last.ToolCallID != "call_x" || !strings.Contains(last.Content, "exit code: 0") {
		t.Fatalf("expected a tool result for the auto-approved command, got %+v", last)
	}
	if m.state != stateStreaming || restream == nil {
		t.Fatal("stream should resume after the auto-approved command completes")
	}
	found := false
	for _, e := range m.transcript {
		if e.kind == entrySystem && strings.Contains(e.text, "Auto-approved (allowlist): echo hi") {
			found = true
		}
	}
	if !found {
		t.Fatal("transcript should note the allowlist auto-approval")
	}
}

func TestPolicy_ChainedCommandNotAutoApproved(t *testing.T) {
	var ran []string
	m := execModel(t, &ran).WithCommandAllowlist([]string{"echo"})

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_x", Name: "execute_command", Arguments: `{"command":"echo hi && cat secrets.txt"}`},
	}})
	m = updated.(Model)

	if m.state != stateConfirmRun {
		t.Fatalf("chained command must still prompt, got state %d", m.state)
	}
	if len(ran) != 0 {
		t.Fatal("nothing should run before approval")
	}
}

func TestPolicy_FlaggedCommandAlwaysPrompts(t *testing.T) {
	var ran []string
	m := execModel(t, &ran).WithCommandAllowlist([]string{"git"})
	m.policy.allCommands = true

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_x", Name: "execute_command", Arguments: `{"command":"git reset --hard"}`},
	}})
	m = updated.(Model)

	if m.state != stateConfirmRun {
		t.Fatalf("safety-flagged command must prompt regardless of policy, got state %d", m.state)
	}
	view := m.View().Content
	if strings.Contains(view, "[y/n/a]") {
		t.Fatal("flagged command must not offer the always-allow option")
	}
	if !strings.Contains(view, "[y/N]") {
		t.Fatal("flagged command should offer plain y/N")
	}

	// 'a' is ignored on a flagged command.
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	m = updated.(Model)
	if m.state != stateConfirmRun || len(ran) != 0 {
		t.Fatal("'a' must not approve a safety-flagged command")
	}
}

func TestPolicy_AlwaysAllowCommandsViaKey(t *testing.T) {
	var ran []string
	m := execModel(t, &ran)

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_1", Name: "execute_command", Arguments: `{"command":"go build ./one"}`},
		{ID: "call_2", Name: "execute_command", Arguments: `{"command":"go build ./two"}`},
	}})
	m = updated.(Model)

	if m.state != stateConfirmRun {
		t.Fatalf("first command should prompt, got state %d", m.state)
	}
	// The second queued command puts a batch behind the card, so [A] joins
	// the keys.
	m = handover(t, m)
	if !strings.Contains(m.View().Content, "[y/n/a/A]") {
		t.Fatal("unflagged command prompt with a queue behind it should offer y/n/a/A")
	}

	// 'a' approves this command and stops the session asking about commands
	// of the same shape — `echo`, not everything.
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	m = updated.(Model)
	if m.policy.allCommands {
		t.Fatal("'a' must not hand out a blanket grant; that is /permissions allow")
	}
	if got := m.policy.commands; len(got) != 1 || got[0] != "go build" {
		t.Fatalf("'a' should have granted the command's leading words, got %v", got)
	}
	updated, cmd = m.Update(driveCmdDone(t, cmd))
	m = updated.(Model)
	if m.state != stateRunningCmd {
		t.Fatalf("second command should auto-run without a prompt, got state %d", m.state)
	}
	updated, restream := m.Update(driveCmdDone(t, cmd))
	m = updated.(Model)
	if len(ran) != 2 || ran[0] != "go build ./one" || ran[1] != "go build ./two" {
		t.Fatalf("both commands should have run in order, got %v", ran)
	}
	if m.state != stateStreaming || restream == nil {
		t.Fatal("stream should resume once the queue drains")
	}
}

func TestPolicy_AlwaysAllowEditsViaKey(t *testing.T) {
	m := gatedModel(t, nil, nil)
	dir := t.TempDir()
	first := filepath.Join(dir, "a.txt")
	second := filepath.Join(dir, "b.txt")

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_1", Name: "write_file", Arguments: fmt.Sprintf(`{"path":%q,"content":"one\n"}`, first)},
		{ID: "call_2", Name: "write_file", Arguments: fmt.Sprintf(`{"path":%q,"content":"two\n"}`, second)},
	}})
	m = updated.(Model)

	if m.state != stateConfirmRun {
		t.Fatalf("first edit should prompt, got state %d", m.state)
	}
	m = handover(t, m)
	if !strings.Contains(m.View().Content, "always allow edits") {
		t.Fatal("edit prompt should offer the always-allow option")
	}

	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	m = updated.(Model)
	if m.policy.allEdits {
		t.Fatal("'a' must not hand out a blanket grant; that is /permissions allow")
	}
	if got := m.policy.editDirs; len(got) != 1 || got[0] != dir {
		t.Fatalf("'a' should have granted the edited file's directory, got %v", got)
	}
	var done approvedToolDoneMsg
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(approvedToolDoneMsg); ok {
			done = msg
		}
	}
	updated, cmd = m.Update(done)
	m = updated.(Model)
	if m.state != stateRunningCmd {
		t.Fatalf("second edit should auto-run without a prompt, got state %d", m.state)
	}
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(approvedToolDoneMsg); ok {
			done = msg
		}
	}
	updated, _ = m.Update(done)
	m = updated.(Model)

	for _, p := range []string{first, second} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected %s to be written: %v", p, err)
		}
	}
	found := false
	for _, e := range m.transcript {
		if e.kind == entrySystem && strings.Contains(e.text, "Auto-approved (session grant): write "+second) {
			found = true
		}
	}
	if !found {
		t.Fatal("transcript should note the session-grant auto-approval")
	}
}

func TestPolicy_GenericGatedToolAlwaysPrompts(t *testing.T) {
	executor := func(name string, args json.RawMessage) (string, error) { return "ok", nil }
	m := gatedModel(t, executor, map[string]GatedPreviewFunc{
		"my_tool": func(raw json.RawMessage) (GatedPreview, error) {
			return GatedPreview{Summary: "do the thing"}, nil
		},
	})
	m.policy.allEdits = true
	m.policy.allCommands = true

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_g", Name: "my_tool", Arguments: `{}`},
	}})
	m = updated.(Model)

	if m.state != stateConfirmRun {
		t.Fatalf("generic gated tool must always prompt, got state %d", m.state)
	}
	if !strings.Contains(m.View().Content, "[y/N]") {
		t.Fatal("generic approval keeps plain y/N")
	}
}

func TestMode_AcceptEditsAutoAppliesEditsButPromptsCommands(t *testing.T) {
	var ran []string
	m := execModel(t, &ran)
	m.policy.mode = agent.ModeAcceptEdits
	path := filepath.Join(t.TempDir(), "a.txt")

	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_w", Name: "write_file", Arguments: fmt.Sprintf(`{"path":%q,"content":"one\n"}`, path)},
	}})
	m = updated.(Model)
	if m.state != stateRunningCmd {
		t.Fatalf("accept-edits should apply the edit without a prompt, got state %d", m.state)
	}
	var done approvedToolDoneMsg
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(approvedToolDoneMsg); ok {
			done = msg
		}
	}
	updated, _ = m.Update(done)
	m = updated.(Model)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected the file to be written: %v", err)
	}
	found := false
	for _, e := range m.transcript {
		if e.kind == entrySystem && strings.Contains(e.text, "Auto-approved (accept-edits mode): write "+path) {
			found = true
		}
	}
	if !found {
		t.Fatal("transcript should note the accept-edits auto-approval")
	}

	// Commands still prompt in accept-edits.
	m.state = stateStreaming
	updated, _ = m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_c", Name: "execute_command", Arguments: `{"command":"echo hi"}`},
	}})
	m = updated.(Model)
	if m.state != stateConfirmRun {
		t.Fatalf("accept-edits must still prompt for commands, got state %d", m.state)
	}
	if len(ran) != 0 {
		t.Fatal("no command should run before approval")
	}
}

func TestMode_AutoAllowsEditsAndAllowlistedCommands(t *testing.T) {
	var ran []string
	m := execModel(t, &ran).WithCommandAllowlist([]string{"echo"})
	m.policy.mode = agent.ModeAuto

	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_x", Name: "execute_command", Arguments: `{"command":"echo hi"}`},
	}})
	m = updated.(Model)
	if m.state != stateRunningCmd {
		t.Fatalf("auto mode should run the allowlisted command, got state %d", m.state)
	}
	updated, _ = m.Update(driveCmdDone(t, cmd))
	m = updated.(Model)
	if len(ran) != 1 || ran[0] != "echo hi" {
		t.Fatalf("expected the command to run, got %v", ran)
	}

	// An unlisted command still asks in auto mode (no classifier yet).
	m.state = stateStreaming
	updated, _ = m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_y", Name: "execute_command", Arguments: `{"command":"go test ./..."}`},
	}})
	m = updated.(Model)
	if m.state != stateConfirmRun {
		t.Fatalf("auto mode must prompt for unlisted commands, got state %d", m.state)
	}
}

func TestMode_AutoFlaggedCommandStillPrompts(t *testing.T) {
	var ran []string
	m := execModel(t, &ran).WithCommandAllowlist([]string{"git"})
	m.policy.mode = agent.ModeAuto
	m.policy.allCommands = true

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_x", Name: "execute_command", Arguments: `{"command":"git reset --hard"}`},
	}})
	m = updated.(Model)
	if m.state != stateConfirmRun || len(ran) != 0 {
		t.Fatalf("safety-flagged command must prompt in auto mode, got state %d", m.state)
	}
}

func TestMode_PlanRefusesGatedCalls(t *testing.T) {
	var ran []string
	m := execModel(t, &ran).WithCommandAllowlist([]string{"echo"})
	m.policy.mode = agent.ModePlan
	m.policy.allEdits = true
	m.policy.allCommands = true
	path := filepath.Join(t.TempDir(), "a.txt")

	updated, restream := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_w", Name: "write_file", Arguments: fmt.Sprintf(`{"path":%q,"content":"one\n"}`, path)},
		{ID: "call_c", Name: "execute_command", Arguments: `{"command":"echo hi"}`},
	}})
	m = updated.(Model)

	if len(ran) != 0 {
		t.Fatal("plan mode must not run commands")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("plan mode must not write files")
	}
	msgs := m.Messages()
	refused := 0
	for _, msg := range msgs {
		if msg.Role == provider.RoleTool && strings.Contains(msg.Content, "plan mode") {
			refused++
		}
	}
	if refused != 2 {
		t.Fatalf("both calls should get plan-mode refusal results, got %d", refused)
	}
	found := 0
	for _, e := range m.transcript {
		if e.kind == entryTool && e.deniedBy == decidedByAuto && e.denyRule == "plan mode" {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("transcript should note both refusals, got %d", found)
	}
	view := stripANSI(m.renderHistory())
	for _, want := range []string{"⊘", "denied · auto · plan mode", "/permissions why"} {
		if !strings.Contains(view, want) {
			t.Fatalf("a rule's refusal names the rule and offers its key, want %q:\n%s", want, view)
		}
	}
	if m.state != stateStreaming || restream == nil {
		t.Fatal("the loop should resume so the model sees the refusals")
	}
}

func TestMode_ReadOnlyToolsBypassApprovalInPlanMode(t *testing.T) {
	m := gatedModel(t, nil, nil)
	m.policy.mode = agent.ModePlan
	for _, name := range []string{"read_file", "list_directory", "search", "glob"} {
		if m.requiresApproval(provider.ToolCall{Name: name}) {
			t.Errorf("read-only tool %s must never be approval-gated", name)
		}
	}
}

// The git tool's read-only classification is the story of the tool: history
// answered without an approval and without a classifier round. Nothing gates
// it, in any mode, and this is the test that says so.
func TestMode_GitToolIsReadOnlyInEveryMode(t *testing.T) {
	call := provider.ToolCall{
		Name:      structural.GitToolName,
		Arguments: `{"verb":"blame","paths":["internal/structural/git.go"]}`,
	}
	for _, mode := range []agent.Mode{agent.ModeManual, agent.ModeAcceptEdits, agent.ModeAuto, agent.ModePlan} {
		m := gatedModel(t, nil, nil)
		m.policy.mode = mode
		if m.requiresApproval(call) {
			t.Errorf("git must never be approval-gated, and is in %s", mode)
		}
	}
}

func TestMode_ShiftTabCyclesAndStatusBarShowsMode(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	if !strings.Contains(m.renderStatusBar(80), "⏸ manual") {
		t.Fatalf("status bar should show the default manual mode, got %q", m.renderStatusBar(80))
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = updated.(Model)
	if m.policy.mode != agent.ModeAcceptEdits {
		t.Fatalf("shift+tab should cycle manual → accept-edits, got %v", m.policy.mode)
	}
	if !strings.Contains(m.renderStatusBar(80), "⏵⏵ accept edits") {
		t.Fatalf("status bar should show the permissive mode, got %q", m.renderStatusBar(80))
	}

	// A configured cycle is honored, wrapping around.
	m = m.WithApprovalMode(agent.ModeManual, []agent.Mode{agent.ModeManual, agent.ModePlan})
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = updated.(Model)
	if m.policy.mode != agent.ModePlan {
		t.Fatalf("configured cycle should go manual → plan, got %v", m.policy.mode)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = updated.(Model)
	if m.policy.mode != agent.ModeManual {
		t.Fatalf("configured cycle should wrap plan → manual, got %v", m.policy.mode)
	}
}

func TestMode_SlashModeShowsAndSets(t *testing.T) {
	m := gatedModel(t, nil, nil)

	_, status := m.handleSlashCommand("/mode")
	if !strings.Contains(status, "Mode: manual") || !strings.Contains(status, "manual → accept-edits → auto → plan") {
		t.Fatalf("/mode should show the current mode and cycle, got %q", status)
	}

	handled, result := m.handleSlashCommand("/mode auto")
	if !handled || !strings.Contains(result, "Mode set to auto") {
		t.Fatalf("/mode auto should set the mode, got %q", result)
	}
	if m.policy.mode != agent.ModeAuto {
		t.Fatalf("mode should be auto after /mode auto, got %v", m.policy.mode)
	}

	_, bad := m.handleSlashCommand("/mode yolo")
	if !strings.Contains(bad, "unknown mode") {
		t.Fatalf("/mode with an unknown name should error, got %q", bad)
	}
	if m.policy.mode != agent.ModeAuto {
		t.Fatal("an invalid /mode must not change the mode")
	}

	_, help := m.handleSlashCommand("/help")
	if !strings.Contains(help, "mode:      auto") {
		t.Fatalf("/help should show the active mode, got:\n%s", help)
	}
}

func TestPolicy_StatusBarAndHelpReflectPolicy(t *testing.T) {
	m := gatedModel(t, nil, nil)
	if m.policyLabel() != "" {
		t.Fatalf("default policy should show no status segment, got %q", m.policyLabel())
	}
	_, help := m.handleSlashCommand("/help")
	if !strings.Contains(help, "Approval policy:") || !strings.Contains(help, "edits:     ask") {
		t.Fatalf("/help should describe the default ask-everything policy, got:\n%s", help)
	}

	m.policy.allEdits = true
	m = m.WithCommandAllowlist([]string{"git status"})
	if !strings.Contains(m.renderStatusBar(80), "auto: edits+allowlist") {
		t.Fatalf("status bar should show the active policy, got %q", m.renderStatusBar(80))
	}
	_, help = m.handleSlashCommand("/help")
	if !strings.Contains(help, "edits:     auto-allow (this session)") ||
		!strings.Contains(help, "1 command pattern(s)") {
		t.Fatalf("/help should reflect the loosened policy, got:\n%s", help)
	}
}

func TestReadOnlyCommandRunsWithoutPrompt(t *testing.T) {
	var ran []string
	m := execModel(t, &ran)
	m.policy.mode = agent.ModeManual
	m.state = stateStreaming

	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_ro", Name: "execute_command", Arguments: `{"command":"git status"}`},
	}})
	m = updated.(Model)
	if m.state != stateRunningCmd {
		t.Fatalf("an inspection command should run without prompting, got state %d", m.state)
	}
	updated, _ = m.Update(driveCmdDone(t, cmd))
	m = updated.(Model)
	if len(ran) != 1 || ran[0] != "git status" {
		t.Fatalf("expected the inspection command to run, got %v", ran)
	}
	found := false
	for _, e := range m.transcript {
		if e.kind == entrySystem && strings.Contains(e.text, "Auto-approved (read-only)") {
			found = true
		}
	}
	if !found {
		t.Fatal("the transcript should say why the command ran")
	}
}

func TestReadOnlyAutoDisabledPrompts(t *testing.T) {
	var ran []string
	m := execModel(t, &ran).WithReadOnlyCommands(nil, true)
	m.policy.mode = agent.ModeManual
	m.state = stateStreaming

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_ro", Name: "execute_command", Arguments: `{"command":"git status"}`},
	}})
	if updated.(Model).state != stateConfirmRun {
		t.Fatal("read_only_auto=false should restore the prompt")
	}
}

func TestModelDefaults(t *testing.T) {
	var wrote [][2]string
	m := New(nil, mockStream).
		WithConfigWriter(func(key, value string) error {
			wrote = append(wrote, [2]string{key, value})
			return nil
		}).
		WithDefaults(Defaults{Model: "gpt-4o"})
	m.modelName = "gpt-4o"

	if _, out := m.handleSlashCommand("/model default"); !strings.Contains(out, "gpt-4o") {
		t.Fatalf("bare /model default should report the setting, got %q", out)
	}
	if _, out := m.handleSlashCommand("/model agents"); !strings.Contains(out, "not set") {
		t.Fatalf("an unset agent model should say so, got %q", out)
	}
	if _, out := m.handleSlashCommand("/model default o3"); !strings.Contains(out, "o3") {
		t.Fatalf("unexpected output: %q", out)
	}
	if _, out := m.handleSlashCommand("/model agents claude-haiku-4-5"); !strings.Contains(out, "claude-haiku-4-5") {
		t.Fatalf("unexpected output: %q", out)
	}
	want := [][2]string{{"provider.model", "o3"}, {"agents.model", "claude-haiku-4-5"}}
	if len(wrote) != 2 || wrote[0] != want[0] || wrote[1] != want[1] {
		t.Fatalf("persisted %v, want %v", wrote, want)
	}
	// A session with no writer says so instead of pretending it stuck.
	plain := New(nil, mockStream)
	if _, out := plain.handleSlashCommand("/model default o3"); !strings.Contains(out, "cannot write") {
		t.Fatalf("expected a not-persisted notice, got %q", out)
	}
}

// The grant ladder. [a] used to have one rung above "this once" —
// every command, or every edit, for the rest of the session — and no way
// down: switching back to manual mode did not clear it, because the grant is
// consulted before the mode is. These hold the two ends of the fix.

func TestGrant_ADifferentShapeOfCommandStillAsks(t *testing.T) {
	var ran []string
	m := execModel(t, &ran)

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_1", Name: "execute_command", Arguments: `{"command":"go build ./one"}`},
		{ID: "call_2", Name: "execute_command", Arguments: `{"command":"npm publish"}`},
	}})
	m = handover(t, updated.(Model))

	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	m = updated.(Model)
	updated, _ = m.Update(driveCmdDone(t, cmd))
	m = updated.(Model)

	// `go build` was granted; `npm publish` is a different thing entirely,
	// and the whole point of a scoped grant is that it still asks.
	if m.state != stateConfirmRun {
		t.Fatalf("a command outside the grant should still prompt, got state %d", m.state)
	}
	if len(ran) != 1 {
		t.Fatalf("only the granted command should have run, got %v", ran)
	}
}

func TestGrant_ADifferentDirectoryStillAsks(t *testing.T) {
	m := gatedModel(t, nil, nil)
	root := t.TempDir()
	inside := filepath.Join(root, "kept", "a.txt")
	beside := filepath.Join(root, "other", "b.txt")
	if err := os.MkdirAll(filepath.Dir(inside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(beside), 0o755); err != nil {
		t.Fatal(err)
	}

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_1", Name: "write_file", Arguments: fmt.Sprintf(`{"path":%q,"content":"one\n"}`, inside)},
		{ID: "call_2", Name: "write_file", Arguments: fmt.Sprintf(`{"path":%q,"content":"two\n"}`, beside)},
	}})
	m = handover(t, updated.(Model))

	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	m = updated.(Model)
	var done approvedToolDoneMsg
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(approvedToolDoneMsg); ok {
			done = msg
		}
	}
	updated, _ = m.Update(done)
	m = updated.(Model)

	if m.state != stateConfirmRun {
		t.Fatalf("an edit outside the granted directory should still prompt, got state %d", m.state)
	}
}

func TestGrant_RevokeTakesThemBack(t *testing.T) {
	m := readyModel(t)

	if got := m.allowCommand([]string{"commands"}); !strings.Contains(got, "without asking") {
		t.Fatalf("/permissions allow commands should grant them, said %q", got)
	}
	m.policy.commands = append(m.policy.commands, "go build")
	m.policy.editDirs = append(m.policy.editDirs, "internal/ui")
	if !m.policy.allCommands {
		t.Fatal("the blanket command grant should be on")
	}

	status := m.grantStatus()
	for _, want := range []string{"every command", "go build", "internal/ui/"} {
		if !strings.Contains(status, want) {
			t.Fatalf("/permissions grants should name %q:\n%s", want, status)
		}
	}

	said := m.revokeCommand(nil)
	if !strings.Contains(said, "every command") || !strings.Contains(said, "go build") {
		t.Fatalf("revoke should name what it took back, said %q", said)
	}
	if m.grants().Any() {
		t.Fatalf("revoke should leave nothing granted, got %+v", m.grants())
	}
	if got := m.modePolicy(); got.AllowCommands || len(got.EditDirs) > 0 || len(got.CommandAllowlist) != len(m.policy.allowlist) {
		t.Fatalf("the policy should be back to asking, got %+v", got)
	}
}

func TestGrant_RevokeOneCategoryLeavesTheOther(t *testing.T) {
	m := readyModel(t)
	m.policy.commands = []string{"go build"}
	m.policy.editDirs = []string{"internal/ui"}

	m.revokeCommand([]string{"edits"})
	if len(m.policy.editDirs) != 0 {
		t.Fatal("the edit grants should be gone")
	}
	if len(m.policy.commands) != 1 {
		t.Fatalf("the command grants should be untouched, got %v", m.policy.commands)
	}
}

func TestGrant_ConfigsAllowlistIsNotTheSessionsToRevoke(t *testing.T) {
	m := readyModel(t).WithCommandAllowlist([]string{"make"})
	m.policy.commands = []string{"go build"}

	m.revokeCommand(nil)
	if got := m.allowlist(); len(got) != 1 || got[0] != "make" {
		t.Fatalf("config's own allowlist should survive a revoke, got %v", got)
	}
}
