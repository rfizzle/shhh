package chat

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
)

func TestReasonCode_Mapping(t *testing.T) {
	cases := map[string]string{
		"accept-edits mode":            "mode-accept-edits",
		"auto mode":                    "mode-auto",
		"session policy":               "session-grant",
		"allowlist":                    "allowlist",
		"plan mode":                    "plan-mode",
		"plan mode inspection":         "plan-inspection",
		"rm -rf / looked really safe!": "other",
		"":                             "other",
	}
	for raw, want := range cases {
		if got := reasonCode(raw); got != want {
			t.Errorf("reasonCode(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestOutcomeFromResult(t *testing.T) {
	if got := outcomeFromResult("file contents"); got != outcomeOK {
		t.Fatalf("expected ok, got %q", got)
	}
	if got := outcomeFromResult("error: no such file"); got != outcomeError {
		t.Fatalf("expected error, got %q", got)
	}
}

func TestAskReason(t *testing.T) {
	if got := askReason(agent.Action{Kind: agent.ActionCommand, SafetyFlagged: true}); got != "safety" {
		t.Fatalf("expected safety, got %q", got)
	}
	if got := askReason(agent.Action{Kind: agent.ActionEdit}); got != "policy" {
		t.Fatalf("expected policy, got %q", got)
	}
}

func TestObserver_UsageReportsTurnsAndTotals(t *testing.T) {
	var gotTurns, gotIn, gotOut int64
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).
		WithObserver(Observer{Usage: func(turns, tokensIn, tokensOut int64) {
			gotTurns, gotIn, gotOut = turns, tokensIn, tokensOut
		}})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	model := updated.(Model)

	updated, _ = model.sendUserMessage("hello")
	model = updated.(Model)
	model.accumulateUsage(&provider.Usage{PromptTokens: 10, CompletionTokens: 5})

	if gotTurns != 1 || gotIn != 10 || gotOut != 5 {
		t.Fatalf("expected (1, 10, 5), got (%d, %d, %d)", gotTurns, gotIn, gotOut)
	}
}

func TestObserver_ToolEventsRecorded(t *testing.T) {
	var events []string
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).
		WithObserver(Observer{ToolCall: func(tool string, duration time.Duration, outcome string) {
			events = append(events, tool+":"+outcome)
		}})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	model := updated.(Model)
	model.state = stateStreaming

	updated, _ = model.Update(toolResultsMsg{runID: 0, results: []agent.ToolResult{
		{Call: provider.ToolCall{ID: "1", Name: "read_file"}, Result: "data", Duration: time.Millisecond},
		{Call: provider.ToolCall{ID: "2", Name: "search"}, Result: "error: bad pattern", Duration: time.Millisecond},
	}})
	_ = updated

	want := []string{"read_file:ok", "search:error"}
	if len(events) != len(want) {
		t.Fatalf("expected %d events, got %v", len(want), events)
	}
	for i, w := range want {
		if events[i] != w {
			t.Fatalf("event %d: expected %q, got %q", i, w, events[i])
		}
	}
}

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
