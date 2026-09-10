package agent

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
)

func TestHeadlessRun_EmitsCheckpointStatusBeforeFollowingTools(t *testing.T) {
	call := provider.ToolCall{ID: "one", Name: "read_file", Arguments: `{}`}
	next := provider.ToolCall{ID: "two", Name: "search", Arguments: `{}`}
	a := New(nil, scriptedStream(t,
		toolCallRound(call),
		[]provider.StreamEvent{{Token: "I found the loop; next I will inspect its callers."}, {ToolCalls: []provider.ToolCall{next}}},
		doneRound("done"),
	))
	a.SetProgressIntervals(1, time.Hour)
	a.SetExecutor(func(string, json.RawMessage) (string, error) { return "ok", nil })
	var progress []string
	h := &Headless{Agent: a, OnProgress: func(s string) { progress = append(progress, s) }}
	if final, err := h.Run("investigate"); err != nil || final != "done" {
		t.Fatalf("Run = %q, %v; want done, nil", final, err)
	}
	if len(progress) != 1 || progress[0] != "I found the loop; next I will inspect its callers." {
		t.Fatalf("progress = %#v", progress)
	}
	if a.Rounds() != 2 {
		t.Fatalf("checkpoint changed tool-round protocol: rounds = %d, want 2", a.Rounds())
	}
}

func TestHeadlessRun_ProgressCheckpointDoesNotPolluteTextCallback(t *testing.T) {
	call := provider.ToolCall{ID: "one", Name: "read_file", Arguments: `{}`}
	a := New(nil, scriptedStream(t,
		toolCallRound(call),
		[]provider.StreamEvent{{Token: "status"}, {ToolCalls: []provider.ToolCall{{ID: "two", Name: "search", Arguments: `{}`}}}},
		doneRound("answer"),
	))
	a.SetProgressIntervals(1, time.Hour)
	a.SetExecutor(func(string, json.RawMessage) (string, error) { return "ok", nil })
	var text string
	h := &Headless{Agent: a, OnText: func(s string) { text += s }}
	if _, err := h.Run("investigate"); err != nil {
		t.Fatal(err)
	}
	if text != "answer" {
		t.Fatalf("text callback = %q, want final answer only", text)
	}
}
