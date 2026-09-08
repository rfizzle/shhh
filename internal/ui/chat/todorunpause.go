package chat

// The pause card: what the run stopped at, the plan as a checklist under it,
// and the keys that answer it. It is its own file because it is the one part
// of a run that holds the keyboard, which is a different kind of code from
// the stages around it.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/observe"
	"github.com/rfizzle/shhh/internal/todo"
	"github.com/rfizzle/shhh/internal/todo/run"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// openTodoPause shows the pause card: why the run stopped, the questions,
// the size, the plan, and three answers — go ahead, re-plan with a note,
// or stop. It borrows the bottom panel the way the memory prompt does.
func (m Model) openTodoPause(step run.Step) (tea.Model, tea.Cmd) {
	st := m.todoRunner.state
	// The plan is on the card as a checklist and on the run's row as the
	// research stage's answer. It used to be pasted into the transcript here
	// as well, which put the same paragraphs in a third place — the one
	// place they could be neither folded nor answered.
	opts := []components.SelectOption{
		{Label: "Go ahead", Desc: "build the plan as it stands"},
		{Label: "Re-plan with my note", Desc: "answer the questions or steer; research runs again", RequireNote: true},
		{Label: "Stop the run", Desc: "the item goes back to open; nothing is built"},
	}
	ns := components.NewNoteSelect("Run paused — "+st.Paused, opts)
	ns.Select.MaxLines = m.maxConfirmPanelHeight() - 1
	m.todoRunner.pause = ns
	m.enterSurface(stateTodoPause)
	m.syncViewport()
	m.viewport.SetLines(m.renderHistoryLines())
	m.viewport.GotoBottom()
	return m, nil
}

// todoPauseLines renders the pause card: the plan as a checklist, the
// questions, the lanes a large item was divided into, and the re-graded size
// against the item's — all above the selector, so the choice is made with
// the facts in view rather than with a scroll back through the transcript.
//
// The plan is bounded rather than complete. The card's height comes out of
// the bottom panel's budget, and a twenty-step plan that pushed the answers
// off the screen would be the facts hiding the choice; what does not fit is
// counted, which is what every other fold in the product does
// (docs/interface/principles.md#fold-never-hide).
func (m Model) todoPauseLines() []string {
	if m.todoRunner.pause == nil || m.todoRunner.state == nil {
		return nil
	}
	st := m.todoRunner.state
	width := m.contentWidth()
	var lines []string
	head := fmt.Sprintf("%s · %s %s", st.Slug, st.Profile.Grade, orDash(st.Grade))
	if st.GradeBefore != st.Grade {
		head += fmt.Sprintf(" (was %s)", orDash(st.GradeBefore))
	}
	if len(st.Steps) > 0 {
		head += fmt.Sprintf(" · %s", plural(len(st.Steps), "step"))
	}
	lines = append(lines, sty.Header.Render(head))
	card := strings.Split(m.todoRunner.pause.View(width), "\n")
	// What the plan may take: the panel less the head, the questions, the
	// lanes and the selector itself.
	room := m.maxConfirmPanelHeight() - len(lines) - len(card) - len(st.Questions) - len(st.Lanes)
	lines = append(lines, m.todoPlanChecklist(st.Steps, room, width)...)
	for _, q := range st.Questions {
		lines = append(lines, clipRow(sty.Step.Stats.Render("? "+q), width))
	}
	for _, lane := range st.Lanes {
		lines = append(lines, clipRow(sty.Step.Stats.Render("lane "+lane.Name+"  "+strings.Join(lane.Paths, ", ")), width))
	}
	return append(lines, card...)
}

// todoPlanChecklist is the plan as the checklist the rail draws a running
// plan as: one row per step, numbered as the plan numbered them, with the
// to-do glyph, because nothing here has been built yet.
func (m Model) todoPlanChecklist(steps []string, room, width int) []string {
	if len(steps) == 0 || room < 1 {
		return nil
	}
	shown := steps
	if len(shown) > room {
		// One of the rows goes to the count of what did not fit.
		shown = shown[:max(room-1, 0)]
	}
	lines := make([]string, 0, room)
	for i, step := range shown {
		lines = append(lines, clipRow(sty.Step.Dim.Render("○ ")+
			sty.Step.Stats.Render(fmt.Sprintf("%d. %s", i+1, step)), width))
	}
	if n := len(steps) - len(shown); n > 0 {
		lines = append(lines, clipRow(sty.Step.Dim.Render(fmt.Sprintf("… %d more", n)), width))
	}
	return lines
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// updateTodoPause routes keys while the pause card shows.
func (m Model) updateTodoPause(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	done, res := m.todoRunner.pause.Update(msg)
	if !done {
		return m, nil
	}
	m.todoRunner.pause = nil
	m.leaveSurface()
	m.syncViewport()
	st, it := m.todoRunner.state, m.todoRunner.item
	if st == nil {
		return m, nil
	}
	switch {
	case res.Canceled, res.Index == 2:
		return m.stopTodoRun()
	case res.Index == 1:
		note := strings.TrimSpace(res.Note)
		_ = todo.Append(it.Path, "## Answers\n"+note)
		m.reloadTodos()
		m.signal(observe.SignalRun, "replan")
		return m.todoRunStep(st.Replan(it, note))
	}
	if note := strings.TrimSpace(res.Note); note != "" {
		_ = todo.Append(it.Path, "## Answers\n"+note)
		st.Answers = append(st.Answers, note)
		m.reloadTodos()
	}
	return m.todoRunStep(st.Resume(it))
}
