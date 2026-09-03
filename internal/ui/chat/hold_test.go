package chat

// Holding a turn between rounds: the mark is taken mid-turn, the park happens
// at the boundary and nowhere else, and letting the turn go asks for the
// round it was about to ask for rather than starting a new turn.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// heldModel is a turn that wrote one file and was asked to hold on the way
// through, so it parked at the boundary the write's results landed on.
func heldModel(t *testing.T) Model {
	t.Helper()
	m := turnModel(t)
	m = sendText(t, m, "rewrite the loop")
	m, _ = pressKey(t, m, ctrlP)
	if !m.holdAsked {
		t.Fatal("the key did not mark the turn to hold")
	}
	m = applyWrite(t, m, filepath.Join(t.TempDir(), "loop.go"), "package agent\n", "y")
	if !m.heldAtBoundary() {
		t.Fatalf("the turn should have parked at the round boundary, state %v", m.turnState())
	}
	return m
}

func TestHold_ParksAtTheBoundaryWithTheConversationIntact(t *testing.T) {
	before := 0
	{
		m := turnModel(t)
		m = sendText(t, m, "rewrite the loop")
		before = len(m.agent.Messages())
	}
	m := heldModel(t)

	if m.turnState() != stateInput {
		t.Fatalf("a held turn hands the keyboard back, state %v", m.turnState())
	}
	// The round's own messages are in there — the assistant's call and its
	// result — and nothing else was added or taken away.
	if got := len(m.agent.Messages()); got <= before {
		t.Fatalf("the round's results should be in the conversation: %d messages, was %d", got, before)
	}
	// The turn has not ended, so nothing closed it.
	for _, e := range m.transcript {
		if e.kind == entryTurnClose {
			t.Fatal("a held turn must not close: its next round is what continues it")
		}
	}
	if !m.turnOpen {
		t.Fatal("a held turn's accounting stays open")
	}
}

func TestHold_TheChipSaysWhichOfTheTwoStatesTheTurnIsIn(t *testing.T) {
	m := turnModel(t)
	m = sendText(t, m, "rewrite the loop")
	m, _ = pressKey(t, m, ctrlP)
	if got := m.holdChip(); !strings.Contains(got, "holding after this round") {
		t.Errorf("a turn asked to hold should say so on the rail, got %q", got)
	}

	m = applyWrite(t, m, filepath.Join(t.TempDir(), "loop.go"), "package agent\n", "y")
	chip := m.holdChip()
	if !strings.Contains(chip, "held") || !strings.Contains(chip, keys.Shown(keys.Draft.Pause)) {
		t.Errorf("a parked turn's chip should say it is held and how to resume, got %q", chip)
	}
	if !strings.Contains(stripANSI(m.frameActivity(60)), "held") {
		t.Errorf("the activity slot should carry the chip:\n%s", stripANSI(m.frameActivity(60)))
	}
}

func TestHold_TheRoundCounterStaysOnTheRail(t *testing.T) {
	m := heldModel(t)
	if got := stripANSI(m.renderStatusBar(130)); !strings.Contains(got, "round ") {
		t.Errorf("a held turn keeps its round counter: %q", got)
	}
}

func TestHold_PressingAgainTakesTheRequestBack(t *testing.T) {
	m := turnModel(t)
	m = sendText(t, m, "rewrite the loop")
	m, _ = pressKey(t, m, ctrlP)
	m, _ = pressKey(t, m, ctrlP)
	if m.holdAsked {
		t.Fatal("a second press should take an unhonoured request back")
	}
	m = applyWrite(t, m, filepath.Join(t.TempDir(), "loop.go"), "package agent\n", "y")
	if m.heldAtBoundary() {
		t.Fatal("a request taken back must not park the turn")
	}
}

func TestHold_ResumeAsksForTheNextRoundWithSteeringInjected(t *testing.T) {
	m := heldModel(t)
	turn := m.turnCount

	m = sendText(t, m, "keep the old signature")
	if len(m.steering) != 1 {
		t.Fatalf("text typed while held is steering, not a new turn: %+v", m.steering)
	}

	before := len(m.agent.Messages())
	m, cmd := pressKey(t, m, ctrlP)
	if m.heldAtBoundary() {
		t.Fatal("the key did not let the turn go")
	}
	if m.turnState() != stateStreaming {
		t.Fatalf("the released turn should be asking again, state %v", m.turnState())
	}
	if cmd == nil {
		t.Fatal("the release should start the next round and autosave")
	}
	if len(m.steering) != 0 || len(m.agent.Messages()) != before+1 {
		t.Fatalf("the steering should have gone out with the resumed round: %d queued, %d messages",
			len(m.steering), len(m.agent.Messages()))
	}
	// Steering counts as fresh input everywhere, so the turn number moves
	// with it — but the conversation is the same one, not a restarted turn.
	if m.turnCount <= turn {
		t.Fatalf("the injected steering should have advanced the turn: %d", m.turnCount)
	}
}

func TestHold_AFollowUpStaysQueuedWhileTheTurnIsHeld(t *testing.T) {
	m := heldModel(t)
	m.input.SetValue("and then update the docs")
	queued, _, ok := m.queueFollowUp()
	if !ok {
		t.Fatal("the follow-up chord should have queued the draft")
	}
	m = queued.(Model)
	if len(m.followUps) != 1 {
		t.Fatalf("the follow-up should have queued: %+v", m.followUps)
	}
	m, _ = pressKey(t, m, ctrlP)
	if len(m.followUps) != 1 {
		t.Fatalf("a follow-up waits for the turn to end, not for it to be let go: %+v", m.followUps)
	}
}

func TestHold_TheCancelChordEndsAHeldTurnRatherThanQuitting(t *testing.T) {
	m := heldModel(t)

	m, _ = pressKey(t, m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !strings.Contains(m.armedNotice(), "cancels the turn") {
		t.Fatalf("the first press should arm the cancel, not the quit: %q", m.armedNotice())
	}
	m, _ = pressKey(t, m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if m.quitting {
		t.Fatal("the chord quit the session instead of giving the turn up")
	}
	if m.heldAtBoundary() {
		t.Fatal("the hold should be gone with the turn")
	}
}

func TestHold_FreshInputMovesPastAHeldTurn(t *testing.T) {
	m := heldModel(t)
	m.resetRounds()
	if m.heldAtBoundary() || m.holdAsked {
		t.Fatal("a turn the session has moved past cannot still be held")
	}
}

func TestHold_TheMarkIsWrittenOnlyWhileTheTurnIsParked(t *testing.T) {
	m := turnModel(t)
	m = sendText(t, m, "rewrite the loop")
	if m.holdMarker() != nil {
		t.Fatal("a working turn has no mark to write")
	}
	m, _ = pressKey(t, m, ctrlP)
	if m.holdMarker() != nil {
		t.Fatal("a turn that has not reached its boundary is not held yet")
	}
	m = applyWrite(t, m, filepath.Join(t.TempDir(), "loop.go"), "package agent\n", "y")
	mark := m.holdMarker()
	if mark == nil {
		t.Fatal("a parked turn writes a mark beside the conversation")
	}
	if mark.Rounds != m.agent.Rounds() {
		t.Errorf("the mark should carry the round the turn reached: %+v", mark)
	}
}

func TestHold_AConversationSavedHeldReopensHeld(t *testing.T) {
	m := readyModel(t).WithHeldTurn(12, 100)

	if !m.heldAtBoundary() {
		t.Fatal("a slot with a mark on it opens held rather than idle")
	}
	if m.roundGrant != 100 {
		t.Errorf("the grant comes back with the turn, got %d", m.roundGrant)
	}
	if chip := m.holdChip(); !strings.Contains(chip, "held") {
		t.Errorf("the reopened session wears the same chip, got %q", chip)
	}
	// Where the turn had got to is the one fact the mark carries that the
	// screen has nowhere else to put: this process's round counter is at nil.
	if got := stripANSI(m.renderHistory()); !strings.Contains(got, "12 rounds") {
		t.Errorf("the reopened session should say where the turn was parked:\n%s", got)
	}

	next, cmd := m.releaseHold()
	released := next.(Model)
	if released.turnState() != stateStreaming || cmd == nil {
		t.Fatalf("letting a reopened hold go should ask for the round it is owed, state %v", released.turnState())
	}
	if !released.turnOpen {
		t.Fatal("the continued turn's accounting should be open in this sitting")
	}
}

// A held turn is idle in every way the frame can see, so its bottom rail has
// to say the three things only it knows.
func TestHold_TheRailSaysHowToLetTheTurnGo(t *testing.T) {
	m := heldModel(t)
	m.width, m.height = 130, 40
	hints := stripANSI(m.frameHints())
	for _, want := range []string{
		keys.Shown(keys.Draft.Pause) + " resumes the turn",
		keys.Shown(keys.Draft.Send) + " queues steering",
		keys.Shown(keys.Draft.Cancel) + " cancels it",
	} {
		if !strings.Contains(hints, want) {
			t.Errorf("the held rail should offer %q, got %q", want, hints)
		}
	}
}

// A hold the turn outran goes with the turn. A model that answers without
// asking for another round never reaches the boundary the mark would have
// been acted on at, and a request left standing would hold the next turn —
// and, worse, park a fan-out on a release nothing would ever send.
func TestHold_ARequestTheTurnOutranGoesWithIt(t *testing.T) {
	m := turnModel(t)
	m = sendText(t, m, "rewrite the loop")
	m, _ = pressKey(t, m, ctrlP)

	m = finishTurn(t, m)
	if m.holdAsked || m.heldAtBoundary() {
		t.Fatal("a turn that answered without another round leaves no hold behind")
	}
}

// A hold that lands on the ceiling is answered by the checkpoint rather than
// by a second stop: the turn has already stopped and is waiting on the
// reader, which is what the hold was for, and the row's offers are the way
// back that says what it managed.
func TestHold_TheCeilingAnswersAHoldAskedOnTheWayToIt(t *testing.T) {
	m := turnModel(t).WithMaxToolRounds(1)
	m = sendText(t, m, "fix the round accounting")
	m, _ = pressKey(t, m, ctrlP)
	m = applyWrite(t, m, filepath.Join(t.TempDir(), "loop.go"), "package agent\n", "y")

	if m.roundPause == nil {
		t.Fatalf("the ceiling should have stopped the turn, held = %v", m.heldAtBoundary())
	}
	if m.heldAtBoundary() || m.holdAsked {
		t.Fatal("the checkpoint answers the hold rather than standing beside it")
	}
}

// A fan-out held to the last child has nothing moving to animate, so the
// spinner stops: a child parked at its round boundary is still `running` as a
// lifecycle, and a screen that reads only that would claim progress the
// reader has deliberately stopped.
func TestHold_TheSpinnerStopsForAParkedFanOut(t *testing.T) {
	sup := subagent.New(context.Background(), subagent.Options{Root: t.TempDir(), NewEnv: heldChildEnv()})
	t.Cleanup(sup.Close)
	m := newSubagentModel(t, sup)
	sup.Hold()
	spawnBlockedChild(t, sup)
	waitFor(t, func() bool {
		st, ok := sup.Get("researcher-1")
		return ok && st.Held
	})
	if m.childrenRunning() {
		t.Fatal("a parked fan-out should not keep the spinner going")
	}

	sup.Release()
	waitFor(t, func() bool {
		st, ok := sup.Get("researcher-1")
		return ok && st.State == subagent.StateRunning && !st.Held
	})
	if !m.childrenRunning() {
		t.Fatal("a released child should have the spinner going again")
	}
}
