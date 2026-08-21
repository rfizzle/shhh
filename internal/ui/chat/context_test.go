package chat

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/provider"
)

func TestContextWindow_DefaultAndTable(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)
	if got := m.contextWindow(); got != DefaultContextWindow {
		t.Fatalf("without pricing, want DefaultContextWindow (%d), got %d", DefaultContextWindow, got)
	}

	table := pricing.NewTable(map[string]pricing.ModelPricing{
		"gpt-4o": {MaxInputTokens: 128000},
	})
	m = m.WithPricing(table, "gpt-4o")
	if got := m.contextWindow(); got != 128000 {
		t.Fatalf("with table, want 128000, got %d", got)
	}

	m = m.WithPricing(table, "mystery-model")
	if got := m.contextWindow(); got != DefaultContextWindow {
		t.Fatalf("unknown model should fall back to default, got %d", got)
	}
}

func TestContextSeverity_Thresholds(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream) // default window 32768: warn 19660, trim 26214

	m.contextTokens = 1000
	if got := m.contextSeverity(); got != 0 {
		t.Fatalf("low usage should be severity 0, got %d", got)
	}
	m.contextTokens = m.warnThreshold()
	if got := m.contextSeverity(); got != 1 {
		t.Fatalf("at warn threshold want severity 1, got %d", got)
	}
	m.contextTokens = m.trimThreshold()
	if got := m.contextSeverity(); got != 2 {
		t.Fatalf("at trim threshold want severity 2, got %d", got)
	}

	if bar := m.renderStatusBar(80); strings.Contains(bar, "ctx ~") {
		t.Fatal("ctx indicator should not show before any usage totals")
	}
	m.TotalTokensIn = 100
	if bar := m.renderStatusBar(80); !strings.Contains(bar, "ctx ~") {
		t.Fatal("status bar should show the ctx indicator")
	}
}

func TestTrimContext_ElidesOldestToolResults(t *testing.T) {
	big := strings.Repeat("x", 40000) // ~10k estimated tokens
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "q1"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "c1", Name: "read_file"}}},
		{Role: provider.RoleTool, Content: big, ToolCallID: "c1"},
		{Role: provider.RoleAssistant, Content: "answer 1"},
		{Role: provider.RoleUser, Content: "q2"},
		{Role: provider.RoleTool, Content: "recent result", ToolCallID: "c2"},
	}, mockStream)
	m.contextTokens = 30000 // over the default trim threshold (26214)

	n := m.trimContext()
	if n != 1 {
		t.Fatalf("want 1 elided result, got %d", n)
	}
	if m.Messages()[3].Content != elidedResult {
		t.Fatalf("old tool result should be elided, got %q", m.Messages()[3].Content)
	}
	if m.Messages()[6].Content != "recent result" {
		t.Fatal("current-turn tool results must be protected")
	}
	if m.Messages()[1].Content != "q1" || m.Messages()[4].Content != "answer 1" {
		t.Fatal("user/assistant text must be kept")
	}
	if m.contextTokens >= 30000 {
		t.Fatalf("context estimate should drop after trimming, got %d", m.contextTokens)
	}
}

func TestTrimContext_NoopUnderThreshold(t *testing.T) {
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "q1"},
		{Role: provider.RoleTool, Content: "small result", ToolCallID: "c1"},
		{Role: provider.RoleUser, Content: "q2"},
	}, mockStream)
	m.contextTokens = 1000

	if n := m.trimContext(); n != 0 {
		t.Fatalf("under the threshold nothing should be trimmed, got %d", n)
	}
	if m.Messages()[2].Content != "small result" {
		t.Fatal("tool result should be untouched under the threshold")
	}
}

func TestSendUserMessage_TrimsAndNotes(t *testing.T) {
	big := strings.Repeat("y", 60000)
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "q1"},
		{Role: provider.RoleTool, Content: big, ToolCallID: "c1"},
	}, mockStream)
	m.contextTokens = 30000

	m = sendText(t, m, "next question")

	if m.Messages()[2].Content != elidedResult {
		t.Fatalf("old tool result should be elided before the request, got %d chars", len(m.Messages()[2].Content))
	}
	var noted bool
	for _, e := range m.transcript {
		if e.kind == entrySystem && strings.Contains(e.text, "Context trimmed") {
			noted = true
		}
	}
	if !noted {
		t.Fatal("trimming should leave a system notice in the transcript")
	}
	if m.state != stateStreaming {
		t.Fatalf("send should still stream, got state %d", m.state)
	}
}

// driveCompact submits /compact and runs the returned commands until the
// stream completes, returning the final model.
func driveCompact(t *testing.T, m Model) Model {
	t.Helper()
	m.input.SetValue("/compact")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if !m.compacting || m.state != stateStreaming {
		t.Fatalf("/compact should enter a compacting stream, compacting=%v state=%d", m.compacting, m.state)
	}
	if cmd == nil {
		t.Fatal("/compact should return a stream cmd")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatal("expected a batched spinner+stream cmd")
	}
	var started *streamStartedMsg
	for _, c := range batch {
		if msg, ok := c().(streamStartedMsg); ok {
			started = &msg
		}
	}
	if started == nil {
		t.Fatal("no streamStartedMsg from the compact cmd")
	}
	updated, cmd = m.Update(*started)
	m = updated.(Model)
	for cmd != nil {
		msg := cmd()
		if msg == nil {
			break
		}
		updated, cmd = m.Update(msg)
		m = updated.(Model)
		if m.state == stateInput {
			break
		}
	}
	return m
}

func TestCompact_RestartsFromSummary(t *testing.T) {
	var gotReq []provider.Message
	stream := func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		gotReq = msgs
		ch := make(chan provider.StreamEvent, 2)
		ch <- provider.StreamEvent{Token: "the summary"}
		ch <- provider.StreamEvent{Done: true, Usage: &provider.Usage{PromptTokens: 500, CompletionTokens: 20}}
		close(ch)
		_, cancel := context.WithCancel(context.Background())
		return ch, cancel, nil
	}
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "question"},
		{Role: provider.RoleAssistant, Content: "answer"},
	}, stream)
	m.contextTokens = 5000

	m = driveCompact(t, m)

	if len(gotReq) != 4 || gotReq[3].Content != compactInstruction {
		t.Fatalf("summarize request should be conversation + instruction, got %d messages", len(gotReq))
	}
	if len(m.Messages()) != 2 {
		t.Fatalf("conversation should restart as system + summary, got %d messages", len(m.Messages()))
	}
	if m.Messages()[0].Role != provider.RoleSystem || m.Messages()[0].Content != "sys" {
		t.Fatal("system prompt must survive compaction")
	}
	if m.Messages()[1].Role != provider.RoleUser || !strings.Contains(m.Messages()[1].Content, "the summary") {
		t.Fatalf("summary message missing, got %+v", m.Messages()[1])
	}
	if m.compacting || m.state != stateInput {
		t.Fatalf("compaction should finish back at input, compacting=%v state=%d", m.compacting, m.state)
	}
	if want := estimateMessageTokens(m.Messages()); m.contextTokens != want {
		t.Fatalf("context estimate should reset to %d, got %d", want, m.contextTokens)
	}
	last := m.transcript[len(m.transcript)-1]
	if last.kind != entryAssistant || last.text != "the summary" {
		t.Fatalf("transcript should show the summary, got %+v", last)
	}
}

func TestCompact_NothingToCompact(t *testing.T) {
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream)
	m.input.SetValue("/compact")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if m.compacting || m.state != stateInput {
		t.Fatalf("nothing to compact should stay at input, compacting=%v state=%d", m.compacting, m.state)
	}
	last := m.transcript[len(m.transcript)-1]
	if last.kind != entrySystem || !strings.Contains(last.text, "Nothing to compact") {
		t.Fatalf("expected a nothing-to-compact notice, got %+v", last)
	}
}

func TestCompact_EmptySummaryKeepsConversation(t *testing.T) {
	stream := func(msgs []provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		ch := make(chan provider.StreamEvent, 1)
		ch <- provider.StreamEvent{Done: true}
		close(ch)
		_, cancel := context.WithCancel(context.Background())
		return ch, cancel, nil
	}
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "question"},
	}, stream)

	m = driveCompact(t, m)

	if len(m.Messages()) != 2 || m.Messages()[1].Content != "question" {
		t.Fatalf("empty summary must leave the conversation unchanged, got %+v", m.Messages())
	}
	last := m.transcript[len(m.transcript)-1]
	if last.kind != entryError || !strings.Contains(last.text, "no summary") {
		t.Fatalf("expected a no-summary error entry, got %+v", last)
	}
}

func TestCompact_CancelKeepsConversation(t *testing.T) {
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "question"},
	}, mockStream)
	m.input.SetValue("/compact")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	m.streaming = "partial sum"

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(Model)

	if m.compacting || m.state != stateInput {
		t.Fatalf("cancel should abort compaction, compacting=%v state=%d", m.compacting, m.state)
	}
	if len(m.Messages()) != 2 || m.Messages()[1].Content != "question" {
		t.Fatalf("cancel must leave the conversation unchanged, got %+v", m.Messages())
	}
	var cancelled bool
	for _, e := range m.transcript {
		if e.kind == entrySystem && strings.Contains(e.text, "Compaction cancelled") {
			cancelled = true
		}
	}
	if !cancelled {
		t.Fatal("expected a compaction-cancelled notice")
	}
}

func TestCompact_ToolCallsAbort(t *testing.T) {
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "question"},
	}, mockStream)
	m.input.SetValue("/compact")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	updated, _ = m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "c1", Name: "read_file", Arguments: `{"path":"a.go"}`},
	}})
	m = updated.(Model)

	if m.compacting || m.state != stateInput {
		t.Fatalf("tool calls should abort compaction, compacting=%v state=%d", m.compacting, m.state)
	}
	if len(m.Messages()) != 2 || m.Messages()[1].Content != "question" {
		t.Fatalf("aborted compaction must leave the conversation unchanged, got %+v", m.Messages())
	}
	last := m.transcript[len(m.transcript)-1]
	if last.kind != entryError || !strings.Contains(last.text, "compaction failed") {
		t.Fatalf("expected a compaction-failed entry, got %+v", last)
	}
}
