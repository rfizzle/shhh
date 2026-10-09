package chat

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/subagent"
)

// A gate row is read from the value its result carried and from nothing in
// its text: the text here is no gate's, and every row that came by a value —
// a reopened session's, a child's transcript's, a trimmed row — still reads.
func TestGate_NoRowReadsTheText(t *testing.T) {
	sum := quality.Summary{Suite: "default", Verdict: quality.VerdictFail, Passed: 1, Total: 3, Duration: "2s"}
	const text = "a sentence no reader of the text would recognise"

	t.Run("a reopened session", func(t *testing.T) {
		m := New(nil, mockStream, Wiring{})
		m.appendMessageEntries([]provider.Message{
			{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "g1", Name: quality.ToolName, Arguments: `{"action":"run"}`}}},
			{Role: provider.RoleTool, ToolCallID: "g1", Content: text, Value: sum.Value()},
		})
		got, ok := gateVerdict(lastGateRow(t, m))
		if !ok || got != sum {
			t.Errorf("gateVerdict = %+v, %v; want %+v", got, ok, sum)
		}
		if row := m.activityRowFor(lastGateRow(t, m)); row.Target != "quality gate · default · 3 checks" {
			t.Errorf("the receipt's subject = %q", row.Target)
		}
	})

	t.Run("a child's transcript", func(t *testing.T) {
		e := convertChildEntry(subagent.TranscriptEntry{Kind: subagent.EntryTool, Tool: quality.ToolName,
			Result: text, Value: sum.Value()})
		got, ok := gateVerdict(e)
		if !ok || got != sum {
			t.Errorf("gateVerdict = %+v, %v; want %+v", got, ok, sum)
		}
	})

	t.Run("a live result, with and without its value", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			value []byte
			gate  bool
		}{{"with", sum.Value(), true}, {"without", nil, false}} {
			m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream, Wiring{})
			updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
			m = updated.(Model)
			updated, _ = m.sendUserMessage("run the gate")
			m = updated.(Model)
			m.state = stateStreaming
			call := provider.ToolCall{ID: "g1", Name: quality.ToolName, Arguments: `{"action":"run"}`}
			m.agent.BeginToolRound("", []provider.ToolCall{call}, nil)
			updated, _ = m.Update(toolResultsMsg{runID: m.agent.RunID(), results: []agent.ToolResult{
				{Call: call, Result: `Quality gate "default": FAIL — 1/3 checks passed (2s)`, Value: tc.value},
			}})
			m = updated.(Model)
			if _, ok := gateVerdict(lastGateRow(t, m)); ok != tc.gate {
				t.Errorf("%s a value: gateVerdict ok = %v, want %v (a result with no value is the plain row)", tc.name, ok, tc.gate)
			}
		}
	})

	t.Run("a stored session from before the value", func(t *testing.T) {
		m := New(nil, mockStream, Wiring{})
		m.appendMessageEntries([]provider.Message{
			{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "g1", Name: quality.ToolName}}},
			{Role: provider.RoleTool, ToolCallID: "g1", Content: `Quality gate "default": FAIL — 1/3 checks passed (2s)`},
		})
		if _, ok := gateVerdict(lastGateRow(t, m)); ok {
			t.Error("a row with no value read the text")
		}
	})
}
