package chat

// The backend: what the screen reaches the agent through.
//
// The screen is moving behind a seam one capability at a time, so that what
// stands on the other side of it can one day be a session in another
// process. The seam is shaped like the protocol a served session already
// speaks — requests go out as commands, and what comes back arrives as
// messages off one channel — rather than like the loop the screen holds
// today, because a seam that mirrored the loop would carry the in-process
// shape onto the wire. The stream is the first capability behind it.
// See docs/architecture.md#one-agent-several-front-ends.
//
// What crosses it is the stream's own vocabulary wherever the stream has a
// word (docs/capabilities/headless.md#the-stream-is-the-record-as-it-happens):
// a batch of tokens is `text`, a round that asked for calls is a `tool-call`
// for each with the round's `usage` beside them, and a reply that finished
// is the round's `usage` and, where the turn ends there, its `close`. A failed
// stream comes back as the failure itself, because whether it ends the
// turn or is a retry's `signal` is the screen's to decide. Three things the
// screen draws have no word on the stream, and are named here as gaps for
// the backend that speaks the protocol rather than spelled a second way:
// reasoning text as it arrives, a call's arguments as they are written, and
// a gateway's keepalive.
//
// Nothing here starts a goroutine. Each call returns a command, as the
// screen's own stream calls did, and Bubble Tea runs it where it ran them:
// the screen still touches the loop on its own goroutine, and a backend
// goroutine added in a move meant to change nothing would add races to it.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
)

// Backend is what the screen asks for a reply through and reads it back
// from. Request's command yields either the opened stream — the one channel
// its events arrive on and the cancel that closes it — or the reason it did
// not open; Next's yields what has arrived on that channel as one message.
type Backend interface {
	Request(msgs []provider.Message, choice string) tea.Cmd
	Next(events eventStream) tea.Cmd
}

// eventStream is the channel a backend hands back for one reply. Its events
// are still the provider's: turning them into the stream's own words on the
// way in would take a goroutine between the two channels, which is the step
// the threading model does not take yet. The screen holds it by this name
// and gives it back to Next, and reads nothing off it itself.
type eventStream = <-chan provider.StreamEvent

// inProcessBackend is the backend over the loop the screen holds: the
// request opens the loop's own stream, and the read drains it the way the
// screen always has.
type inProcessBackend struct{ agent *agent.Agent }

// Request starts a stream over an explicit message list (callers pass a
// copy so in-flight requests are immune to later mutation) under an
// explicit tool choice.
func (b inProcessBackend) Request(msgs []provider.Message, choice string) tea.Cmd {
	a := b.agent
	return func() tea.Msg {
		events, cancel, err := a.StreamWithChoice(msgs, choice)
		if err != nil {
			return streamErrMsg{err: err}
		}
		return streamStartedMsg{events: events, cancel: cancel}
	}
}

// Next reads the next batch of the stream.
func (inProcessBackend) Next(events eventStream) tea.Cmd { return waitForEvent(events) }

// waitForEvent reads the next stream event. If it is a token, any further
// tokens already buffered on the channel are drained into a single batch so
// the UI re-renders once per batch instead of once per token. Reasoning text
// drains into the same batch on its own string: it is a different act with a
// row of its own (think.go), and the two never have to be told apart after
// the fact because they never share a field.
func waitForEvent(events eventStream) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-events
		if !ok {
			return doneMsg{}
		}
		if final := terminalMsg(ev); final != nil {
			return final
		}
		var text, think strings.Builder
		text.WriteString(ev.Token)
		think.WriteString(ev.Thinking)
		pinged := ev.Keepalive
		// batch is the message for what has been drained: a batch of pings
		// alone is a keepalive, and one with anything else in it is a batch.
		batch := func(final tea.Msg) tea.Msg {
			if pinged && final == nil && text.Len() == 0 && think.Len() == 0 {
				return keepaliveMsg{}
			}
			return tokenMsg{text: text.String(), think: think.String(), final: final}
		}
		for {
			select {
			case ev, ok := <-events:
				if !ok {
					return batch(doneMsg{})
				}
				if final := terminalMsg(ev); final != nil {
					return batch(final)
				}
				text.WriteString(ev.Token)
				think.WriteString(ev.Thinking)
				pinged = pinged || ev.Keepalive
			default:
				return batch(nil)
			}
		}
	}
}

// terminalMsg converts a non-token stream event into its message, or returns
// nil for a plain token event.
func terminalMsg(ev provider.StreamEvent) tea.Msg {
	if ev.ToolCallDelta != nil {
		// A fragment of a tool call's arguments. It ends the token batch
		// rather than draining into it: what it feeds is a row of its own,
		// and the two never have to be told apart after the fact because
		// they never share a field (activity.go).
		//
		// It is read before the terminal signals below because every parser
		// sends a fragment on an event of its own and puts nothing else on
		// it. A dialect that ever rode a fragment on the event that ended
		// its round would lose the end of the round here, not the fragment.
		return toolDeltaMsg{delta: *ev.ToolCallDelta}
	}
	if ev.Err != nil {
		// The completed tool calls ride the failure: a stream that
		// broke after the model finished writing a call kept that call.
		return streamErrMsg{err: ev.Err, calls: ev.ToolCalls, reasoning: ev.Reasoning}
	}
	if len(ev.ToolCalls) > 0 {
		return toolCallsMsg{calls: ev.ToolCalls, usage: ev.Usage, reasoning: ev.Reasoning, stop: ev.Stop}
	}
	if ev.Done {
		return doneMsg{usage: ev.Usage, stop: ev.Stop}
	}
	return nil
}
