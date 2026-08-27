package chat

// The round-limit pause (S-109, §17a): a turn that runs out of tool rounds
// stops on a checkpoint rather than on a notice, and the checkpoint is the
// turn's close — one block, one set of offers, one turn to grant more rounds
// to.

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// firstGrantOffer is the bracket a first stop draws. It is derived from the
// block rather than written out, so a change to the block size lands in one
// place — and the tests below that assert a *second* stop derive theirs from
// the pause, because the block doubles.
var firstGrantOffer = fmt.Sprintf("[+%d]", roundGrantBlock)

// pausedModel is a turn that wrote one file and then used up its only round.
func pausedModel(t *testing.T) (Model, string) {
	t.Helper()
	m := turnModel(t).WithMaxToolRounds(1)
	m = sendText(t, m, "fix the round accounting")
	path := filepath.Join(t.TempDir(), "loop.go")
	m = applyWrite(t, m, path, "package agent\n", "y")
	if m.roundPause == nil {
		t.Fatalf("the turn should have stopped at its ceiling, state %v", m.turnState())
	}
	return m, path
}

// pauseEntry is the pause row in the transcript.
func pauseEntry(t *testing.T, m Model) entry {
	t.Helper()
	return m.transcript[indexOfKind(t, m, entryRoundPause)]
}

func pauseView(m Model, e entry) string {
	return ansi.Strip(m.roundPauseRow(e).View(110))
}

func TestRoundLimit_PausesWithRoundsUsedAndWhatChanged(t *testing.T) {
	m, _ := pausedModel(t)

	if m.turnState() != stateInput {
		t.Fatalf("the pause hands the keyboard back, state %v", m.turnState())
	}
	e := pauseEntry(t, m)
	view := pauseView(m, e)
	for _, want := range []string{"rounds", "1 of 1 used", "stopped", "1 file changed +1 −0"} {
		if !strings.Contains(view, want) {
			t.Errorf("the row should state %q:\n%s", want, view)
		}
	}
	// The suite has not run since the write, and the row says so — that is
	// the difference between stopping and stopping halfway.
	if !strings.Contains(view, "the suite has not been re-run since") {
		t.Errorf("an unchecked edit should be named:\n%s", view)
	}
	for _, want := range []string{"[v] review what it did", firstGrantOffer + " more rounds", "[u] undo the turn"} {
		if !strings.Contains(view, want) {
			t.Errorf("the row should offer %q:\n%s", want, view)
		}
	}
}

func TestRoundLimit_ThePauseIsTheTurnsClose(t *testing.T) {
	m, _ := pausedModel(t)

	// A close block beside the pause row would offer [v] and [u] twice.
	for _, e := range m.transcript {
		if e.kind == entryTurnClose {
			t.Fatalf("the pause row stands in for the close block, got %+v", e.close)
		}
	}
	if m.turnOpen {
		t.Error("the turn is closed; only the block that says so is different")
	}
	// And the notice it replaced is gone.
	for _, e := range m.transcript {
		if e.kind == entrySystem && strings.Contains(e.text, "Paused after") {
			t.Errorf("the grey notice should be gone, got %q", e.text)
		}
	}
}

func TestRoundLimit_GrantContinuesTheSameTurn(t *testing.T) {
	m, _ := pausedModel(t)
	before := len(m.agent.Messages())
	turn, rounds := m.turnCount, m.agent.Rounds()

	m.focusIdx = indexOfKind(t, m, entryRoundPause)
	updated, cmd, claimed := m.roundPauseKey(grantRoundsKey)
	if !claimed {
		t.Fatal("the grant should be claimed by the focused pause row")
	}
	next := updated.(Model)
	if cmd == nil {
		t.Fatal("granting rounds should ask the model")
	}
	if next.turnState() != stateStreaming {
		t.Errorf("state = %v, want streaming", next.turnState())
	}
	if got := len(next.agent.Messages()); got != before {
		t.Errorf("the conversation continues as it stands, got %d new messages", got-before)
	}
	if next.turnCount != turn {
		t.Errorf("turn = %d, want the same turn %d", next.turnCount, turn)
	}
	if next.agent.Rounds() != rounds {
		t.Errorf("rounds = %d, want the counter kept at %d", next.agent.Rounds(), rounds)
	}
	if got, want := next.effectiveMaxToolRounds(), 1+roundGrantBlock; got != want {
		t.Errorf("ceiling = %d, want %d", got, want)
	}
	if !next.turnOpen {
		t.Error("the turn is open again, so it can close when it really ends")
	}
	// Taking the offer spends it, on the row as well as in the dispatch.
	if _, _, claimed := next.roundPauseKey(grantRoundsKey); claimed {
		t.Error("a spent offer should stop claiming its key")
	}
	if view := pauseView(next, pauseEntry(t, next)); strings.Contains(view, firstGrantOffer) {
		t.Errorf("a spent offer keeps its words and loses its key:\n%s", view)
	}
}

func TestRoundLimit_AGrantedTurnClosesOnceForTheWholeTurn(t *testing.T) {
	m, _ := pausedModel(t)
	m.focusIdx = indexOfKind(t, m, entryRoundPause)
	updated, _, _ := m.roundPauseKey(grantRoundsKey)
	m = finishTurn(t, updated.(Model))

	closes := 0
	for _, e := range m.transcript {
		if e.kind == entryTurnClose {
			closes++
		}
	}
	if closes != 1 {
		t.Fatalf("one turn closes once, got %d close blocks", closes)
	}
	c := lastClose(t, m)
	if c.Changes == nil || c.Changes.Files != 1 {
		t.Fatalf("the close covers everything the turn changed, got %+v", c.Changes)
	}
	if want := fmt.Sprintf("round 1/%d", 1+roundGrantBlock); c.Note != want {
		t.Errorf("the close reports the ceiling it finished under, got %q", c.Note)
	}
	// One turn in the history, not two: the accounting was reopened.
	if got := len(m.vitals.turns); got != 1 {
		t.Errorf("the granted turn is one entry in the history, got %d", got)
	}
}

func TestRoundLimit_TheRailCarriesTheLimitAndTheGrant(t *testing.T) {
	m, _ := pausedModel(t)
	if got, want := m.cockpitData(true).Round, fmt.Sprintf("round 1/1 +%d", roundGrantBlock); got != want {
		t.Errorf("paused rail = %q, want the limit and the grant on offer", got)
	}

	m.focusIdx = indexOfKind(t, m, entryRoundPause)
	updated, _, _ := m.roundPauseKey(grantRoundsKey)
	if got, want := updated.(Model).cockpitData(true).Round, fmt.Sprintf("round 1/%d", 1+roundGrantBlock); got != want {
		t.Errorf("granted rail = %q, want the raised ceiling", got)
	}
}

func TestRoundLimit_ANewTurnGetsTheConfiguredCeilingBack(t *testing.T) {
	m, _ := pausedModel(t)
	m.focusIdx = indexOfKind(t, m, entryRoundPause)
	updated, _, _ := m.roundPauseKey(grantRoundsKey)
	m = finishTurn(t, updated.(Model))

	m = sendText(t, m, "now do the other one")
	if got := m.effectiveMaxToolRounds(); got != 1 {
		t.Errorf("ceiling = %d, want the configured 1 back", got)
	}
	if m.pausedAtRoundLimit() {
		t.Error("a new turn is not paused at anything")
	}
	if got := m.cockpitData(true).Round; strings.Contains(got, "+") {
		t.Errorf("no grant is on offer in a fresh turn, got %q", got)
	}
}

func TestRoundLimit_AFreshMessageSpendsTheStandingOffer(t *testing.T) {
	m, _ := pausedModel(t)
	m = sendText(t, m, "continue")

	if got := m.agent.Rounds(); got != 0 {
		t.Errorf("fresh input resets the counter, got %d", got)
	}
	e := pauseEntry(t, m)
	if !e.pause.spent {
		t.Error("a turn the session has moved past cannot be granted more rounds")
	}
	if view := pauseView(m, e); strings.Contains(view, firstGrantOffer) {
		t.Errorf("the row keeps its words and loses the key:\n%s", view)
	}
	if !strings.Contains(pauseView(m, e), "[v] review what it did") {
		t.Error("reviewing what it did survives: the changeset is still there")
	}
}

func TestRoundLimit_ReviewAndUndoActOnThePausedTurn(t *testing.T) {
	m, path := pausedModel(t)
	m.focusIdx = indexOfKind(t, m, entryRoundPause)

	updated, _, claimed := m.roundPauseKey(reviewKey)
	if !claimed {
		t.Fatal("[v] should be claimed by the pause row")
	}
	reviewed := updated.(Model)
	if reviewed.state != stateReview || reviewed.reviewTurnN != m.turnCount {
		t.Fatalf("[v] opens the paused turn in review, got state %v turn %d",
			reviewed.state, reviewed.reviewTurnN)
	}

	updated, _, claimed = m.roundPauseKey(undoKey)
	if !claimed {
		t.Fatal("[u] should be claimed by the pause row")
	}
	undone := updated.(Model)
	if undone.state != stateUndoConfirm || undone.undoAsk == nil {
		t.Fatalf("[u] asks before it writes, got state %v", undone.state)
	}
	if got := undoPlanPaths(undone.undoPlan); len(got) != 1 || got[0] != path {
		t.Errorf("the undo covers what the turn wrote, got %v", got)
	}
}

func TestRoundLimit_ATurnThatChangedNothingOffersOnlyTheGrant(t *testing.T) {
	m := turnModel(t).WithMaxToolRounds(1)
	m = sendText(t, m, "look around")
	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_r", Name: "read_file", Arguments: `{"path":"nope.go"}`},
	}})
	m = updated.(Model)
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(toolResultsMsg); ok {
			updated, _ = m.Update(msg)
			m = updated.(Model)
		}
	}
	if m.roundPause == nil {
		t.Fatalf("the read should have used the only round, state %v", m.turnState())
	}

	view := pauseView(m, pauseEntry(t, m))
	if !strings.Contains(view, "nothing changed") {
		t.Errorf("a turn that changed nothing says so rather than reporting zeroes:\n%s", view)
	}
	for _, unwanted := range []string{"[v]", "[u]"} {
		if strings.Contains(view, unwanted) {
			t.Errorf("a key that cannot be honoured is not offered (%s):\n%s", unwanted, view)
		}
	}
	if !strings.Contains(view, firstGrantOffer) {
		t.Errorf("the grant is always on offer:\n%s", view)
	}
}

func TestRoundLimit_ASecondPauseNamesWhatWasAlreadyGranted(t *testing.T) {
	p := roundPause{used: 11, limit: 11, granted: roundGrantBlock, files: 1, added: 1}
	if got := p.qualifier(); got != fmt.Sprintf("%d already granted", roundGrantBlock) {
		t.Errorf("qualifier = %q, want the grants already made", got)
	}
	first := roundPause{used: 25, limit: 25}
	if got := first.qualifier(); got != "the turn's own bound" {
		t.Errorf("first qualifier = %q, want the bound named", got)
	}
}

func TestRoundLimit_StalenessIsAboutTheLastEdit(t *testing.T) {
	edit := entry{kind: entryDiff}
	suite := entry{kind: entryCommand, text: "go test ./..."}
	cases := []struct {
		name string
		es   []entry
		want bool
	}{
		{"nothing edited makes no claim", []entry{suite}, false},
		{"edited and never checked", []entry{edit}, true},
		{"checked after the last edit", []entry{edit, suite}, false},
		{"edited again after the check", []entry{edit, suite, edit}, true},
	}
	for _, tc := range cases {
		if got := checksStale(tc.es); got != tc.want {
			t.Errorf("%s: checksStale = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRoundLimit_ThePauseDefersTheContextCard(t *testing.T) {
	m := pressureModel(t, 110).WithMaxToolRounds(1)
	m = sendText(t, m, "keep going")
	updated, cmd := m.Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_r", Name: "read_file", Arguments: `{"path":"nope.go"}`},
	}})
	m = updated.(Model)
	for _, c := range unwrapBatch(cmd) {
		if msg, ok := c().(toolResultsMsg); ok {
			updated, _ = m.Update(msg)
			m = updated.(Model)
		}
	}
	if m.roundPause == nil {
		t.Fatalf("the turn should be paused at its ceiling, state %v", m.turnState())
	}
	if m.pressure != nil || m.state == statePressure {
		t.Error("two decision surfaces at once is one too many; the card waits a turn")
	}
	if m.pressureShown {
		t.Error("deferring must not spend the crossing")
	}
}

func TestRoundLimit_FocusModeLandsOnThePauseAndTakesTheGrant(t *testing.T) {
	m, _ := pausedModel(t)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	focused := updated.(Model)
	if focused.state != stateFocus {
		t.Fatalf("ctrl+e should enter focus mode, got state %v", focused.state)
	}
	if got := focused.transcript[focused.focusIdx].kind; got != entryRoundPause {
		t.Fatalf("the cursor should land on the row holding the way out, got kind %v", got)
	}
	if hint := ansi.Strip(focused.renderFocusHint()); !strings.Contains(hint, fmt.Sprintf("[+] %d more rounds", roundGrantBlock)) {
		t.Errorf("the hint names the literal key the row draws as %s:\n%s", firstGrantOffer, hint)
	}

	updated, cmd := focused.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(grantRoundsKey)})
	next := updated.(Model)
	if cmd == nil || next.turnState() != stateStreaming {
		t.Fatalf("the grant in focus mode should continue the turn, state %v", next.turnState())
	}
	if got, want := next.effectiveMaxToolRounds(), 1+roundGrantBlock; got != want {
		t.Errorf("ceiling = %d, want %d", got, want)
	}
}

// The row is a RecoveryRow like every other §17a row, on the same grid.
func TestRoundLimit_TheRowIsOnTheGrid(t *testing.T) {
	m, _ := pausedModel(t)
	row := m.roundPauseRow(pauseEntry(t, m))
	if row.Verb != components.VerbRounds {
		t.Errorf("verb = %q, want the rounds verb", row.Verb)
	}
	if row.State != components.RecoveryStalled {
		t.Errorf("state = %v, want the recoverable ⚠ — nothing failed here", row.State)
	}
}

// offers reports whether a row's key list carries this bracket, which is how
// the tests below ask what a stop is offering without re-rendering it.
func offers(keys []components.KeyOffer, bracket string) bool {
	for _, k := range keys {
		if k.Key == bracket {
			return true
		}
	}
	return false
}

// The grant doubles: each one is everything the turn has been given already,
// plus another block. Three stops put the ceiling past any turn that finishes,
// which is the point — the checkpoint is meant to go quiet, not to become a
// toll collected every few minutes.
func TestRoundLimit_TheGrantDoubles(t *testing.T) {
	granted := 0
	for _, want := range []int{roundGrantBlock, 2 * roundGrantBlock, 4 * roundGrantBlock, 8 * roundGrantBlock} {
		p := roundPause{granted: granted}
		got := p.grant()
		if got != want {
			t.Fatalf("the grant after %d already granted = %d, want %d", granted, got, want)
		}
		granted += got
	}
}

// [!] belongs to the second stop, not the first: the first is the checkpoint
// doing the job it exists for, and there is nothing to stop asking yet.
func TestRoundLimit_LetItRunIsTheSecondStopsOffer(t *testing.T) {
	uncap := "[" + uncapRoundsKey + "]"
	first := roundPause{files: 1}
	if offers(first.keys(), uncap) {
		t.Error("the first stop offers the grant, not the end of the question")
	}
	second := roundPause{files: 1, granted: roundGrantBlock}
	if !offers(second.keys(), uncap) {
		t.Error("a stop you have already answered once offers to stop asking")
	}
	if !offers(second.keys(), fmt.Sprintf("[+%d]", 2*roundGrantBlock)) {
		t.Error("the second stop's bracket is the doubled block")
	}
	// The bar names the keys, so its labels carry the numbers the row's
	// brackets do.
	bar := roundPauseOffers(&second)
	if !offers(bar, "["+grantRoundsKey+"]") || !offers(bar, uncap) {
		t.Errorf("the hint bar names both literal keys, got %+v", bar)
	}
	// Taking either spends both: the row keeps its words and loses its keys.
	spent := roundPause{files: 1, granted: roundGrantBlock, spent: true}
	for _, k := range spent.keys() {
		if k.Key == uncap || strings.HasPrefix(k.Key, "[+") {
			t.Errorf("a spent stop still offers %q", k.Key)
		}
	}
}

// [!] lifts the ceiling for the rest of the turn and for no longer than that.
func TestRoundLimit_LetItRunClearsTheCeilingForTheTurn(t *testing.T) {
	m, _ := pausedModel(t)
	// Stand the row up as a second stop, which is the only one that offers
	// the key: one grant already taken.
	pauseEntry(t, m).pause.granted = roundGrantBlock
	m.roundGrant = roundGrantBlock
	m.focusIdx = indexOfKind(t, m, entryRoundPause)

	updated, cmd, claimed := m.roundPauseKey(uncapRoundsKey)
	if !claimed || cmd == nil {
		t.Fatal("[!] should be claimed by the focused pause row and continue the turn")
	}
	next := updated.(Model)
	if !next.roundsUnbounded() {
		t.Error("the rest of the turn runs against no ceiling")
	}
	if next.turnState() != stateStreaming {
		t.Errorf("state = %v, want streaming", next.turnState())
	}
	if got, want := next.cockpitData(true).Round, "round 1/∞"; got != want {
		t.Errorf("rail = %q, want %q — the rail must not invent a bound", got, want)
	}
	if _, _, claimed := next.roundPauseKey(uncapRoundsKey); claimed {
		t.Error("a spent offer should stop claiming its key")
	}

	// It expires with the turn, exactly as the grant does: a session that
	// should never stop says so at the command line, not by a key.
	done := sendText(t, finishTurn(t, next), "now do the other one")
	if done.roundsUnbounded() {
		t.Error("a new turn gets the configured ceiling back")
	}
}

// A session started with `--max-rounds 0` never reaches the checkpoint at all,
// which is the whole point of it: there is nobody there to press a key.
func TestRoundLimit_AnUncappedSessionNeverStops(t *testing.T) {
	m := turnModel(t).WithMaxToolRounds(agent.UnlimitedToolRounds)
	m = sendText(t, m, "fix the round accounting")
	m = applyWrite(t, m, filepath.Join(t.TempDir(), "loop.go"), "package agent\n", "y")
	if m.roundPause != nil {
		t.Fatal("an uncapped session has no checkpoint to stop at")
	}
	if m.turnState() == stateInput {
		t.Errorf("the turn should have carried straight on, state %v", m.turnState())
	}
	if got, want := m.cockpitData(true).Round, "round 1/∞"; got != want {
		t.Errorf("rail = %q, want %q", got, want)
	}
}
