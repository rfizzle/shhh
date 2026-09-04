package chat

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
)

// markerReduce is a stand-in reduction pipeline that tags what it processed.
func markerReduce(tool, result string) string {
	return "[reduced " + tool + "]\n" + result
}

func TestEvidence_ExecResultReducedConsistently(t *testing.T) {
	var ran []string
	m := execModel(t, &ran).
		WithCommandAllowlist([]string{"echo"}).
		WithEvidence(Evidence{Reduce: markerReduce})

	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_x", Name: "execute_command", Arguments: `{"command":"echo hi"}`},
	}})
	m = updated.(Model)
	updated, _ = m.Update(driveCmdDone(t, cmd))
	m = updated.(Model)

	last := m.Messages()[len(m.Messages())-1]
	if last.Role != provider.RoleTool || !strings.Contains(last.Content, "[reduced execute_command]") {
		t.Fatalf("exec tool result should be reduced, got %+v", last)
	}
	// Display consistency: the transcript entry carries the same reduced view.
	found := false
	for _, e := range m.transcript {
		if e.kind == entryCommand && strings.Contains(e.toolResult, "[reduced execute_command]") {
			found = true
		}
	}
	if !found {
		t.Fatal("transcript must show the same reduced view the model got")
	}
}

func TestEvidence_UserRunNotReduced(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleAssistant, Content: "```\necho hi\n```"},
	}
	m := New(msgs, mockStream).
		WithRunner(func(ctx context.Context, cmd string) (string, int) { return "ok", 0 }).
		WithEvidence(Evidence{Reduce: markerReduce})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	updated, _ = m.Update(cmdDoneMsg{command: "echo hi", output: "ok", exitCode: 0})
	m = updated.(Model)
	for _, e := range m.transcript {
		if strings.Contains(e.toolResult, "[reduced") {
			t.Fatal("/run output — the user's own command — must not be reduced")
		}
	}
}

func TestEvidence_SlashCommand(t *testing.T) {
	var got []string
	m := gatedModel(t, nil, nil).WithEvidence(Evidence{
		Manage: func(args []string) string {
			got = append(got, strings.Join(args, " "))
			return "status here"
		},
	})
	handled, result := m.handleSlashCommand("/evidence")
	if !handled || result != "status here" {
		t.Fatalf("/evidence = %v %q", handled, result)
	}
	m.handleSlashCommand("/evidence purge")
	if len(got) != 2 || got[1] != "purge" {
		t.Fatalf("manage args = %v", got)
	}
}

func TestEvidence_SlashCommandUnavailable(t *testing.T) {
	m := gatedModel(t, nil, nil)
	handled, result := m.handleSlashCommand("/evidence")
	if !handled || !strings.Contains(result, "unavailable") {
		t.Fatalf("/evidence without a store = %v %q", handled, result)
	}
}

func TestEvidence_HelpMentionsCommand(t *testing.T) {
	m := gatedModel(t, nil, nil).WithEvidence(Evidence{Manage: func([]string) string { return "" }})
	if !strings.Contains(helpText(&m), "/evidence") {
		t.Fatal("/help must list /evidence")
	}
}
