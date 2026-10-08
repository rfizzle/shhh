package chat

// A request that hears nothing, walked through the real program: the frame
// says the turn is waiting on the model and that nothing has arrived, and
// says `thinking…` only once reasoning does. The scene `waiting-on-the-model`
// is the same route in a terminal; this is it where there is no tmux.

import (
	"context"
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

// heldProvider holds its stream open with nothing on it until reason is
// closed, then sends one reasoning event and holds again until the request
// is cancelled.
type heldProvider struct{ reason chan struct{} }

func (p *heldProvider) Name() string { return "scripted" }

func (p *heldProvider) StreamCompletion(ctx context.Context, _ []provider.Message, _ provider.CompletionOpts) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent)
	go func() {
		defer close(ch)
		select {
		case <-p.reason:
		case <-ctx.Done():
			return
		}
		select {
		case ch <- provider.StreamEvent{Thinking: "weighing where the flake comes from"}:
		case <-ctx.Done():
			return
		}
		<-ctx.Done()
	}()
	return ch, nil
}

// The frame reads `waiting…` with the model's own clock and `silent — nothing
// arrived` while the request has heard nothing, and `thinking…` with the
// reasoning's count once it has (docs/interface/surfaces.md#the-input-frame).
func TestProgram_ARequestThatHearsNothingSaysSo(t *testing.T) {
	p := &heldProvider{reason: make(chan struct{})}
	m := New([]provider.Message{{Role: provider.RoleSystem, Content: "sys"}}, streamOf(p))
	tm := runProgramAt(t, m, 110, 40)

	tm.Type("why does the test flake")
	tm.Send(programEnter)
	waitForText(t, tm, "waiting… · model ")
	waitForText(t, tm, "silent — nothing arrived")

	close(p.reason)
	waitForText(t, tm, "thinking… · model ")
	waitForText(t, tm, "1 reasoning event, last ")
}
