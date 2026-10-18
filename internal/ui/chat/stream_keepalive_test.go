package chat

import (
	"strings"
	"testing"

	"github.com/rfizzle/llm"
)

// A batch of pings alone is a keepalive; a ping drained beside text is just
// the text's batch, and the answer is not lost to it.
func TestWaitForEvent_AKeepaliveBatchIsOnlyPings(t *testing.T) {
	events := make(chan llm.StreamEvent, 3)
	events <- llm.StreamEvent{Keepalive: true}
	events <- llm.StreamEvent{Keepalive: true}
	msg := waitForEvent(events)()
	if _, ok := msg.(keepaliveMsg); !ok {
		t.Errorf("two pings should batch as one keepalive, got %#v", msg)
	}

	events <- llm.StreamEvent{Keepalive: true}
	events <- llm.StreamEvent{Token: "hi"}
	tok, ok := waitForEvent(events)().(tokenMsg)
	if !ok || tok.text != "hi" {
		t.Errorf("a ping beside text should batch as the text, got %#v", tok)
	}
}

// A read that was waiting on the channel when the run was cancelled brings
// back the cancelled turn's tail. It is dropped without a trace: not drawn,
// not added to the next turn's reply, and the stream is not read again.
func TestStream_ALateMessageFromACancelledRunIsDropped(t *testing.T) {
	m := streamingCancelModel(t)
	events := make(chan llm.StreamEvent, 2)
	events <- llm.StreamEvent{Token: "late tail"}
	m.events = events
	read := m.waitForNext()

	m, _ = pressKey(t, m, ctrlC)
	before := len(m.transcript)

	updated, cmd := m.Update(read())
	m = updated.(Model)
	if cmd != nil {
		t.Error("a dropped message must not ask for the next one")
	}
	if m.streaming != "" || strings.Contains(m.View().Content, "late tail") {
		t.Errorf("the late message reached the screen: streaming %q", m.streaming)
	}
	if len(m.transcript) != before {
		t.Errorf("the transcript moved from %d to %d entries", before, len(m.transcript))
	}

	// The current run's own read is still taken.
	m.events = events
	events <- llm.StreamEvent{Token: "fresh"}
	m.setTurnState(stateStreaming)
	updated, _ = m.Update(m.waitForNext()())
	m = updated.(Model)
	if !strings.Contains(m.streaming, "fresh") {
		t.Errorf("a message of the current run was dropped: streaming %q", m.streaming)
	}
}
