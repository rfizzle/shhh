package chat

// The conversation's route through the real program: a fetch the model asks
// for runs without a card, and the mode chord answers with a sentence rather
// than a mode (docs/capabilities/chat.md#a-conversation-has-one-mode).

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
)

func TestProgram_AConversationFetchesWithoutACard(t *testing.T) {
	var fetched int
	m, _ := scriptedSession(
		programTurn{text: "Reading the release notes.\n", calls: []provider.ToolCall{
			fetchCall("f-1", "https://docs.example.test/notes"),
		}},
		programTurn{text: "The notes say the cache now expires after an hour."},
	)
	m = m.WithConversation().WithGatedTools(fetchPreviews()).
		WithToolExecutor(func(string, json.RawMessage) (string, error) {
			fetched++
			return "The cache now expires after an hour.", nil
		})
	tm := runProgramAt(t, m, 110, 40)

	waitForText(t, tm, "read-only")
	send(tm, "what do the release notes say")
	waitForText(t, tm, "The notes say the cache now expires after an hour")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	waitForText(t, tm, "A conversation has one mode")

	frame := finalFrame(t, tm)
	frameHas(t, frame, "✓ 1 tool", "read-only")
	if strings.Contains(frame, "Approve") {
		t.Errorf("a card was drawn in a conversation:\n%s", frame)
	}
	if fetched != 1 {
		t.Errorf("the page was fetched %d times; want once", fetched)
	}
}
