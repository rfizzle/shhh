package provider

import (
	"context"
	"strings"
)

// Reply is a whole answer read off a stream: the text the model wrote, the
// tool calls it finished and what the request cost. It is what a bounded side
// request — a title, a verdict, a summary — acts on, because none of them
// draws anything while the answer is still arriving.
type Reply struct {
	Text  string
	Calls []ToolCall
	// Usage is the last usage the stream reported, and nil when it reported
	// none: zero would read as a free request.
	Usage *Usage
}

// Collect reads events until the terminal event, a closed channel, an error
// event or the end of ctx, whichever comes first. ctx is watched beside the
// channel because a provider that ignores cancellation would otherwise hold
// the caller past its own deadline.
//
// The error is ctx.Err() or the event's own Err, unwrapped, so each caller
// keeps the words it already puts in front of it. On an error the Reply is
// what had arrived before the failing event, and nothing of that event is in
// it; callers that report a cost on a failed read take Usage from there.
func Collect(ctx context.Context, events <-chan StreamEvent) (Reply, error) {
	var r Reply
	var text strings.Builder
	for {
		select {
		case <-ctx.Done():
			r.Text = text.String()
			return r, ctx.Err()
		case ev, ok := <-events:
			if !ok {
				r.Text = text.String()
				return r, nil
			}
			if ev.Err != nil {
				r.Text = text.String()
				return r, ev.Err
			}
			text.WriteString(ev.Token)
			r.Calls = append(r.Calls, ev.ToolCalls...)
			if ev.Usage != nil {
				r.Usage = ev.Usage
			}
			if ev.Done {
				r.Text = text.String()
				return r, nil
			}
		}
	}
}
