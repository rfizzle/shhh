package cli

import (
	"context"
	"sync"
	"time"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
)

// oneShotTimer splits the one-shot's single turn by what it waited on: the
// model before its first event and while it writes, the person at the result
// surface, and the command they chose running. The one-shot has no loop to
// mark it, so its requests mark it as they stream (timedProvider), and the
// act marks where the person's choice is carried out. A request's events are
// relayed on a goroutine of their own, which is why the clock is behind a
// lock here when it is not on any other surface.
// See docs/capabilities/sessions-and-memory.md#startup-and-waits-are-timed.
type oneShotTimer struct {
	mu sync.Mutex
	// now is the clock the stamps are read off; nil is the wall clock.
	now   func() time.Time
	start time.Time
	clock agent.TurnClock
	// answered is whether a request that has finished leaves the turn
	// waiting on a person: the result surface is drawn when the answer is
	// in, and a piped run has nobody to draw it for.
	answered bool
}

func (t *oneShotTimer) stamp() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

// begin starts the turn now.
func (t *oneShotTimer) begin() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.start = t.stamp()
	t.clock.Begin(t.start)
}

func (t *oneShotTimer) mark(to func(*agent.TurnClock, time.Time)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	to(&t.clock, t.stamp())
}

func (t *oneShotTimer) event(drew bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.clock.Event(t.stamp(), drew)
}

// ended marks a request's stream closing.
func (t *oneShotTimer) ended() {
	if t.answered {
		t.mark((*agent.TurnClock).Ask)
	}
}

// working marks the person's choice being carried out.
func (t *oneShotTimer) working() { t.mark((*agent.TurnClock).Tool) }

// span is the turn's duration and its split, ending now.
func (t *oneShotTimer) span() (time.Duration, agent.TurnSplit) {
	t.mu.Lock()
	defer t.mu.Unlock()
	end := t.stamp()
	return end.Sub(t.start), t.clock.Split(end)
}

// timedProvider marks the one-shot's clock with each request it sends: the
// request going out, every event it relays, and the stream closing. It sits
// outside the meter, so what it times is what the person waited for, the
// gate included.
type timedProvider struct {
	provider.Provider
	timer *oneShotTimer
}

func (p timedProvider) StreamCompletion(ctx context.Context, messages []provider.Message, opts provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	p.timer.mark((*agent.TurnClock).Request)
	events, err := p.Provider.StreamCompletion(ctx, messages, opts)
	if err != nil {
		return nil, err
	}
	out := make(chan provider.StreamEvent)
	go func() {
		defer close(out)
		defer p.timer.ended()
		for ev := range events {
			p.timer.event(ev.Token != "" || len(ev.ToolCalls) > 0 || ev.Done)
			select {
			case out <- ev:
			case <-ctx.Done():
				// The reader is gone; the rest is drained so the stream
				// underneath can finish (meter's gate does the same).
				for range events {
				}
				return
			}
		}
	}()
	return out, nil
}
