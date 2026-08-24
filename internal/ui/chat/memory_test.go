package chat

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/memory"
	"github.com/rfizzle/shhh/internal/provider"
)

type savedMemory struct {
	scope, kind, text string
}

// memoryModel builds a model with durable memory wired and a remember call
// mid-stream, recording saves into the returned slice.
func memoryModel(t *testing.T, mode agent.Mode) (Model, *[]savedMemory) {
	t.Helper()
	var saves []savedMemory
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "do the thing"},
	}
	m := New(msgs, mockStream).
		WithToolExecutor(func(name string, args json.RawMessage) (string, error) {
			t.Fatalf("the remember tool must never reach the executor, got %s", name)
			return "", nil
		}).
		WithMemory(Memory{
			Manage: func(args []string) string { return "managed:" + strings.Join(args, ",") },
			Save: func(scope, kind, text string) (string, error) {
				saves = append(saves, savedMemory{scope, kind, text})
				return "Saved memory [m1] (" + memory.ScopeLabel(scope) + " " + kind + "): " + text, nil
			},
			ProjectScope: "/proj",
		})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.mode = mode
	m.state = stateStreaming
	return m, &saves
}

func rememberCall() toolCallsMsg {
	return toolCallsMsg{calls: []provider.ToolCall{{
		ID:        "call_m",
		Name:      memory.RememberToolName,
		Arguments: `{"text":"prefers table-driven tests","kind":"convention"}`,
	}}}
}

func TestRemember_AlwaysPromptsEvenInPermissiveModes(t *testing.T) {
	for _, mode := range []agent.Mode{agent.ModeManual, agent.ModeAcceptEdits, agent.ModeAuto} {
		m, saves := memoryModel(t, mode)
		updated, _ := m.Update(rememberCall())
		m = updated.(Model)

		if m.state != stateConfirmRun {
			t.Fatalf("%s: a remember call must prompt, got state %d", mode, m.state)
		}
		if m.memoryAsk == nil {
			t.Fatalf("%s: expected the memory prompt, not the generic card", mode)
		}
		if len(*saves) != 0 {
			t.Fatalf("%s: nothing may persist before confirmation", mode)
		}
		view := m.View()
		if !strings.Contains(view, "Remember this?") {
			t.Fatalf("%s: prompt should render the memory card:\n%s", mode, view)
		}
		if !strings.Contains(view, "prefers table-driven tests") {
			t.Fatalf("%s: prompt should show the proposed text", mode)
		}
	}
}

func TestRemember_SaveProjectScope(t *testing.T) {
	m, saves := memoryModel(t, agent.ModeManual)
	updated, _ := m.Update(rememberCall())
	m = updated.(Model)

	// Option 1 ("Save (project)") is focused by default; enter confirms.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if len(*saves) != 1 {
		t.Fatalf("expected one save, got %d", len(*saves))
	}
	got := (*saves)[0]
	if got.scope != "/proj" || got.kind != "convention" || got.text != "prefers table-driven tests" {
		t.Fatalf("save mismatch: %+v", got)
	}
	if m.memoryAsk != nil || m.pendingApproval != nil {
		t.Fatal("prompt state should be cleared after saving")
	}
	last := m.Messages()[len(m.Messages())-1]
	if last.Role != provider.RoleTool || last.ToolCallID != "call_m" || !strings.Contains(last.Content, "Saved memory [m1]") {
		t.Fatalf("expected the saved-entry tool result, got %+v", last)
	}
	if m.state != stateStreaming {
		t.Fatalf("stream should resume after the save, got state %d", m.state)
	}
}

func TestRemember_SaveGlobalWithNote(t *testing.T) {
	m, saves := memoryModel(t, agent.ModeManual)
	updated, _ := m.Update(rememberCall())
	m = updated.(Model)

	// Jump to option 2 ("Save (global)"), tab into the note, type, confirm.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Go only")})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if m.memoryAsk != nil || m.state != stateStreaming {
		t.Fatalf("prompt should close and the stream resume, got ask=%v state=%d", m.memoryAsk != nil, m.state)
	}
	if len(*saves) != 1 {
		t.Fatalf("expected one save, got %d", len(*saves))
	}
	got := (*saves)[0]
	if got.scope != memory.GlobalScope {
		t.Fatalf("expected global scope, got %q", got.scope)
	}
	if got.text != "prefers table-driven tests (Go only)" {
		t.Fatalf("the note should amend the entry, got %q", got.text)
	}
}

func TestRemember_Declined(t *testing.T) {
	m, saves := memoryModel(t, agent.ModeManual)
	updated, _ := m.Update(rememberCall())
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)

	if len(*saves) != 0 {
		t.Fatal("declining must not persist anything")
	}
	last := m.Messages()[len(m.Messages())-1]
	if last.Role != provider.RoleTool || !strings.Contains(last.Content, "declined to save this memory") {
		t.Fatalf("expected a decline tool result, got %+v", last)
	}
	if m.state != stateStreaming {
		t.Fatalf("stream should resume after the decline, got state %d", m.state)
	}
}

func TestRemember_PlanModeRefuses(t *testing.T) {
	m, saves := memoryModel(t, agent.ModePlan)
	updated, _ := m.Update(rememberCall())
	m = updated.(Model)

	if m.memoryAsk != nil {
		t.Fatal("plan mode must not open the memory prompt")
	}
	if len(*saves) != 0 {
		t.Fatal("plan mode must not persist anything")
	}
	var toolResult string
	for _, msg := range m.Messages() {
		if msg.Role == provider.RoleTool && msg.ToolCallID == "call_m" {
			toolResult = msg.Content
		}
	}
	if !strings.Contains(toolResult, "plan mode") {
		t.Fatalf("expected the plan-mode refusal result, got %q", toolResult)
	}
}

func TestRemember_InvalidArgumentsSkipped(t *testing.T) {
	m, saves := memoryModel(t, agent.ModeManual)
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{{
		ID: "call_bad", Name: memory.RememberToolName, Arguments: `{"text":"x","kind":"vibe"}`,
	}}})
	m = updated.(Model)

	if len(*saves) != 0 {
		t.Fatal("invalid proposals must not persist")
	}
	var toolResult string
	for _, msg := range m.Messages() {
		if msg.Role == provider.RoleTool && msg.ToolCallID == "call_bad" {
			toolResult = msg.Content
		}
	}
	if !strings.HasPrefix(toolResult, "error:") || !strings.Contains(toolResult, "kind") {
		t.Fatalf("expected an invalid-arguments error result, got %q", toolResult)
	}
}

func TestMemorySlashCommand(t *testing.T) {
	m, _ := memoryModel(t, agent.ModeManual)
	handled, out := m.handleSlashCommand("/memory forget m3")
	if !handled || out != "managed:forget,m3" {
		t.Fatalf("expected /memory to route to Manage, got handled=%v out=%q", handled, out)
	}

	bare := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream)
	handled, out = bare.handleSlashCommand("/memory")
	if !handled || !strings.Contains(out, "unavailable") {
		t.Fatalf("without wiring, /memory should report unavailable, got %q", out)
	}
}
