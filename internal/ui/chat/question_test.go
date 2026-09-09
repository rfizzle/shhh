package chat

// The question card against the promises it makes: that no mode and no grant
// can answer it, that an answer names a label, that a row the model marked
// unavailable cannot be taken, and that an empty note is an empty note.

import (
	"encoding/json"
	"fmt"
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

// escapedQuestion is the card arriving on an idle draft and the reader
// pressing esc: no card, the question outstanding, and the draft holding the
// keyboard with the next message the answer.
func escapedQuestion(t *testing.T, args string) Model {
	t.Helper()
	updated, _ := questionModel(t, agent.ModeManual).Update(askCall(args))
	m := updated.(Model)
	if m.question == nil || !m.decisionGated() {
		t.Fatal("the card should arrive holding the keyboard on an idle draft")
	}
	return sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
}

// submitDraft types a line and sends it the way enter does.
func submitDraft(t *testing.T, m Model, text string) Model {
	t.Helper()
	m.input.SetValue(text)
	updated, _ := m.submitInput()
	return updated.(Model)
}

// countRows is the transcript's user messages and its answered questions,
// which is what tells an answer from a message that merely looked like one.
func countRows(m Model) (users, answered int) {
	for _, e := range m.transcript {
		switch {
		case e.kind == entryUser:
			users++
		case e.kind == entryTool && e.toolName == ask.ToolName:
			answered++
		}
	}
	return users, answered
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

// Esc closes the card and answers nothing: the call is still outstanding, the
// turn is still blocked on it, and the notice rail is what says so.
func TestQuestion_EscHandsTheQuestionToTheDraft(t *testing.T) {
	m := escapedQuestion(t, chooseArgs)
	if m.question != nil {
		t.Fatal("esc should close the card")
	}
	if m.pendingApproval == nil {
		t.Fatal("esc must not resolve the call — the question stays outstanding")
	}
	if !m.questionAside() {
		t.Fatal("the question should be waiting behind the draft")
	}
	if m.turnState() != stateQuestion {
		t.Errorf("the turn should stay where it was, got state %d", m.turnState())
	}
	if tc, ok := m.agent.NextApproval(); !ok || tc.Name != ask.ToolName {
		t.Error("the call should still be at the head of the approval queue")
	}
	for _, e := range m.transcript {
		if e.kind == entryTool && e.toolName == ask.ToolName {
			t.Fatal("esc answered the call; it should have left it outstanding")
		}
	}
	if !m.inputLive() {
		t.Error("the draft should have the keyboard once the card has gone")
	}
	if lines := m.interruptLines(); len(lines) != 0 {
		t.Errorf("nothing of the card should be left on the screen: %q", lines)
	}
	if !strings.Contains(m.noticeLine(), "1 question waiting") {
		t.Errorf("the rail is the only thing that can say a question is waiting: %q", stripANSI(m.noticeLine()))
	}
}

// The rail counts, in the shape the follow-up count already uses.
func TestQuestion_TheNoticeCountsAndPluralises(t *testing.T) {
	for n, want := range map[int]string{0: "", 1: "1 question waiting", 2: "2 questions waiting"} {
		if got := questionNoticeFor(n, false); got != want {
			t.Errorf("%d: %q, want %q", n, got, want)
		}
	}
	// With no hint rail under it, the count says what the next message does.
	want := "1 question waiting — " + keys.Bracket(keys.Draft.Send) + " answers"
	if got := questionNoticeFor(1, true); got != want {
		t.Errorf("%q, want %q", got, want)
	}
}

// The next message is the answer, in the reader's own words — and it is not
// also a user message and not a steer, because one sentence is one thing.
func TestQuestion_TheNextMessageAnswersItAndIsNotAlsoAMessage(t *testing.T) {
	m := escapedQuestion(t, chooseArgs)
	users, tools := countRows(m)
	m = submitDraft(t, m, "whichever one needs no new service")

	got := answeredResult(t, m)
	if got["answered"] != string(ask.AnsweredTyped) {
		t.Errorf("answered = %v, want typed", got["answered"])
	}
	if got["note"] != "whichever one needs no new service" {
		t.Errorf("the answer should be their words verbatim, got %v", got["note"])
	}
	if picked, _ := got["picked"].([]any); len(picked) != 0 {
		t.Errorf("a typed answer picks nothing: %v", picked)
	}
	if nowUsers, nowTools := countRows(m); nowUsers != users || nowTools != tools+1 {
		t.Errorf("the sentence should be one answered call and no user message: %d user rows (was %d), %d answered (was %d)",
			nowUsers, users, nowTools, tools)
	}
	if len(m.steering) != 0 {
		t.Errorf("an answer is not a steer: %+v", m.steering)
	}
	if m.questionAside() || m.pendingApproval != nil {
		t.Error("the question should be answered and gone")
	}
}

// The queue chord still means what it always did, and the rail counts the two
// separately: a sentence queued while a question waits keeps the promise it
// was queued under, and goes out after the turn the answer lets finish. It is
// never the answer, and that is a property of when the queue is dispatched
// rather than a choice — a turn cannot reach its end with a call outstanding.
func TestQuestion_AQueuedFollowUpIsStillForAfterTheTurn(t *testing.T) {
	m := escapedQuestion(t, chooseArgs)
	m.input.SetValue("then update the README")
	next, _, claimed := m.queueFollowUp()
	if !claimed {
		t.Fatal("the queue chord should still claim a typed draft")
	}
	m = next.(Model)
	if len(m.followUps) != 1 {
		t.Fatalf("the sentence should be queued, got %+v", m.followUps)
	}
	rail := stripANSI(m.noticeLine())
	if !strings.Contains(rail, "1 question waiting") || !strings.Contains(rail, "1 follow-up") {
		t.Errorf("the rail should count the two promises separately: %q", rail)
	}
	if _, tools := countRows(m); tools != 0 {
		t.Error("queueing a sentence answers nothing")
	}
	// And the queue is only ever dispatched where the turn has ended, which
	// it cannot do while the call is outstanding.
	if m.turnState() == stateInput {
		t.Fatal("a question outstanding is a turn that has not ended")
	}
}

// A command is not an answer. The line is dispatched as what it is, the way
// the queue chord already refuses one, so a question waiting behind the draft
// does not swallow the two kinds of line that were never messages.
func TestQuestion_ACommandAndABangAreNotTheAnswer(t *testing.T) {
	for _, line := range []string{"/model", "!git status", "/secret set TOKEN=hunter2"} {
		m := escapedQuestion(t, chooseArgs)
		m = submitDraft(t, m, line)
		if _, tools := countRows(m); tools != 0 {
			t.Errorf("%q was delivered as the answer", line)
		}
		if !m.questionAside() {
			t.Errorf("%q should leave the question waiting", line)
		}
		// The last of the three is the only line that can carry a secret's
		// value, and a question waiting behind the draft does not move where
		// that is decided: it is settled above the dispatch, for every line
		// the draft sends (secrets.go).
		for _, past := range m.inputHistory {
			if strings.Contains(past, "hunter2") {
				t.Error("the secret value reached the input history")
			}
		}
	}
}

// The rail while attached is the child's, so it does not offer the
// orchestrator's question — enter there acts on the child.
func TestQuestion_TheAttachedRailDoesNotOfferTheQuestion(t *testing.T) {
	m := escapedQuestion(t, chooseArgs)
	m.attachedTo = "scout"
	if got := stripANSI(m.frameHints(m.contentWidth())); strings.Contains(got, "answers the question") {
		t.Errorf("the attached rail offered an act enter does not keep there: %q", got)
	}
	if got := stripANSI(m.promptGutter()); !strings.Contains(got, "scout") {
		t.Errorf("the attached gutter is the child's: %q", got)
	}
}

// The reader who pressed esc by reflex gets the list back for one key, and
// the key is the handover — the same act it is on every other decision.
func TestQuestion_TheHandoverBringsTheCardBackAndAnswersNothing(t *testing.T) {
	m := escapedQuestion(t, chooseArgs)
	m = handover(t, m)
	if m.question == nil {
		t.Fatal("the handover should draw the card again")
	}
	if m.questionAside() {
		t.Error("the question is on the card again, not behind the draft")
	}
	if _, tools := countRows(m); tools != 0 {
		t.Error("reopening a question answers nothing")
	}
	if !strings.Contains(stripANSI(strings.Join(m.questionLines(), "\n")), "Which store should the cache use?") {
		t.Error("the card should come back as it arrived")
	}
	// And the card answers the way it always did.
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if answeredResult(t, m)["answered"] != string(ask.AnsweredOnCard) {
		t.Error("the reopened card should still answer on the card")
	}
}

// A cancel takes a question that was waiting behind the draft with it, so the
// next message the reader sends is an ordinary message again rather than the
// answer to a call that no longer exists.
func TestQuestion_ACancelMakesTheNextMessageAnOrdinaryMessageAgain(t *testing.T) {
	m := escapedQuestion(t, chooseArgs)
	m.cancelStreaming()
	if m.questionAside() || m.pendingApproval != nil || m.question != nil {
		t.Fatal("the cancel should have taken the question with the turn")
	}
	if got := stripANSI(m.noticeLine()); strings.Contains(got, "question waiting") {
		t.Errorf("the notice should have cleared: %q", got)
	}
	users, _ := countRows(m)
	m = submitDraft(t, m, "start again with SQLite")
	if nowUsers, _ := countRows(m); nowUsers != users+1 {
		t.Error("after a cancel the next message is an ordinary message again")
	}
}

// The session is waiting on the reader whether or not the card is drawn: the
// summons and the frame both read this one fact.
func TestQuestion_WaitingHoldsWithTheCardClosed(t *testing.T) {
	on := openedQuestion(t, agent.ModeManual, chooseArgs)
	if !on.waiting() {
		t.Error("a question on the card is the session waiting on the reader")
	}
	aside := escapedQuestion(t, chooseArgs)
	if !aside.waiting() {
		t.Error("a question behind the draft is still the session waiting on the reader")
	}
	if aside.waitingCount() != 1 {
		t.Errorf("the frame should still count it, got %d", aside.waitingCount())
	}
}

// The gutter says which of the two the sentence being typed is, because an
// answer and a steer reach the model differently. It says it in the tone: the
// draft draws one glyph, and what is waiting is said in words on the top rail.
func TestQuestion_TheGutterSaysTheDraftIsAnswering(t *testing.T) {
	m := escapedQuestion(t, chooseArgs)
	if got, want := m.promptGutter(), sty.Frame.NoticeInfo.Render(draftGutter)+" "; got != want {
		t.Errorf("the gutter should say the draft is answering with %q, got %q", want, got)
	}
	if got := stripANSI(m.promptGutter()); strings.TrimSpace(got) != draftGutter {
		t.Errorf("the draft keeps its own glyph, got %q", got)
	}
	// A bang line is a command before it is anything else, so it keeps its
	// own tone.
	m.input.SetValue("!git status")
	if got, want := m.promptGutter(), sty.Frame.GutterBang.Render(draftGutter)+" "; got != want {
		t.Errorf("a bang draft keeps its tone %q, got %q", want, got)
	}
}

// Nothing of the card is left on the screen, the queue strip above it
// included: a strip describing a decision that is not there would be the one
// thing the reader could not act on.
func TestQuestion_EscLeavesNothingOfTheCardOnTheScreen(t *testing.T) {
	updated, _ := questionModel(t, agent.ModeManual).Update(toolCallsMsg{calls: []provider.ToolCall{
		{ID: "call_q", Name: ask.ToolName, Arguments: chooseArgs},
		{ID: "call_w", Name: "write_file", Arguments: `{"path":"cache.go","content":"package cache"}`},
	}})
	m := updated.(Model)
	if len(m.questionLines()) == 0 {
		t.Fatal("the fixture should have a card with a queue strip above it")
	}
	if !strings.Contains(stripANSI(strings.Join(m.questionLines(), "\n")), "cache.go") {
		t.Fatal("the fixture should have the second call on the strip")
	}
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if lines := m.questionLines(); len(lines) != 0 {
		t.Errorf("the strip should have gone with the card: %q", lines)
	}
	if h := m.interruptHeight(); h != 0 {
		t.Errorf("the panel should pay nothing for a card that is not there, got %d rows", h)
	}
}

// The rail says the two ways back to the question and the one way past it.
func TestQuestion_TheRailOffersTheCardAgainAndTheQueue(t *testing.T) {
	m := escapedQuestion(t, chooseArgs)
	hints := stripANSI(m.frameHints(m.contentWidth()))
	for _, want := range []string{
		keys.Bracket(keys.Draft.Answer), keys.Bracket(keys.Draft.Send), keys.Bracket(keys.Draft.Queue),
	} {
		if !strings.Contains(hints, want) {
			t.Errorf("the rail does not offer %q: %q", want, hints)
		}
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
	if !m.questionAside() {
		t.Error("leaving the card hands the question to the draft")
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
	if !m.questionAside() {
		t.Error("leaving the card hands the question to the draft")
	}
}

// tabbedArgs is a call carrying three questions: a list, a yes-or-no and a
// free answer, so the strip is exercised over all three dressings.
const tabbedArgs = `{"questions":[
	{"question":"Which store should the cache use?","shape":"choose","options":[
		{"label":"Postgres","detail":"one more service to run","field":"3 files"},
		{"label":"SQLite","detail":"in the checkout already","recommended":true}]},
	{"question":"Should the migration be reversible?","shape":"confirm"},
	{"question":"What should the flag be called?","shape":"text"}]}`

// arrow is the strip's own key, which is the one movement the card answers
// that the list under it does not.
func arrow(back bool) tea.KeyPressMsg {
	if back {
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	}
	return tea.KeyPressMsg{Code: tea.KeyRight}
}

// answeredList is the list of answers a call that asked several got back, in
// the order the questions were sent.
func answeredList(t *testing.T, m Model) []map[string]any {
	t.Helper()
	for i := len(m.transcript) - 1; i >= 0; i-- {
		e := m.transcript[i]
		if e.kind != entryTool || e.toolName != ask.ToolName {
			continue
		}
		var got []map[string]any
		if err := json.Unmarshal([]byte(e.toolResult), &got); err != nil {
			t.Fatalf("the answers to a call that asked several are a list: %v (%q)", err, e.toolResult)
		}
		return got
	}
	t.Fatal("no answered question in the transcript")
	return nil
}

// Several questions in one call are one card with a tab per question and a
// submit at the end, and the strip says which are answered in a glyph and in
// words — both of which have to survive a terminal with one colour.
func TestQuestion_SeveralInOneCallAreTabs(t *testing.T) {
	m := openedQuestion(t, agent.ModeManual, tabbedArgs)
	if m.question.sheet == nil {
		t.Fatal("a call carrying three questions should draw a sheet of tabs")
	}
	if n := len(m.question.sheet.pages); n != 3 {
		t.Fatalf("pages = %d, want 3", n)
	}
	view := stripANSI(m.View().Content)
	for _, want := range []string{"▸ 1", "· 2", "· 3", "submit", "1 of 3", "3 unanswered"} {
		if !strings.Contains(view, want) {
			t.Errorf("the strip does not say %q:\n%s", want, view)
		}
	}
	// The first tab answered: the glyph turns and the words follow it, so
	// nothing about which tabs are done is carried by colour alone.
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	view = stripANSI(m.View().Content)
	for _, want := range []string{"✓ 1 answered", "▸ 2", "2 of 3", "2 unanswered"} {
		if !strings.Contains(view, want) {
			t.Errorf("after one answer the strip does not say %q:\n%s", want, view)
		}
	}
	if m.pendingApproval == nil {
		t.Fatal("answering one tab must not answer the call")
	}
}

// Answering steps to the next question still open, and the arrows walk the
// strip both ways and wrap onto the submit.
func TestQuestion_TheArrowsWalkTheStripAndAnsweringStepsIt(t *testing.T) {
	m := openedQuestion(t, agent.ModeManual, tabbedArgs)
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if at := m.question.sheet.at; at != 1 {
		t.Fatalf("answering the first tab should step to the second, at = %d", at)
	}
	m = sendKey(t, m, arrow(true))
	if at := m.question.sheet.at; at != 0 {
		t.Fatalf("the left arrow goes back a tab, at = %d", at)
	}
	// Forward off the last question is the submit, and once more wraps to
	// the first: the submit is one key back from the first question rather
	// than three keys forward from it.
	for i := 0; i < 3; i++ {
		m = sendKey(t, m, arrow(false))
	}
	if !m.question.submit {
		t.Fatalf("the tab past the last question is the submit, at = %d", m.question.sheet.at)
	}
	m = sendKey(t, m, arrow(false))
	if m.question.submit || m.question.sheet.at != 0 {
		t.Fatalf("the strip wraps, at = %d", m.question.sheet.at)
	}
}

// The answers go back in the order the questions were sent, each naming its
// own question, and a tab nobody answered goes back skipped rather than
// holding the reader at the card.
func TestQuestion_SubmitSendsEveryAnswerInSendOrder(t *testing.T) {
	m := openedQuestion(t, agent.ModeManual, tabbedArgs)
	// The first question's recommendation, then the yes-or-no, then the free
	// answer left alone.
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = sendKey(t, m, tea.KeyPressMsg{Code: 'y', Text: "y"})
	if m.question.submit || m.question.sheet.at != 2 {
		t.Fatalf("two answers should step to the question still open, at = %d", m.question.sheet.at)
	}
	// The last question is the free answer, stepped onto and left; the strip
	// says what leaving it costs before the submit is reached.
	view := stripANSI(m.View().Content)
	if !strings.Contains(view, "1 unanswered") {
		t.Errorf("the strip should say what is still open:\n%s", view)
	}
	m = sendKey(t, m, arrow(false))
	if !m.question.submit {
		t.Fatal("the tab after the last question is the submit")
	}
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	got := answeredList(t, m)
	if len(got) != 3 {
		t.Fatalf("answers = %d, want 3", len(got))
	}
	want := []string{
		"Which store should the cache use?",
		"Should the migration be reversible?",
		"What should the flag be called?",
	}
	for i, w := range want {
		if got[i]["ask"] != w {
			t.Errorf("answer %d names %v, want %q", i+1, got[i]["ask"], w)
		}
	}
	if picked, _ := got[0]["picked"].([]any); len(picked) != 1 || picked[0] != "SQLite" {
		t.Errorf("the first answer is the pick: %+v", got[0])
	}
	if picked, _ := got[1]["picked"].([]any); len(picked) != 1 || picked[0] != "yes" {
		t.Errorf("the second answer is the yes-or-no: %+v", got[1])
	}
	if got[2]["answered"] != string(ask.AnsweredSkipped) {
		t.Errorf("a tab nobody answered goes back skipped: %+v", got[2])
	}
	if m.pendingApproval != nil || m.question != nil {
		t.Error("the submit answers the whole call")
	}
}

// Esc is one press on a tabbed card: it closes the whole card, not a tab, and
// the sentence the reader sends next answers every question still open.
func TestQuestion_EscFromASecondTabClosesTheWholeCard(t *testing.T) {
	m := openedQuestion(t, agent.ModeManual, tabbedArgs)
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.question == nil || m.question.sheet.at != 1 {
		t.Fatal("the second tab should have the keyboard")
	}
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.question != nil {
		t.Fatal("esc closes the whole card rather than one tab of it")
	}
	if !m.questionAside() || m.pendingApproval == nil {
		t.Fatal("every unanswered question stays outstanding")
	}
	m = submitDraft(t, m, "use whatever is already in the checkout")
	got := answeredList(t, m)
	if len(got) != 3 {
		t.Fatalf("answers = %d, want 3", len(got))
	}
	if picked, _ := got[0]["picked"].([]any); len(picked) != 1 || picked[0] != "SQLite" {
		t.Errorf("the answer already given survives esc: %+v", got[0])
	}
	for _, i := range []int{1, 2} {
		if got[i]["answered"] != string(ask.AnsweredTyped) {
			t.Errorf("answer %d should be the reader's own words: %+v", i+1, got[i])
		}
		if got[i]["note"] != "use whatever is already in the checkout" {
			t.Errorf("answer %d does not carry the sentence: %+v", i+1, got[i])
		}
	}
	if users, _ := countRows(m); users != 0 {
		t.Errorf("the sentence answers the questions and is not also a message, user rows = %d", users)
	}
}

// The full view puts the marked row's long form on the screen the dry run
// already uses and gives the screen back with the question still waiting. It
// answers nothing.
func TestQuestion_TheFullViewReadsARowAndAnswersNothing(t *testing.T) {
	m := openedQuestion(t, agent.ModeManual, chooseArgs)
	m = sendKey(t, m, tea.KeyPressMsg{Code: 'd', Text: "d"})
	if m.state != stateOutputFull || m.fullOutput == nil {
		t.Fatalf("the full-view key should open the screen, state = %d", m.state)
	}
	view := stripANSI(m.View().Content)
	for _, want := range []string{"in the checkout already", "back to the question"} {
		if !strings.Contains(view, want) {
			t.Errorf("the screen does not carry %q:\n%s", want, view)
		}
	}
	if m.pendingApproval == nil {
		t.Fatal("reading a row must not answer the question")
	}
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.state != stateQuestion || m.question == nil {
		t.Fatalf("the screen gives itself back to the question, state = %d", m.state)
	}
	if m.pendingApproval == nil {
		t.Fatal("the question is still waiting")
	}
	// The row the model did not write has a long form of its own, because
	// what it means is the card's to say.
	m = sendKey(t, m, tea.KeyPressMsg{Code: '4', Text: "4"})
	if m.question == nil {
		t.Fatal("the something-else row opens the note rather than answering")
	}
}

// While the note holds the keyboard the card's own keys are text: an arrow
// moves the cursor and `d` is a `d`.
func TestQuestion_TheStripAndTheFullViewAreInertWhileTheNoteIsOpen(t *testing.T) {
	m := openedQuestion(t, agent.ModeManual, tabbedArgs)
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if !m.question.noteHolds() {
		t.Fatal("tab should put the keyboard in the note")
	}
	m = sendKey(t, m, tea.KeyPressMsg{Code: 'd', Text: "d"})
	if m.state == stateOutputFull {
		t.Error("the full-view key is a letter while the note has the keyboard")
	}
	m = sendKey(t, m, arrow(false))
	if m.question.sheet.at != 0 {
		t.Errorf("the strip does not move while the note has the keyboard, at = %d", m.question.sheet.at)
	}
	if got := m.question.sel.Note.Value(); got != "d" {
		t.Errorf("the letter should have been typed, note = %q", got)
	}
}

// A call past what a card can draw is refused before it reaches the screen,
// the way every other malformed call is.
func TestQuestion_AFifthQuestionIsRefusedBeforeTheCard(t *testing.T) {
	var call strings.Builder
	call.WriteString(`{"questions":[`)
	for i := 0; i <= ask.MaxQuestions; i++ {
		if i > 0 {
			call.WriteString(",")
		}
		fmt.Fprintf(&call, `{"question":"question %d","shape":"confirm"}`, i)
	}
	call.WriteString(`]}`)
	updated, _ := questionModel(t, agent.ModeManual).Update(askCall(call.String()))
	m := updated.(Model)
	if m.question != nil || m.state == stateQuestion {
		t.Fatal("a call past the limit draws no card")
	}
	var found string
	for _, e := range m.transcript {
		found += e.text + "\n" + e.toolResult + "\n"
	}
	for _, want := range []string{"too many questions", fmt.Sprintf("max %d", ask.MaxQuestions)} {
		if !strings.Contains(found, want) {
			t.Errorf("the refusal should name the limit (%q), got:\n%s", want, found)
		}
	}
}

// The card that was set down comes back as it was left: the answers already
// given are still on it and the tab the reader left is the tab they return to.
// Reopening answers nothing.
func TestQuestion_ReopeningATabbedCardKeepsWhatWasAnswered(t *testing.T) {
	m := openedQuestion(t, agent.ModeManual, tabbedArgs)
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if !m.questionAside() {
		t.Fatal("esc should set the whole card down with the call outstanding")
	}
	// The rail counts what the next sentence is about to answer, which is the
	// tabs still open rather than the one card holding them.
	if rail := stripANSI(m.noticeLine()); !strings.Contains(rail, "2 questions waiting") {
		t.Errorf("the rail should count the questions still open: %q", rail)
	}
	m.reopenQuestion()
	if m.question == nil || m.question.sheet == nil {
		t.Fatal("the handover should bring the card back")
	}
	if at := m.question.sheet.at; at != 1 {
		t.Errorf("the card comes back on the tab it was left on, at = %d", at)
	}
	if n := m.question.sheet.answered(); n != 1 {
		t.Errorf("the answer already given must survive, answered = %d", n)
	}
	if m.pendingApproval == nil {
		t.Error("reopening answers nothing")
	}
}

// A card whose tabs were all answered and set down before it was sent is
// still a call waiting: the sentence is not an answer to a question, so it
// goes beside the picks as the words the note field is for, and nothing is
// swallowed.
func TestQuestion_ASentenceOverAnAnsweredCardIsTheWordsBesideThePicks(t *testing.T) {
	m := openedQuestion(t, agent.ModeManual, tabbedArgs)
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = sendKey(t, m, tea.KeyPressMsg{Code: 'y', Text: "y"})
	// The free answer, opened with its own key and confirmed.
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	m.question.sel.Note.SetValue("cacheStore")
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.question.submit {
		t.Fatalf("three answers should stand on the submit, at = %d", m.question.sheet.at)
	}
	if rail := stripANSI(m.noticeLine()); rail != "" {
		// While the card is on the screen the rail says nothing; the count is
		// for a card behind the draft.
		t.Logf("rail while the card is up: %q", rail)
	}
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if rail := stripANSI(m.noticeLine()); !strings.Contains(rail, "1 question waiting") {
		t.Errorf("a call with nothing open is still a call waiting: %q", rail)
	}
	m = submitDraft(t, m, "whichever needs no new service")
	got := answeredList(t, m)
	if len(got) != 3 {
		t.Fatalf("answers = %d, want 3", len(got))
	}
	for _, i := range []int{0, 1} {
		if got[i]["answered"] != string(ask.AnsweredOnCard) {
			t.Errorf("answer %d should keep its pick: %+v", i+1, got[i])
		}
		if got[i]["note"] != "whichever needs no new service" {
			t.Errorf("answer %d should carry the sentence beside the pick: %+v", i+1, got[i])
		}
	}
	if got[2]["note"] != "cacheStore" {
		t.Errorf("an answer that carries its own words keeps them: %+v", got[2])
	}
}
