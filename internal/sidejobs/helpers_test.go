package sidejobs

import (
	"context"

	"github.com/rfizzle/llm"
	"github.com/rfizzle/llm/effort"
)

// fakeClassifierProvider scripts StreamCompletion responses for the side
// jobs' tests.
type fakeClassifierProvider struct {
	fn    func(attempt int, opts llm.CompletionOpts) (<-chan llm.StreamEvent, error)
	calls int
	// msgs is the last request's messages, for the tests that assert which
	// half of the prompt went in which channel.
	msgs []llm.Message
}

func (f *fakeClassifierProvider) StreamCompletion(ctx context.Context, msgs []llm.Message, opts llm.CompletionOpts) (<-chan llm.StreamEvent, error) {
	f.calls++
	f.msgs = msgs
	return f.fn(f.calls, opts)
}

func (f *fakeClassifierProvider) Name() string { return "fake" }

func eventsOf(evs ...llm.StreamEvent) <-chan llm.StreamEvent {
	ch := make(chan llm.StreamEvent, len(evs))
	for _, ev := range evs {
		ch <- ev
	}
	close(ch)
	return ch
}

// thinkingProvider models what every current frontier model does: it spends
// part of the output ceiling on a thought before it says anything, and a
// ceiling that does not reach the end of the thought ends the response with
// nothing in it — no tool call, no text, no error.
type thinkingProvider struct {
	spend  int
	answer llm.StreamEvent
	seen   llm.CompletionOpts
}

func (t *thinkingProvider) Name() string { return "thinking" }

func (t *thinkingProvider) StreamCompletion(_ context.Context, _ []llm.Message, opts llm.CompletionOpts) (<-chan llm.StreamEvent, error) {
	t.seen = opts
	if opts.MaxTokens <= t.spend {
		return eventsOf(llm.StreamEvent{Done: true}), nil
	}
	return eventsOf(t.answer), nil
}

// lowBudget is what a shallow thought costs on the dialects whose knob is a
// number rather than a name — the floor the ceilings here have to clear.
func lowBudget() int { return effort.Low.ThinkingBudget(0) }
