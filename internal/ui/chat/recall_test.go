package chat

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
)

// resumedModel is a session that came back from storage: a conversation
// loaded before the first key is pressed, which is what --continue, --resume,
// /load and a branch switch all reduce to.
func resumedModel(t *testing.T, msgs []provider.Message) Model {
	t.Helper()
	m := New(msgs[:1], multiTokenStream("ok")).
		WithStartScreen(StartInfo{}).
		WithResumedMessages(msgs)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	return updated.(Model)
}

func pressUp(t *testing.T, m Model) Model {
	t.Helper()
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	return updated.(Model)
}

func pressDown(t *testing.T, m Model) Model {
	t.Helper()
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	return updated.(Model)
}

func TestResumedSession_RecallsWhatWasTyped(t *testing.T) {
	m := resumedModel(t, []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "first saved prompt"},
		{Role: provider.RoleAssistant, Content: "an answer"},
		{Role: provider.RoleUser, Content: "second saved prompt"},
		{Role: provider.RoleAssistant, Content: "another answer"},
	})

	m = pressUp(t, m)
	if m.input.Value() != "second saved prompt" {
		t.Fatalf("↑ on a resumed session should recall the newest prompt, got %q", m.input.Value())
	}
	m = pressUp(t, m)
	if m.input.Value() != "first saved prompt" {
		t.Fatalf("↑ again should walk back, got %q", m.input.Value())
	}
	m = pressDown(t, m)
	if m.input.Value() != "second saved prompt" {
		t.Fatalf("↓ should walk forward, got %q", m.input.Value())
	}
	m = pressDown(t, m)
	if m.input.Value() != "" {
		t.Fatalf("↓ past the newest entry should clear the draft, got %q", m.input.Value())
	}
}

// A prompt typed this sitting joins the recalled ones rather than replacing
// them: resuming does not start the history over.
func TestResumedSession_NewInputJoinsTheHistory(t *testing.T) {
	m := resumedModel(t, []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "saved prompt"},
		{Role: provider.RoleAssistant, Content: "an answer"},
	})

	m = sendText(t, m, "typed this sitting")
	updated, _ := m.Update(doneMsg{})
	m = updated.(Model)

	m = pressUp(t, m)
	if m.input.Value() != "typed this sitting" {
		t.Fatalf("expected the newest prompt, got %q", m.input.Value())
	}
	m = pressUp(t, m)
	if m.input.Value() != "saved prompt" {
		t.Fatalf("expected the resumed prompt behind it, got %q", m.input.Value())
	}
}

// The three user-role messages the session writes for itself are not lines
// anyone typed, so ↑ never offers them (recall.go).
func TestResumedSession_SkipsWhatNobodyTyped(t *testing.T) {
	m := resumedModel(t, []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: compactContextMessage("the session so far, summarised")},
		{Role: provider.RoleUser, Content: "the only thing anyone typed"},
		{Role: provider.RoleAssistant, Content: "an answer"},
		{Role: provider.RoleUser, Content: commandContextMessage("go test ./...", "ok", 0)},
		{Role: provider.RoleUser, Content: continuePrompt},
	})

	if got := len(m.inputHistory); got != 1 {
		t.Fatalf("expected one recallable prompt, got %d: %q", got, m.inputHistory)
	}
	m = pressUp(t, m)
	if m.input.Value() != "the only thing anyone typed" {
		t.Fatalf("↑ recalled a line nobody typed: %q", m.input.Value())
	}
	// Nothing older to reach: the draft stays on the one real prompt.
	m = pressUp(t, m)
	if m.input.Value() != "the only thing anyone typed" {
		t.Fatalf("↑ past the oldest entry should hold, got %q", m.input.Value())
	}
}

// Loading a second conversation replaces the ring rather than concatenating
// two histories: the ring belongs to the conversation on screen.
func TestLoadConversation_ReplacesTheHistory(t *testing.T) {
	m := resumedModel(t, []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "a prompt from the first conversation"},
	})
	m.loadConversation([]provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "a prompt from the second"},
	})

	if len(m.inputHistory) != 1 || m.inputHistory[0] != "a prompt from the second" {
		t.Fatalf("expected only the loaded conversation's prompts, got %q", m.inputHistory)
	}
	if m.historyIdx != len(m.inputHistory) {
		t.Fatalf("a fresh load should not be mid-browse: idx %d of %d", m.historyIdx, len(m.inputHistory))
	}
}

// recordInput's rules are the loaded conversation's rules, because it is the
// one that does the appending: a prompt repeated back to back is one entry.
func TestResumedSession_CollapsesRepeatedPrompts(t *testing.T) {
	m := resumedModel(t, []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "try again"},
		{Role: provider.RoleAssistant, Content: "an answer"},
		{Role: provider.RoleUser, Content: "try again"},
	})

	if got := len(m.inputHistory); got != 1 {
		t.Fatalf("expected consecutive repeats to collapse, got %d: %q", got, m.inputHistory)
	}
}

// An empty conversation is an empty ring, and ↑ leaves the draft alone.
func TestResumedSession_NothingToRecall(t *testing.T) {
	m := resumedModel(t, []provider.Message{{Role: provider.RoleSystem, Content: "sys"}})

	m.input.SetValue("")
	m = pressUp(t, m)
	if m.input.Value() != "" {
		t.Fatalf("↑ with nothing to recall should leave the draft empty, got %q", m.input.Value())
	}
}
