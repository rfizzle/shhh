package cli

// Where the question tool is registered, and — the part that matters — where
// it is not. A run with nobody in front of it never sees the tool, is never
// offered it and is never refused it: that is a registration decision rather
// than a gate decision, because a tool the run can only be refused is worse
// than one it never saw
// (docs/capabilities/coding-agent.md#nobody-to-ask).

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/ask"
	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/rpc"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
)

// The gate itself: the tool joins the toolset for a session that says there
// is somebody to ask, and for no other.
func TestAskToolIsRegisteredOnlyWhereSomebodyCanBeAsked(t *testing.T) {
	base := codeToolset()
	if names := toolsetNames(askToolDefs(base)); containsString(names, ask.ToolName) {
		t.Errorf("a session with nobody to ask was offered %s: %v", ask.ToolName, names)
	}
	asked := codeToolset()
	asked.ask = true
	if !containsString(toolsetNames(askToolDefs(asked)), ask.ToolName) {
		t.Errorf("a session with somebody to ask was not offered %s", ask.ToolName)
	}
	// And the registration is a copy: a session that grew the tool must not
	// have grown it on the slice every other session shares.
	if containsString(toolsetNames(base.toolDefs), ask.ToolName) {
		t.Error("registering the tool wrote through to the caller's own toolset")
	}
}

// A child never asks. A fan-out exists for work that does not need the
// reader, and a child that could stop it for a question would be a fan-out
// that needs watching — so the parent's own question tool does not travel
// down with everything else a child inherits.
func TestAskToolNeverReachesAChild(t *testing.T) {
	session := codeToolset()
	session.ask = true
	for _, role := range []subagent.Role{subagent.RoleResearcher, subagent.RoleReviewer, subagent.RoleWriter} {
		defs := tools.Definitions()
		if role == subagent.RoleWriter {
			defs = tools.DefinitionsFull()
		}
		defs, _, sysPrompt, _ := withSessionTools(session, nil, "child-1", t.TempDir(), defs,
			tools.Execute, "you are a child")
		if names := toolsetNames(defs); containsString(names, ask.ToolName) {
			t.Errorf("%s was offered %s: %v", role, ask.ToolName, names)
		}
		if strings.Contains(sysPrompt, ask.ToolName+" ") {
			t.Errorf("%s was told it has the question tool", role)
		}
	}
	// The profile roles a person writes take the same path, so the same
	// answer covers them: their definitions come from the profile and the
	// session's own shared tools, never from the session's toolset.
	def := config.AgentDefinition{Name: "reader", Permissions: []string{config.PermissionWeb}}
	_, defs, _ := profileEnv(def, subagent.Spec{}, shell.Info{}, "", session.web, map[string]bool{})
	if containsString(toolsetNames(defs), ask.ToolName) {
		t.Errorf("a profile role was offered %s", ask.ToolName)
	}
}

// The shared registration never adds it either, on any surface: the one place
// it joins a toolset is the gate above.
func TestAskToolIsNotPartOfTheSharedRegistration(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	sc, err := sessionScope(config.Config{}, nil)
	if err != nil {
		t.Fatalf("session scope: %v", err)
	}
	for _, surface := range []string{"code", "print", "serve"} {
		session := codeToolset()
		ts, err := buildToolset(toolsetCmd(t), &session, surface, toolsetOpts{scope: sc})
		if err != nil {
			t.Fatalf("%s registration: %v", surface, err)
		}
		if names := toolsetNames(session.toolDefs); containsString(names, ask.ToolName) {
			t.Errorf("%s was offered %s by the shared registration: %v", surface, ask.ToolName, names)
		}
		if strings.Contains(prompt.Toolbox(session.toolDefs), ask.ToolName) {
			t.Errorf("%s was told it has the question tool", surface)
		}
		ts.close()
	}
}

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
