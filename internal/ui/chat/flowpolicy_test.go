package chat

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/rfizzle/shhh/internal/permission"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
)

// flowTick is a message nothing handles, sent so that the Update tail — the
// one place every transition is read — runs once after a step a test drove
// by calling a handler directly.
type flowTick struct{}

func settleFlow(m Model) Model {
	next, _ := m.Update(flowTick{})
	return next.(Model)
}

func submitFlow(t *testing.T, m Model, line string) Model {
	t.Helper()
	m.input.SetValue(line)
	next, _ := m.submitInput()
	return settleFlow(next.(Model))
}

// editVerdict is the gate's answer to one in-scope edit, which is the call
// whose answer differs across every pair of modes the flows move between.
func editVerdict(m Model) (permission.Decision, string) {
	return m.modePolicy().Decide(permission.Action{Kind: permission.ActionEdit, Path: filepath.Join(m.todo.wiring.Root, "a.go")})
}

// withSupervisor gives a model a supervisor with no way to spawn, so the
// children's ceiling it is pushed can be read back.
func withSupervisor(t *testing.T, m Model) Model {
	t.Helper()
	sup := subagent.New(context.Background(), subagent.Options{Root: m.todo.wiring.Root})
	t.Cleanup(sup.Close)
	m.wiring.Subagents = sup
	m.adoptChildren()
	return m
}

type flowEnd struct {
	name string
	end  func(t *testing.T, m Model) Model
}

var (
	// /todo stop is taken between turns, so a stage turn in flight is
	// answered first, up to the verify step that runs no turn.
	endStop = flowEnd{"/todo stop", func(t *testing.T, m Model) Model {
		for _, reply := range []string{runPlan, "Changed a.go."} {
			if !m.working() {
				break
			}
			m = answer(t, m, reply)
		}
		return submitFlow(t, m, "/todo stop")
	}}
	endCancel = flowEnd{"the cancel chord", func(t *testing.T, m Model) Model {
		m.streaming = "half an answer"
		updated, _ := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
		m = updated.(Model)
		updated, _ = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
		return updated.(Model)
	}}
	endBoundary = flowEnd{"the session boundary", func(t *testing.T, m Model) Model {
		m.startNewSession()
		return settleFlow(m)
	}}
	// A turn that ends under another number is another message's, and the
	// flow lets go of it: a run keeps its checkpoint, a reading stops.
	endDisplaced = flowEnd{"a displaced turn", func(t *testing.T, m Model) Model {
		m.turnCount++
		return answer(t, m, "an answer to something else")
	}}
	endBlocked = flowEnd{"a block", func(t *testing.T, m Model) Model {
		return answer(t, m, "## Plan: x\n\n1. a\n\nsize: S\nquestions:\n- keep the flag?\n")
	}}
)

// A flow runs under its own policy and leaves the person's mode exactly as
// they set it, whichever way it ends: the session's mode before, during and
// after is the one set, the gate answers under the flow's policy while the
// flow holds and under the session's mode the moment it does not, and the
// children's ceiling follows the gate.
func TestFlowPolicy_TheSessionModeIsNotRewritten(t *testing.T) {
	runStart := func(t *testing.T, session permission.Mode) Model {
		m, _ := runModel(t)
		m.policy.mode = session
		m = withSupervisor(t, m)
		return submitFlow(t, m, "/todo run do-it")
	}
	rows := []struct {
		name   string
		policy permission.Mode
		start  func(t *testing.T, session permission.Mode) Model
		ends   []flowEnd
	}{{
		name: "groom", policy: permission.ModePlan,
		start: func(t *testing.T, session permission.Mode) Model {
			m, _ := groomModel(t, map[string]string{"cache-ttl.md": groomItem})
			m.policy.mode = session
			return submitFlow(t, m, "/todo groom cache-ttl")
		},
		ends: []flowEnd{
			{"the card answered with esc", func(t *testing.T, m Model) Model {
				return press(t, answer(t, m, groomAnswer), "esc")
			}},
			endBoundary, endDisplaced,
		},
	}, {
		name: "sprint plan", policy: permission.ModePlan,
		start: func(t *testing.T, session permission.Mode) Model {
			m, root := sprintModel(t, "")
			for _, slug := range []string{"a-high", "b-second", "c-third", "d-fourth"} {
				must(t, todo.SaveReading(root, todo.Reading{Slug: slug}))
			}
			m.policy.mode = session
			return submitFlow(t, m, "/todo sprint plan")
		},
		ends: []flowEnd{
			{"the proposal", func(t *testing.T, m Model) Model { return answer(t, m, planAnswerForFixture) }},
			endBoundary, endDisplaced,
		},
	}, {
		name: "run research", policy: permission.ModePlan, start: runStart,
		ends: []flowEnd{endStop, endCancel, endBoundary, endBlocked, endDisplaced},
	}, {
		name: "run implement", policy: permission.ModeAuto,
		start: func(t *testing.T, session permission.Mode) Model {
			return answer(t, runStart(t, session), runPlan)
		},
		ends: []flowEnd{endStop, endCancel, endBoundary, {"the commit", func(t *testing.T, m Model) Model {
			m = answer(t, m, "Changed a.go.")
			updated, _ := m.Update(todoVerifyMsg{slug: "do-it", ok: true, output: "$ true → exit 0"})
			m = answer(t, updated.(Model), "verdict: clean")
			m = answer(t, m, "COMMIT:\nDo it\n\nBody.\nREPORT:\n## Report\nSummary: did it")
			updated, _ = m.Update(todoCommitMsg{slug: "do-it", files: []string{"a.go"}})
			return updated.(Model)
		}}},
	}, {
		name: "fan-out", policy: permission.ModeAuto,
		start: func(t *testing.T, session permission.Mode) Model {
			m, sup := largeRunModel(t, writingEnv("package a\n\nvar changed = true\n"))
			// The fixture starts the run in a manual session; the writers
			// are spawned under the run's ceiling whatever the session is.
			if mode, _ := sup.AgentMode("tw1-alpha"); mode != permission.ModeAuto {
				t.Fatalf("a writer was spawned under %s", mode)
			}
			m.policy.mode = session
			return settleFlow(m)
		},
		ends: []flowEnd{endStop},
	}, {
		name: "--all", policy: permission.ModePlan,
		start: func(t *testing.T, session permission.Mode) Model {
			m, _ := sprintRunModel(t)
			m.policy.mode = session
			m = withSupervisor(t, m)
			return submitFlow(t, m, "/todo run --all")
		},
		ends: []flowEnd{endStop, endBlocked, endBoundary},
	}}
	for _, row := range rows {
		for _, session := range []permission.Mode{permission.ModeManual, permission.ModeAuto} {
			for _, end := range row.ends {
				t.Run(row.name+"/"+session.String()+"/"+end.name, func(t *testing.T) {
					m := row.start(t, session)
					if _, _, ok := m.flowPolicy(); !ok {
						t.Fatal("the flow is not going")
					}
					if m.policy.mode != session {
						t.Fatalf("during: the session's mode was rewritten to %s", m.policy.mode)
					}
					if m.gateMode() != row.policy {
						t.Fatalf("during: the gate answers under %s, want %s", m.gateMode(), row.policy)
					}
					want := map[permission.Mode]permission.Decision{permission.ModePlan: permission.Deny, permission.ModeAuto: permission.Allow}[row.policy]
					if d, why := editVerdict(m); d != want || why != row.policy.String()+" mode" {
						t.Fatalf("during: an edit is %v (%q) under %s", d, why, row.policy)
					}
					if sup := m.wiring.Subagents; sup != nil && sup.ParentMode() != row.policy {
						t.Fatalf("during: the children's ceiling is %s, want %s", sup.ParentMode(), row.policy)
					}

					m = end.end(t, m)

					if _, flow, ok := m.flowPolicy(); ok {
						t.Fatalf("after %s: %s still holds a policy; %q", end.name, flow, lastSystem(t, m))
					}
					if m.policy.mode != session || m.gateMode() != session {
						t.Fatalf("after %s: mode %s, gate %s, want %s", end.name, m.policy.mode, m.gateMode(), session)
					}
					d, _ := editVerdict(m)
					if after := map[permission.Mode]permission.Decision{permission.ModeManual: permission.Ask, permission.ModeAuto: permission.Allow}[session]; d != after {
						t.Fatalf("after %s: an edit is %v, want %v", end.name, d, after)
					}
					if sup := m.wiring.Subagents; sup != nil && sup.ParentMode() != session {
						t.Fatalf("after %s: the children's ceiling is %s, want %s", end.name, sup.ParentMode(), session)
					}
				})
			}
		}
	}
}

// A session the person bounded to read-only is not let start a run whose
// steps write, alone or as a set, and nothing is written for the refusal;
// a reading, which writes nothing, still starts there.
func TestFlowPolicy_AReadOnlySessionRefusesAWritingRun(t *testing.T) {
	for _, session := range []permission.Mode{permission.ModeReadOnly, permission.ModePlan} {
		for _, line := range []string{"/todo run do-it", "/todo run --all"} {
			t.Run(session.String()+"/"+line, func(t *testing.T) {
				m, root := sprintRunModel(t)
				m.policy.mode = session
				m = submitFlow(t, m, line)
				if m.todo.runner.state != nil || m.working() {
					t.Fatalf("a run started in %s: %+v", session, m.todo.runner.state)
				}
				if note := lastSystem(t, m); !strings.Contains(note, session.String()+" mode, which writes nothing") || !strings.Contains(note, "/permissions") {
					t.Fatalf("the refusal says %q", note)
				}
				if it, _ := todo.Load(todo.BuiltinCode(), root).Find("do-it"); it.Status != todo.StatusOpen {
					t.Fatalf("the refusal wrote the item's status: %s", it.Status)
				}
				if _, live := run.Live(root); live {
					t.Fatal("the refusal left a sprint checkpoint")
				}
				if m.policy.mode != session {
					t.Fatalf("the mode moved to %s", m.policy.mode)
				}
			})
		}
	}
	m, _ := groomModel(t, map[string]string{"cache-ttl.md": groomItem})
	m.policy.mode = permission.ModeReadOnly
	m = submitFlow(t, m, "/todo groom cache-ttl")
	if !m.todo.groomer.going() || m.gateMode() != permission.ModePlan {
		t.Fatalf("a reading should start in read-only, under plan: going=%t gate=%s", m.todo.groomer.going(), m.gateMode())
	}
}

// A session put into a read-only mode while a writing step is going reads
// its own bound: the flow's policy narrows a session and never widens it
// past read-only, so the edit is refused and the frame draws no chip.
func TestFlowPolicy_ARunNeverWidensASessionPutIntoReadOnly(t *testing.T) {
	m, _ := runModel(t)
	m = answer(t, submitFlow(t, m, "/todo run do-it"), runPlan)
	if m.gateMode() != permission.ModeAuto {
		t.Fatalf("implement should hold auto, gate %s", m.gateMode())
	}
	m.applyMode(permission.ModePlan)
	if m.gateMode() != permission.ModePlan {
		t.Fatalf("the run widened a plan session to %s", m.gateMode())
	}
	if d, why := editVerdict(m); d != permission.Deny || why != "plan mode" {
		t.Fatalf("an edit is %v (%q)", d, why)
	}
	if c := m.cockpitData(false); c.Flow != "" {
		t.Fatalf("the frame draws a chip for a policy that does not apply: %+v", c)
	}
}

// The frame's mode segment keeps the session's word, and carries the flow's
// mode beside it only while the two differ.
func TestFlowPolicy_TheFrameNamesBothModesWhileTheyDiffer(t *testing.T) {
	m, _ := runModel(t)
	m = submitFlow(t, m, "/todo run do-it")
	if c := m.cockpitData(false); c.Mode != "manual" || c.Flow != "plan" {
		t.Fatalf("research under manual: %q → %q", c.Mode, c.Flow)
	}
	m = answer(t, m, runPlan)
	if c := m.cockpitData(false); c.Mode != "manual" || c.Flow != "auto" {
		t.Fatalf("implement under manual: %q → %q", c.Mode, c.Flow)
	}
	m.policy.mode = permission.ModeAuto
	if c := m.cockpitData(false); c.Mode != "auto" || c.Flow != "" {
		t.Fatalf("implement under auto draws a chip: %q → %q", c.Mode, c.Flow)
	}
	m = submitFlow(t, m, "/todo stop")
	if c := m.cockpitData(false); c.Mode != "auto" || c.Flow != "" {
		t.Fatalf("after the run: %q → %q", c.Mode, c.Flow)
	}
	if note := m.flowPolicyLine(); note != "" {
		t.Fatalf("no flow, and /permissions says %q", note)
	}
}

// `/permissions` names the person's mode, and says under which one a flow
// is answering while it differs.
func TestFlowPolicy_PermissionsSaysTheFlowAnswersUnderItsOwn(t *testing.T) {
	m, _ := runModel(t)
	m = answer(t, submitFlow(t, m, "/todo run do-it"), runPlan)
	status := m.modeStatus()
	if !strings.Contains(status, "Mode: manual") || !strings.Contains(status, "While the backlog run is going it answers under auto") {
		t.Fatalf("/permissions = %q", status)
	}
}
