package chat

// The budget one turn has for questions: what it refuses, what it must not
// refuse, when it starts over, and what the reader is told instead of a card.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/ask"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// questionOf is one call, distinct from the last, so a run of them is a run
// of different questions and never the detector's repeat.
func questionOf(n int) string {
	return fmt.Sprintf(`{"question":"Fork number %d?","shape":"confirm"}`, n)
}

// askAndAnswer puts one question and answers it with the key given, which is
// enter for a yes and escape for a reader who left.
func askAndAnswer(t *testing.T, m Model, args string, key tea.KeyPressMsg) Model {
	t.Helper()
	updated, _ := m.Update(askCall(args))
	next := updated.(Model)
	if next.state != stateQuestion || next.question == nil {
		t.Fatalf("%s: the card should be drawn", args)
	}
	return sendKey(t, handover(t, next), key)
}

// The turn's allowance stands where the model spends it: the questions inside
// it are drawn, and the one past it is answered without ever reaching the
// reader — because a card drawn only to be answered by a rule would be the
// interruption charged for twice.
func TestQuestionBudget_OnePastTheCapDrawsNoCard(t *testing.T) {
	m := questionModel(t, agent.ModeManual)
	for i := 1; i <= ask.PerTurnBudget; i++ {
		m = askAndAnswer(t, m, questionOf(i), tea.KeyPressMsg{Code: tea.KeyEnter})
	}
	updated, _ := m.Update(askCall(questionOf(ask.PerTurnBudget + 1)))
	m = updated.(Model)
	if m.state == stateQuestion || m.question != nil {
		t.Fatal("a question past the budget must draw no card")
	}
	got := answeredResult(t, m)
	if got["answered"] != string(ask.AnsweredSkipped) {
		t.Errorf("answered = %v, want skipped", got["answered"])
	}
	if !strings.Contains(got["instruction"].(string), "state the assumption") {
		t.Errorf("instruction = %v", got["instruction"])
	}
	notice, _ := got["notice"].(string)
	if !strings.Contains(notice, "budget") {
		t.Errorf("the run should be told which skip this is, got %q", notice)
	}
}

// The budget is spent by the asking and not by the answering: the count moves
// when the card is drawn, and a question the reader set down without
// answering has already cost them the interruption. A refund would make
// leaving a question cost more than answering it.
func TestQuestionBudget_TheAskingSpendsItAndNotTheAnswering(t *testing.T) {
	m := questionModel(t, agent.ModeManual)
	updated, _ := m.Update(askCall(questionOf(1)))
	m = updated.(Model)
	if m.questionsAsked != 1 {
		t.Fatalf("the card is drawn and unanswered, and the count is %d", m.questionsAsked)
	}
	m = sendKey(t, handover(t, m), tea.KeyPressMsg{Code: tea.KeyEscape})
	if !m.questionAside() {
		t.Fatal("esc should set the question aside for the draft to answer")
	}
	if m.questionsAsked != 1 {
		t.Errorf("a question set down is still a question asked, got %d", m.questionsAsked)
	}
}

// The row says what answered a question nobody saw. `answered` would name a
// decision the reader never made, and they are owed the reason their session
// stopped bringing them questions.
func TestQuestionBudget_TheRowNamesTheRuleAndNotADecision(t *testing.T) {
	m := questionModel(t, agent.ModeManual)
	m.questionsAsked = ask.PerTurnBudget
	updated, _ := m.Update(askCall(questionOf(1)))
	m = updated.(Model)

	var e entry
	for _, ent := range m.transcript {
		if ent.kind == entryTool && ent.toolName == ask.ToolName {
			e = ent
		}
	}
	row := m.activityRowFor(e)
	want := components.OutcomeBy(components.OutcomeSkipped, overBudgetRule)
	if row.Outcome != want {
		t.Errorf("outcome = %q, want %q", row.Outcome, want)
	}
	if strings.Contains(stripANSI(m.renderEntry(e, 100)), "▎") {
		t.Error("a question changed nothing, so its row carries no rail")
	}
}

// The count is the turn's and starts over where a turn opens. A turn ending
// is not that moment: a question outstanding when the next instruction
// arrives was asked by the turn that asked it.
func TestQuestionBudget_TheNextTurnOpeningStartsItOver(t *testing.T) {
	m := questionModel(t, agent.ModeManual)
	m = askAndAnswer(t, m, questionOf(1), tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.questionsAsked != 1 {
		t.Fatalf("questionsAsked = %d after one question", m.questionsAsked)
	}
	m.setTurnState(stateInput)
	if m.questionsAsked != 1 {
		t.Errorf("a turn closing does not refund its questions, got %d", m.questionsAsked)
	}
	m.nextTurn()
	if m.questionsAsked != 0 {
		t.Errorf("a turn opening starts the budget over, got %d", m.questionsAsked)
	}
}

// The detector's half is separate from the budget's: a model that rephrases
// nothing and asks the same thing twice is told it already has the answer,
// and the sentence rides in the answer rather than in front of it, because
// the answer is what this result is read for.
func TestQuestionBudget_TheSameQuestionTwiceIsToldSo(t *testing.T) {
	m := questionModel(t, agent.ModeManual).WithRepeats(agent.NewRepeatDetector())
	same := `{"question":"Should the migration be reversible?","shape":"confirm"}`

	m = askAndAnswer(t, m, same, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := answeredResult(t, m); got["notice"] != nil {
		t.Fatalf("the first asking is not a repeat: %v", got["notice"])
	}
	m = askAndAnswer(t, m, same, tea.KeyPressMsg{Code: 'n', Text: "n"})
	got := answeredResult(t, m)
	notice, _ := got["notice"].(string)
	if !strings.Contains(notice, "answer you have") {
		t.Errorf("the second asking should say the answer is already in hand, got %q", notice)
	}
	// The answer is still the answer, and still where the model reads it: a
	// notice that led this result would be a line to skip past first.
	if got["answered"] != string(ask.AnsweredOnCard) {
		t.Errorf("answered = %v", got["answered"])
	}
	for _, e := range m.transcript {
		if e.kind == entryTool && e.toolName == ask.ToolName {
			var into map[string]any
			if err := json.Unmarshal([]byte(e.toolResult), &into); err != nil {
				t.Fatalf("the result must stay the JSON the answer is read out of: %v (%q)", err, e.toolResult)
			}
		}
	}
}
