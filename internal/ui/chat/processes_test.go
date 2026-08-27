package chat

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
)

// processModel is gatedModel with the process supervisor wired (S-073).
func processModel(t *testing.T, executor ToolExecutor) Model {
	t.Helper()
	m := gatedModel(t, executor, nil)
	return m.WithProcesses(Processes{Manage: func(args []string) string { return "process list" }})
}

func startCall(id, name, command string) provider.ToolCall {
	args, _ := json.Marshal(map[string]string{"action": "start", "name": name, "command": command})
	return provider.ToolCall{ID: id, Name: "process", Arguments: string(args)}
}

func TestProcessTool_OnlyStartRequiresApproval(t *testing.T) {
	m := processModel(t, nil)
	if !m.requiresApproval(startCall("c1", "web", "npm run dev")) {
		t.Fatal("a process start must be approval-gated")
	}
	for _, action := range []string{"status", "read", "input", "stop"} {
		tc := provider.ToolCall{Name: "process", Arguments: `{"action":"` + action + `","name":"web"}`}
		if m.requiresApproval(tc) {
			t.Errorf("process %s must auto-run without approval", action)
		}
	}
	if !m.requiresApproval(provider.ToolCall{Name: "process", Arguments: "not json"}) {
		t.Fatal("unparsable process arguments must fail closed into the approval queue")
	}
	// Without the supervisor wired, the process tool is not specially gated.
	bare := gatedModel(t, nil, nil)
	if bare.requiresApproval(startCall("c1", "web", "npm run dev")) {
		t.Fatal("an unwired process tool must not be gated by the chat model")
	}
}

func TestProcessTool_StartApprovalFlow(t *testing.T) {
	var executed []string
	executor := func(name string, args json.RawMessage) (string, error) {
		executed = append(executed, name)
		return "process web: running (pid 1)", nil
	}
	m := processModel(t, executor)

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{startCall("call_p", "web", "npm run dev")}})
	m = updated.(Model)

	if m.state != stateConfirmRun {
		t.Fatalf("a process start should enter confirm state, got %d", m.state)
	}
	if len(executed) != 0 {
		t.Fatal("the start must not run before approval")
	}
	view := m.View()
	if !strings.Contains(view, "start process web") {
		t.Fatalf("confirm prompt should name the process start, got %q", view)
	}
	if !strings.Contains(view, "npm run dev") {
		t.Fatal("confirm prompt should show the command")
	}

	m = handover(t, m)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
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
	if len(executed) != 1 || executed[0] != "process" {
		t.Fatalf("executor should have run the process tool once, got %v", executed)
	}
	updated, _ = m.Update(done)
	m = updated.(Model)
	last := m.Messages()[len(m.Messages())-1]
	if last.Role != provider.RoleTool || last.ToolCallID != "call_p" || !strings.Contains(last.Content, "running") {
		t.Fatalf("expected the start result recorded, got %+v", last)
	}
}

func TestProcessTool_StartHonorsCommandAllowlist(t *testing.T) {
	executor := func(name string, args json.RawMessage) (string, error) { return "ok", nil }
	m := processModel(t, executor).WithCommandAllowlist([]string{"npm run dev"})

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{startCall("call_p", "web", "npm run dev")}})
	m = updated.(Model)

	if m.state != stateRunningCmd {
		t.Fatalf("an allowlisted start should auto-approve like a command, got state %d", m.state)
	}
	found := false
	for _, e := range m.transcript {
		if e.kind == entrySystem && strings.Contains(e.text, "Auto-approved (allowlist)") {
			found = true
		}
	}
	if !found {
		t.Fatal("transcript should note the allowlist auto-approval")
	}
}

func TestProcessTool_SafetyFlaggedStartAlwaysAsks(t *testing.T) {
	m := processModel(t, nil)
	m.allowAllCommands = true // even a blanket session grant must not skip this

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{startCall("call_p", "wipe", "rm -rf /tmp/x")}})
	m = updated.(Model)

	if m.state != stateConfirmRun {
		t.Fatalf("a safety-flagged start must prompt, got state %d", m.state)
	}
	if !strings.Contains(m.View(), "⚠") {
		t.Fatal("confirm prompt should show the safety warning")
	}
}

func TestProcessTool_PlanModeRefusesStart(t *testing.T) {
	m := processModel(t, nil)
	m.mode = agent.ModePlan

	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{startCall("call_p", "web", "npm run dev")}})
	m = updated.(Model)

	last := m.Messages()[len(m.Messages())-1]
	if last.Role != provider.RoleTool || !strings.Contains(last.Content, "plan mode") {
		t.Fatalf("plan mode should refuse the start with an explanatory result, got %+v", last)
	}
}

func TestSlashPS(t *testing.T) {
	m := processModel(t, nil)
	handled, result := m.handleSlashCommand("/ps")
	if !handled || result != "process list" {
		t.Fatalf("/ps should report the process list, got handled=%v %q", handled, result)
	}

	bare := gatedModel(t, nil, nil)
	handled, result = bare.handleSlashCommand("/ps")
	if !handled || !strings.Contains(result, "unavailable") {
		t.Fatalf("/ps without a supervisor should say it is unavailable, got %q", result)
	}
}
