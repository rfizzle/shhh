package subagent

import (
	"testing"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/tools"
)

// A child's delete of the home directory is refused by the rule the session
// refuses it with: in auto mode, under the parent's blanket grant, with a
// person there to route a card to, it never reaches the classifier or the
// card and never runs.
func TestChildIrreplaceableTargetIsRefusedByRule(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	env := &scriptedEnv{
		steps: gatedCommandSteps("rm -rf ~"),
		gated: map[string]bool{tools.ExecCommandName: true},
	}
	rec := &testRecorder{}
	judge := &verdictProvider{decision: "allow", reason: "cleaning up"}
	sup := New(t.Context(), Options{
		Root:       t.TempDir(),
		NewEnv:     env.factory(),
		Classifier: agent.NewClassifier(judge, agent.ClassifierConfig{Model: "judge"}),
		Record:     func(Spec, string) Recorder { return rec.recorder() },
	})
	t.Cleanup(sup.Close)
	sup.SetParentMode(agent.ModeAuto)
	sup.SetParentGrants(agent.Grants{AllCommands: true})
	sup.SetAttended()
	execTool(t, sup, SpawnToolName, `{"role":"researcher","task":"tidy up"}`)
	execTool(t, sup, ReportToolName, `{"name":"researcher-1"}`)

	if env.ranCommand.Load() {
		t.Fatal("a delete of the home directory ran")
	}
	if judge.calls.Load() != 0 {
		t.Fatal("the rule answers before the classifier is paid to think")
	}
	decisions := rec.of("decision")
	if len(decisions) != 1 || decisions[0].outcome != observe.DecisionDeny || decisions[0].reason != observe.ReasonSafety {
		t.Fatalf("want one rule refusal filed with the safety table's, got %+v", decisions)
	}
}
