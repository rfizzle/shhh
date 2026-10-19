package subagent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rfizzle/shhh/internal/permission"
	"github.com/rfizzle/shhh/internal/web"
)

// A conversation's child reads the web the way the conversation does: a
// fetch is allowed without a card routed up to the person, the host deny
// list still refuses first, and a coding session's child is untouched
// (docs/capabilities/chat.md#a-conversation-has-one-mode).
func TestConversationChildFetchesWithoutAsking(t *testing.T) {
	sup := New(context.Background(), Options{
		Root:      t.TempDir(),
		DenyHosts: []string{"paste.example.test"},
	})
	t.Cleanup(sup.Close)
	sup.SetParentMode(permission.ModeManual)
	c := &child{mode: permission.ModeManual}
	decide := func(rawURL string) (permission.Decision, string) {
		t.Helper()
		action, err := actionFor(web.FetchToolName, json.RawMessage(`{"url":"`+rawURL+`"}`))
		if err != nil {
			t.Fatalf("actionFor: %v", err)
		}
		return sup.childPolicy(c).Decide(action)
	}

	if got, _ := decide("https://pkg.go.dev/context"); got != permission.Ask {
		t.Fatalf("a coding session's child = %v; want Ask", got)
	}
	sup.SetConversationPolicy()
	if got, reason := decide("https://pkg.go.dev/context"); got != permission.Allow || reason != permission.ConversationReadReason {
		t.Errorf("a conversation's child = %v (%q); want Allow as a conversation read", got, reason)
	}
	if got, _ := decide("https://paste.example.test/x"); got != permission.Deny {
		t.Errorf("a denied host = %v; want Deny in a conversation's child too", got)
	}
}
