package chat

import (
	"testing"

	"github.com/rfizzle/shhh/internal/provider"
)

// A batch of pings alone is a keepalive; a ping drained beside text is just
// the text's batch, and the answer is not lost to it.
func TestWaitForEvent_AKeepaliveBatchIsOnlyPings(t *testing.T) {
	events := make(chan provider.StreamEvent, 3)
	events <- provider.StreamEvent{Keepalive: true}
	events <- provider.StreamEvent{Keepalive: true}
	msg := waitForEvent(events)()
	if _, ok := msg.(keepaliveMsg); !ok {
		t.Errorf("two pings should batch as one keepalive, got %#v", msg)
	}

	events <- provider.StreamEvent{Keepalive: true}
	events <- provider.StreamEvent{Token: "hi"}
	tok, ok := waitForEvent(events)().(tokenMsg)
	if !ok || tok.text != "hi" {
		t.Errorf("a ping beside text should batch as the text, got %#v", tok)
	}
}
