package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

func noStream([]provider.Message) (<-chan provider.StreamEvent, context.CancelFunc, error) {
	ch := make(chan provider.StreamEvent)
	close(ch)
	_, cancel := context.WithCancel(context.Background())
	return ch, cancel, nil
}

func newTestAgent() *Agent {
	return New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, noStream)
}

func TestStartTurn_AppendsAndResetsRounds(t *testing.T) {
	a := newTestAgent()
	a.BeginToolRound("", nil, nil)
	if a.Rounds() != 1 {
		t.Fatalf("expected 1 round, got %d", a.Rounds())
	}

	a.StartTurn("hello")
	if a.Rounds() != 0 {
		t.Fatalf("fresh turn should reset rounds, got %d", a.Rounds())
	}
	last := a.Messages()[len(a.Messages())-1]
	if last.Role != provider.RoleUser || last.Content != "hello" {
		t.Fatalf("expected user message 'hello', got %+v", last)
	}
}

func TestRequestMessages_ImmuneToLaterMutation(t *testing.T) {
	a := newTestAgent()
	snapshot := a.RequestMessages()
	a.Append(provider.Message{Role: provider.RoleUser, Content: "later"})
	if len(snapshot) != 1 {
		t.Fatalf("snapshot should not grow with the conversation, got %d", len(snapshot))
	}
}

func TestBeginToolRound_SplitsByGate(t *testing.T) {
	a := newTestAgent()
	calls := []provider.ToolCall{
		{ID: "c1", Name: "read_file"},
		{ID: "c2", Name: "execute_command"},
		{ID: "c3", Name: "search"},
	}
	gate := func(tc provider.ToolCall) bool { return tc.Name == "execute_command" }

	auto, gated := a.BeginToolRound("checking", calls, gate)
	if len(auto) != 2 || auto[0].ID != "c1" || auto[1].ID != "c3" {
		t.Fatalf("unexpected auto set: %+v", auto)
	}
	if len(gated) != 1 || gated[0].ID != "c2" {
		t.Fatalf("unexpected gated set: %+v", gated)
	}
	if !a.Executing() {
		t.Fatal("auto calls should mark the agent executing")
	}
	if a.QueuedApprovals() != 1 {
		t.Fatalf("expected 1 queued approval, got %d", a.QueuedApprovals())
	}
	last := a.Messages()[len(a.Messages())-1]
	if last.Role != provider.RoleAssistant || last.Content != "checking" || len(last.ToolCalls) != 3 {
		t.Fatalf("assistant message should carry text and all calls, got %+v", last)
	}
}

func TestBeginToolRound_NilGateAutoRunsEverything(t *testing.T) {
	a := newTestAgent()
	auto, gated := a.BeginToolRound("", []provider.ToolCall{{ID: "c1", Name: "read_file"}}, nil)
	if len(auto) != 1 || len(gated) != 0 {
		t.Fatalf("nil gate should auto-run all calls, got auto=%d gated=%d", len(auto), len(gated))
	}
}

func TestExecuteCalls_FormatsErrors(t *testing.T) {
	a := newTestAgent()
	a.SetExecutor(func(name string, args json.RawMessage) (string, error) {
		if name == "bad" {
			return "", errors.New("boom")
		}
		return "ok:" + name, nil
	})

	results := a.ExecuteCalls([]provider.ToolCall{
		{ID: "c1", Name: "good", Arguments: `{}`},
		{ID: "c2", Name: "bad", Arguments: `{}`},
	})
	if results[0].Result != "ok:good" {
		t.Fatalf("unexpected result: %q", results[0].Result)
	}
	if results[1].Result != "error: boom" {
		t.Fatalf("executor errors should become error results, got %q", results[1].Result)
	}
}

func TestExecuteWith_NilExecutor(t *testing.T) {
	got := ExecuteWith(nil, provider.ToolCall{Name: "x"})
	if !strings.Contains(got, "no tool executor") {
		t.Fatalf("expected a no-executor error, got %q", got)
	}
}

func TestRecordAutoResults_AppendsAndOwesQueue(t *testing.T) {
	a := newTestAgent()
	calls := []provider.ToolCall{
		{ID: "c1", Name: "read_file"},
		{ID: "c2", Name: "execute_command"},
	}
	auto, _ := a.BeginToolRound("", calls, func(tc provider.ToolCall) bool { return tc.Name == "execute_command" })

	a.RecordAutoResults([]ToolResult{{Call: auto[0], Result: "contents"}})
	if a.Executing() {
		t.Fatal("recording results should end the executing phase")
	}
	last := a.Messages()[len(a.Messages())-1]
	if last.Role != provider.RoleTool || last.ToolCallID != "c1" || last.Content != "contents" {
		t.Fatalf("expected tool result for c1, got %+v", last)
	}
	if a.QueuedApprovals() != 1 {
		t.Fatalf("gated call should still be queued, got %d", a.QueuedApprovals())
	}
}

func TestResolveApproval_PopsInOrder(t *testing.T) {
	a := newTestAgent()
	calls := []provider.ToolCall{
		{ID: "c1", Name: "execute_command"},
		{ID: "c2", Name: "write_file"},
	}
	a.BeginToolRound("", calls, func(provider.ToolCall) bool { return true })

	head, ok := a.NextApproval()
	if !ok || head.ID != "c1" {
		t.Fatalf("expected c1 first, got %+v ok=%v", head, ok)
	}
	a.ResolveApproval("exit code: 0")
	head, ok = a.NextApproval()
	if !ok || head.ID != "c2" {
		t.Fatalf("expected c2 next, got %+v ok=%v", head, ok)
	}
	a.ResolveApproval("error: the user declined this tool call")
	if _, ok := a.NextApproval(); ok {
		t.Fatal("queue should be empty")
	}

	msgs := a.Messages()
	r1, r2 := msgs[len(msgs)-2], msgs[len(msgs)-1]
	if r1.ToolCallID != "c1" || r1.Content != "exit code: 0" {
		t.Fatalf("unexpected first result: %+v", r1)
	}
	if r2.ToolCallID != "c2" || !strings.Contains(r2.Content, "declined") {
		t.Fatalf("unexpected second result: %+v", r2)
	}

	// Resolving with an empty queue is a no-op.
	before := len(a.Messages())
	a.ResolveApproval("stray")
	if len(a.Messages()) != before {
		t.Fatal("resolving an empty queue must not append")
	}
}

func TestCancelTurn_SyntheticResultsWhileExecuting(t *testing.T) {
	a := newTestAgent()
	calls := []provider.ToolCall{
		{ID: "c1", Name: "read_file"},
		{ID: "c2", Name: "execute_command"},
	}
	a.BeginToolRound("", calls, func(tc provider.ToolCall) bool { return tc.Name == "execute_command" })
	runBefore := a.RunID()

	cancelled := a.CancelTurn()
	if a.RunID() != runBefore+1 {
		t.Fatal("cancel should advance the run ID")
	}
	if len(cancelled) != 2 {
		t.Fatalf("both outstanding calls should be cancelled, got %d", len(cancelled))
	}
	msgs := a.Messages()
	for i, id := range []string{"c1", "c2"} {
		msg := msgs[len(msgs)-2+i]
		if msg.Role != provider.RoleTool || msg.ToolCallID != id || msg.Content != CancelledResult {
			t.Fatalf("expected synthetic result for %s, got %+v", id, msg)
		}
	}
	if a.QueuedApprovals() != 0 || a.Executing() {
		t.Fatal("cancel should clear the queue and executing state")
	}
}

func TestCancelTurn_NoSyntheticsWhenIdle(t *testing.T) {
	a := newTestAgent()
	before := len(a.Messages())
	if cancelled := a.CancelTurn(); len(cancelled) != 0 {
		t.Fatalf("idle cancel should cancel nothing, got %d", len(cancelled))
	}
	if len(a.Messages()) != before {
		t.Fatal("idle cancel must not append messages")
	}
}

func TestMaxRounds_DefaultAndOverride(t *testing.T) {
	a := newTestAgent()
	if a.MaxRounds() != DefaultMaxToolRounds {
		t.Fatalf("expected default cap %d, got %d", DefaultMaxToolRounds, a.MaxRounds())
	}
	a.SetMaxRounds(0)
	if a.MaxRounds() != DefaultMaxToolRounds {
		t.Fatalf("zero should keep the default cap, got %d", a.MaxRounds())
	}
	a.SetMaxRounds(2)
	if a.MaxRounds() != 2 {
		t.Fatalf("configured cap should win, got %d", a.MaxRounds())
	}

	a.BeginToolRound("", nil, nil)
	if a.CapReached() {
		t.Fatal("one round of two should not reach the cap")
	}
	a.BeginToolRound("", nil, nil)
	if !a.CapReached() {
		t.Fatal("two rounds of two should reach the cap")
	}
	a.ResetRounds()
	if a.CapReached() {
		t.Fatal("resetting rounds should clear the cap")
	}
}

func TestTrimOldToolResults_ProtectsCurrentTurn(t *testing.T) {
	big := strings.Repeat("x", 40000)
	a := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "q1"},
		{Role: provider.RoleTool, Content: big, ToolCallID: "c1"},
		{Role: provider.RoleAssistant, Content: "answer 1"},
		{Role: provider.RoleUser, Content: "q2"},
		{Role: provider.RoleTool, Content: "recent result", ToolCallID: "c2"},
	}, noStream)

	elided, newEst := a.TrimOldToolResults(30000, 26000)
	if elided != 1 {
		t.Fatalf("want 1 elided result, got %d", elided)
	}
	if newEst >= 30000 {
		t.Fatalf("estimate should drop after trimming, got %d", newEst)
	}
	msgs := a.Messages()
	if msgs[2].Content != ElidedResult {
		t.Fatalf("old tool result should be elided, got %q", msgs[2].Content)
	}
	if msgs[5].Content != "recent result" {
		t.Fatal("current-turn tool results must be protected")
	}
	if msgs[1].Content != "q1" || msgs[3].Content != "answer 1" {
		t.Fatal("user/assistant text must be kept")
	}
}

func TestTrimOldToolResults_NoopUnderThreshold(t *testing.T) {
	a := New([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "q1"},
		{Role: provider.RoleTool, Content: "small result", ToolCallID: "c1"},
		{Role: provider.RoleUser, Content: "q2"},
	}, noStream)

	elided, newEst := a.TrimOldToolResults(1000, 26000)
	if elided != 0 || newEst != 1000 {
		t.Fatalf("under the threshold nothing should change, got elided=%d est=%d", elided, newEst)
	}
	if a.Messages()[2].Content != "small result" {
		t.Fatal("tool result should be untouched under the threshold")
	}
}

func TestEstimateMessageTokens_CountsContentAndArgs(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: strings.Repeat("a", 400)},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{Arguments: strings.Repeat("b", 400)}}},
	}
	if got := EstimateMessageTokens(msgs); got != 200 {
		t.Fatalf("expected 200 estimated tokens, got %d", got)
	}
}
