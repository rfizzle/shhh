package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/rfizzle/llm"
	"github.com/rfizzle/shhh/internal/meter"
	"github.com/rfizzle/shhh/internal/permission"
	"github.com/rfizzle/shhh/internal/spend"
)

// A turn the person starts while a run fans out answers under the person's
// mode: a skill typed while the writers build is the person's, not the
// run's, and the model is told the mode its calls are answered under. The
// children's ceiling keeps the run's policy, so the writers are not clamped
// to manual mid-build.
func TestPersonsTurn_ASkillDuringTheFanOutAnswersUnderTheSessionMode(t *testing.T) {
	m, sup := largeRunModel(t, writingEnv("package a\n\nvar changed = true\n"))
	m.policy.mode = permission.ModeManual
	m = settleFlow(m)
	if m.personsTurn() || m.turnMode() != permission.ModeAuto {
		t.Fatalf("between turns the run's policy stands: person's turn %v, mode %s", m.personsTurn(), m.turnMode())
	}

	next, _ := m.sendUserMessageAs("skill body", "/skill documentation")
	m = settleFlow(next.(Model))

	if _, _, ok := m.flowPolicy(); !ok || m.gateMode() != permission.ModeAuto {
		t.Fatalf("the run no longer holds auto: gate %s", m.gateMode())
	}
	if !m.personsTurn() || m.turnMode() != permission.ModeManual {
		t.Fatalf("the skill turn is the person's and answers under manual: person's turn %v, mode %s", m.personsTurn(), m.turnMode())
	}
	if d, why := editVerdict(m); d != permission.Ask {
		t.Fatalf("an in-scope edit on the skill turn is %v (%q), want Ask", d, why)
	}
	tools := m.toolNames()
	if got, want := permission.ModeInstructions(m.turnMode(), m.policy.readOnlyExtra, tools), permission.ModeInstructions(permission.ModeManual, nil, tools); got != want {
		t.Fatal("the model is told a mode other than the one its calls are answered under")
	}
	if got := sup.ParentMode(); got != permission.ModeAuto {
		t.Fatalf("the children's ceiling is %s, want auto: a skill turn must not clamp the writers", got)
	}
	if mode, _ := sup.AgentMode("tw1-alpha"); mode != permission.ModeAuto {
		t.Fatalf("a lane writer is under %s, want auto", mode)
	}
}

// The run's own stage turn is not a person's turn: it is answered, and the
// model told, under the stage's policy whatever the session's mode is.
func TestPersonsTurn_ARunsOwnStageTurnKeepsTheRunsPolicy(t *testing.T) {
	for _, session := range []permission.Mode{permission.ModeManual, permission.ModeAuto} {
		t.Run(session.String(), func(t *testing.T) {
			m, _ := runModel(t)
			m.policy.mode = session
			m = submitFlow(t, m, "/todo run do-it")
			if !m.working() || m.personsTurn() {
				t.Fatalf("the research turn is the run's: working %v, person's %v", m.working(), m.personsTurn())
			}
			if m.turnMode() != permission.ModePlan {
				t.Fatalf("research answers under %s, want plan", m.turnMode())
			}
			if permission.ModeInstructions(m.turnMode(), m.policy.readOnlyExtra, m.toolNames()) == "" {
				t.Fatal("the research turn is not told it is in plan mode")
			}
			m = answer(t, m, runPlan)
			if !m.working() || m.personsTurn() || m.turnMode() != permission.ModeAuto {
				t.Fatalf("the implement turn is the run's and auto: person's %v, mode %s", m.personsTurn(), m.turnMode())
			}
		})
	}
}

// The classifier is auto mode's, and a person's turn in a manual session is
// not answered under auto: a call the static policy asks about on the skill
// turn is asked of the person, not judged by the classifier and run unasked.
func TestPersonsTurn_TheClassifierNeverJudgesTheSkillTurn(t *testing.T) {
	m, _ := largeRunModel(t, writingEnv("package a\n\nvar changed = true\n"))
	m.policy.mode = permission.ModeManual
	m.wiring.Runner = legacyRunner(func(context.Context, string) (string, int) { return "ok", 0 })
	p := &verdictProvider{decision: "allow", reason: "fine"}
	m.classifier.judge = permission.NewClassifier(meter.New(nil).For(p, spend.SourceClassifier), permission.ClassifierConfig{Model: "judge"})
	m = settleFlow(m)

	next, _ := m.sendUserMessageAs("skill body", "/skill documentation")
	m = next.(Model)
	updated, _ := m.Update(toolCallsMsg{calls: []llm.ToolCall{
		{ID: "call_x", Name: "execute_command", Arguments: `{"command":"go test ./..."}`},
	}})
	m = updated.(Model)
	if m.state == stateClassifying || p.calls != 0 {
		t.Fatalf("the classifier judged a call on the person's manual turn (state %d, judged %d)", m.state, p.calls)
	}
	if m.state != stateConfirmRun {
		t.Fatalf("the call should ask the person, got state %d", m.state)
	}
}

// A steer joins the stage turn it steers; it does not make the rest of a
// reading stage the person's, which would widen it to the session's mode.
func TestPersonsTurn_ASteerDuringAPlanStageLeavesItPlan(t *testing.T) {
	m, _ := runModel(t)
	m.policy.mode = permission.ModeAuto
	m = submitFlow(t, m, "/todo run do-it")
	if !m.working() || m.turnMode() != permission.ModePlan {
		t.Fatalf("the research turn should be in flight under plan, got %s", m.turnMode())
	}
	m.turnCount++ // what a steer does (stream.go), without opening a turn
	if m.personsTurn() || m.turnMode() != permission.ModePlan {
		t.Fatalf("a steer widened the stage: person's turn %v, mode %s", m.personsTurn(), m.turnMode())
	}
}

// A line from elsewhere is held while the session is auto, even when the run
// is at a plan step: the turn it would open answers under the session's mode
// with nobody there.
func TestPersonsTurn_AnAutoSessionHoldsAnInboundLineAtAPlanStep(t *testing.T) {
	m, _ := runModel(t)
	m.policy.mode = permission.ModeAuto
	m = submitFlow(t, m, "/todo run do-it")
	m = answer(t, m, strings.Replace(runPlan, "size: S", "size: L", 1))
	if m.todo.runner.pause == nil || m.gateMode() != permission.ModePlan {
		t.Fatalf("the run should wait at the gate under plan: pause %v, gate %s", m.todo.runner.pause, m.gateMode())
	}
	if got := m.inboundPolicy(); got != InboundHold {
		t.Fatalf("inbound policy %q, want hold", got)
	}
}
