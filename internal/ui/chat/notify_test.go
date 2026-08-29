package chat

// The summons and its two gates (S-157,
// docs/interface/surfaces.md#when-you-are-not-there).
//
// The rule is an edge and a fact: shhh notifies on the transition into
// waiting, and only where the terminal has said the window is not in front.
// What is worth asserting is every way that rule says no — while the reader
// is looking, while the session is still working, a second time while it is
// already waiting, and for something that was never a turn — and that when it
// says yes, it says the words that are on the screen it is calling the reader
// back to.

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/caps"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// notifyModel is a session on a terminal that answered the OSC 99 query, with
// a turn in flight and the window reported away.
func notifyModel(t *testing.T) Model {
	t.Helper()
	m := New(nil, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	m.caps = caps.Terminal{Asked: true, Notifications: true}
	m.away = true
	m.turnCount = 1
	m.turnOpen, m.turnOutcome = true, components.TurnDone
	m.turnStarted = time.Now().Add(-time.Minute)
	m.state = stateStreaming
	return m
}

// notifyRaw runs the command and returns the bytes it would write.
func notifyRaw(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a notification, got nil")
	}
	msg, ok := cmd().(tea.RawMsg)
	if !ok {
		t.Fatalf("expected a tea.RawMsg, got %T", cmd())
	}
	s, ok := msg.Msg.(string)
	if !ok {
		t.Fatalf("expected the raw message to carry a string, got %T", msg.Msg)
	}
	return s
}

// ended is the same session after its turn closed.
func ended(m Model) Model {
	m.setTurnState(stateInput)
	return m
}

func TestNotify_AFinishedTurnSaysHowItEnded(t *testing.T) {
	prev := notifyModel(t)
	seq := notifyRaw(t, ended(prev).notifyCmd(prev))

	if !strings.Contains(seq, notifyName+" · Turn done") {
		t.Errorf("the notification does not name the program and how the turn ended:\n%q", seq)
	}
}

func TestNotify_SaysNothingWhileTheReaderIsLookingAtTheScreen(t *testing.T) {
	prev := notifyModel(t)
	prev.away = false
	if cmd := ended(prev).notifyCmd(prev); cmd != nil {
		t.Error("shhh notified a reader who was watching the screen")
	}
}

func TestNotify_SaysNothingWhenTheSettingIsOff(t *testing.T) {
	prev := notifyModel(t)
	prev.notifyOn = false
	if cmd := ended(prev).notifyCmd(prev); cmd != nil {
		t.Error("appearance.notify off did not silence the notification")
	}
}

func TestNotify_SaysNothingTwiceForOneWait(t *testing.T) {
	prev := notifyModel(t)
	next := ended(prev)
	if cmd := next.notifyCmd(prev); cmd == nil {
		t.Fatal("expected the transition into waiting to notify")
	}
	// The reader has not come back, and nothing has changed. A second
	// notification would be shhh saying the same thing again.
	if cmd := next.notifyCmd(next); cmd != nil {
		t.Error("a session that was already waiting notified again")
	}
}

func TestNotify_SaysNothingForWhatWasNeverATurn(t *testing.T) {
	// A /run the reader started themselves reaches the input the same way a
	// turn does, and closes nothing on the way (S-098).
	prev := notifyModel(t)
	prev.turnOpen = false
	prev.state = stateRunningCmd
	if cmd := ended(prev).notifyCmd(prev); cmd != nil {
		t.Error("a command the reader ran themselves raised a notification")
	}
}

func TestNotify_AnApprovalSaysWhatTheCardSays(t *testing.T) {
	prev := notifyModel(t)
	next := prev
	next.pendingRun = "rm -rf build"
	next.setTurnState(stateConfirmRun)

	seq := notifyRaw(t, next.notifyCmd(prev))
	card := next.buildApprovalCard()
	for _, want := range []string{card.Title, card.Headline} {
		if !strings.Contains(seq, want) {
			t.Errorf("the notification does not say the card's own words %q:\n%q", want, seq)
		}
	}
}

func TestNotify_AChildAskArrivesWhileTheParentIsStillWorking(t *testing.T) {
	// A routed approval is a queue rather than a turn state: the parent
	// keeps streaming underneath, so working() never drops and the wait has
	// to be seen some other way.
	prev := notifyModel(t)
	next := prev
	next.childAsks = []*subagent.Ask{subagent.NewAsk("scout", subagent.AskCommand, "run go build")}

	seq := notifyRaw(t, next.notifyCmd(prev))
	if !strings.Contains(seq, "scout") {
		t.Errorf("the notification does not name the agent that is blocked:\n%q", seq)
	}
	if !next.working() {
		t.Fatal("the parent turn should still be in flight")
	}
}

func TestNotify_FocusMessagesAreWhatDecidesWhetherAnyoneIsLooking(t *testing.T) {
	m := New(nil, mockStream)
	if m.away {
		t.Fatal("a session nobody has told about focus must not assume the reader is gone")
	}
	updated, _ := m.Update(tea.BlurMsg{})
	if !updated.(Model).away {
		t.Fatal("a blur should mean the window is not the one in front")
	}
	updated, _ = updated.(Model).Update(tea.FocusMsg{})
	if updated.(Model).away {
		t.Fatal("focus coming back should mean the reader can see the screen again")
	}
}

func TestNotify_TheViewAsksForFocusReporting(t *testing.T) {
	m := New(nil, mockStream)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	if !updated.(Model).View().ReportFocus {
		t.Fatal("without focus reporting nothing can ever say the window is away")
	}
}

func TestNotifyCommand_SaysWhatItIsAndSavesIt(t *testing.T) {
	m := notifyModel(t)
	var wroteKey, wroteValue string
	m.writeConfig = func(key, value string) error {
		wroteKey, wroteValue = key, value
		return nil
	}
	if out := m.notifyCommand([]string{"ui", "notify", "off"}); !strings.Contains(out, "Saved") {
		t.Errorf("/ui notify off did not report the setting was saved: %q", out)
	}
	if m.notifyOn {
		t.Error("/ui notify off left notifications on")
	}
	if wroteKey != "appearance.notify" || wroteValue != "false" {
		t.Errorf("wrote %q=%q, want appearance.notify=false", wroteKey, wroteValue)
	}
	// The readout names the dialect, because "on" and "on and audible" are
	// different facts about the terminal.
	if out := m.notifyCommand([]string{"ui", "notify"}); !strings.Contains(out, "off") {
		t.Errorf("the readout does not report the new state: %q", out)
	}
	m.notifyOn = true
	if out := m.notifyCommand([]string{"ui", "notify"}); !strings.Contains(out, "OSC 99") {
		t.Errorf("the readout does not name the dialect the terminal answered for: %q", out)
	}
}
