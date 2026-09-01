package components

// The rating screen (docs/interface/surfaces.md#the-supporting-screens). The
// assertions here are about what separates it from every other supporting
// screen: an answer is one keystroke and the card is already the next one,
// the answer keys stop being offered once there is nothing left to answer,
// and esc leaves whatever has been answered standing.

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func rateRows() []RateRow {
	return []RateRow{
		{ID: "1", Prompt: "delete every log file older than a week",
			Kind: ActivityCommand, Verb: "run",
			Target: "find . -name '*.log' -mtime +7 -delete",
			When:   "4m ago", Outcome: "exit 0", State: ActivityDone},
		{ID: "2", Prompt: "show the ten biggest files here",
			Kind: ActivityCommand, Verb: "run",
			Target: "du -ah . | sort -rh | head -10",
			When:   "yesterday", Outcome: "copied", State: ActivityDone},
		{ID: "3", Prompt: "rebase onto main and force push",
			Kind: ActivityCommand, Verb: "run",
			Target: "git rebase main && git push --force-with-lease",
			When:   "tue", Outcome: "exit 128", State: ActivityFailed},
	}
}

func rateScreen() *RateScreen {
	return &RateScreen{Rows: rateRows(), MaxLines: 18}
}

func rateView(r *RateScreen, width int) string { return ansi.Strip(r.View(width)) }

// rateAnswer presses one key and reports what the screen made of it.
func rateAnswer(t *testing.T, r *RateScreen, pressed string) (bool, any) {
	t.Helper()
	return r.Update(key(pressed))
}

// Each of the three answers resolves to the entry that was on the card, and
// the card has already moved on: a run of entries is answered without a key
// between them, which is the whole reason this is a screen and not a list.
func TestRateScreen_AnswerCarriesTheEntryAndMovesOn(t *testing.T) {
	for _, tc := range []struct {
		pressed string
		want    RateAct
	}{{"y", RateWorked}, {"n", RateFailed}, {"s", RateSkipped}} {
		r := rateScreen()
		done, result := rateAnswer(t, r, tc.pressed)
		if done {
			t.Errorf("%q closed the screen with two entries still to ask about", tc.pressed)
		}
		got, ok := result.(RateAnswer)
		if !ok {
			t.Fatalf("%q resolved to %T, not an answer", tc.pressed, result)
		}
		if got.Act != tc.want || got.ID != "1" {
			t.Errorf("%q resolved to %+v, want %v on entry 1", tc.pressed, got, tc.want)
		}
		if r.Focus != 1 {
			t.Errorf("%q left the card on %d, want the next one", tc.pressed, r.Focus)
		}
	}
}

// The title counts the question rather than indexing the list: a reader is
// being asked `3 of 7`, not shown row two.
func TestRateScreen_TitleCountsTheQuestions(t *testing.T) {
	r := rateScreen()
	if got := rateView(r, 80); !strings.Contains(got, "1 of 3") {
		t.Errorf("the header does not count the questions:\n%s", got)
	}
	rateAnswer(t, r, "y")
	if got := rateView(r, 80); !strings.Contains(got, "2 of 3") {
		t.Errorf("the header did not follow the answer:\n%s", got)
	}
}

// The card carries both halves of the question — what was asked and what came
// back — with the exit code as the command row's outcome.
func TestRateScreen_CardHoldsThePromptAndTheCommand(t *testing.T) {
	got := rateView(rateScreen(), 110)
	for _, want := range []string{
		"4m ago",
		"delete every log file older than a week",
		"find . -name '*.log' -mtime +7 -delete",
		"exit 0",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the card does not say %q:\n%s", want, got)
		}
	}
}

// Answering the last entry closes the screen, and the answer still comes back
// with it: the host has to write the last one down too.
func TestRateScreen_TheLastAnswerClosesTheScreen(t *testing.T) {
	r := rateScreen()
	rateAnswer(t, r, "y")
	rateAnswer(t, r, "n")
	done, result := rateAnswer(t, r, "s")
	if !done {
		t.Error("the screen stayed up with nothing left to ask about")
	}
	got, ok := result.(RateAnswer)
	if !ok || got.ID != "3" || got.Act != RateSkipped {
		t.Errorf("the last answer came back as %#v", result)
	}
}

// Esc stops, and it stops without an answer for the card that was up: the way
// out never writes anything (docs/interface/principles.md#esc-is-always-the-safe-answer).
func TestRateScreen_EscStopsWithoutAnswering(t *testing.T) {
	for _, pressed := range []string{"esc", "q"} {
		r := rateScreen()
		rateAnswer(t, r, "y")
		done, result := rateAnswer(t, r, pressed)
		if !done {
			t.Errorf("%q did not stop", pressed)
		}
		if got, ok := result.(RateResult); !ok || !got.Stopped {
			t.Errorf("%q resolved to %#v, not a stop", pressed, result)
		}
		if r.Focus != 1 {
			t.Errorf("%q moved the card", pressed)
		}
	}
}

// A key that cannot act is not an offer (invariant 5): once the last card has
// been answered, the three answers are gone from the row and the way out is
// what is left.
func TestRateScreen_TheAnswersStopBeingOfferedWhenThereIsNothingToAnswer(t *testing.T) {
	r := &RateScreen{Rows: rateRows(), Focus: 3, MaxLines: 18}
	got := rateView(r, 80)
	for _, gone := range []string{"[y]", "[n]", "[s]"} {
		if strings.Contains(got, gone) {
			t.Errorf("%s is still offered with nothing left to answer:\n%s", gone, got)
		}
	}
	if !strings.Contains(got, "[esc]") {
		t.Errorf("the way out is not offered:\n%s", got)
	}
	// And a key that is not offered does not act: only the way out closes this
	// card, so a stray letter or an arrow key leaves it standing.
	for _, pressed := range []string{"y", "n", "s", "down", "enter"} {
		done, result := rateAnswer(t, r, pressed)
		if done || result != nil {
			t.Errorf("%q acted on a card with nothing left to answer: done=%v result=%#v",
				pressed, done, result)
		}
	}
	if done, _ := rateAnswer(t, r, "esc"); !done {
		t.Error("esc no longer closes the card it is the only key on")
	}
}

// `[?]` swaps the compact row for the whole register, in place, the way every
// other supporting screen answers that key.
func TestRateScreen_TheRegisterIsBehindOneKey(t *testing.T) {
	r := rateScreen()
	rateAnswer(t, r, "?")
	got := rateView(r, 80)
	if !strings.Contains(got, "it did what was asked") {
		t.Errorf("[?] did not open the register:\n%s", got)
	}
	rateAnswer(t, r, "?")
	if got := rateView(r, 80); strings.Contains(got, "it did what was asked") {
		t.Errorf("[?] did not put the register away:\n%s", got)
	}
}

// A narrow terminal folds the card rather than losing the question: the
// prompt wraps and the command continues under its own row (invariant 4).
func TestRateScreen_NarrowKeepsTheWholeQuestion(t *testing.T) {
	row := rateRows()[2]
	got := rateView(&RateScreen{Rows: rateRows(), Focus: 2}, 60)
	// The outcome sits in its own column at the end of the first line, so it
	// comes back out before the command's two lines are read as one command.
	flat := strings.ReplaceAll(got, "│", " ")
	flat = strings.Replace(flat, row.Outcome, "", 1)
	flat = strings.Join(strings.Fields(flat), " ")
	if !strings.Contains(flat, "git rebase main && git push --force-with-lease") {
		t.Errorf("the command was clipped away at w60:\n%s", got)
	}
	if !strings.Contains(flat, "rebase onto main and force push") {
		t.Errorf("the prompt was clipped away at w60:\n%s", got)
	}
}

// One card over two kinds. A session is drawn on the same grid a command is,
// and reads as the agent run it is: the sub-agent glyph, the `agent` verb,
// and the same three answers underneath.
func TestRateScreen_ASessionIsTheSameCard(t *testing.T) {
	r := &RateScreen{MaxLines: 18, Rows: []RateRow{sessionRateRow(nil)}}

	got := rateView(r, 110)
	for _, want := range []string{
		"┌─ mon ", "get the observe dashboard to show the gate pass rate",
		"◇ agent", "2026-09-01T18-02 · chat · claude-opus-5 · 14 turns", "completed",
		"[y] worked · [n] did not · [s] skip · [esc] stop",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the session card does not say %q:\n%s", want, got)
		}
	}
}

// The rail follows the outcome, not the kind. A session that came out well
// is a report of work rather than an act and takes no rail; one that was
// abandoned or failed keeps the rail every broken row on this grid keeps, so
// the sessions most worth rating are the ones the gutter marks
// (docs/interface/principles.md#weight-tracks-risk).
func TestRateScreen_ASessionsRailFollowsItsOutcome(t *testing.T) {
	for _, tc := range []struct {
		outcome string
		state   ActivityState
		rail    bool
	}{
		{"completed", ActivityDone, false},
		{"unknown", ActivityQueued, false},
		{"abandoned", ActivityDenied, true},
		{"error", ActivityFailed, true},
	} {
		r := &RateScreen{MaxLines: 18, Rows: []RateRow{sessionRateRow(func(row *RateRow) {
			row.Outcome, row.State = tc.outcome, tc.state
		})}}
		if got := strings.Contains(rateView(r, 110), "▎"); got != tc.rail {
			t.Errorf("a %s session draws the rail %v, want %v:\n%s",
				tc.outcome, got, tc.rail, rateView(r, 110))
		}
	}
}

func sessionRateRow(mut func(*RateRow)) RateRow {
	row := RateRow{ID: "s5",
		Prompt: "get the observe dashboard to show the gate pass rate",
		Kind:   ActivitySubagent, Verb: "agent",
		Target: "2026-09-01T18-02 · chat · claude-opus-5 · 14 turns",
		When:   "mon", Outcome: "completed", State: ActivityDone}
	if mut != nil {
		mut(&row)
	}
	return row
}
