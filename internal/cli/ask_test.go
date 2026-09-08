package cli

// Where the question tool is registered, and — the part that matters — where
// it is not. A run with nobody in front of it never sees the tool, is never
// offered it and is never refused it: that is a registration decision rather
// than a gate decision, because a tool the run can only be refused is worse
// than one it never saw
// (docs/capabilities/coding-agent.md#nobody-to-ask).

import (
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
