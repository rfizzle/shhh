package tree

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rfizzle/agentcore/agent"
	"github.com/rfizzle/llm"
)

// scriptedStream, toolCallRound and doneRound are copies of the helpers in
// internal/agent's headless_test.go, which this test needs and a test file
// cannot export across packages.
func scriptedStream(t *testing.T, rounds ...[]llm.StreamEvent) agent.StreamFunc {
	t.Helper()
	i := 0
	return func([]llm.Message, string) (<-chan llm.StreamEvent, context.CancelFunc, error) {
		if i >= len(rounds) {
			t.Fatalf("unexpected stream request #%d", i+1)
		}
		evs := rounds[i]
		i++
		ch := make(chan llm.StreamEvent, len(evs))
		for _, ev := range evs {
			ch <- ev
		}
		close(ch)
		_, cancel := context.WithCancel(context.Background())
		return ch, cancel, nil
	}
}

func toolCallRound(calls ...llm.ToolCall) []llm.StreamEvent {
	return []llm.StreamEvent{{ToolCalls: calls}}
}

func doneRound(text string) []llm.StreamEvent {
	return []llm.StreamEvent{{Token: text}, {Done: true}}
}

// A headless run is told the tree moved the same way a session is: at the
// round boundary, as a user message, before the next request. The reading
// reaches the loop only through its two seams.
func TestHeadlessRun_TellsTheTurnTheTreeMoved(t *testing.T) {
	ws, _ := treeFixture(t)
	a := agent.New(nil, scriptedStream(t,
		toolCallRound(llm.ToolCall{ID: "c1", Name: "read_file", Arguments: `{"path":"a.txt"}`}),
		doneRound("done"),
	))
	tr := New(TreeCheck{Dir: ws})
	a.SetCallObserver(tr.NoteCalls)
	a.SetExecutor(func(ctx context.Context, name string, args json.RawMessage) (string, error) {
		// Somebody else writes while the round runs.
		write(t, ws, "theirs.txt", "x\n")
		return "hello", nil
	})
	var told []TreeNotice
	h := &agent.Headless{Agent: a, Boundary: func(turnStart bool) (string, bool) {
		n, ok := tr.Next(turnStart)
		if !ok {
			return "", false
		}
		told = append(told, n)
		return n.Message, true
	}}
	if _, err := h.Run("go"); err != nil {
		t.Fatal(err)
	}
	if len(told) != 1 || told[0].Paths != 1 {
		t.Fatalf("Boundary should see one notice naming one path, got %+v", told)
	}
	msgs := a.Messages()
	// system-less: user, assistant(tool call), tool result, tree notice, assistant.
	notice := msgs[len(msgs)-2]
	if notice.Role != llm.RoleUser || !strings.Contains(notice.Content, "[tree: 1 path changed outside this session: theirs.txt]") {
		t.Errorf("the notice should sit between the results and the final answer, got %+v", notice)
	}
}
