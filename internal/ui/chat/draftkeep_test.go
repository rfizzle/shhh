package chat

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Esc backs out one level and never destroys
// (docs/interface/principles.md#esc-is-always-the-safe-answer): a sentence in
// the box is still there after it, however many times it is pressed.
func TestDraft_EscKeepsTheText(t *testing.T) {
	m := typeChars(t, readyModel(t), "half a thought")
	for range 3 {
		m, _ = pressKey(t, m, escK)
		if got := m.input.Value(); got != "half a thought" {
			t.Fatalf("esc changed the draft to %q", got)
		}
	}
	if len(m.inputHistory) != 0 {
		t.Fatalf("esc recorded history it had no reason to: %v", m.inputHistory)
	}
}

// ctrl+c's first press clears the sentence and keeps it first, so the up
// arrow brings it back.
func TestDraft_CtrlCRecordsBeforeItClears(t *testing.T) {
	m := typeChars(t, readyModel(t), "half a thought")

	m, _ = pressKey(t, m, ctrlC)
	if got := m.input.Value(); got != "" {
		t.Fatalf("ctrl+c should clear the draft, got %q", got)
	}
	if m.quitting {
		t.Fatal("the first ctrl+c over a draft must not quit")
	}
	m, _ = pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	if got := m.input.Value(); got != "half a thought" {
		t.Fatalf("up should bring the cleared draft back, got %q", got)
	}
}
