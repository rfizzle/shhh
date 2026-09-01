package chat

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

func TestStatsReport_BreakdownAndSpend(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: strings.Repeat("s", 400)},
		{Role: provider.RoleUser, Content: strings.Repeat("u", 40)},
		{Role: provider.RoleAssistant, Content: strings.Repeat("a", 40)},
		{Role: provider.RoleTool, Content: strings.Repeat("t", 80), ToolCallID: "1"},
	}
	m := New(msgs, mockStream).WithToolTokenEstimate(50)
	m.TotalTokensIn = 1200
	m.TotalTokensOut = 300
	m.turnCount = 2

	out := m.statsReport()
	for _, want := range []string{
		"Context occupancy",
		"system prompt", "tool definitions", "messages", "tool results",
		"Session spend", "Turns: 2",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stats report missing %q:\n%s", want, out)
		}
	}
	// 400 chars ≈ 100 tokens for the system prompt.
	if !strings.Contains(out, "system prompt     ~100") {
		t.Fatalf("expected system prompt estimate ~100:\n%s", out)
	}
	if !strings.Contains(out, "tool definitions  ~50") {
		t.Fatalf("expected tool definition estimate ~50:\n%s", out)
	}
}

func TestSlashStats_Handled(t *testing.T) {
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream)
	handled, result := m.handleSlashCommand("/stats")
	if !handled {
		t.Fatal("/stats should be handled")
	}
	if !strings.Contains(result, "Context occupancy") {
		t.Fatalf("unexpected /stats output: %s", result)
	}
}

func TestHelp_ListsStats(t *testing.T) {
	if !strings.Contains(helpText(), "/stats") {
		t.Fatal("help should list /stats")
	}
}
