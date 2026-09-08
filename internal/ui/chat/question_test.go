package chat

// The question card against the promises it makes: that no mode and no grant
// can answer it, that an answer names a label, that a row the model marked
// unavailable cannot be taken, and that an empty note is an empty note.

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/ask"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

const chooseArgs = `{"question":"Which store should the cache use?","shape":"choose","options":[
	{"label":"Postgres","detail":"one more service to run","field":"3 files"},
	{"label":"SQLite","detail":"in the checkout already","recommended":true},
	{"label":"Redis","unavailable":"no client in this project"}]}`

// questionModel is a session mid-turn with an ask call arriving, and an
// executor that fails the test if anything routes a question to it: a
// question is a decision and never runs.
func questionModel(t *testing.T, mode agent.Mode) Model {
	t.Helper()
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "do the thing"},
	}
	m := New(msgs, mockStream).
		WithToolExecutor(func(name string, _ json.RawMessage) (string, error) {
			t.Fatalf("a question must never reach the executor, got %s", name)
			return "", nil
		}).
		WithAsk()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = updated.(Model)
	m.policy.mode = mode
	m.state = stateStreaming
	return m
}

func askCall(args string) toolCallsMsg {
	return toolCallsMsg{calls: []provider.ToolCall{{ID: "call_q", Name: ask.ToolName, Arguments: args}}}
}

// openedQuestion is the card up and holding the keyboard, which is what a
// reader who walked up to it has.
func openedQuestion(t *testing.T, mode agent.Mode, args string) Model {
	t.Helper()
	updated, _ := questionModel(t, mode).Update(askCall(args))
	m := updated.(Model)
	if m.state != stateQuestion || m.question == nil {
		t.Fatalf("an ask call should draw the question card, got state %d", m.state)
	}
	return handover(t, m)
}

// sendKey is press for a keystroke that is not a letter: the card is
// answered with tab, space and enter as often as with a digit.
func sendKey(t *testing.T, m Model, msg tea.KeyPressMsg) Model {
	t.Helper()
	updated, _ := m.Update(msg)
	return updated.(Model)
}

// answeredResult is the tool result the card sent back for the question.
func answeredResult(t *testing.T, m Model) map[string]any {
	t.Helper()
	for i := len(m.transcript) - 1; i >= 0; i-- {
		e := m.transcript[i]
		if e.kind != entryTool || e.toolName != ask.ToolName {
			continue
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(e.toolResult), &got); err != nil {
			t.Fatalf("the result is not the JSON the model reads: %v (%q)", err, e.toolResult)
		}
		return got
	}
	t.Fatal("no answered question in the transcript")
	return nil
}

// A question is a decision and not an act, so every gate that exists to
// decide which acts stop to ask is above it in the queue: none of them may
// answer one.
func TestQuestion_EveryModeAndEveryGrantStillDrawsTheCard(t *testing.T) {
	for _, mode := range []agent.Mode{agent.ModeManual, agent.ModeAcceptEdits, agent.ModeAuto, agent.ModePlan} {
		for _, grant := range []string{"none", "every edit and every command", "a classifier"} {
			m := questionModel(t, mode)
			switch grant {
			case "every edit and every command":
				// What --yes leaves behind on a session.
				m.policy.allEdits, m.policy.allCommands = true, true
			case "a classifier":
				// One that answers allow to everything it is asked. It has
				// no standing on which of two designs a person prefers, and
				// the executor above is what would catch it deciding.
				ledger := meter.New(nil)
				m = m.WithLedger(ledger).WithClassifier(agent.NewClassifier(
					ledger.For(&verdictProvider{decision: "allow", reason: "the work that was asked for"},
						meter.SourceClassifier),
					agent.ClassifierConfig{Model: "judge"}))
			}
			updated, _ := m.Update(askCall(chooseArgs))
			next := updated.(Model)
			if next.state != stateQuestion || next.question == nil {
				t.Fatalf("%s with %s: the card must be drawn, got state %d", mode, grant, next.state)
			}
			if !strings.Contains(stripANSI(next.View().Content), "Which store should the cache use?") {
				t.Fatalf("%s with %s: the question is not on the screen", mode, grant)
			}
		}
	}
}

// The answer names the option's label, never its index — the list the reader
// saw is not the list the model wrote, because the recommendation leads it.
func TestQuestion_AnAnswerNamesTheLabelAndNotTheIndex(t *testing.T) {
	m := openedQuestion(t, agent.ModeManual, chooseArgs)
	if got := m.question.rows[0].Label; got != "SQLite" {
		t.Fatalf("the recommendation should lead the list, got %q", got)
	}
	// The first row, which is the model's second option.
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	got := answeredResult(t, m)
	picked, _ := got["picked"].([]any)
	if len(picked) != 1 || picked[0] != "SQLite" {
		t.Fatalf("the answer should carry the label: %+v", got)
	}
	if got["answered"] != string(ask.AnsweredOnCard) {
		t.Errorf("answered = %v", got["answered"])
	}
	if got["note"] != "" {
		t.Errorf("an empty note is an empty string, got %#v", got["note"])
	}
}

// The digit rows the selector already draws take one outright, and they
// address the list the reader is looking at.
func TestQuestion_ADigitTakesTheRowItNumbers(t *testing.T) {
	m := openedQuestion(t, agent.ModeManual, chooseArgs)
	m = sendKey(t, m, tea.KeyPressMsg{Code: '2', Text: "2"})
	picked, _ := answeredResult(t, m)["picked"].([]any)
	if len(picked) != 1 || picked[0] != "Postgres" {
		t.Fatalf("the second row is the model's first option, got %+v", picked)
	}
}

// A row the model said cannot be taken says why again rather than answering,
// and the reason is the one already on the row.
func TestQuestion_AnUnavailableRowCannotBeTaken(t *testing.T) {
	m := openedQuestion(t, agent.ModeManual, chooseArgs)
	m = sendKey(t, m, tea.KeyPressMsg{Code: '3', Text: "3"})
	if m.question == nil {
		t.Fatal("taking a row that cannot be taken must not answer the question")
	}
	view := stripANSI(m.View().Content)
	if !strings.Contains(view, "no client in this project") {
		t.Fatalf("the card should re-state the reason:\n%s", view)
	}
	if !strings.Contains(view, "⊘") {
		t.Fatalf("an unavailable row carries the glyph, never merely a dimming:\n%s", view)
	}
}

// The last row is the model's own list saying it may have got the list wrong.
func TestQuestion_SomethingElseTakesTypedWords(t *testing.T) {
	m := openedQuestion(t, agent.ModeManual, chooseArgs)
	last := len(m.question.rows)
	if m.question.rows[last-1].Label != somethingElse {
		t.Fatalf("every list ends in the row the model did not write, got %q", m.question.rows[last-1].Label)
	}
	m = sendKey(t, m, tea.KeyPressMsg{Code: rune('0' + last), Text: string(rune('0' + last))})
	if m.question == nil {
		t.Fatal("the row needs words before it is an answer")
	}
	if !m.question.sel.FocusNote {
		t.Fatal("taking it should open the note field")
	}
	if !strings.Contains(stripANSI(m.View().Content), "note required") {
		t.Fatal("the field should say the note is what is missing")
	}
	m = typeInto(t, m, "neither — put it behind an interface")
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	got := answeredResult(t, m)
	if got["answered"] != string(ask.AnsweredTyped) {
		t.Errorf("answered = %v, want typed", got["answered"])
	}
	if got["note"] != "neither — put it behind an interface" {
		t.Errorf("note = %#v", got["note"])
	}
	if picked, _ := got["picked"].([]any); len(picked) != 0 {
		t.Errorf("nothing was picked, got %+v", picked)
	}
}

// One key opens the note on a pick, and it comes back beside it.
func TestQuestion_TheNoteRidesBesideThePick(t *testing.T) {
	m := openedQuestion(t, agent.ModeManual, chooseArgs)
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if !m.question.sel.FocusNote {
		t.Fatal("the note key should open the field")
	}
	m = typeInto(t, m, "and cap the pool at 4")
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	got := answeredResult(t, m)
	picked, _ := got["picked"].([]any)
	if len(picked) != 1 || picked[0] != "SQLite" || got["note"] != "and cap the pool at 4" {
		t.Fatalf("the pick and the note travel together: %+v", got)
	}
}

// A required note opens the field with the card and refuses an empty answer.
func TestQuestion_ARequiredNoteOpensWithTheCard(t *testing.T) {
	m := openedQuestion(t, agent.ModeManual,
		`{"question":"Name the flag","shape":"text","note":"required"}`)
	if !m.question.sel.FocusNote {
		t.Fatal("a free-text question opens with the keyboard in the field")
	}
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.question == nil {
		t.Fatal("an empty required note is not an answer")
	}
	if !strings.Contains(stripANSI(m.View().Content), "note required") {
		t.Fatal("the field should say so")
	}
	m = typeInto(t, m, "--keep-going")
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	got := answeredResult(t, m)
	if got["answered"] != string(ask.AnsweredTyped) || got["note"] != "--keep-going" {
		t.Fatalf("the typed answer did not travel: %+v", got)
	}
}

// The yes-or-no is the inline confirm, and its enter is the answer that
// changes nothing.
func TestQuestion_ConfirmAnswersInTwoLetters(t *testing.T) {
	const args = `{"question":"Should the migration be reversible?","shape":"confirm"}`
	for _, tc := range []struct {
		key  tea.KeyPressMsg
		want string
	}{
		{tea.KeyPressMsg{Code: 'y', Text: "y"}, "yes"},
		{tea.KeyPressMsg{Code: 'n', Text: "n"}, "no"},
		{tea.KeyPressMsg{Code: tea.KeyEnter}, "no"},
	} {
		m := sendKey(t, openedQuestion(t, agent.ModeManual, args), tc.key)
		got := answeredResult(t, m)
		picked, _ := got["picked"].([]any)
		if len(picked) != 1 || picked[0] != tc.want {
			t.Errorf("%v answered %+v, want %q", tc.key, picked, tc.want)
		}
	}
}

// Several answers at once, with the note under the boxes.
func TestQuestion_ChooseManyCarriesEveryTickedLabel(t *testing.T) {
	m := openedQuestion(t, agent.ModeManual, `{"question":"Which packages?","shape":"choose_many","options":[
		{"label":"internal/agent"},{"label":"internal/cli"},{"label":"internal/ui"}]}`)
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	m = sendKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	got := answeredResult(t, m)
	picked, _ := got["picked"].([]any)
	if len(picked) != 2 || picked[0] != "internal/agent" || picked[1] != "internal/cli" {
		t.Fatalf("both ticked labels should travel: %+v", got)
	}
	if got["answered"] != string(ask.AnsweredOnCard) {
		t.Errorf("answered = %v", got["answered"])
	}
}

// Esc leaves and answers `skipped`: nothing chosen, nothing lost, and the
// model told to state the assumption and carry on.
func TestQuestion_EscSkipsAndTellsTheModelToCarryOn(t *testing.T) {
	m := sendKey(t, openedQuestion(t, agent.ModeManual, chooseArgs), tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.question != nil || m.pendingApproval != nil {
		t.Fatal("esc should close the card and answer the call")
	}
	got := answeredResult(t, m)
	if got["answered"] != string(ask.AnsweredSkipped) {
		t.Errorf("answered = %v, want skipped", got["answered"])
	}
	if !strings.Contains(got["instruction"].(string), "state the assumption") {
		t.Errorf("instruction = %v", got["instruction"])
	}
}

// The row the answer leaves: the verb, the outcome from the closed list, and
// no mutation rail, because nothing on the machine changed.
func TestQuestion_TheAnswersRowCarriesNoRail(t *testing.T) {
	m := sendKey(t, openedQuestion(t, agent.ModeManual, chooseArgs), tea.KeyPressMsg{Code: tea.KeyEnter})
	var e entry
	for _, ent := range m.transcript {
		if ent.kind == entryTool && ent.toolName == ask.ToolName {
			e = ent
		}
	}
	row := m.activityRowFor(e)
	if row.Verb != "asked" {
		t.Errorf("verb = %q", row.Verb)
	}
	if row.Outcome != "answered · on the card" {
		t.Errorf("outcome = %q", row.Outcome)
	}
	rendered := stripANSI(m.renderEntry(e, 100))
	if strings.Contains(rendered, "▎") {
		t.Errorf("a question changed nothing, so its row carries no rail:\n%s", rendered)
	}
	if strings.Contains(rendered, "1 line") {
		t.Errorf("what came back is one decision and not a quantity:\n%s", rendered)
	}
}

// A question that arrives on top of a half-typed sentence is inert: every
// letter stays the sentence's until the handover chord.
func TestQuestion_ArrivesInertOverASentenceAndHeldOverAnEmptyDraft(t *testing.T) {
	m := questionModel(t, agent.ModeManual)
	m.input.SetValue("I was in the middle of")
	updated, _ := m.Update(askCall(chooseArgs))
	m = updated.(Model)
	if !m.decisionUngated() {
		t.Fatal("a card landing on a sentence waits for the handover")
	}
	m = sendKey(t, m, tea.KeyPressMsg{Code: 'y', Text: "y"})
	if !strings.HasSuffix(m.input.Value(), "y") {
		t.Fatalf("the letter belongs to the sentence, got %q", m.input.Value())
	}
	if m.question == nil {
		t.Fatal("nothing should have answered the question")
	}

	// Inert is not invisible: the card rides above the frame while the draft
	// keeps the keyboard, and a question nobody can see is a turn that
	// stopped for no stated reason.
	view := stripANSI(m.View().Content)
	if !strings.Contains(view, "Which store should the cache use?") {
		t.Fatalf("the ungated card should be on screen:\n%s", view)
	}
	if !strings.Contains(view, "I was in the middle of") {
		t.Fatalf("the draft should still be under it:\n%s", view)
	}

	empty := questionModel(t, agent.ModeManual)
	updated, _ = empty.Update(askCall(chooseArgs))
	if !updated.(Model).decisionGated() {
		t.Fatal("a card landing on an empty draft takes the keyboard")
	}
}

// A cancelled turn takes the outstanding question with it. The card is not
// only off the screen: the grace window reads the card to decide which keys
// a burst may not answer, so one left behind would protect the next decision
// with the wrong card's keys.
func TestQuestion_ACancelledTurnTakesTheQuestionWithIt(t *testing.T) {
	m := openedQuestion(t, agent.ModeManual, chooseArgs)
	next, _ := m.cancelTurnNow()
	m = next.(Model)
	if m.question != nil || m.pendingApproval != nil {
		t.Fatal("the cancel should leave no question behind")
	}
	if m.graceDiscards(keys.Shown(keys.Decision.Allow)) != true {
		t.Error("the grace window should be back on the decision keys")
	}
}

// A queued question is recognisable in the strip. Two of them both labelled
// with the tool's name would be two rows nobody can tell apart.
func TestQuestion_TheQueueStripNamesTheQuestion(t *testing.T) {
	req, err := questionModel(t, agent.ModeManual).buildQuestionApproval(provider.ToolCall{
		ID: "call_q", Name: ask.ToolName, Arguments: chooseArgs,
	})
	if err != nil {
		t.Fatal(err)
	}
	label, _ := queueLabel(req)
	if label != "Which store should the cache use?" {
		t.Errorf("the strip says %q", label)
	}
}

// A question is a decision waiting on the reader, so the summons it raises
// when they are elsewhere says the question rather than nothing.
func TestQuestion_TheSummonsSaysTheQuestion(t *testing.T) {
	m := openedQuestion(t, agent.ModeManual, chooseArgs)
	title, body := m.notifyWords()
	if title != "Which store should the cache use?" {
		t.Errorf("title = %q", title)
	}
	if body == "" {
		t.Error("the summons says nothing about what is wanted")
	}
}

// An unreadable question is a skipped call with the parse error as its
// result, the way every other malformed call is answered.
func TestQuestion_AnUnreadableCallIsSkippedWithTheReason(t *testing.T) {
	updated, _ := questionModel(t, agent.ModeManual).Update(askCall(`{"question":"?","shape":"rank"}`))
	m := updated.(Model)
	if m.state == stateQuestion {
		t.Fatal("a question that cannot be drawn must not draw a card")
	}
	found := false
	for _, e := range m.transcript {
		if strings.Contains(e.toolResult, "unknown shape") || strings.Contains(e.text, "unknown shape") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the model should be told what it may write instead: %+v", m.transcript)
	}
}

// Esc out of the note drops the note and keeps the pick; esc again leaves the
// card. A way out that lost the pick would not be the safe one.
func TestQuestion_EscOutOfTheNoteKeepsThePick(t *testing.T) {
	m := openedQuestion(t, agent.ModeManual, chooseArgs)
	m = sendKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	m = typeInto(t, m, "second thoughts")
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.question == nil {
		t.Fatal("esc out of the note must not leave the card")
	}
	if m.question.sel.FocusNote {
		t.Error("esc out of the note should hand the keyboard back to the list")
	}
	if got := strings.TrimSpace(m.question.sel.Note.Value()); got != "" {
		t.Errorf("esc should drop the note, got %q", got)
	}
	if got := m.question.sel.Select.Focus; got != 1 {
		t.Errorf("the pick moved to row %d", got)
	}
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.question != nil {
		t.Fatal("esc again should leave the card")
	}
	if answeredResult(t, m)["answered"] != string(ask.AnsweredSkipped) {
		t.Error("leaving the card answers skipped")
	}
}

// The free-answer card is the other branch: the note is the answer, so there
// is no pick for esc to keep and it leaves outright.
func TestQuestion_EscLeavesAFreeAnswerOutright(t *testing.T) {
	m := openedQuestion(t, agent.ModeManual, `{"question":"Name the flag","shape":"text"}`)
	m = typeInto(t, m, "--keep-going")
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.question != nil {
		t.Fatal("a card with no rows has no pick to keep, so esc leaves it")
	}
	if answeredResult(t, m)["answered"] != string(ask.AnsweredSkipped) {
		t.Error("leaving the card answers skipped")
	}
}
