package chat

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
)

// streamingCancelModel is a model mid-stream with an empty draft: the state
// both two-press windows are about.
func streamingCancelModel(t *testing.T) Model {
	t.Helper()
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, multiTokenStream("partial", " content"))
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.input.SetValue("go")
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	events, cancel, _ := multiTokenStream("partial")(m.Messages(), provider.ToolChoiceAuto)
	updated, _ = m.Update(streamStartedMsg{events: events, cancel: cancel})
	m = updated.(Model)
	updated, _ = m.Update(tokenMsg{text: "partial"})
	return updated.(Model)
}

func pressKey(t *testing.T, m Model, msg tea.KeyPressMsg) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(msg)
	return updated.(Model), cmd
}

var (
	ctrlC = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	ctrlD = tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}
	escK  = tea.KeyPressMsg{Code: tea.KeyEscape}
)

func TestCancel_FirstPressArmsSecondCancels(t *testing.T) {
	m := streamingCancelModel(t)

	m, _ = pressKey(t, m, ctrlC)
	if m.state != stateStreaming {
		t.Fatal("a single ctrl+c must leave the stream live")
	}
	if note := m.armedNotice(); note != "ctrl+c again cancels the turn" {
		t.Fatalf("the rail must say what the second press does, got %q", note)
	}

	m, _ = pressKey(t, m, ctrlC)
	if m.state != stateInput {
		t.Fatal("the second ctrl+c inside the window must cancel the turn")
	}
}

func TestCancel_ExpiredWindowArmsAgain(t *testing.T) {
	m := streamingCancelModel(t)

	m, _ = pressKey(t, m, ctrlC)
	// Advance the clock past the window by moving its deadline behind now.
	m.armed.deadline = time.Now().Add(-100 * time.Millisecond)

	m, _ = pressKey(t, m, ctrlC)
	if m.state != stateStreaming {
		t.Fatal("a press after the window expired must arm again, not cancel")
	}
	if m.armedNotice() == "" {
		t.Fatal("the late press should have re-armed the window")
	}
}

// Esc is the key that leaves whatever is open, so it never stops a turn: on
// an empty draft under a streaming one it does nothing at all, including to
// a window the cancel chord opened
// (docs/interface/principles.md#esc-is-always-the-safe-answer).
func TestCancel_EscOnAnEmptyStreamingDraftIsInert(t *testing.T) {
	m := streamingCancelModel(t)

	m, _ = pressKey(t, m, escK)
	if m.state != stateStreaming {
		t.Fatal("esc must leave the stream live")
	}
	if note := m.armedNotice(); note != "" {
		t.Fatalf("esc must arm nothing, the rail says %q", note)
	}
	m, _ = pressKey(t, m, escK)
	if m.state != stateStreaming {
		t.Fatal("a second esc must not cancel the turn either")
	}

	m, _ = pressKey(t, m, ctrlC)
	if note := m.armedNotice(); note != "ctrl+c again cancels the turn" {
		t.Fatalf("only the cancel chord arms, and the rail names it: %q", note)
	}
	m, _ = pressKey(t, m, escK)
	if note := m.armedNotice(); note != "ctrl+c again cancels the turn" {
		t.Fatalf("esc must leave an open window as it found it, got %q", note)
	}
	m, _ = pressKey(t, m, ctrlC)
	if m.state != stateInput {
		t.Fatal("the cancel chord's second press must still cancel the turn")
	}
}

func TestCancel_EscWithDraftClearsItFirst(t *testing.T) {
	m := streamingCancelModel(t)
	m.input.SetValue("foo")

	m, _ = pressKey(t, m, escK)
	if m.input.Value() != "" {
		t.Fatalf("esc with a draft must clear it, got %q", m.input.Value())
	}
	if m.state != stateStreaming {
		t.Fatal("esc spent on the draft must not touch the turn")
	}
	if m.armedNotice() != "" {
		t.Fatal("clearing the draft must not arm the cancel")
	}
}

func TestCancel_AnotherKeystrokeDisarms(t *testing.T) {
	m := streamingCancelModel(t)

	m, _ = pressKey(t, m, ctrlC)
	m, _ = pressKey(t, m, tea.KeyPressMsg{Code: 'x', Text: "x"})
	m, _ = pressKey(t, m, ctrlC)
	if m.state != stateStreaming {
		t.Fatal("typing between the presses must disarm the window")
	}
}

func TestCancel_ExpiryMessageRevertsTheHint(t *testing.T) {
	m := streamingCancelModel(t)

	m, _ = pressKey(t, m, ctrlC)
	updated, _ := m.Update(armExpiredMsg{seq: m.armed.seq})
	m = updated.(Model)
	if m.armedNotice() != "" {
		t.Fatal("the expiry message must shut the window silently")
	}
	// A stale expiry for a window already replaced changes nothing.
	m, _ = pressKey(t, m, ctrlC)
	updated, _ = m.Update(armExpiredMsg{seq: m.armed.seq - 1})
	m = updated.(Model)
	if m.armedNotice() == "" {
		t.Fatal("a stale expiry must not shut the new window")
	}
}

func TestQuit_OverALiveTurnAsksFirst(t *testing.T) {
	m := streamingCancelModel(t)

	m, _ = pressKey(t, m, ctrlD)
	if m.state != stateQuitConfirm {
		t.Fatalf("ctrl+d over a live turn must open the confirm, got state %d", m.state)
	}
	if m.quitAsk == nil || !strings.Contains(m.quitAsk.Prompt, "cancelled") {
		t.Fatal("the confirm must say what quitting cancels")
	}

	// Enter is the default answer, No: nothing stops, nothing quits.
	m, cmd := pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.quitting || cmd != nil {
		t.Fatal("enter on the quit confirm must not quit")
	}
	if m.state != stateStreaming {
		t.Fatalf("declining must hand the screen back to the running turn, got state %d", m.state)
	}

	// Asked again and answered yes, it quits.
	m, _ = pressKey(t, m, ctrlD)
	m, cmd = pressKey(t, m, tea.KeyPressMsg{Code: 'y', Text: "y"})
	if !m.quitting || cmd == nil {
		t.Fatal("[y] on the quit confirm must emit the quit cmd")
	}
}

func TestQuit_IdleTakesTwoPresses(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	m, _ = pressKey(t, m, ctrlD)
	if m.quitting {
		t.Fatal("a single ctrl+d must not quit")
	}
	if note := m.armedNotice(); note != "press again to quit" {
		t.Fatalf("the rail must offer the second press, got %q", note)
	}

	m, cmd := pressKey(t, m, ctrlD)
	if !m.quitting || cmd == nil {
		t.Fatal("the second ctrl+d inside the window must quit")
	}
}

func TestQuit_ExpiredWindowArmsAgain(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)

	m, _ = pressKey(t, m, ctrlD)
	m.armed.deadline = time.Now().Add(-100 * time.Millisecond)

	m, cmd := pressKey(t, m, ctrlD)
	if m.quitting || cmd == nil {
		t.Fatal("a press after the window expired must arm again, not quit")
	}
	if m.armedNotice() != "press again to quit" {
		t.Fatal("the late press should have re-armed the window")
	}
}
