package chat

// A stall, walked through the real program: the provider refuses a request
// as overloaded, the session waits it out under the retry wait's meter and
// asks again, and the answer lands. The scene `retry-wait` is the same route
// in a terminal; this is it where there is no tmux.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// stallingProvider refuses its first requests with one failure and answers
// every request after them with the same text.
type stallingProvider struct {
	mu      sync.Mutex
	refuse  int
	fail    *provider.Failure
	answer  string
	started int
}

func (p *stallingProvider) Name() string { return "scripted" }

func (p *stallingProvider) StreamCompletion(context.Context, []provider.Message, provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	p.mu.Lock()
	p.started++
	refused := p.started <= p.refuse
	p.mu.Unlock()
	if refused {
		return nil, p.fail
	}
	ch := make(chan provider.StreamEvent, 2)
	ch <- provider.StreamEvent{Token: p.answer}
	ch <- provider.StreamEvent{Done: true}
	close(ch)
	return ch, nil
}

// A 503 read as overloaded is a stall the session waits out on its own: the
// wait row names the attempt against the configured bound and the one way
// out, and the request after it is answered onto the same turn.
func TestProgram_AStalledRequestIsWaitedOutAndAnswered(t *testing.T) {
	p := &stallingProvider{
		refuse: 1,
		// RetryAfter is the provider naming its own wait, which the backoff
		// believes down to its one-second floor: it keeps the wait this test
		// sits out at about a second rather than the two the doubling starts
		// from, without a seam of the test's own in the session.
		fail: &provider.Failure{
			Class: provider.ClassOverloaded, Status: 503, Provider: "openai",
			Message:    "The upstream model is overloaded, try again shortly",
			RetryAfter: time.Millisecond,
		},
		answer: "The loop stops at the round cap, and nowhere else.",
	}
	retries := 3
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, streamOf(p), Wiring{RetryLimit: &retries})
	tm := runProgramAt(t, m, 110, 40)

	tm.Type("why does the loop stop")
	tm.Send(programEnter)
	// The wait is drawn for a second and the frames are read every ten
	// milliseconds, so both halves of the row are read while it is up.
	waitForText(t, tm, "attempt 1 of 3")
	waitForText(t, tm, "[esc] stop the turn")

	waitForText(t, tm, "and nowhere else")
	frame := finalFrame(t, tm)
	if strings.Contains(frame, "attempt 1 of 3") {
		t.Errorf("the wait is still drawn after the answer landed:\n%s", frame)
	}
	p.mu.Lock()
	started := p.started
	p.mu.Unlock()
	if started != 2 {
		t.Errorf("the provider was asked %d times, want the refused request and its retry", started)
	}
}

// A turn that broke is retried from a half-typed line: the newest failure
// names itself as the last failure while nothing is selected, the handover
// gives it the keyboard, its letter asks again, and the sentence being typed
// stays in the draft (docs/interface/surfaces.md#the-recovery-row).
func TestProgram_TheLastFailureIsRetriedFromAHalfTypedLine(t *testing.T) {
	p := &stallingProvider{
		refuse: 1,
		// Unclassified is never waited out on its own, so the row is what
		// the turn ends on and the retry is the reader's.
		fail: &provider.Failure{
			Class: provider.ClassUnclassified, Status: 400, Provider: "openai",
			Message: "Unknown parameter: 'reasoning.effort'",
		},
		answer: "The loop stops at the round cap, and nowhere else.",
	}
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, streamOf(p), Wiring{})
	tm := runProgramAt(t, m, 110, 40)

	tm.Type("why does the loop stop")
	tm.Send(programEnter)
	offer := keys.Bracket(keys.Row.Retry) + " retry the last failure"
	waitForText(t, tm, offer)

	tm.Type(draftSentence)
	waitForText(t, tm, draftSentence)
	tm.Send(answerMsg(t))
	tm.Type(keys.Shown(keys.Row.Retry))

	waitForText(t, tm, "and nowhere else")
	frame := finalFrame(t, tm)
	if !strings.Contains(frame, draftSentence) {
		t.Errorf("the retry took the half-typed line with it:\n%s", frame)
	}
	if strings.Contains(frame, offer) {
		t.Errorf("the failure still offers itself as the last one after the retry answered:\n%s", frame)
	}
	p.mu.Lock()
	started := p.started
	p.mu.Unlock()
	if started != 2 {
		t.Errorf("the provider was asked %d times, want the refused request and its retry", started)
	}
}
