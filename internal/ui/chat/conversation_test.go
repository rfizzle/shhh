package chat

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/golden"
)

// A conversation reads the web without a card: the fetch runs, its row says
// the conversation's own rule allowed it, and the record files it under a
// reason of its own (docs/capabilities/chat.md#a-conversation-has-one-mode).
func TestConversation_AFetchRunsWithoutACard(t *testing.T) {
	var fetched []string
	executor := func(name string, args json.RawMessage) (string, error) {
		fetched = append(fetched, name)
		return "page text", nil
	}
	var decisions [][2]string
	m := gatedModel(t, executor, fetchPreviews()).WithConversation().
		WithObserver(observe.Observer{Decision: func(_ observe.Pos, decision, reason string) {
			decisions = append(decisions, [2]string{decision, reason})
		}})

	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		fetchCall("call_1", "https://docs.python.org/3/library/json.html"),
	}})
	m = updated.(Model)
	if m.state == stateConfirmRun {
		t.Fatal("a conversation put a fetch to the person")
	}
	if m.pendingApproval == nil || m.pendingApproval.autoRule != agent.ConversationReadReason {
		t.Fatalf("the fetch was not allowed by the conversation's rule: %+v", m.pendingApproval)
	}
	m = drainApproved(t, m, cmd)
	if len(fetched) != 1 {
		t.Fatalf("the fetch ran %d times; want once", len(fetched))
	}
	want := [2]string{observe.DecisionAllow, observe.ReasonConversationRead}
	if len(decisions) != 1 || decisions[0] != want {
		t.Errorf("recorded %v; want %v", decisions, want)
	}
}

// The deny list is still read first: a refused host is refused in a
// conversation exactly as it is in a coding session, and nothing is fetched.
func TestConversation_TheHostDenyListStillRefuses(t *testing.T) {
	var fetched int
	executor := func(string, json.RawMessage) (string, error) { fetched++; return "page text", nil }
	m := gatedModel(t, executor, fetchPreviews()).WithConversation().
		WithHostRules(nil, []string{"paste.example.test"})
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		fetchCall("call_d", "https://paste.example.test/x"),
	}})
	m = updated.(Model)
	if m.state == stateConfirmRun || fetched != 0 {
		t.Fatalf("a denied host was carded or fetched (state %d, fetched %d)", m.state, fetched)
	}
	if !strings.Contains(m.lastDenial, "web.deny_hosts") {
		t.Errorf("the refusal does not name the host list: %q", m.lastDenial)
	}
}

// A conversation has one mode. A configured default does not move it, the
// chord and /permissions answer with a sentence instead of switching, no
// picker opens, the frame says read-only, and the rail offers no key for a
// mode.
func TestConversation_HasOneMode(t *testing.T) {
	m := gatedModel(t, nil, nil).WithConversation().
		WithApprovalMode(agent.ModeAuto, []agent.Mode{agent.ModeAuto, agent.ModePlan})
	m.state = stateInput
	if m.policy.mode != agent.ModeManual {
		t.Fatalf("a configured mode reached a conversation: %v", m.policy.mode)
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = updated.(Model)
	if m.policy.mode != agent.ModeManual {
		t.Fatalf("the chord moved a conversation's mode to %v", m.policy.mode)
	}
	if last := m.transcript[len(m.transcript)-1]; last.kind != entrySystem || last.text != conversationModeNote {
		t.Errorf("the chord did not say a conversation has one mode: %+v", last)
	}

	if got := slashPermissions(&m, []string{"/permissions", "plan"}); got != conversationModeNote {
		t.Errorf("/permissions plan answered %q", got)
	}
	if m.policy.mode != agent.ModeManual {
		t.Fatalf("/permissions moved a conversation's mode to %v", m.policy.mode)
	}
	for _, o := range modeArgs(&m) {
		if _, err := agent.ParseMode(o.value); err == nil {
			t.Errorf("the completion offers the mode %q in a conversation", o.value)
		}
	}

	picked, _ := m.openModePick()
	if pm := picked.(Model); pm.state == statePick {
		t.Error("the mode picker opened in a conversation")
	}

	if got := m.cockpitData(false).Mode; got != "read-only" {
		t.Errorf("the frame's mode word is %q; want read-only", got)
	}
}

// The key rail offers the mode chord in a coding session and not in a
// conversation, which has no mode for it to cycle.
func TestConversation_TheRailOffersNoModeKey(t *testing.T) {
	coding := gatedModel(t, nil, nil)
	coding.state = stateInput
	if got := coding.frameHints(200); !strings.Contains(got, "mode") {
		t.Fatalf("the control is wrong: a coding session's rail offers no mode key: %q", got)
	}
	chat := gatedModel(t, nil, nil).WithConversation()
	chat.state = stateInput
	if got := chat.frameHints(200); strings.Contains(got, "mode") {
		t.Errorf("the rail offers a key for a mode in a conversation: %q", got)
	}
}

// The key list leaves the mode chord out of a conversation the way the rail
// does — through the chord that prints it and through /help's key section,
// which are one list — and keeps it in a coding session.
func TestConversation_TheKeyListOffersNoModeKey(t *testing.T) {
	const row = "cycle the permission mode"
	coding := gatedModel(t, nil, nil)
	if !strings.Contains(coding.helpKeys(), row) {
		t.Fatal("the control is wrong: a coding session's key list has no mode row")
	}
	chat := gatedModel(t, nil, nil).WithConversation()
	chat.state = stateInput
	if got := helpText(&chat); strings.Contains(got, row) {
		t.Errorf("/help's key section offers the mode chord in a conversation:\n%s", got)
	}
	updated, _ := chat.Update(tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl})
	chat = updated.(Model)
	if !transcriptContains(chat, "[ctrl+n]") {
		t.Fatal("the key-list chord printed no key list")
	}
	if transcriptContains(chat, row) {
		t.Error("the key list printed in a conversation offers the mode chord")
	}
}

// TestGolden_ScreenChat captures a conversation's frame: the mode word fixed
// at read-only, no key offered for a mode, the sentence the chord answers
// with, and a fetch that ran without a card with the rule that allowed it on
// its row (docs/capabilities/chat.md#a-conversation-has-one-mode).
func TestGolden_ScreenChat(t *testing.T) {
	captureGolden(t, "screen-chat", "a conversation's frame", goldenWidths, func(width int) []golden.Panel {
		base := func() Model {
			m := frameModel(t, width, 24).WithConversation()
			m.state = stateInput
			return m
		}
		chord := base()
		updated, _ := chord.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
		chord = updated.(Model)

		fetch := base().WithToolExecutor(func(string, json.RawMessage) (string, error) {
			return "The cache now expires after an hour.", nil
		}).WithGatedTools(fetchPreviews())
		fetch.state = stateStreaming
		updated, cmd := fetch.Update(toolCallsMsg{calls: []provider.ToolCall{
			fetchCall("call_1", "https://docs.python.org/3/library/json.html"),
		}})
		fetch = drainApproved(t, updated.(Model), cmd)
		fetch.setTurnState(stateInput)

		settle := func(m Model) string {
			m.invalidateRenderCache()
			m.syncViewport()
			m.viewport.SetLines(m.renderHistoryLines())
			m.viewport.GotoBottom()
			return m.View().Content
		}
		return []golden.Panel{
			{Label: "a conversation at rest", View: settle(base())},
			{Label: "the mode chord, answered", View: settle(chord)},
			{Label: "a fetch that ran without a card", View: settle(fetch)},
		}
	})
}
