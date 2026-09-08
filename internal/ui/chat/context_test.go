package chat

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/digest"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/pricing"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/quality"
	"github.com/rfizzle/shhh/internal/ui/components"
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

	if bar := m.renderStatusBar(120); strings.Contains(bar, "ctx ") {
		t.Fatal("ctx meter should not show before any usage totals")
	}
	m.TotalTokensIn = 100
	if bar := m.renderStatusBar(120); !strings.Contains(bar, "ctx ") || !strings.Contains(bar, "%") {
		t.Fatalf("status bar should show the ctx meter, got %q", bar)
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

// trimFixture is a conversation four large tool results deep, all of them in
// closed turns and so all of them eligible. The default window puts the
// threshold at 26214 and the mark at 19660, which the fixture is comfortably
// over.
func trimFixture(t *testing.T) Model {
	t.Helper()
	big := strings.Repeat("x", 40000) // ~10k estimated tokens each
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "q1"},
	}
	for _, id := range []string{"c1", "c2", "c3", "c4"} {
		msgs = append(msgs,
			provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: id, Name: "read_file"}}},
			provider.Message{Role: provider.RoleTool, Content: big, ToolCallID: id})
	}
	msgs = append(msgs,
		provider.Message{Role: provider.RoleAssistant, Content: "answer 1"},
		provider.Message{Role: provider.RoleUser, Content: "q2"})
	return New(msgs, mockStream)
}

// TestTrimContext_TrimsOnceAcrossTwoRequests is the behaviour the low-water
// mark buys. The first trim runs well past the threshold that triggered it,
// so the next round can add another large result and still be sent without
// surgery. A trim that stopped on the threshold would clear it by a few
// hundred tokens and be called again here — and each call costs the whole
// prompt prefix the provider was caching.
func TestTrimContext_TrimsOnceAcrossTwoRequests(t *testing.T) {
	m := trimFixture(t)
	if before := m.estimatedContextTokens(); before <= m.trimThreshold() {
		t.Fatalf("the fixture starts at %d, under the threshold %d", before, m.trimThreshold())
	}

	n := m.trimContext()
	if n == 0 {
		t.Fatal("the first request should have trimmed")
	}
	if got := m.estimatedContextTokens(); got > m.trimLowWater() {
		t.Fatalf("the trim stopped at %d, above the mark %d", got, m.trimLowWater())
	}

	// The next round: another large result and the turn that closes over it.
	m.agent.SetMessages(append(m.Messages(),
		provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "c5", Name: "read_file"}}},
		provider.Message{Role: provider.RoleTool, Content: strings.Repeat("x", 40000), ToolCallID: "c5"},
		provider.Message{Role: provider.RoleUser, Content: "q3"}))

	if n := m.trimContext(); n != 0 {
		t.Fatalf("the second request trimmed %d more results; one deep trim should have covered it", n)
	}
}

// TestTrimContext_SignalCarriesTheEstimateEitherSide: the count alone cannot
// tell a deep trim from a shallow one, and telling them apart is the whole
// question a reader of the record is asking.
//
// The estimates go in as shares of the window, which is also what keeps the
// qualifier countable — a raw token figure repeats approximately never, and
// the dashboard groups these events by their qualifier. Reading the numbers
// back as percentages is the guard against a regression to raw estimates.
func TestTrimContext_SignalCarriesTheEstimateEitherSide(t *testing.T) {
	m := trimFixture(t)
	var reasons []string
	m = m.WithObserver(observe.Observer{Signal: func(_ observe.Pos, code, reason string) {
		if code == observe.SignalTrim {
			reasons = append(reasons, reason)
		}
	}})

	n := m.trimContext()
	if len(reasons) != 1 {
		t.Fatalf("want one trim signal, got %v", reasons)
	}
	count, rest, ok := strings.Cut(reasons[0], " ")
	if !ok {
		t.Fatalf("the trim signal carries no estimates: %q", reasons[0])
	}
	if count != strconv.Itoa(n) {
		t.Errorf("the signal counts %s results, the trim elided %d", count, n)
	}
	from, to, ok := strings.Cut(rest, "→")
	if !ok {
		t.Fatalf("the trim signal carries one estimate, not two: %q", reasons[0])
	}
	before, after := pct(t, from), pct(t, to)
	if before < trimThresholdPercent {
		t.Errorf("the signal reports %d%% before the trim, under the %d%% that triggers one",
			before, trimThresholdPercent)
	}
	if after > trimLowWaterPercent || after >= before {
		t.Errorf("the signal reports %d%% after the trim, want under both %d%% and the %d%% mark",
			after, before, trimLowWaterPercent)
	}
}

// pct reads one of the shares out of a trim qualifier. A figure that lost
// its sign, or one over 100, is the record having gone back to raw token
// counts — which is what makes every trim a row of its own on the dashboard.
func pct(t *testing.T, s string) int {
	t.Helper()
	trimmed, ok := strings.CutSuffix(s, "%")
	if !ok {
		t.Fatalf("the trim signal carries %q, which is not a share of the window", s)
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil || n < 0 || n > 100 {
		t.Fatalf("the trim signal carries %q, which is not a percentage", s)
	}
	return n
}

// TestTrimContext_TheRoundThatJustLandedCounts is the failure the report's
// index exists to stop. The provider counts the messages a request carried;
// the round that request set off then returns a large tool result, and the
// figure the trim reads has to move with it. Anchored on the report alone it
// did not move at all, the trim declined to fire, and the next request — the
// one carrying the large result — went out oversize.
func TestTrimContext_TheRoundThatJustLandedCounts(t *testing.T) {
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "q1"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "c1", Name: "read_file"}}},
		{Role: provider.RoleTool, Content: strings.Repeat("x", 48000), ToolCallID: "c1"},
		{Role: provider.RoleAssistant, Content: "answer 1"},
		{Role: provider.RoleUser, Content: "q2"},
	}, mockStream)

	// The provider counts that request at 15k of the default 32768-token
	// window, comfortably under the 26214 that trims.
	m.accumulateUsage(&provider.Usage{PromptTokens: 15000, CompletionTokens: 200})
	if m.contextTokens >= m.trimThreshold() {
		t.Fatalf("the fixture's report (%d) has to sit under the threshold %d",
			m.contextTokens, m.trimThreshold())
	}
	if n := m.trimContext(); n != 0 {
		t.Fatalf("nothing should trim while the report still describes the whole conversation, got %d", n)
	}

	// And then the round that report was taken for lands: a call, and 60 KB
	// of output behind it.
	m.agent.Append(provider.Message{Role: provider.RoleAssistant,
		ToolCalls: []provider.ToolCall{{ID: "c2", Name: "search"}}})
	m.agent.Append(provider.Message{Role: provider.RoleTool,
		Content: strings.Repeat("y", 60000), ToolCallID: "c2"})

	if got := m.estimatedContextTokens(); got <= m.contextTokens {
		t.Fatalf("the accounting ignored the round: %d against the report's %d", got, m.contextTokens)
	}
	m.trimForRequest()
	if got := m.Messages()[3].Content; got != elidedResult {
		t.Fatalf("the older result should have been elided, got %d bytes", len(got))
	}
	if len(m.Messages()[7].Content) != 60000 {
		t.Fatal("the round's own result is in the current turn and must be kept")
	}
	if last := m.transcript[len(m.transcript)-1]; !strings.Contains(last.text, "Context trimmed") {
		t.Fatalf("the trim should be noted in the transcript, got %q", last.text)
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
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	stream := func(msgs []provider.Message, _ string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
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

	if len(gotReq) != 4 || gotReq[3].Content != agent.CompactInstruction {
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
	if m.contextTokens != 0 {
		t.Fatalf("the pre-compaction report describes a discarded conversation, got %d", m.contextTokens)
	}
	// Back to the session's own arithmetic over the rebuilt conversation,
	// scaled by what the report the compaction itself produced taught the
	// session about that arithmetic.
	if want := m.calibration.Apply(estimateMessageTokens(m.Messages())); m.estimatedContextTokens() != want {
		t.Fatalf("context estimate should reset to %d, got %d", want, m.estimatedContextTokens())
	}
	last := m.transcript[len(m.transcript)-1]
	if last.kind != entryAssistant || last.text != "the summary" {
		t.Fatalf("transcript should show the summary, got %+v", last)
	}
}

// A summary is prose, and the request says so. The instruction sits under a
// whole session of tool results, and a model that reads it as one more turn
// answers with the call the turn was about to make — which the abort path
// can only turn into a failed compaction.
func TestCompact_ForbidsAToolCall(t *testing.T) {
	var choices []string
	stream := func(msgs []provider.Message, choice string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		choices = append(choices, choice)
		ch := make(chan provider.StreamEvent, 2)
		ch <- provider.StreamEvent{Token: "the summary"}
		ch <- provider.StreamEvent{Done: true}
		close(ch)
		_, cancel := context.WithCancel(context.Background())
		return ch, cancel, nil
	}
	conversation := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "question"},
		{Role: provider.RoleAssistant, Content: "answer"},
	}

	turn := New(conversation, stream)
	turn.input.SetValue("ordinary turn")
	_, cmd := turn.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatal("expected a batched spinner+stream cmd")
	}
	for _, c := range batch {
		c()
	}

	_ = driveCompact(t, New(conversation, stream))

	if len(choices) != 2 {
		t.Fatalf("expected a turn request and a compaction request, got %v", choices)
	}
	if choices[0] != provider.ToolChoiceAuto {
		t.Errorf("a turn must leave the tools open, got %q", choices[0])
	}
	if choices[1] != provider.ToolChoiceNone {
		t.Errorf("a compaction must forbid a tool call, got %q", choices[1])
	}
}

func TestCompact_NothingToCompact(t *testing.T) {
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream)
	m.input.SetValue("/compact")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	stream := func(msgs []provider.Message, _ string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
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
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	m.streaming = "partial sum"

	updated, _ = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
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
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	if last.kind != entryError || !strings.Contains(last.text, "forbade one") {
		t.Fatalf("expected a compaction-failed entry, got %+v", last)
	}
}

// Where the context window comes from when the pricing table is silent. The
// window sets the trim threshold, so assuming 32k against a model with far
// more was throwing away findings the session had room to keep.
func TestContextWindow_FallsBackToTheModelFamily(t *testing.T) {
	for _, tc := range []struct {
		model string
		want  int64
	}{
		{"gpt-4o", 128_000},
		{"claude-opus-5", 1_000_000},
		{"claude-3-5-sonnet", 200_000},
		{"gemini-3.7-flash", 1_000_000},
		{"google/gemini-2.5-pro", 1_000_000},
		{"claude-opus-5[1m]", 1_000_000},
		{"llama3.1:70b", 128_000},
		{"some-local-llama-build", DefaultContextWindow},
		{"", DefaultContextWindow},
	} {
		m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).WithPricing(nil, tc.model)
		if got := m.contextWindow(); got != tc.want {
			t.Errorf("%q: context window = %d, want %d", tc.model, got, tc.want)
		}
	}
}

// The endpoint outranks the table: a runtime reporting the length it loaded
// the weights at knows something no public table can.
func TestContextWindow_EndpointOutranksTheTable(t *testing.T) {
	windows := map[string]int64{"qwen3:8b": 262_144}
	lookup := func(model string) (int64, bool) {
		w, ok := windows[model]
		return w, ok
	}
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, mockStream).
		WithPricing(nil, "qwen3:8b").
		WithEndpointWindows(lookup)
	if got := m.contextWindow(); got != 262_144 {
		t.Errorf("endpoint window = %d, want 262144", got)
	}

	// A model the endpoint has not described falls through to the family.
	m = m.WithPricing(nil, "claude-opus-5")
	if got := m.contextWindow(); got != 1_000_000 {
		t.Errorf("unanswered model = %d, want the family floor 1000000", got)
	}
}

// TestTrimContext_WiredStoreMakesElisionRecoverable checks the plumbing the
// host does: a session with an evidence store elides through it, so the
// placeholder names the entry that still holds the result and the model can
// page it back instead of running the tool again.
func TestTrimContext_WiredStoreMakesElisionRecoverable(t *testing.T) {
	kept := map[string]string{}
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "q1"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "c1", Name: "read_file"}}},
		{Role: provider.RoleTool, Content: strings.Repeat("x", 40000), ToolCallID: "c1"},
		{Role: provider.RoleUser, Content: "q2"},
	}, mockStream).WithEvidence(Evidence{
		Keep: func(tool, content string) (string, bool) {
			id := "ev-00000000000000" + fmt.Sprintf("%02d", len(kept))
			kept[id] = content
			return id, true
		},
	})
	m.contextTokens = 30000

	if n := m.trimContext(); n != 1 {
		t.Fatalf("want 1 elided result, got %d", n)
	}
	placeholder := m.Messages()[3].Content
	if placeholder == elidedResult {
		t.Fatal("a session with a store must name the entry rather than eliding blind")
	}
	var found bool
	for id, content := range kept {
		if strings.Contains(placeholder, id) {
			found = true
			if len(content) != 40000 {
				t.Fatalf("the store was handed %d bytes, not the whole result", len(content))
			}
		}
	}
	if !found {
		t.Fatalf("the placeholder names no entry the store took: %q", placeholder)
	}
}

// A compaction keeps the system prompt and replaces everything under it, so
// the workspace block is the one thing left describing the checkout as it was
// when the session opened.
func TestCompact_RereadsTheWorkspace(t *testing.T) {
	stream := func(_ []provider.Message, _ string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		ch := make(chan provider.StreamEvent, 2)
		ch <- provider.StreamEvent{Token: "the summary"}
		ch <- provider.StreamEvent{Done: true}
		close(ch)
		_, cancel := context.WithCancel(context.Background())
		return ch, cancel, nil
	}
	opened := project.PromptBlock(project.Info{Dir: "/work", Repo: true, Branch: "master"})
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys\n\n" + opened},
		{Role: provider.RoleUser, Content: "question"},
		{Role: provider.RoleAssistant, Content: "answer"},
	}, stream).WithWorkspaceBlock(func() string {
		return project.PromptBlock(project.Info{Dir: "/work", Repo: true, Branch: "side"})
	})

	m = driveCompact(t, m)

	sysPrompt := m.Messages()[0].Content
	if !strings.Contains(sysPrompt, "Git branch: side") {
		t.Fatalf("the rebuilt conversation should name the branch it is on now:\n%s", sysPrompt)
	}
	if strings.Contains(sysPrompt, "Git branch: master") {
		t.Fatalf("the branch of the first minute should be gone:\n%s", sysPrompt)
	}
	if !strings.HasPrefix(sysPrompt, "sys\n\n") {
		t.Fatalf("the rest of the prompt is not compaction's to touch:\n%s", sysPrompt)
	}
}

// A host with no reading of the tree leaves the prompt exactly as it was,
// which is what every front-end without one did before there was anything to
// ask.
func TestCompact_WithoutAWorkspaceReadingKeepsThePrompt(t *testing.T) {
	stream := func(_ []provider.Message, _ string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		ch := make(chan provider.StreamEvent, 2)
		ch <- provider.StreamEvent{Token: "the summary"}
		ch <- provider.StreamEvent{Done: true}
		close(ch)
		_, cancel := context.WithCancel(context.Background())
		return ch, cancel, nil
	}
	opened := project.PromptBlock(project.Info{Dir: "/work", Repo: true, Branch: "master"})
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys\n\n" + opened},
		{Role: provider.RoleUser, Content: "question"},
	}, stream)

	m = driveCompact(t, m)

	if m.Messages()[0].Content != "sys\n\n"+opened {
		t.Fatalf("nothing to read the tree with, so nothing changes:\n%s", m.Messages()[0].Content)
	}
}

// A loaded conversation brings its own system prompt back out of the store,
// written in a sitting that may be days old. The checkout in front of it is
// this one.
func TestChatLoad_RereadsTheWorkspace(t *testing.T) {
	db := rewindTestDB(t)
	stale := project.PromptBlock(project.Info{Dir: "/work", Repo: true, Branch: "master", Dirty: 4})
	if err := db.SaveChat("alpha", []provider.Message{
		{Role: provider.RoleSystem, Content: "sys\n\n" + stale},
		{Role: provider.RoleUser, Content: "q"},
		{Role: provider.RoleAssistant, Content: "a"},
	}); err != nil {
		t.Fatal(err)
	}
	m := readyModel(t).WithDB(db).WithWorkspaceBlock(func() string {
		return project.PromptBlock(project.Info{Dir: "/work", Repo: true, Branch: "side"})
	})

	m.loadChatByName("alpha")

	sysPrompt := m.Messages()[0].Content
	if !strings.Contains(sysPrompt, "Git branch: side") {
		t.Fatalf("a loaded conversation should name the branch it is on now:\n%s", sysPrompt)
	}
	if strings.Contains(sysPrompt, "Git branch: master") || strings.Contains(sysPrompt, "4 uncommitted") {
		t.Fatalf("the checkout of the sitting that saved it is gone:\n%s", sysPrompt)
	}
	if !strings.HasPrefix(sysPrompt, "sys\n\n") {
		t.Fatalf("the rest of the stored prompt is not this reading's to touch:\n%s", sysPrompt)
	}
}

// TestTrimContext_ElidesTheTranscriptCopy: the transcript holds the same
// bytes the conversation does, so a trim that took one and left the other
// recovered nothing a day-long session's memory could see. The row survives
// with what it was showing — its counts — and its body becomes the
// placeholder the model got.
func TestTrimContext_ElidesTheTranscriptCopy(t *testing.T) {
	big := strings.Repeat("line\n", 8000) // 40000 bytes, ~10k estimated tokens
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "q1"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "c1", Name: "read_file"}}},
		{Role: provider.RoleTool, Content: big, ToolCallID: "c1"},
		{Role: provider.RoleUser, Content: "q2"},
	}, mockStream)
	m.appendEntry(entry{kind: entryUser, text: "q1"})
	m.appendEntry(entry{kind: entryTool, toolName: "read_file", toolArgs: `{"path":"big.txt"}`, toolResult: big})
	m.contextTokens = 30000

	want := activityCounts("read_file", big)
	if n := m.trimContext(); n != 1 {
		t.Fatalf("want 1 elided result, got %d", n)
	}
	row := m.transcript[1]
	if row.toolResult == big {
		t.Fatal("the transcript still holds the whole result the conversation just gave up")
	}
	if _, elided := agent.Elided(row.toolResult); !elided {
		t.Fatalf("row body is %q, want the placeholder the model was left with", row.toolResult)
	}
	if row.toolResult != m.Messages()[3].Content {
		t.Fatalf("row says %q, model was told %q", row.toolResult, m.Messages()[3].Content)
	}
	if row.elided == nil || row.elided.counts != want {
		t.Fatalf("counts %#v, want %q kept from before the elision", row.elided, want)
	}
	if got := m.activityRowDetail(row, false).Counts; got != want {
		t.Fatalf("the row draws counts %q, want %q", got, want)
	}
}

// The full-screen body of an elided row is the original, paged back out of
// the store: the transcript let the text go and this is where it comes back.
func TestElidedRow_OffersTheEvidencePage(t *testing.T) {
	big := strings.Repeat("line\n", 8000)
	kept := map[string]string{}
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "q1"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "c1", Name: "read_file"}}},
		{Role: provider.RoleTool, Content: big, ToolCallID: "c1"},
		{Role: provider.RoleUser, Content: "q2"},
	}, mockStream).WithEvidence(Evidence{
		Keep: func(_, content string) (string, bool) {
			id := fmt.Sprintf("ev-000000000000000%d", len(kept))
			kept[id] = content
			return id, true
		},
		Read: func(id string, _ int) (string, bool) {
			content, ok := kept[id]
			return content, ok
		},
	})
	m.appendEntry(entry{kind: entryTool, toolName: "read_file", toolResult: big})
	m.contextTokens = 30000

	if n := m.trimContext(); n != 1 {
		t.Fatalf("want 1 elided result, got %d", n)
	}
	row := m.transcript[0]
	if row.elided == nil || row.elided.evidence == "" {
		t.Fatalf("row kept no evidence id: %#v", row.elided)
	}
	// The in-place body says the result was elided; the depth past it is the
	// result itself.
	if body := outputLines(row); len(body) != 1 || !strings.Contains(body[0], "elided") {
		t.Fatalf("in-place body %q, want the placeholder", body)
	}
	if !row.opensFullOutput(outputLines(row)) {
		t.Fatal("an elided row with a stored original does not open whole")
	}
	if got := m.rowOutputView(row).Lines; len(got) != 8000 {
		t.Fatalf("full output is %d lines, want the 8000 the store kept", len(got))
	}

	// A store that no longer holds it says so rather than opening a screen
	// with a placeholder on it.
	clear(kept)
	lines := m.rowOutputView(row).Lines
	if last := lines[len(lines)-1]; !strings.Contains(last, "no longer in the evidence store") {
		t.Fatalf("purged entry ends with %q", last)
	}
}

// A failed call that is elided is still a failed call: the row's ✗ and its
// outcome were read off the body, and the placeholder is not the body.
func TestElidedRow_KeepsWhatTheBodySaid(t *testing.T) {
	boom := "error: " + strings.Repeat("stack frame\n", 3000)
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "q1"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "c1", Name: "read_file"}}},
		{Role: provider.RoleTool, Content: boom, ToolCallID: "c1"},
		{Role: provider.RoleUser, Content: "q2"},
	}, mockStream)
	m.appendEntry(entry{kind: entryTool, toolName: "read_file", toolResult: boom})
	m.contextTokens = 30000
	before := m.activityRowDetail(m.transcript[0], false)

	if n := m.trimContext(); n != 1 {
		t.Fatalf("want 1 elided result, got %d", n)
	}
	after := m.activityRowDetail(m.transcript[0], false)
	if !after.Failed() {
		t.Fatal("eliding the body turned a failed call into a clean one")
	}
	if after.Outcome != before.Outcome {
		t.Fatalf("outcome %q, was %q", after.Outcome, before.Outcome)
	}
}

// A turn's verdict outlives the trim that takes the check's output: the
// reading was reported when the turn closed, and re-parsing it out of the
// placeholder would report no checks at all.
func TestElidedCheck_TheTurnKeepsItsVerdict(t *testing.T) {
	gate := "Quality gate \"default\": FAIL — 3/5 checks passed (1.2s)\n" +
		strings.Repeat("a failing check said something\n", 2000)
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "q1"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "c1", Name: quality.ToolName}}},
		{Role: provider.RoleTool, Content: gate, ToolCallID: "c1"},
		{Role: provider.RoleUser, Content: "q2"},
	}, mockStream)
	m.appendEntry(entry{kind: entryTool, turn: 1, toolName: quality.ToolName, toolResult: gate})
	m.appendEntry(entry{kind: entryTurnClose, turn: 1, close: &components.TurnClose{
		Checks: turnChecksRow(m.transcript),
	}})
	m.contextTokens = 30000

	before := m.reviewVerdict(1)
	if before == nil || !before.Failed {
		t.Fatalf("the turn failed its checks before the trim, got %+v", before)
	}
	if n := m.trimContext(); n != 1 {
		t.Fatalf("want 1 elided result, got %d", n)
	}
	after := m.reviewVerdict(1)
	if after == nil || !after.Failed || after.Label != before.Label {
		t.Fatalf("the verdict changed with the trim: %+v, was %+v", after, before)
	}
}

// The steering digest says how a call came back, and an elided error is
// still an error: the placeholder carries no `error:` for it to read.
func TestElidedRow_StillReportsAsAnErrorToTheDigest(t *testing.T) {
	boom := "error: " + strings.Repeat("stack frame\n", 3000)
	m := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "q1"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "c1", Name: "read_file"}}},
		{Role: provider.RoleTool, Content: boom, ToolCallID: "c1"},
		{Role: provider.RoleUser, Content: "q2"},
	}, mockStream)
	m.appendEntry(entry{kind: entryTool, toolName: "read_file", toolResult: boom})
	m.contextTokens = 30000
	if n := m.trimContext(); n != 1 {
		t.Fatalf("want 1 elided result, got %d", n)
	}
	rows := m.summaryActivity()
	if len(rows) != 1 || !strings.Contains(rows[0], digest.OutcomeError) {
		t.Fatalf("the digest reports %q, want the call still failing", rows)
	}
}
