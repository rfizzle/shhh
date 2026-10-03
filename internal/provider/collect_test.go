package provider

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// feed is a closed channel holding events, the way a parser leaves one.
func feed(events ...StreamEvent) <-chan StreamEvent {
	ch := make(chan StreamEvent, len(events))
	for _, ev := range events {
		ch <- ev
	}
	close(ch)
	return ch
}

func TestCollect_EachEventKind(t *testing.T) {
	first := &Usage{PromptTokens: 1}
	last := &Usage{PromptTokens: 10, CompletionTokens: 2, CachedTokens: 3}
	callA := ToolCall{ID: "a", Name: "one", Arguments: `{}`}
	callB := ToolCall{ID: "b", Name: "two", Arguments: `{"x":1}`}
	tests := []struct {
		name   string
		events []StreamEvent
		want   Reply
	}{
		{
			name:   "tokens join in order",
			events: []StreamEvent{{Token: "he"}, {Token: "llo"}, {Done: true}},
			want:   Reply{Text: "hello"},
		},
		{
			name:   "tool calls gather across events",
			events: []StreamEvent{{ToolCalls: []ToolCall{callA}}, {ToolCalls: []ToolCall{callB}, Done: true}},
			want:   Reply{Calls: []ToolCall{callA, callB}},
		},
		{
			name:   "the last usage is the one kept",
			events: []StreamEvent{{Usage: first}, {Token: "x"}, {Usage: last, Done: true}},
			want:   Reply{Text: "x", Usage: last},
		},
		{
			name:   "the terminal event's own content counts",
			events: []StreamEvent{{Token: "a"}, {Token: "b", ToolCalls: []ToolCall{callA}, Usage: last, Done: true}},
			want:   Reply{Text: "ab", Calls: []ToolCall{callA}, Usage: last},
		},
		{
			name:   "nothing after the terminal event is read",
			events: []StreamEvent{{Token: "kept", Done: true}, {Token: " dropped", Usage: first}},
			want:   Reply{Text: "kept"},
		},
		{
			name:   "thinking and fragments are not the answer",
			events: []StreamEvent{{Thinking: "hmm"}, {ToolCallDelta: &ToolCallDelta{}}, {Token: "yes", Done: true}},
			want:   Reply{Text: "yes"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Collect(context.Background(), feed(tt.events...))
			if err != nil {
				t.Fatalf("Collect: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Collect = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestCollect_ErrorKeepsWhatCameBeforeIt(t *testing.T) {
	boom := errors.New("boom")
	usage := &Usage{PromptTokens: 5}
	got, err := Collect(context.Background(), feed(
		StreamEvent{Token: "part", Usage: usage},
		StreamEvent{Token: " lost", ToolCalls: []ToolCall{{ID: "c", Name: "n"}}, Usage: &Usage{PromptTokens: 9}, Err: boom, Done: true},
		StreamEvent{Token: " never"},
	))
	if err != boom {
		t.Fatalf("err = %v, want the event's own error unwrapped", err)
	}
	want := Reply{Text: "part", Usage: usage}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Collect = %+v, want %+v", got, want)
	}
}

func TestCollect_ClosedChannelEndsTheReply(t *testing.T) {
	got, err := Collect(context.Background(), feed(StreamEvent{Token: "no terminal event"}))
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if got.Text != "no terminal event" || got.Calls != nil || got.Usage != nil {
		t.Errorf("Collect = %+v", got)
	}

	got, err = Collect(context.Background(), feed())
	if err != nil || !reflect.DeepEqual(got, Reply{}) {
		t.Errorf("an empty closed stream = %+v, %v; want the zero reply", got, err)
	}
}

func TestCollect_ContextEndStopsAStreamThatNeverEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan StreamEvent, 1)
	usage := &Usage{PromptTokens: 2}
	ch <- StreamEvent{Token: "so far", Usage: usage}
	// The channel is never closed and nothing more arrives: a provider that
	// ignores cancellation. The buffered event is read first, then the
	// cancel is what ends the read.
	done := make(chan struct{})
	var got Reply
	var err error
	go func() {
		got, err = Collect(ctx, ch)
		close(done)
	}()
	for len(ch) > 0 {
		// Wait for the buffered event to be taken before cancelling, so
		// what the reply holds is deterministic.
		select {
		case <-done:
			t.Fatal("Collect returned before the context ended")
		default:
		}
	}
	cancel()
	<-done
	if !errors.Is(err, context.Canceled) || err != ctx.Err() {
		t.Fatalf("err = %v, want ctx.Err() unwrapped", err)
	}
	if got.Text != "so far" || got.Usage != usage {
		t.Errorf("Collect = %+v, want what arrived before the cancel", got)
	}
}
