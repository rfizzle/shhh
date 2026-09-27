package chat

// The session's own working steps: the numbered list the model writes before
// a call when a task will take several steps, and the progress lines that
// mark it, read into the rail's STEPS block. It is the session's list and
// nobody else's — each child keeps its own through the same reader
// (internal/subagent/steps.go) — and it is never the approved plan: nothing
// here reads or writes planRun or the plan record.
// See docs/capabilities/coding-agent.md#the-session-keeps-its-own-working-steps.

import (
	"github.com/rfizzle/shhh/internal/plan"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// noteWorkSteps reads one of the session's own messages into its checklist:
// beforeCall for the prose that goes on to a round's calls, false for the
// message that ends the turn. The grammar and its rules are plan.Checklist's,
// the one reader a child's lane is counted by too.
//
// While an approved plan is being executed the plan is the checklist, so the
// session keeps no second one beside it; and a conversation asks for no list,
// so none is read out of its answers.
func (m *Model) noteWorkSteps(text string, beforeCall bool) {
	if text == "" || m.planRun != nil || !m.codingSurfaces() {
		return
	}
	m.workSteps.Note(text, beforeCall)
}

// inspectorSteps is the STEPS block: how far the session is through its own
// list, and the step it is on. It is nil where there is no list — a short
// task declares none, and zero of zero is not a reading — and while an
// approved plan is being executed, since PLAN is the checklist then.
func (m Model) inspectorSteps() *components.InspectorSteps {
	if m.planRun != nil || !m.codingSurfaces() {
		return nil
	}
	sc := subagent.StepsOf(m.workSteps)
	if !sc.Own {
		return nil
	}
	return &components.InspectorSteps{Done: sc.Done, Total: sc.Total, Current: sc.Current}
}

// storedChatSteps is the checklist a slot holds, and none where the slot has
// none or the store cannot be read.
func storedChatSteps(m *Model, slot string) plan.Checklist {
	if m.db == nil || slot == "" {
		return plan.Checklist{}
	}
	saved, err := m.db.ChatResume(slot)
	if err != nil {
		return plan.Checklist{}
	}
	return plan.DecodeChecklist(saved.Steps)
}
