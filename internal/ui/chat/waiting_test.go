package chat

// The session waiting on a person, said in four places at once
// (docs/interface/surfaces.md#when-you-are-not-there).
//
// What these hold is the shape of the wait rather than any one drawing of
// it: it opens when a decision lands and closes when nothing is left to
// answer, so a queue of three is one wait; the bell rings on that opening
// and on nothing else — not a second card, not a finished turn, not a
// window that happens to be in front; and the row, the frame and the tab
// read the same count and the same clock, because a title that says two
// over a rail that says three is a bug by definition.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/ask"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/caps"
)

// twelveMinutes is a wait long enough to show in every place the clock is
// drawn, half a second past the boundary so a slow test does not read it as
// eleven.
const twelveMinutes = 12*time.Minute + 500*time.Millisecond

// decline answers the card on screen with its plain no. The card landed on
// an empty draft, so it already holds the keyboard and the letter is live.
func decline(t *testing.T, m Model) Model {
	t.Helper()
	next, _ := m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	rm, ok := next.(Model)
	if !ok {
		t.Fatal("the answer should return the chat model")
	}
	if rm.decisionWaiting() && rm.turnState() == stateConfirmRun && rm.pendingApproval != nil && rm.pendingApproval.call.ID == m.pendingApproval.call.ID {
		t.Fatal("the card is still up after its no")
	}
	return rm
}

// twoCardsModel lands two gated writes in one round on an empty draft, so
// the second card is the queue advancing rather than a new wait.
func twoCardsModel(t *testing.T) Model {
	t.Helper()
	m := gatedModel(t, func(string, json.RawMessage) (string, error) { return "", nil }, map[string]GatedPreviewFunc{
		"write_file": writeFilePreview("line one\n"),
	})
	m.width, m.height = 130, 40
	m.syncInputWidth()
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_a", Name: "write_file", Arguments: `{"path":"a.go","content":"line one\nline two\n"}`},
		{ID: "call_b", Name: "write_file", Arguments: `{"path":"b.go","content":"line one\nline three\n"}`},
	}})
	m = updated.(Model)
	if m.state != stateConfirmRun {
		t.Fatalf("the gated calls should have raised a decision, got %d", m.state)
	}
	return m
}

func TestWait_OpensWhenADecisionLandsAndClosesWithItsAnswer(t *testing.T) {
	before := interruptedModel(t, "")
	before.state = stateStreaming
	before.pendingApproval = nil
	if !before.waitingSince.IsZero() {
		// The fixture lands its card through Update, so the stamp is on it
		// already; what is being asked is what a fresh landing does.
		before.waitingSince = time.Time{}
	}
	m := interruptedModel(t, "")
	if m.waitingSince.IsZero() {
		t.Fatal("a decision landing did not open the wait")
	}
	if !m.decisionWaiting() {
		t.Fatal("a card on screen is not read as a decision waiting")
	}
	answered := decline(t, m)
	if !answered.waitingSince.IsZero() {
		t.Fatalf("the wait stayed open after the only decision was answered: %v", answered.waitingSince)
	}
}

func TestWait_RunsAcrossTheQueueRatherThanPerCard(t *testing.T) {
	m := twoCardsModel(t)
	opened := m.waitingSince
	if opened.IsZero() {
		t.Fatal("two decisions landing did not open the wait")
	}
	if n := m.waitingCount(); n != 2 {
		t.Fatalf("the count is the queue, want 2 got %d", n)
	}
	second := decline(t, m)
	if second.turnState() != stateConfirmRun {
		t.Fatalf("the second card did not follow the first's answer, state %d", second.turnState())
	}
	if !second.waitingSince.Equal(opened) {
		t.Fatalf("the queue advancing restarted the clock: %v then %v", opened, second.waitingSince)
	}
	if n := second.waitingCount(); n != 1 {
		t.Fatalf("one answered leaves one waiting, got %d", n)
	}
}

// bellModel is a card landed on a terminal shhh may write to, with the
// model it landed on beside it.
func bellModel(t *testing.T) (before, after Model) {
	t.Helper()
	after = interruptedModel(t, "")
	after.caps = caps.Terminal{Asked: true}
	before = after
	before.waitingSince = time.Time{}
	return before, after
}

func TestBell_RingsOnceOnTheTransitionIntoWaiting(t *testing.T) {
	before, after := bellModel(t)
	if got := notifyRaw(t, after.bellCmd(before)); got != "\a" {
		t.Fatalf("the bell wrote %q", got)
	}
	if cmd := after.bellCmd(after); cmd != nil {
		t.Fatal("a session already waiting rang again")
	}
}

func TestBell_StaysQuietForTheSecondCardInAQueue(t *testing.T) {
	first := twoCardsModel(t)
	first.caps = caps.Terminal{Asked: true}
	second := decline(t, first)
	if cmd := second.bellCmd(first); cmd != nil {
		t.Fatal("the queue advancing rang the bell a second time")
	}
}

func TestBell_RingsWhetherOrNotTheWindowIsInFront(t *testing.T) {
	before, after := bellModel(t)
	after.away = false
	if after.bellCmd(before) == nil {
		t.Fatal("a window in front silenced the bell, which is the summons for exactly that window")
	}
}

func TestBell_IsSilentWhenTheSettingIsOff(t *testing.T) {
	before, after := bellModel(t)
	after.notifyOn = false
	if after.bellCmd(before) != nil {
		t.Fatal("the bell rang with the summons switched off")
	}
}

func TestBell_NeedsATerminalToRingAt(t *testing.T) {
	before, after := bellModel(t)
	after.caps = caps.Terminal{}
	if after.bellCmd(before) != nil {
		t.Fatal("a bell went down a pipe")
	}
}

func TestBell_AFinishedTurnRingsNothing(t *testing.T) {
	prev := notifyModel(t)
	next := ended(prev)
	if next.notifyCmd(prev) == nil {
		t.Fatal("the fixture's finished turn should still notify")
	}
	if next.bellCmd(prev) != nil {
		t.Fatal("a turn finishing rang the bell: you asked shhh to work, not to talk")
	}
}

func TestWaitLabel_IsCoarserThanACallsClock(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{0, ""},
		{700 * time.Millisecond, ""},
		{19 * time.Second, "19s"},
		{12*time.Minute + 30*time.Second, "12m"},
		{time.Hour + 4*time.Minute, "1h04m"},
	} {
		if got := waitLabel(tc.d); got != tc.want {
			t.Errorf("waitLabel(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestWaitingChip_SaysTheCountAndTheIdleClock(t *testing.T) {
	// A sentence in the box, so the handover below has a draft to hold.
	m := interruptedModel(t, "also add a --max-rounds flag")
	m.waitingSince = time.Time{}
	if got := m.waitingChip(); got != "⏸ 1 waiting" {
		t.Errorf("with no clock the chip says %q", got)
	}
	m.waitingSince = time.Now().Add(-twelveMinutes)
	if got := m.waitingChip(); got != "⏸ 1 waiting · idle 12m" {
		t.Errorf("the chip says %q", got)
	}
	// The frame's live rail and the held draft's dim one draw the same chip.
	m.width, m.height = 110, 40
	m.syncInputWidth()
	if rail := ansi.Strip(frameTopRail(m.renderPromptFrame())); !strings.Contains(rail, "⏸ 1 waiting · idle 12m") {
		t.Errorf("the frame's top rail does not carry the chip:\n%s", rail)
	}
	held := handover(t, m)
	held.waitingSince = m.waitingSince
	if draft := ansi.Strip(strings.Join(held.undressedDraft(held.contentWidth()), "\n")); !strings.Contains(draft, "⏸ 1 waiting · idle 12m") {
		t.Errorf("the held draft's rail does not carry the chip:\n%s", draft)
	}
}

func TestWindowTitle_CountsTheWaitAndHowLong(t *testing.T) {
	m := windowModel(t)
	m.setTurnState(stateConfirmRun)
	if got := m.windowTitle(); got != "⏸ shhh code · Projects/shhh · 1 waiting" {
		t.Errorf("the tab says %q", got)
	}
	m.waitingSince = time.Now().Add(-twelveMinutes)
	if got := m.windowTitle(); got != "⏸ shhh code · Projects/shhh · 1 waiting · 12m" {
		t.Errorf("the tab says %q", got)
	}
}

func TestFrame_TheVitalsSayTheyAreFrozenWhileADecisionWaits(t *testing.T) {
	m := interruptedModel(t, "")
	if bar := ansi.Strip(m.renderStatusBar(120)); !strings.Contains(bar, "frozen") {
		t.Fatalf("the vitals do not say they have held still:\n%s", bar)
	}
	answered := decline(t, m)
	if bar := ansi.Strip(answered.renderStatusBar(120)); strings.Contains(bar, "frozen") {
		t.Fatalf("the vitals still say frozen after the answer:\n%s", bar)
	}
}

func TestWaitingRow_TheHeldCallIsTheLastRowAboveTheCard(t *testing.T) {
	m := interruptedModel(t, "")
	m.waitingSince = time.Now().Add(-twelveMinutes)
	tail := ansi.Strip(m.liveTail(m.paneWidth()))
	// The state's glyph stands in the kind's column, as it does on a call
	// the classifier is judging: a call stopped on a judgement is ✦, and the
	// outcome word says whose.
	for _, want := range []string{"▎✦", "write", "main.go", "waiting for you", "12m"} {
		if !strings.Contains(tail, want) {
			t.Fatalf("the held call's row lacks %q:\n%s", want, tail)
		}
	}
	if strings.Contains(tail, "running") {
		t.Fatalf("a call nobody has answered claims to be running:\n%s", tail)
	}
	answered := decline(t, m)
	if tail := ansi.Strip(answered.liveTail(answered.paneWidth())); strings.Contains(tail, "waiting for you") {
		t.Fatalf("the waiting row outlived the decision:\n%s", tail)
	}
}

func TestWaitingRow_AQuestionIsARowToo(t *testing.T) {
	m := frameModel(t, 110, 40).WithAsk()
	m.state = stateStreaming
	updated, _ := m.Update(toolCallsMsg{calls: []provider.ToolCall{{
		ID: "call_q", Name: ask.ToolName, Arguments: `{"question":"Which store should the cache use?","shape":"choose","options":[{"label":"SQLite","recommended":true},{"label":"Postgres"}]}`,
	}}})
	q := updated.(Model)
	tail := ansi.Strip(q.liveTail(q.paneWidth()))
	if !strings.Contains(tail, "waiting for you") {
		t.Fatalf("the question has no row while it waits:\n%s", tail)
	}
	// Handed to the draft, the question is still what the session is
	// stopped on, so the row stays.
	esc, _ := q.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	aside := esc.(Model)
	if tail := ansi.Strip(aside.liveTail(aside.paneWidth())); !strings.Contains(tail, "waiting for you") {
		t.Fatalf("the row left with the card, though the question did not:\n%s", tail)
	}
}

func TestSpin_TheChainRunsWhileADecisionWaits(t *testing.T) {
	if readyModel(t).spinnerWanted() {
		t.Fatal("an idle session has nothing to tick")
	}
	m := interruptedModel(t, "")
	if !m.spinnerWanted() {
		t.Fatal("nothing ticks the wait's clock")
	}
}
