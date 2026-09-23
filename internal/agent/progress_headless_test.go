package agent

import (
	"context"
	"encoding/json"
	"errors"
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

// A checkpoint's status is buffered until the response says what it leads,
// so a round interrupted after writing it has shown it nowhere. The words the
// conversation keeps reach the feed as status, and never as the answer.
func TestHeadlessRun_AnInterruptedCheckpointKeepsItsStatus(t *testing.T) {
	call := provider.ToolCall{ID: "one", Name: "read_file", Arguments: `{}`}
	var h *Headless
	n := 0
	stream := func([]provider.Message, string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
		n++
		ch := make(chan provider.StreamEvent)
		ctx, cancel := context.WithCancel(context.Background())
		first := n == 1
		go func() {
			defer close(ch)
			if first {
				ch <- provider.StreamEvent{ToolCalls: []provider.ToolCall{call}}
				return
			}
			ch <- provider.StreamEvent{Token: "status"}
			h.Interrupt()
			<-ctx.Done()
		}()
		return ch, cancel, nil
	}
	a := New(nil, stream)
	a.SetProgressIntervals(1, time.Hour)
	a.SetExecutor(func(string, json.RawMessage) (string, error) { return "ok", nil })
	var progress []string
	var text string
	h = &Headless{
		Agent:      a,
		OnProgress: func(s string) { progress = append(progress, s) },
		OnText:     func(s string) { text += s },
	}
	if _, err := h.Run("investigate"); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("err = %v, want ErrInterrupted", err)
	}
	if len(progress) != 1 || progress[0] != "status" {
		t.Fatalf("progress = %#v, want the buffered status", progress)
	}
	if text != "" {
		t.Fatalf("text callback = %q, want nothing: an interrupted turn has no answer", text)
	}
}
