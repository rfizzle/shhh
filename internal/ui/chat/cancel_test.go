package chat

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// streamingCancelModel is a model mid-stream with an empty draft: the state
// both two-press windows are about.
func streamingCancelModel(t *testing.T) Model {
	t.Helper()
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, multiTokenStream("partial", " content"), Wiring{})
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

// idleCancelModel is a ready session with nothing running and an empty draft:
// the state the quit's first press is about.
func idleCancelModel(t *testing.T) Model {
	t.Helper()
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream, Wiring{})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	return updated.(Model)
}

// The first press of the one chord stops a working run, arms nothing, and
// leaves nothing behind that a second press could mistake for a quit.
func TestCancel_FirstPressStopsTheRun(t *testing.T) {
	m := streamingCancelModel(t)

	m, _ = pressKey(t, m, ctrlC)
	if m.state != stateInput {
		t.Fatal("the first ctrl+c over a working turn must stop it")
	}
	if note, ok := m.armedHint(); ok {
		t.Fatalf("a stop must open no window, the rail says %+v", note)
	}
	if m.quitting {
		t.Fatal("a stop must not quit")
	}
}

// Esc is the key that leaves whatever is open, so it never stops a turn: on
// an empty draft under a streaming one it does nothing at all
// (docs/interface/principles.md#esc-is-always-the-safe-answer).
func TestCancel_EscOnAnEmptyStreamingDraftIsInert(t *testing.T) {
	m := streamingCancelModel(t)

	m, _ = pressKey(t, m, escK)
	if m.state != stateStreaming {
		t.Fatal("esc must leave the stream live")
	}
	if note, ok := m.armedHint(); ok {
		t.Fatalf("esc must arm nothing, the rail says %+v", note)
	}
	m, _ = pressKey(t, m, escK)
	if m.state != stateStreaming {
		t.Fatal("a second esc must not cancel the turn either")
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
	if _, ok := m.armedHint(); ok {
		t.Fatal("clearing the draft must not arm the quit")
	}
}

func TestCancel_AnotherKeystrokeDisarms(t *testing.T) {
	m := idleCancelModel(t)

	m, _ = pressKey(t, m, ctrlC)
	m, _ = pressKey(t, m, tea.KeyPressMsg{Code: 'x', Text: "x"})
	m, _ = pressKey(t, m, ctrlC)
	if m.quitting {
		t.Fatal("typing between the presses must disarm the window")
	}
	if m.input.Value() != "" {
		t.Fatalf("the press after typing is the clear, got draft %q", m.input.Value())
	}
}

func TestCancel_ExpiryMessageRevertsTheHint(t *testing.T) {
	m := idleCancelModel(t)

	m, _ = pressKey(t, m, ctrlC)
	updated, _ := m.Update(armExpiredMsg{seq: m.armed.seq})
	m = updated.(Model)
	if _, ok := m.armedHint(); ok {
		t.Fatal("the expiry message must shut the window silently")
	}
	// A stale expiry for a window already replaced changes nothing.
	m, _ = pressKey(t, m, ctrlC)
	updated, _ = m.Update(armExpiredMsg{seq: m.armed.seq - 1})
	m = updated.(Model)
	if _, ok := m.armedHint(); !ok {
		t.Fatal("a stale expiry must not shut the new window")
	}
}

// Quitting over a live turn by the typed command is a question, not a chord:
// the confirm names what it would cancel and what the autosave keeps.
func TestQuit_OverALiveTurnAsksFirst(t *testing.T) {
	m := streamingCancelModel(t)

	next, _ := m.openQuitConfirm()
	m = next.(Model)
	if m.state != stateQuitConfirm {
		t.Fatalf("quitting over a live turn must open the confirm, got state %d", m.state)
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
	next, _ = m.openQuitConfirm()
	m = next.(Model)
	m, cmd = pressKey(t, m, tea.KeyPressMsg{Code: 'y', Text: "y"})
	if !m.quitting || cmd == nil {
		t.Fatal("[y] on the quit confirm must emit the quit cmd")
	}
}

// Over a card or a picker the chord escalates as it does everywhere: cancel
// first, then quit. The first press does what the surface lets it do (back
// out) and opens the quit window; a second press of the same chord inside it
// quits, and a different key inside the window completes nothing.
func TestQuit_NeedsTwoOfTheSameChordEverywhere(t *testing.T) {
	surfaces := []struct {
		name string
		open func(t *testing.T) Model
		held state
	}{
		{"a picker", func(t *testing.T) Model {
			m := readyModel(t)
			m.input.SetValue("/mode")
			updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			return updated.(Model)
		}, statePick},
		{"the key list", func(t *testing.T) Model {
			m := readyModel(t)
			next, _ := m.openKeyList(keys.OnInput, nil)
			return next.(Model)
		}, stateKeyList},
	}
	for _, tc := range surfaces {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.open(t)
			if m.state != tc.held {
				t.Fatalf("setup: want state %d, got %d", tc.held, m.state)
			}

			m, _ = pressKey(t, m, ctrlC)
			if m.quitting {
				t.Fatal("one ctrl+c must not quit")
			}
			if m.state == tc.held {
				t.Fatal("the first press must do the surface's own cancel")
			}
			if !m.armed.openOn(armQuit, quitChord()) {
				t.Fatal("the first press must open the quit window")
			}

			// Another key inside the window completes no quit.
			other, _ := pressKey(t, m, ctrlD)
			if other.quitting {
				t.Fatal("ctrl+d inside the window must not quit")
			}

			m, cmd := pressKey(t, m, ctrlC)
			if !m.quitting || cmd == nil {
				t.Fatal("the second ctrl+c inside the window must quit")
			}
		})
	}
}

// ctrl+c is the one quit and it escalates one step at a time: stop the run,
// then (draft or none) clear it, then arm, then quit. A stop never becomes a
// quit by itself, so the press after a stop is a new first press.
func TestQuit_CtrlCEscalatesAndNeverSkipsAStep(t *testing.T) {
	m := streamingCancelModel(t)

	// Working: the first press stops, and nothing is armed.
	m, _ = pressKey(t, m, ctrlC)
	if m.state != stateInput || m.quitting {
		t.Fatalf("working: the first press must stop the run only (state %d, quitting %v)", m.state, m.quitting)
	}
	// The window is clear, so the next press is a first press: it arms.
	m, _ = pressKey(t, m, ctrlC)
	if m.quitting {
		t.Fatal("the press after a stop must not quit")
	}
	if _, ok := m.armedHint(); !ok {
		t.Fatal("the press after a stop must arm the quit")
	}
	// A run begins inside the open window: the next press stops it and
	// does not carry the quit out.
	m.state = stateStreaming
	m, _ = pressKey(t, m, ctrlC)
	if m.quitting {
		t.Fatal("a stop must never become a quit by itself")
	}
	if _, ok := m.armedHint(); ok {
		t.Fatal("a stop must leave the window clear")
	}

	// Idle with a draft: clears it, arms nothing.
	m.input.SetValue("half a thought")
	m, _ = pressKey(t, m, ctrlC)
	if m.input.Value() != "" || m.quitting {
		t.Fatalf("idle with a draft: the press must clear it only (draft %q)", m.input.Value())
	}
	if _, ok := m.armedHint(); ok {
		t.Fatal("clearing a draft must not arm the quit")
	}

	// Idle and empty: arm, then quit.
	m, _ = pressKey(t, m, ctrlC)
	if m.quitting {
		t.Fatal("the first press on an empty idle draft must only arm")
	}
	m, cmd := pressKey(t, m, ctrlC)
	if !m.quitting || cmd == nil {
		t.Fatal("the second press inside the window must quit")
	}
}

func TestQuit_IdleTakesTwoPresses(t *testing.T) {
	m := idleCancelModel(t)

	m, _ = pressKey(t, m, ctrlC)
	if m.quitting {
		t.Fatal("a single ctrl+c must not quit")
	}
	if note, ok := m.armedHint(); !ok || note.render() != armedQuitHint() {
		t.Fatalf("the rail must offer the second press, got %+v", note)
	}

	m, cmd := pressKey(t, m, ctrlC)
	if !m.quitting || cmd == nil {
		t.Fatal("the second ctrl+c inside the window must quit")
	}
}

// ctrl+d is reserved, so it quits nowhere: not idle, not twice, not over a
// working turn.
func TestQuit_CtrlDQuitsNowhere(t *testing.T) {
	m := idleCancelModel(t)
	m, _ = pressKey(t, m, ctrlD)
	m, _ = pressKey(t, m, ctrlD)
	if m.quitting {
		t.Fatal("ctrl+d must not quit an idle session")
	}
	if _, ok := m.armedHint(); ok {
		t.Fatal("ctrl+d must arm nothing")
	}
	m = streamingCancelModel(t)
	m, _ = pressKey(t, m, ctrlD)
	if m.quitting || m.state != stateStreaming {
		t.Fatal("ctrl+d must not touch a working turn")
	}
}

func TestQuit_ExpiredWindowArmsAgain(t *testing.T) {
	m := idleCancelModel(t)

	m, _ = pressKey(t, m, ctrlC)
	was := clock
	now := time.Now()
	clock = func() time.Time { return now.Add(pressAgain + time.Second) }
	t.Cleanup(func() { clock = was })

	m, cmd := pressKey(t, m, ctrlC)
	if m.quitting || cmd == nil {
		t.Fatal("a press after the window expired must arm again, not quit")
	}
	if note, ok := m.armedHint(); !ok || note.render() != armedQuitHint() {
		t.Fatal("the late press should have re-armed the window")
	}
}

// Quitting cancels the running command, but a cancellation only schedules the
// kill — inside the process that is about to exit. So the quit finishes the
// stop itself, and a command still running when the session leaves is gone by
// the time it does.
func TestQuit_StopsACommandTheSessionWasRunning(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell")
	}
	t.Setenv("SHELL", "/bin/sh")

	ran := make(chan struct{})
	started := make(chan struct{})
	go func() {
		defer close(ran)
		// Nothing else stops this: the model under test holds no cancel for
		// it, which is exactly the case of a command started through some
		// other surface of the same session. It says when it is running,
		// which is the moment there is a command to leave behind.
		runner.RunCaptureTail(context.Background(), "echo started; sleep 30", func(line string) {
			if line == "started" {
				close(started)
			}
		})
	}()
	select {
	case <-started:
	case <-time.After(factBound):
		t.Fatal("the command never started")
	}

	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	m := New(msgs, mockStream, Wiring{})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.quitNow()

	// The drain is synchronous, so the command is already gone; the bound
	// is for the runner's own return behind it, not for the stop. A command
	// the quit missed sleeps out its thirty seconds, past the bound.
	select {
	case <-ran:
	case <-time.After(20 * time.Second):
		t.Fatal("quitting left a running command behind")
	}
}

// armedQuitHint is the row an open window puts on the rail, built the way
// the rail builds it so a change to the grammar moves the test with the code
// rather than against it.
func armedQuitHint() string {
	return hintSeg{key: keys.Shown(keys.Draft.Cancel), label: "again quits"}.render()
}
