//go:build contract

package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/ask"
	"github.com/rfizzle/shhh/internal/rpc"
)

// End to end, which is the reading that cannot be arranged: what the model
// was actually handed on a run behind --print, on both surfaces.
func TestPrintRun_IsNeverOfferedTheQuestionTool(t *testing.T) {
	for _, surface := range []string{"code", "chat"} {
		t.Run(surface, func(t *testing.T) {
			f := startFakeProvider(t, reply{text: "the answer"})
			s := newPrintSession(t, f)
			_, errs, code := s.run(t, "", surface, "-p", "say hi")
			if code != 0 {
				t.Fatalf("the run exited %d\nstderr: %s", code, errs)
			}
			if f.toolsOffered(t)[ask.ToolName] {
				t.Errorf("a run with nobody in front of it was offered %s", ask.ToolName)
			}
		})
	}
}

// And a served session started --mode auto, which is a run that has said
// there is no client to ask: the classifier answers its gated calls, and
// there is nothing for it to answer about a question.
func TestServe_AutoModeIsNeverOfferedTheQuestionTool(t *testing.T) {
	f := startFakeProvider(t, reply{text: "done"})
	s := newPrintSession(t, f)
	c := serveOverStdio(t, s, "--mode", "auto")

	var opened rpc.SessionResult
	c.mustCall(rpc.MethodSessionStart, rpc.StartParams{}, &opened)
	var turn rpc.TurnResult
	c.mustCall(rpc.MethodTurnStart, rpc.TurnParams{Session: opened.Session, Prompt: "do it"}, &turn)
	c.drainToClose()

	if f.toolsOffered(t)[ask.ToolName] {
		t.Errorf("a served session in auto mode was offered %s", ask.ToolName)
	}
}

// A served session with a client is the other half of the same decision:
// there is somebody to answer, so the tool is registered and the question
// crosses the protocol as a question rather than as a call to approve. What
// comes back is the reader's own answer, in the words the model is told it in.
func TestServe_AQuestionReachesTheClientAndItsAnswerReachesTheModel(t *testing.T) {
	f := startFakeProvider(t,
		reply{tool: ask.ToolName, args: map[string]string{
			"question": "Should I delete the old path?",
			"shape":    string(ask.ShapeConfirm),
			"note":     string(ask.NoteRequired),
		}},
		reply{text: "kept it"})
	s := newPrintSession(t, f)
	c := serveOverStdio(t, s)

	var opened rpc.SessionResult
	c.mustCall(rpc.MethodSessionStart, rpc.StartParams{}, &opened)
	var turn rpc.TurnResult
	c.mustCall(rpc.MethodTurnStart, rpc.TurnParams{Session: opened.Session, Prompt: "tidy up"}, &turn)

	put := c.waitQuestion()
	if put.Question != "Should I delete the old path?" || put.Shape != ask.ShapeConfirm {
		t.Fatalf("the question did not cross as one: %+v", put)
	}
	if put.Note != ask.NoteRequired {
		t.Errorf("the model asked for a note and the client was not told: %+v", put)
	}
	c.mustCall(rpc.MethodQuestionAnswer, rpc.QuestionAnswerParams{
		Session: opened.Session, ID: put.ID, Answered: ask.AnsweredOnCard,
		Picked: []string{"no"}, Note: "keep it for a release"}, nil)
	events := c.drainToClose()

	if !f.toolsOffered(t)[ask.ToolName] {
		t.Fatalf("a served session with a client was not offered %s", ask.ToolName)
	}
	// The answer is the result of the call, so it is on the stream where
	// every other tool result is and in the conversation the next round reads.
	var result string
	for _, ev := range events {
		if ev.Tool == ask.ToolName && ev.Result != "" {
			result = ev.Result
		}
	}
	if result == "" {
		t.Fatalf("the question was answered and the run recorded nothing: %v", kindsOf(events))
	}
	for _, want := range []string{string(ask.AnsweredOnCard), "keep it for a release", `"no"`} {
		if !strings.Contains(result, want) {
			t.Errorf("the model was not told %q: %s", want, result)
		}
	}
}

// The turn's allowance of questions is the same allowance on the wire as on
// the screen: a client is a person too, and two front-ends of one agent have
// to spend one budget alike.
func TestServe_ATurnsQuestionsAreBoundedForAClientToo(t *testing.T) {
	// Two past the budget rather than one: a request the run makes for
	// something other than a round spends a step of this script, and the case
	// needs at least one question left over after the allowance is gone.
	replies := make([]reply, 0, ask.PerTurnBudget+3)
	for i := 1; i <= ask.PerTurnBudget+2; i++ {
		replies = append(replies, reply{tool: ask.ToolName, args: map[string]string{
			"question": fmt.Sprintf("Fork number %d?", i),
			"shape":    string(ask.ShapeConfirm),
		}})
	}
	replies = append(replies, reply{text: "carried on"})
	f := startFakeProvider(t, replies...)
	s := newPrintSession(t, f)
	c := serveOverStdio(t, s)

	var opened rpc.SessionResult
	c.mustCall(rpc.MethodSessionStart, rpc.StartParams{}, &opened)
	var turn rpc.TurnResult
	c.mustCall(rpc.MethodTurnStart, rpc.TurnParams{Session: opened.Session, Prompt: "tidy up"}, &turn)

	for i := 1; i <= ask.PerTurnBudget; i++ {
		put := c.waitQuestion()
		c.mustCall(rpc.MethodQuestionAnswer, rpc.QuestionAnswerParams{
			Session: opened.Session, ID: put.ID, Answered: ask.AnsweredOnCard, Picked: []string{"yes"}}, nil)
	}
	events := c.drainToClose()

	// The one past the budget never crossed: it was answered where it was
	// asked, so nothing reached the client to be answered.
	select {
	case put := <-c.questions:
		t.Fatalf("a question past the budget reached the client: %q", put.Question)
	default:
	}
	onCard, budgeted := 0, 0
	for _, ev := range events {
		if ev.Tool != ask.ToolName || ev.Result == "" {
			continue
		}
		switch {
		case strings.Contains(ev.Result, string(ask.AnsweredOnCard)):
			onCard++
		case strings.Contains(ev.Result, "budget"):
			budgeted++
			for _, want := range []string{string(ask.AnsweredSkipped), "state the assumption"} {
				if !strings.Contains(ev.Result, want) {
					t.Errorf("the run was not told %q: %s", want, ev.Result)
				}
			}
		default:
			t.Errorf("a question was answered by nothing this story knows: %s", ev.Result)
		}
	}
	if onCard != ask.PerTurnBudget {
		t.Errorf("the client answered %d questions and the budget is %d: %v",
			onCard, ask.PerTurnBudget, kindsOf(events))
	}
	if budgeted == 0 {
		t.Errorf("no question ran into the budget: %v", kindsOf(events))
	}
}

// A call carrying several questions crosses the protocol one question at a
// time, in the order they were sent, and the answers reach the model as a
// list in that order with each naming its own question. The window on the
// wire answers one question at a time
// (docs/capabilities/headless.md#a-client-answers-one-call-at-a-time), so the
// card's tab strip and this loop are two front-ends of the same promise.
func TestServe_SeveralQuestionsInOneCallCrossOneAtATime(t *testing.T) {
	f := startFakeProvider(t,
		reply{tool: ask.ToolName, rawArgs: `{"questions":[
			{"question":"Which store?","shape":"confirm"},
			{"question":"Reversible?","shape":"confirm"},
			{"question":"Call it what?","shape":"text"}]}`},
		reply{text: "carried on"})
	s := newPrintSession(t, f)
	c := serveOverStdio(t, s)

	var opened rpc.SessionResult
	c.mustCall(rpc.MethodSessionStart, rpc.StartParams{}, &opened)
	var turn rpc.TurnResult
	c.mustCall(rpc.MethodTurnStart, rpc.TurnParams{Session: opened.Session, Prompt: "tidy up"}, &turn)

	asked := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		put := c.waitQuestion()
		asked = append(asked, put.Question)
		answer := rpc.QuestionAnswerParams{
			Session: opened.Session, ID: put.ID,
			Answered: ask.AnsweredOnCard, Picked: []string{"yes"},
		}
		if i == 2 {
			// The last one in the reader's own words, so the list is not
			// three of one thing.
			answer.Answered, answer.Picked, answer.Note = ask.AnsweredTyped, nil, "cacheStore"
		}
		c.mustCall(rpc.MethodQuestionAnswer, answer, nil)
	}
	events := c.drainToClose()

	want := []string{"Which store?", "Reversible?", "Call it what?"}
	for i, w := range want {
		if asked[i] != w {
			t.Errorf("question %d crossed as %q, want %q", i+1, asked[i], w)
		}
	}
	var result string
	for _, ev := range events {
		if ev.Tool == ask.ToolName && ev.Result != "" {
			result = ev.Result
		}
	}
	var got []struct {
		Ask      string `json:"ask"`
		Answered string `json:"answered"`
		Note     string `json:"note"`
	}
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatalf("the answers to a call that asked three are a list: %v (%q)", err, result)
	}
	if len(got) != 3 {
		t.Fatalf("answers = %d, want 3: %s", len(got), result)
	}
	for i, w := range want {
		if got[i].Ask != w {
			t.Errorf("answer %d names %q, want %q", i+1, got[i].Ask, w)
		}
	}
	if got[2].Answered != string(ask.AnsweredTyped) || got[2].Note != "cacheStore" {
		t.Errorf("the last answer is the reader's own words: %+v", got[2])
	}
	// One call, one interruption: three questions on one card spend one of
	// the turn's allowance rather than three of it.
	if !strings.Contains(result, string(ask.AnsweredOnCard)) {
		t.Errorf("the picks did not reach the model: %s", result)
	}
}
