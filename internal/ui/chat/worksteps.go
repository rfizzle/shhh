package chat

// The session's own working steps: the list the model keeps with the steps
// tool when a task will take several steps, drawn in the rail's STEPS block.
// It is the session's list and nobody else's — each child keeps its own
// through the same call (internal/subagent/steps.go) — and it is never the
// approved plan: nothing here reads or writes planRun or the plan record.
// See docs/capabilities/coding-agent.md#the-session-keeps-its-own-working-steps.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/plan"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// noteStepsCall applies a steps call to the session's checklist once its
// result has landed. The executor checked the same arguments and answered
// with the list they name (plan.WrapStepsExecutor); the list lives here, on
// the UI goroutine, so the call is read again here rather than written from
// the goroutine the round ran on — and a call the executor refused parses to
// the same refusal and changes nothing.
//
// It reports whether the call moved the list. Such a call leaves no row of its
// own: the STEPS block is where the list is drawn, and a row per revision
// would be bookkeeping between the rows of the work it counts. A call that
// was refused keeps its row, since its error is the model's to act on and
// the reader's to see.
//
// While an approved plan is being executed the plan is the checklist, so the
// session keeps no second one beside it; and a conversation keeps no list.
func (m *Model) noteStepsCall(call provider.ToolCall) bool {
	if call.Name != plan.StepsToolName || m.planRun != nil || !m.codingSurfaces() {
		return false
	}
	l, err := plan.ParseStepsCall(json.RawMessage(call.Arguments))
	if err != nil {
		return false
	}
	m.workSteps = l
	return true
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

// The steps screen (docs/interface/surfaces.md#the-supporting-screens):
// `/steps`, and the rail's STEPS heading, open the whole list the block
// counts.
//
// Two different things meet on it. The checklist is the session's working
// list — what the agent said it would do, marked as it says it has. The
// transcript's step blocks are the model's titled runs of calls (steps.go).
// They are joined by title, where the titles match once case, spacing, a
// leading number and closing punctuation are set aside, and a step no block
// is titled for is `not started`. Only the list as it stands is drawn: a
// revised list is the declaration the agent is working to, and the one it
// replaced is not kept.

// openSteps puts the screen up. It is built once per opening, like the
// sources screen: what it draws is the list and the transcript as they stood
// when the reader asked, and a screen that moved under them mid-read would be
// answering a question they had stopped asking.
func (m Model) openSteps() (tea.Model, tea.Cmd) {
	// A conversation keeps no list, and the command is not offered there
	// (complete.go), so the two refusals left are these.
	switch {
	case m.planRun != nil:
		return m.systemNotice("an approved plan is being executed, and its steps are the checklist · " + planHintRail)
	case len(m.workSteps.Steps) == 0:
		return m.systemNotice("the session has declared no working steps")
	}
	screen := m.stepsScreenData()
	m.screens = m.screens.with(stateSteps, &screen)
	m.enterSurface(stateSteps)
	return m, nil
}

// updateSteps routes keys while the screen is up.
func (m Model) updateSteps(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	screen := m.screens.steps()
	if screen == nil || screen.Update(msg) {
		return m.closeStepsScreen()
	}
	return m, nil
}

// closeStepsScreen hands the screen back to the turn, the way its own esc
// does and the way the rail cell that opened it does.
func (m Model) closeStepsScreen() (tea.Model, tea.Cmd) {
	m.screens = m.screens.without(stateSteps)
	m.leaveSurface()
	m.syncViewport()
	return m, nil
}

// renderStepsHint is the one line the screen leaves where the draft box was:
// the way out and nothing else, the way the sources screen's does.
func (m Model) renderStepsHint() string {
	return sty.SystemMsg.Render("steps · ") + segAs(keys.Screen.Quit, "back to the prompt").render()
}

// stepsScreenData builds the screen from the session's checklist and its own
// transcript — never an attached child's, whose steps are its own.
func (m Model) stepsScreenData() components.StepsScreen {
	es := m.transcript
	runs := map[string][]*stepGroup{}
	for _, blk := range m.blocksOf(es) {
		if blk.step != nil && !blk.step.queued() {
			k := stepKey(blk.step.title)
			runs[k] = append(runs[k], blk.step)
		}
	}
	list := m.workSteps
	done, total, _ := list.Tally()
	current := -1
	items := make([]components.StepsItem, 0, len(list.Steps))
	for i, s := range list.Steps {
		item := components.StepsItem{Number: s.Number, Title: s.Title, Paths: s.Paths, Done: list.Done[s.Number]}
		if !item.Done && current < 0 {
			item.Current, current = true, i
		}
		// A step the model titled more than one run for is still one step:
		// its runs are drawn under it in the order they happened.
		tools := 0
		var took time.Duration
		for _, g := range runs[stepKey(s.Title)] {
			_, n, d := m.stepStats(g, es)
			tools, took = tools+n, took+d
			item.Rows = append(item.Rows, m.stepsRows(es[g.start:g.end])...)
		}
		if item.Started = len(item.Rows) > 0; item.Started {
			item.Count, item.Duration = plural(tools, "tool"), activityDuration(took)
		}
		items = append(items, item)
	}
	focus := current
	if focus < 0 {
		focus = len(items) - 1
	}
	return components.StepsScreen{Steps: items, Focus: focus, Subject: fmt.Sprintf("%d of %d", done, total)}
}

// stepsRows is a run's calls as the screen draws them: each one the row the
// transcript drew for it, folded to its one line. A step's detail is the
// transcript's to open; the screen is where the steps are compared.
func (m Model) stepsRows(es []entry) []components.ActivityRow {
	var rows []components.ActivityRow
	for _, e := range es {
		switch {
		case e.kind == entryDiff && e.diff != nil:
			rows = append(rows, e.diff.Row())
		case e.kind == entryTool || e.kind == entryCommand:
			row := m.activityRowFor(e)
			row.Expanded, row.Detail, row.Tail = false, nil, ""
			rows = append(rows, row)
		}
	}
	return rows
}

// stepLead is what a title may open with and still be the same step: a
// number the list or the model gave it, with or without the word.
var stepLead = regexp.MustCompile(`^(?:step\s+\d+\s*[.):-]?|\d+[.)])\s*`)

// stepKey is a title as the join compares it: case, spacing, a leading number
// and closing punctuation set aside, since the list and the prose before a
// call are the same words written in two places.
func stepKey(title string) string {
	k := strings.ToLower(strings.Join(strings.Fields(title), " "))
	k = stepLead.ReplaceAllString(k, "")
	return strings.TrimRight(k, ".:;,!…")
}
