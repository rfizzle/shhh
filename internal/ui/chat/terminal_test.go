package chat

// The window the session borrowed (
// docs/interface/surfaces.md#what-the-tab-says).
//
// Four things are worth asserting here, and they are the four ways this can
// be wrong from outside the rectangle: a tab that says the wrong thing, a
// progress light that is left on, a suspend that stops a running turn, and a
// redraw that redraws something other than the screen.

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/caps"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// windowModel is a session on an ordinary terminal, in a named directory.
func windowModel(t *testing.T) Model {
	t.Helper()
	m := readyModel(t)
	m.caps = caps.Terminal{Asked: true}
	m.title = "shhh code"
	m.windowDir = "Projects/shhh"
	return m
}

func TestWindowTitle_NamesTheCommandAndTheDirectory(t *testing.T) {
	m := windowModel(t)
	if got := m.windowTitle(); got != "shhh code · Projects/shhh" {
		t.Errorf("the tab says %q", got)
	}
	if got := m.View().WindowTitle; got != "shhh code · Projects/shhh" {
		t.Errorf("the frame carries %q", got)
	}
}

// A waiting decision is the one state the tab reports, because it is the one
// state the reader has to come back for.
func TestWindowTitle_MarksAWaitingDecision(t *testing.T) {
	m := windowModel(t)
	m.setTurnState(stateConfirmRun)
	if got := m.windowTitle(); !strings.HasPrefix(got, "⏸ ") {
		t.Errorf("a waiting decision is not marked on the tab: %q", got)
	}
	if got := m.windowTitle(); !strings.Contains(got, "shhh code · Projects/shhh") {
		t.Errorf("the marked tab lost its name: %q", got)
	}
}

// Three ways the tab says nothing, and empty is how the frame says it: Bubble
// Tea reads an empty title as "clear it", which is also what puts the tab
// back when the session ends.
func TestWindowTitle_SilentWhereItShouldBe(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(m *Model)
	}{
		{"no terminal was asked", func(m *Model) { m.caps = caps.Terminal{} }},
		{"a dumb terminal", func(m *Model) { m.caps = caps.Terminal{Asked: true, Dumb: true} }},
		{"the reader turned it off", func(m *Model) { m.windowTitleOn = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := windowModel(t)
			tc.mut(&m)
			if got := m.windowTitle(); got != "" {
				t.Errorf("the tab was named anyway: %q", got)
			}
		})
	}
}

// A deep path is the case the shortening exists for, and the root is the case
// that has nothing to shorten.
func TestTabDir_KeepsTheLastTwoSegments(t *testing.T) {
	for _, tc := range []struct{ dir, want string }{
		{"/home/reader/work/Projects/shhh", "Projects/shhh"},
		{"/home/reader", "home/reader"},
		{"/tmp", "tmp"},
		{"/", "/"},
		{"", ""},
		{"relative/path/here", "path/here"},
	} {
		if got := tabDir(tc.dir); got != tc.want {
			t.Errorf("tabDir(%q) = %q, want %q", tc.dir, got, tc.want)
		}
	}
}

// The progress light's whole life: off, on while the turn runs, off when it
// stops. The renderer writes a sequence only where two frames disagree, so
// asserting the frames is asserting the writes.
func TestProgress_RunsWithTheTurnAndStops(t *testing.T) {
	m := windowModel(t)
	if bar := m.progressBar(); bar != nil {
		t.Fatalf("an idle session lights the tab: %v", bar.State)
	}
	m.setTurnState(stateStreaming)
	bar := m.progressBar()
	if bar == nil || bar.State != tea.ProgressBarIndeterminate {
		t.Fatalf("a running turn does not light the tab: %v", bar)
	}
	m.setTurnState(stateInput)
	if bar := m.progressBar(); bar != nil {
		t.Fatalf("the tab stays lit after the turn: %v", bar.State)
	}
}

// The light changes state exactly twice over a turn: on when it starts, off
// when it ends. The renderer writes a sequence per change, so a frame that
// re-derived a different state mid-turn would be a sequence per frame.
func TestProgress_ChangesStateOnceAtEachEdge(t *testing.T) {
	m := windowModel(t)
	var states []string
	frame := func() {
		bar := m.progressBar()
		state := "none"
		if bar != nil {
			state = bar.State.String()
		}
		if len(states) == 0 || states[len(states)-1] != state {
			states = append(states, state)
		}
	}
	frame()
	m.setTurnState(stateStreaming)
	frame()
	frame()
	m.setTurnState(stateRunningCmd)
	frame()
	m.setTurnState(stateStreaming)
	frame()
	m.setTurnState(stateInput)
	frame()
	frame()
	// No bar at all, then one indeterminate bar for the whole turn whatever
	// the turn is doing, then no bar again.
	want := []string{"none", tea.ProgressBarIndeterminate.String(), "none"}
	if strings.Join(states, ",") != strings.Join(want, ",") {
		t.Errorf("the tab's light went %v, want %v", states, want)
	}
}

// A broken turn turns the tab red, and the red does not outlive the tick that
// was scheduled for it.
func TestProgress_GoesRedBrieflyOnABrokenTurn(t *testing.T) {
	prev := windowModel(t)
	prev.turnOpen = true
	prev.setTurnState(stateStreaming)

	m := prev
	m.turnOpen = false
	m.setTurnState(stateInput)
	m.turnOutcome = components.TurnFailed
	tick := m.progressCmd(prev)
	if tick == nil {
		t.Fatal("a broken turn scheduled nothing to clear the tab")
	}
	bar := m.progressBar()
	if bar == nil || bar.State != tea.ProgressBarError {
		t.Fatalf("a broken turn did not turn the tab red: %v", bar)
	}

	// The tick is run rather than its message rebuilt: the sequence it
	// captured is the whole guard, and a tick carrying a sequence nothing
	// matches would leave the tab red for the rest of the session while
	// every hand-built message in a test went on clearing it.
	cleared, ok := tick().(progressClearedMsg)
	if !ok {
		t.Fatalf("the tick sends a %T", tick())
	}
	updated, _ := m.Update(cleared)
	if bar := updated.(Model).progressBar(); bar != nil {
		t.Fatalf("the tab is still lit after its window shut: %v", bar.State)
	}
}

// The next turn takes the tab back rather than waiting for the previous
// failure's tick, so a turn that succeeds cannot end up under a red tab.
func TestProgress_ANewTurnClearsTheRed(t *testing.T) {
	m := windowModel(t)
	m.progressFailed = true
	prev := m

	m.setTurnState(stateStreaming)
	m.progressCmd(prev)
	if m.progressFailed {
		t.Fatal("a new turn left the previous failure's red standing")
	}

	// And the tick the failure scheduled cannot clear the state that
	// replaced it.
	stale := progressClearedMsg{seq: m.progressSeq - 1}
	m.progressFailed = true
	updated, _ := m.Update(stale)
	if !updated.(Model).progressFailed {
		t.Fatal("a stale tick cleared a state it was not scheduled for")
	}
}

// Both out-of-window states ride their own switch, and each says why it is
// silent when it is.
func TestProgress_RidesTheNotificationSwitch(t *testing.T) {
	m := windowModel(t)
	m.setTurnState(stateStreaming)
	m.notifyOn = false
	if bar := m.progressBar(); bar != nil {
		t.Fatalf("notifications off still lights the tab: %v", bar.State)
	}
	m.notifyOn = true
	m.caps = caps.Terminal{Asked: true, Dumb: true}
	if bar := m.progressBar(); bar != nil {
		t.Fatalf("a dumb terminal was sent a progress state: %v", bar.State)
	}
	// And the pipe: nothing was asked because there was nothing to ask.
	m.caps = caps.Terminal{}
	if bar := m.progressBar(); bar != nil {
		t.Fatalf("a session with no terminal was sent a progress state: %v", bar.State)
	}
}

// The whole path rather than the pieces: a real broken turn, through Update,
// lights the tab red — which is what says the derivation is wired into the
// tail and not merely written.
func TestProgress_ABrokenTurnThroughUpdateReddensTheTab(t *testing.T) {
	m := sendText(t, windowModel(t), "do it")
	if bar := m.progressBar(); bar == nil || bar.State != tea.ProgressBarIndeterminate {
		t.Fatalf("the sent turn did not light the tab: %v", bar)
	}

	updated, _ := m.Update(streamErrMsg{err: errors.New("upstream refused the request")})
	next, ok := updated.(Model)
	if !ok {
		t.Fatalf("the update returned a %T", updated)
	}
	if next.turnOutcome != components.TurnFailed {
		t.Fatalf("the turn did not end failed, got %v", next.turnOutcome)
	}
	bar := next.progressBar()
	if bar == nil || bar.State != tea.ProgressBarError {
		t.Fatalf("a turn that broke through Update did not redden the tab: %v", bar)
	}
}

// And the bug the guard exists for: a command is not a turn, so a command
// that succeeds after a turn that failed leaves the tab dark. turnOutcome
// belongs to the last turn and outlives it, which is why the edge reads
// whether a turn was open rather than whether the session was busy.
func TestProgress_ACommandAfterAFailedTurnStaysDark(t *testing.T) {
	m := windowModel(t)
	m.turnOutcome = components.TurnFailed

	// The command runs and finishes: busy, then not, with no turn open.
	prev := m
	prev.setTurnState(stateRunningCmd)
	after := prev
	after.setTurnState(stateInput)
	if tick := after.progressCmd(prev); tick != nil {
		t.Error("a command that is not a turn scheduled the tab's red")
	}
	if after.progressFailed {
		t.Error("a command that succeeded inherited the last turn's failure")
	}
	if bar := after.progressBar(); bar != nil {
		t.Fatalf("the tab went red for a command that worked: %v", bar.State)
	}
}

var ctrlZ = tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl}
var ctrlL = tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl}

// The idle session hands the terminal back.
func TestSuspend_IdleSuspends(t *testing.T) {
	m := windowModel(t)
	_, cmd := pressKey(t, m, ctrlZ)
	if cmd == nil {
		t.Fatal("ctrl+z on an idle session did nothing")
	}
	if _, ok := cmd().(tea.SuspendMsg); !ok {
		t.Fatalf("ctrl+z produced a %T", cmd())
	}
}

// And a working one refuses, with the reason on the transcript rather than in
// silence.
//
// The decision case is the card that arrived on top of a sentence, because
// that is the state in which the chord reaches this at all: a card holding
// the keyboard answers every key itself, and ctrl+z is as inert there as
// every other one.
func TestSuspend_RefusedWhileWorking(t *testing.T) {
	for _, tc := range []struct {
		name string
		open func(t *testing.T) Model
		want string
	}{
		{"streaming", func(t *testing.T) Model {
			m := windowModel(t)
			m.setTurnState(stateStreaming)
			return m
		}, "not while the turn is running"},
		{"running a command", func(t *testing.T) Model {
			m := windowModel(t)
			m.setTurnState(stateRunningCmd)
			return m
		}, "not while the turn is running"},
		{"a decision waiting on a live draft", func(t *testing.T) Model {
			m := interruptedModel(t, "also add a --max-rounds ")
			m.caps = caps.Terminal{Asked: true}
			return m
		}, "a decision is waiting"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.open(t)
			next, cmd := pressKey(t, m, ctrlZ)
			if cmd != nil {
				if _, ok := cmd().(tea.SuspendMsg); ok {
					t.Fatal("the session suspended with work in flight")
				}
			}
			if !strings.Contains(transcriptText(next), tc.want) {
				t.Errorf("the refusal never said %q:\n%s", tc.want, transcriptText(next))
			}
		})
	}
}

// A card that holds the keyboard swallows the chord instead: the fifth
// invariant is not suspended for a chord
// (docs/interface/principles.md#a-key-is-inert-until-its-surface-holds-the-keyboard).
func TestSuspend_InertUnderACardThatHoldsTheKeyboard(t *testing.T) {
	m := handover(t, interruptedModel(t, "also add a --max-rounds "))
	next, cmd := pressKey(t, m, ctrlZ)
	if cmd != nil {
		if _, ok := cmd().(tea.SuspendMsg); ok {
			t.Fatal("a card holding the keyboard let the chord through")
		}
	}
	if next.state != stateConfirmRun {
		t.Fatal("the chord answered the card")
	}
}

// Attached, the turn that must not be stopped is the child's — the
// orchestrator is idle and its own state says nothing about it.
func TestSuspend_RefusedWhileTheAttachedChildWorks(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	spawnBlockedChild(t, sup)
	m.attachedTo = "researcher-1"
	if m.working() {
		t.Fatal("the orchestrator's own turn is not what is running here")
	}

	next, cmd := pressKey(t, m, ctrlZ)
	if cmd != nil {
		if _, ok := cmd().(tea.SuspendMsg); ok {
			t.Fatal("the session suspended while the attached child worked")
		}
	}
	// Attached, a notice belongs to the session the reader is looking at,
	// which is the child's (attach.go).
	var said string
	for _, e := range sup.Transcript("researcher-1") {
		said += e.Text + "\n"
	}
	if !strings.Contains(said, "not while the turn is running") {
		t.Errorf("the refusal never said why:\n%s", said)
	}
	if strings.Contains(transcriptText(next), "not while the turn") {
		t.Error("the refusal landed on the orchestrator's transcript instead")
	}
}

// A child's routed approval is a decision like any other, so the tab marks it
// — the same set the summons reads.
func TestWindowTitle_MarksAChildsRoutedDecision(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: blockingEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	m.caps = caps.Terminal{Asked: true}
	m.windowDir = "Projects/shhh"
	m.childAsks = []*subagent.Ask{subagent.NewAsk("writer-1", subagent.AskCommand, "run make")}
	if got := m.windowTitle(); !strings.HasPrefix(got, "⏸ ") {
		t.Errorf("a child's decision is not marked on the tab: %q", got)
	}
}

// Coming back re-asserts the one terminal setting that is not on the View:
// the alternate-scroll suppression belongs to whoever started the program,
// and the shell that ran in between may have had opinions.
func TestResume_ReassertsAlternateScroll(t *testing.T) {
	m := windowModel(t)
	updated, cmd := m.Update(tea.ResumeMsg{})
	if cmd == nil {
		t.Fatal("resuming sent the terminal nothing")
	}
	raw, ok := cmd().(tea.RawMsg)
	if !ok {
		t.Fatalf("resuming produced a %T", cmd())
	}
	if s, _ := raw.Msg.(string); s != disableAlternateScroll {
		t.Errorf("resuming wrote %q", s)
	}
	// The save is deliberately not repeated: it would save the suppression
	// over what the reader had.
	if s, _ := raw.Msg.(string); strings.Contains(s, saveAlternateScroll) {
		t.Error("resuming saved the mode again, clobbering what the reader had")
	}
	if updated.(Model).input.Value() != m.input.Value() {
		t.Error("resuming touched the draft")
	}
}

// The redraw repaints and does nothing else.
func TestRedraw_RepaintsAndKeepsEverything(t *testing.T) {
	m := typeChars(t, windowModel(t), "half a sentence")
	before := m.input.Value()
	next, cmd := pressKey(t, m, ctrlL)
	if cmd == nil {
		t.Fatal("ctrl+l did not ask for a repaint")
	}
	// Bubble Tea's clear message is unexported, so the assertion is that the
	// command produces the same one tea.ClearScreen does.
	if got, want := cmd(), tea.ClearScreen(); got != want {
		t.Fatalf("ctrl+l produced %T, want the clear-screen message", got)
	}
	if got := next.input.Value(); got != before {
		t.Errorf("the redraw changed the draft: %q", got)
	}
	if got, want := transcriptText(next), transcriptText(m); got != want {
		t.Errorf("the redraw changed the transcript:\n%s", got)
	}
}

// /ui window is the switch, and it says what it did and why it might still be
// invisible.
func TestUIWindow_SwitchesAndExplainsItself(t *testing.T) {
	m := windowModel(t)
	if out := m.uiCommand([]string{"/ui", "window"}); !strings.Contains(out, "shhh code · Projects/shhh") {
		t.Errorf("the readout does not show the title: %q", out)
	}
	if out := m.uiCommand([]string{"/ui", "window", "off"}); !strings.Contains(out, "off") {
		t.Errorf("turning it off said %q", out)
	}
	if m.windowTitle() != "" {
		t.Error("the tab kept its name after the switch went off")
	}
	if out := m.uiCommand([]string{"/ui", "window", "sideways"}); !strings.Contains(out, "Error") {
		t.Errorf("an unknown setting was accepted: %q", out)
	}
	if out := m.uiCommand([]string{"/ui"}); !strings.Contains(out, "Window title: off") {
		t.Errorf("the bare readout does not carry the window title: %q", out)
	}
	dumb := windowModel(t)
	dumb.caps = caps.Terminal{Asked: true, Dumb: true}
	if out := dumb.uiCommand([]string{"/ui", "window"}); !strings.Contains(out, "dumb one") {
		t.Errorf("a dumb terminal is not named as the reason: %q", out)
	}
}
