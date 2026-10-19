package agent

import (
	"context"

	"github.com/rfizzle/llm"
)

// fakeClassifierProvider scripts StreamCompletion responses for the
// session-record readers' tests. A copy of the permission package's, which
// left with the classifier.
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
