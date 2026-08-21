package chat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
)

// planModel is a ready model sitting in plan mode with a streamed planning
// response about to complete.
func planModel(t *testing.T, stream StreamFunc) Model {
	t.Helper()
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "plan the change"},
	}
	m := New(msgs, stream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.mode = agent.ModePlan
	m.state = stateStreaming
	m.streaming = "1. edit a.go\n2. run tests"
	return m
}

// recordingStream captures the message list of each stream request.
func recordingStream(captured *[]provider.Message) StreamFunc {
	return func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		*captured = msgs
		ch := make(chan provider.StreamEvent)
		close(ch)
		_, cancel := context.WithCancel(context.Background())
		return ch, cancel, nil
	}
}

// driveStream runs every cmd in a possibly-batched tea.Cmd so the stream
// request actually fires.
func driveStream(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a command to run")
	}
	for _, c := range unwrapBatch(cmd) {
		c()
	}
}

func TestPlan_ApprovalPromptAfterPlanningResponse(t *testing.T) {
	m := planModel(t, mockStream)
	updated, _ := m.Update(doneMsg{})
	m = updated.(Model)

	if m.state != statePlanApprove {
		t.Fatalf("a completed planning response should enter the plan-approval prompt, got state %d", m.state)
	}
	view := m.View()
	if !strings.Contains(view, "Plan ready — how should I proceed?") {
		t.Fatalf("view should show the plan-approval prompt, got:\n%s", view)
	}
	for _, opt := range planApproveOptions {
		if !strings.Contains(view, opt) {
			t.Errorf("view should list option %q", opt)
		}
	}
}

func TestPlan_EmptyResponseSkipsPrompt(t *testing.T) {
	m := planModel(t, mockStream)
	m.streaming = ""
	updated, _ := m.Update(doneMsg{})
	m = updated.(Model)
	if m.state != stateInput {
		t.Fatalf("an empty response should not offer plan approval, got state %d", m.state)
	}
}

func TestPlan_NonPlanModeSkipsPrompt(t *testing.T) {
	m := planModel(t, mockStream)
	m.mode = agent.ModeManual
	updated, _ := m.Update(doneMsg{})
	m = updated.(Model)
	if m.state != stateInput {
		t.Fatalf("modes other than plan should finish the turn normally, got state %d", m.state)
	}
}

func TestPlan_SteeringTakesPrecedenceOverPrompt(t *testing.T) {
	m := planModel(t, mockStream)
	m.steering = []string{"also cover the CLI"}
	updated, _ := m.Update(doneMsg{})
	m = updated.(Model)
	if m.state != stateStreaming {
		t.Fatalf("queued steering should continue planning, got state %d", m.state)
	}
	if m.mode != agent.ModePlan {
		t.Fatal("steering must not leave plan mode")
	}
}

func TestPlan_ApproveExecutesInChosenMode(t *testing.T) {
	var captured []provider.Message
	m := planModel(t, recordingStream(&captured))
	updated, _ := m.Update(doneMsg{})
	m = updated.(Model)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = updated.(Model)
	if m.mode != agent.ModeAcceptEdits {
		t.Fatalf("option 1 should switch to accept-edits, got %v", m.mode)
	}
	if m.state != stateStreaming {
		t.Fatalf("approval should continue straight into execution, got state %d", m.state)
	}
	msgs := m.Messages()
	last := msgs[len(msgs)-1]
	if last.Role != provider.RoleUser || last.Content != planApprovedMessage {
		t.Fatalf("approval should append the execution turn, got %+v", last)
	}
	// The plan itself stays in context: same session, same conversation.
	foundPlan := false
	for _, msg := range msgs {
		if msg.Role == provider.RoleAssistant && strings.Contains(msg.Content, "edit a.go") {
			foundPlan = true
		}
	}
	if !foundPlan {
		t.Fatal("the plan must remain in the conversation after approval")
	}
	driveStream(t, cmd)
	if len(captured) == 0 {
		t.Fatal("approval should open the next stream request")
	}
	if strings.Contains(captured[0].Content, "# Plan mode") {
		t.Fatal("execution requests must not carry the planning instructions")
	}
	noted := false
	for _, e := range m.transcript {
		if e.kind == entrySystem && strings.Contains(e.text, "Plan approved — executing in accept-edits mode") {
			noted = true
		}
	}
	if !noted {
		t.Fatal("transcript should note the approval and chosen mode")
	}
}

func TestPlan_ApproveOtherModes(t *testing.T) {
	cases := []struct {
		key  rune
		want agent.Mode
	}{
		{'2', agent.ModeAuto},
		{'3', agent.ModeManual},
	}
	for _, c := range cases {
		m := planModel(t, mockStream)
		updated, _ := m.Update(doneMsg{})
		m = updated.(Model)
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{c.key}})
		m = updated.(Model)
		if m.mode != c.want || m.state != stateStreaming {
			t.Errorf("option %c: mode %v state %d, want mode %v streaming", c.key, m.mode, m.state, c.want)
		}
	}
}

func TestPlan_KeepPlanningReturnsToInput(t *testing.T) {
	m := planModel(t, mockStream)
	updated, _ := m.Update(doneMsg{})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.state != stateInput {
		t.Fatalf("esc should dismiss to keep planning, got state %d", m.state)
	}
	if m.mode != agent.ModePlan {
		t.Fatal("keep planning must stay in plan mode")
	}
	found := false
	for _, e := range m.transcript {
		if e.kind == entrySystem && strings.Contains(e.text, "Keep planning") {
			found = true
		}
	}
	if !found {
		t.Fatal("transcript should note the keep-planning choice")
	}
}

func TestPlan_RejectStaysInPlanMode(t *testing.T) {
	m := planModel(t, mockStream)
	updated, _ := m.Update(doneMsg{})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	m = updated.(Model)
	if m.state != stateInput || m.mode != agent.ModePlan {
		t.Fatalf("reject should return to input in plan mode, got state %d mode %v", m.state, m.mode)
	}
	found := false
	for _, e := range m.transcript {
		if e.kind == entrySystem && strings.Contains(e.text, "Plan rejected") {
			found = true
		}
	}
	if !found {
		t.Fatal("transcript should note the rejection")
	}
}

func TestPlan_NavigationAndEnterSelect(t *testing.T) {
	m := planModel(t, mockStream)
	updated, _ := m.Update(doneMsg{})
	m = updated.(Model)

	// k at the top stays put.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = updated.(Model)
	if m.planChoice != 0 {
		t.Fatalf("k at the top should stay at 0, got %d", m.planChoice)
	}
	for range 10 {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		m = updated.(Model)
	}
	if m.planChoice != len(planApproveOptions)-1 {
		t.Fatalf("j should clamp at the last option, got %d", m.planChoice)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	if m.planChoice != 2 {
		t.Fatalf("expected focus on option 3, got %d", m.planChoice)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.mode != agent.ModeManual || m.state != stateStreaming {
		t.Fatalf("enter should select the focused option (manual execution), got mode %v state %d", m.mode, m.state)
	}
}

func TestPlan_RequestStreamInjectsInstructions(t *testing.T) {
	var captured []provider.Message
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, recordingStream(&captured))
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.mode = agent.ModePlan

	updated, cmd := m.sendUserMessage("plan the change")
	m = updated.(Model)
	driveStream(t, cmd)

	if len(captured) == 0 || !strings.Contains(captured[0].Content, "# Plan mode") {
		t.Fatal("plan-mode requests should carry the planning instructions in the system prompt")
	}
	if strings.Contains(m.Messages()[0].Content, "# Plan mode") {
		t.Fatal("the stored conversation's system prompt must stay untouched")
	}
}

func TestPlan_InspectionCommandRunsWithoutPrompt(t *testing.T) {
	var ran []string
	m := execModel(t, &ran)
	m.mode = agent.ModePlan

	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_i", Name: "execute_command", Arguments: `{"command":"git status"}`},
	}})
	m = updated.(Model)
	if m.state != stateRunningCmd {
		t.Fatalf("plan mode should run inspection commands without a prompt, got state %d", m.state)
	}
	updated, _ = m.Update(driveCmdDone(t, cmd))
	m = updated.(Model)
	if len(ran) != 1 || ran[0] != "git status" {
		t.Fatalf("expected git status to run, got %v", ran)
	}
	found := false
	for _, e := range m.transcript {
		if e.kind == entrySystem && strings.Contains(e.text, "Auto-approved (plan mode inspection): git status") {
			found = true
		}
	}
	if !found {
		t.Fatal("transcript should note the inspection auto-approval")
	}
}

func TestPlan_SlashPlanSave(t *testing.T) {
	t.Chdir(t.TempDir())
	m := runCapableModel("1. edit a.go\n2. run tests")

	handled, result := m.handleSlashCommand("/plan save my plan")
	if !handled || !strings.Contains(result, "Plan saved to") {
		t.Fatalf("/plan save should save, got %q", result)
	}
	path := filepath.Join(".shhh", "plans", "my-plan.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
	if !strings.Contains(string(data), "edit a.go") {
		t.Fatalf("saved plan should contain the plan text, got %q", data)
	}

	// A default name is generated when none is given.
	_, result = m.handleSlashCommand("/plan save")
	if !strings.Contains(result, "Plan saved to") {
		t.Fatalf("/plan save without a name should still save, got %q", result)
	}

	_, usage := m.handleSlashCommand("/plan")
	if !strings.Contains(usage, "Usage: /plan save") {
		t.Fatalf("/plan alone should show usage, got %q", usage)
	}
}

func TestPlan_SlashPlanSaveWithoutPlan(t *testing.T) {
	t.Chdir(t.TempDir())
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)
	_, result := m.handleSlashCommand("/plan save x")
	if !strings.Contains(result, "No plan to save yet") {
		t.Fatalf("saving with no assistant response should refuse, got %q", result)
	}
}

func TestSanitizePlanName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"my-plan", "my-plan"},
		{"my plan", "my-plan"},
		{"../../etc/passwd", "etc-passwd"},
		{"Plan_2.0", "Plan_2.0"},
	}
	for _, c := range cases {
		if got := sanitizePlanName(c.in); got != c.want {
			t.Errorf("sanitizePlanName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := sanitizePlanName("///"); !strings.HasPrefix(got, "plan-") {
		t.Errorf("all-unsafe name should fall back to a timestamp, got %q", got)
	}
}
